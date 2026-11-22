package media

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// h264FmtpLine 与 FFmpeg 输出的 baseline profile 严格对齐，
// 保证 Chrome / Edge 协商 H.264 后能直接硬解。
const h264FmtpLine = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"

// WebRTCPeer 维护单条共享 H.264 Track 与多个浏览器 PeerConnection。
//
// 所有浏览器共用同一条 TrackLocalStaticRTP：RTP 中转层只 WriteRTP 一次，
// Pion 会自动把包复制给每个已绑定的 PeerConnection（单机 MVP 足够，且零拷贝开销）。
type WebRTCPeer struct {
	api        *webrtc.API
	track      *webrtc.TrackLocalStaticRTP
	iceServers []webrtc.ICEServer

	mu     sync.Mutex
	peers  map[uint64]*webrtc.PeerConnection
	nextID uint64
	closed bool
}

// NewWebRTCPeer 初始化 Pion API：只注册 H.264，关闭 mDNS 以便局域网演示直连。
func NewWebRTCPeer(stunServers []string) (*WebRTCPeer, error) {
	mediaEngine := &webrtc.MediaEngine{}
	capability := webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeH264,
		ClockRate:   90000,
		SDPFmtpLine: h264FmtpLine,
	}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: capability,
		PayloadType:        102,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, fmt.Errorf("注册 H.264 编码能力失败: %w", err)
	}

	registry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, registry); err != nil {
		return nil, fmt.Errorf("注册 RTCP 拦截器失败: %w", err)
	}

	settings := webrtc.SettingEngine{}
	// 局域网 / 本机演示不需要 mDNS 候选，关掉可避免部分网络下 ICE 卡住。
	settings.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)

	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(registry),
		webrtc.WithSettingEngine(settings),
	)

	track, err := webrtc.NewTrackLocalStaticRTP(capability, "nexus-video", "nexus-stream")
	if err != nil {
		return nil, fmt.Errorf("创建 H.264 Track 失败: %w", err)
	}

	servers := make([]webrtc.ICEServer, 0, len(stunServers))
	for _, url := range stunServers {
		servers = append(servers, webrtc.ICEServer{URLs: []string{url}})
	}

	return &WebRTCPeer{
		api:        api,
		track:      track,
		iceServers: servers,
		peers:      make(map[uint64]*webrtc.PeerConnection),
	}, nil
}

// WriteRTP 把改写后的 RTP 包喂给所有已绑定的浏览器。
func (w *WebRTCPeer) WriteRTP(pkt *rtp.Packet) error {
	if pkt == nil {
		return nil
	}
	return w.track.WriteRTP(pkt)
}

// HandleOffer 处理 SDP Offer：绑定 H.264 Track -> 生成 Answer -> 等待 ICE 收集完成。
//
// 采用非 Trickle 模式（等 ICE 收集完毕再返回完整 Answer），
// 前端只需一次 fetch 就能拿到可直接播放的 SDP，实现无插件秒开。
func (w *WebRTCPeer) HandleOffer(ctx context.Context, offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, fmt.Errorf("WebRTC 服务已关闭")
	}
	w.nextID++
	id := w.nextID
	w.mu.Unlock()

	pc, err := w.api.NewPeerConnection(webrtc.Configuration{ICEServers: w.iceServers})
	if err != nil {
		return nil, fmt.Errorf("创建 PeerConnection 失败: %w", err)
	}

	w.attach(id, pc)
	cleanup := func() { w.detach(id, pc) }

	sender, err := pc.AddTrack(w.track)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("绑定 H.264 Track 失败: %w", err)
	}

	// 必须持续读取 RTCP，Pion 的 NACK / PLI 拦截器才会工作。
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, readErr := sender.Read(buf); readErr != nil {
				return
			}
		}
	}()

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		switch state {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			cleanup()
		}
	})

	if err := pc.SetRemoteDescription(offer); err != nil {
		cleanup()
		return nil, fmt.Errorf("设置远端 SDP 失败: %w", err)
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("生成 Answer 失败: %w", err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		cleanup()
		return nil, fmt.Errorf("设置本地 SDP 失败: %w", err)
	}

	select {
	case <-gatherComplete:
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	case <-time.After(6 * time.Second):
		// 超时也返回已收集到的候选，避免前端一直转圈。
	}

	local := pc.LocalDescription()
	if local == nil {
		cleanup()
		return nil, fmt.Errorf("ICE 收集失败，未生成本地 SDP")
	}
	return local, nil
}

// PeerCount 返回当前活跃的浏览器连接数。
func (w *WebRTCPeer) PeerCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.peers)
}

// Codec 返回下发给浏览器的编码描述。
func (w *WebRTCPeer) Codec() string { return "H.264" }

// Close 关闭所有 PeerConnection。
func (w *WebRTCPeer) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	peers := make([]*webrtc.PeerConnection, 0, len(w.peers))
	for _, pc := range w.peers {
		peers = append(peers, pc)
	}
	w.peers = map[uint64]*webrtc.PeerConnection{}
	w.mu.Unlock()

	for _, pc := range peers {
		_ = pc.Close()
	}
}

func (w *WebRTCPeer) attach(id uint64, pc *webrtc.PeerConnection) {
	w.mu.Lock()
	w.peers[id] = pc
	w.mu.Unlock()
}

func (w *WebRTCPeer) detach(id uint64, pc *webrtc.PeerConnection) {
	w.mu.Lock()
	if current, ok := w.peers[id]; ok && current == pc {
		delete(w.peers, id)
	}
	w.mu.Unlock()
	_ = pc.Close()
}
