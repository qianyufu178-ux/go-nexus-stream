package media

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/pion/rtp"
)

// RTPRelay 监听本地 UDP 端口，接收 FFmpeg 输出的 H.264 RTP 包，
// 并把序号 / 时间戳改写成连续单调的流，再喂给 Pion 的 TrackLocalStaticRTP。
//
// 为什么要重写序号与时间戳？
//   - 每次热切换双轨模式 FFmpeg 都会被重启，SSRC / 时间戳基准 / 序号全部重置；
//   - 浏览器端的抖动缓冲与 NACK 重传如果看到序号倒流会直接卡死；
//   - 这里统一改写后，前端可以「无感」地完成 60fps <-> 12fps 的平滑切换。
type RTPRelay struct {
	conn *net.UDPConn
	addr *net.UDPAddr

	mu           sync.RWMutex
	sink         func(*rtp.Packet) error
	packets      uint64
	forwarded    uint64
	dropped      uint64
	payloadBytes uint64
	streams      uint64
	lastPacket   time.Time
	lastSSRC     uint32

	seq        uint16
	tsOffset   uint32
	outTS      uint32
	haveStream bool
}

// RelayStats 是 RTP 中转层的运行指标。
type RelayStats struct {
	ListenAddr   string    `json:"listen_addr"`
	Packets      uint64    `json:"packets_received"`
	Forwarded    uint64    `json:"packets_forwarded"`
	Dropped      uint64    `json:"packets_dropped"`
	PayloadBytes uint64    `json:"payload_bytes"`
	Streams      uint64    `json:"stream_count"`
	LastPacketAt time.Time `json:"last_packet_at,omitempty"`
	SilenceMs    int64     `json:"silence_ms"`
	Active       bool      `json:"active"`
}

// NewRTPRelay 绑定 UDP 监听地址（默认 127.0.0.1:5004）。
func NewRTPRelay(host string, port int) (*RTPRelay, error) {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, fmt.Errorf("解析 RTP 监听地址失败: %w", err)
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("RTP 端口 %s 被占用或不可用: %w", addr, err)
	}
	// 加大接收缓冲，降低 1080P60 高码率下的丢包概率。
	_ = conn.SetReadBuffer(4 * 1024 * 1024)

	return &RTPRelay{
		conn: conn,
		addr: addr,
		seq:  uint16(rand.Intn(65536)),
	}, nil
}

// Addr 返回监听地址。
func (r *RTPRelay) Addr() string { return r.addr.String() }

// SetSink 注册下游写入函数（通常是 WebRTC Track）。
func (r *RTPRelay) SetSink(fn func(*rtp.Packet) error) {
	r.mu.Lock()
	r.sink = fn
	r.mu.Unlock()
}

// Reset 标记「下一包来自新流」，重建时间戳基准。
func (r *RTPRelay) Reset() {
	r.mu.Lock()
	r.haveStream = false
	r.lastSSRC = 0
	r.mu.Unlock()
}

// Run 持续读取 RTP 包并转发，直到 ctx 结束。
func (r *RTPRelay) Run(ctx context.Context) {
	buf := make([]byte, 2048)
	for {
		if ctx.Err() != nil {
			return
		}
		_ = r.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		if n == 0 {
			continue
		}

		pkt := &rtp.Packet{}
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			r.countDropped()
			continue
		}
		r.forward(pkt)
	}
}

// Close 释放 UDP 端口。
func (r *RTPRelay) Close() error {
	if r.conn == nil {
		return nil
	}
	return r.conn.Close()
}

// Stats 返回中转层指标。
func (r *RTPRelay) Stats() RelayStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := RelayStats{
		ListenAddr:   r.addr.String(),
		Packets:      r.packets,
		Forwarded:    r.forwarded,
		Dropped:      r.dropped,
		PayloadBytes: r.payloadBytes,
		Streams:      r.streams,
		LastPacketAt: r.lastPacket,
	}
	if !r.lastPacket.IsZero() {
		silence := time.Since(r.lastPacket)
		stats.SilenceMs = silence.Milliseconds()
		stats.Active = silence < 2*time.Second
	}
	return stats
}

// forward 完成序号 / 时间戳改写并投递给下游。
func (r *RTPRelay) forward(pkt *rtp.Packet) {
	r.mu.Lock()

	if !r.haveStream || pkt.SSRC != r.lastSSRC {
		// 新流（首次启动 / 模式切换 / ffmpeg 重启）
		r.tsOffset = r.outTS + 3600 - pkt.Timestamp
		r.lastSSRC = pkt.SSRC
		r.haveStream = true
		r.streams++
	}

	pkt.SequenceNumber = r.seq
	r.seq++

	outTS := pkt.Timestamp + r.tsOffset
	pkt.Timestamp = outTS
	r.outTS = outTS

	r.packets++
	r.payloadBytes += uint64(len(pkt.Payload))
	r.lastPacket = time.Now()
	sink := r.sink
	r.mu.Unlock()

	if sink == nil {
		return
	}
	if err := sink(pkt); err != nil {
		r.countDropped()
		return
	}
	r.mu.Lock()
	r.forwarded++
	r.mu.Unlock()
}

func (r *RTPRelay) countDropped() {
	r.mu.Lock()
	r.dropped++
	r.mu.Unlock()
}
