// Command rtcprobe 是本地联调工具：模拟浏览器与 /api/webrtc/offer 完成一次
// 真实的 Offer/Answer + ICE 协商，并统计收到的 H.264 RTP 包数量，
// 用于在无浏览器环境下验证「FFmpeg -> RTP 中转 -> Pion -> 浏览器」整条链路。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
)

const fmtpLine = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"

func main() {
	target := "http://127.0.0.1:8080/api/webrtc/offer"
	if len(os.Args) > 1 {
		target = os.Args[1]
	}

	engine := &webrtc.MediaEngine{}
	if err := engine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeH264,
			ClockRate:   90000,
			SDPFmtpLine: fmtpLine,
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		fail("注册 H.264 失败: %v", err)
	}

	registry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(engine, registry); err != nil {
		fail("注册拦截器失败: %v", err)
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(engine), webrtc.WithInterceptorRegistry(registry))
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		fail("创建 PeerConnection 失败: %v", err)
	}
	defer pc.Close()

	var packets int64
	var bytesIn int64
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		buf := make([]byte, 1500)
		for {
			n, _, readErr := track.Read(buf)
			if readErr != nil {
				return
			}
			atomic.AddInt64(&packets, 1)
			atomic.AddInt64(&bytesIn, int64(n))
		}
	})

	// 以 recvonly 方式声明要接收视频，否则 Offer 里连 m= 段都不会有。
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly},
	); err != nil {
		fail("添加视频接收 transceiver 失败: %v", err)
	}

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		fail("创建 Offer 失败: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		fail("设置本地 SDP 失败: %v", err)
	}
	<-gathered

	local := pc.LocalDescription()
	if local == nil {
		fail("ICE 收集失败，未生成本地 SDP")
	}
	sdp := local.SDP

	body, _ := json.Marshal(map[string]string{"sdp": sdp, "type": "offer"})
	resp, err := http.Post(target, "application/json", bytes.NewReader(body))
	if err != nil {
		fail("请求 %s 失败: %v", target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail("服务端返回 HTTP %d", resp.StatusCode)
	}

	var answer struct {
		SDP   string `json:"sdp"`
		Type  string `json:"type"`
		Codec string `json:"codec"`
		Peers int    `json:"peers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		fail("解析 Answer 失败: %v", err)
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.NewSDPType(answer.Type),
		SDP:  answer.SDP,
	}); err != nil {
		fail("设置远端 SDP 失败: %v", err)
	}

	// 先等首包（ICE 建立 + 中转推流），再固定窗口统计收包速率。
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt64(&packets) == 0 {
		time.Sleep(100 * time.Millisecond)
	}
	window := 5 * time.Second
	if raw := os.Getenv("PROBE_WINDOW_MS"); raw != "" {
		if ms, convErr := strconv.Atoi(raw); convErr == nil && ms > 0 {
			window = time.Duration(ms) * time.Millisecond
		}
	}

	beforePkts := atomic.LoadInt64(&packets)
	beforeBytes := atomic.LoadInt64(&bytesIn)
	windowStart := time.Now()
	time.Sleep(window)
	elapsed := time.Since(windowStart).Seconds()
	deltaPkts := atomic.LoadInt64(&packets) - beforePkts
	deltaBytes := atomic.LoadInt64(&bytesIn) - beforeBytes

	pps := float64(deltaPkts) / elapsed
	kbps := float64(deltaBytes) * 8 / elapsed / 1000

	state := pc.ConnectionState().String()
	result := map[string]any{
		"codec":            answer.Codec,
		"peers":            answer.Peers,
		"offer_sdp_bytes":  len(sdp),
		"packets_received": atomic.LoadInt64(&packets),
		"bytes_received":   atomic.LoadInt64(&bytesIn),
		"window_seconds":   math.Round(elapsed*10) / 10,
		"rtp_pps":          math.Round(pps*10) / 10,
		"payload_kbps":     math.Round(kbps*10) / 10,
		"pc_state":         state,
	}
	out, _ := json.Marshal(result)
	fmt.Println(string(out))

	if atomic.LoadInt64(&packets) == 0 {
		os.Exit(2)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
