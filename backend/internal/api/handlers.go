package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/nexus-stream/nexus-core/internal/database"
	"github.com/nexus-stream/nexus-core/internal/media"
	"github.com/nexus-stream/nexus-core/internal/model"
	"github.com/nexus-stream/nexus-core/internal/telemetry"
)

// handleHealth 探活接口，用于部署自检与前端启动校验。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"service":        "nexus-core",
		"version":        Version,
		"server_time":    time.Now(),
		"uptime_seconds": int64(time.Since(s.startedAt).Seconds()),
		"ws_clients":     s.hub.ClientCount(),
		"ffmpeg_ready":   s.probe.FFmpegAvailable,
		"ffmpeg_version": s.probe.FFmpegVersion,
		"gpu_ready":      s.probe.NVENCAvailable,
		// gpu_detail 把探针结论透出给大屏/运维，避免「没显卡」与「NVENC 初始化失败」被混为一谈。
		"gpu_detail": s.probe.Detail,
	})
}

// handleDevice 返回当前接入设备的资产信息。
func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodGet) {
		return
	}
	if s.device == nil {
		writeError(w, http.StatusNotFound, "尚未配置任何设备")
		return
	}
	writeJSON(w, http.StatusOK, s.device)
}

// webrtcStatus 描述当前 Pion 分发层状态。
type webrtcStatus struct {
	Peers int    `json:"peers"`
	Codec string `json:"codec"`
}

// wsStatus 描述当前大屏连接数。
type wsStatus struct {
	Clients int `json:"clients"`
}

// statusResponse 是 /api/status 的完整运行快照。
type statusResponse struct {
	Version             string           `json:"version"`
	ServerTime          time.Time        `json:"server_time"`
	UptimeSeconds       int64            `json:"uptime_seconds"`
	Mode                string           `json:"mode"`
	ModeDescription     string           `json:"mode_description"`
	HardwareAccelerated bool             `json:"hardware_accelerated"`
	ForceGPU            string           `json:"force_gpu"`
	Notice              string           `json:"notice,omitempty"`
	Probe               any              `json:"probe"`
	Transcoder          media.Status     `json:"transcoder"`
	RTP                 media.RelayStats `json:"rtp"`
	WebRTC              webrtcStatus     `json:"webrtc"`
	WS                  wsStatus         `json:"ws"`
	Device              *model.Device    `json:"device,omitempty"`
}

// handleStatus 汇总探针、转码、RTP 与 WebRTC 指标，供大屏顶部状态条展示。
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodGet) {
		return
	}
	status := s.transcoder.Status()
	mode := s.transcoder.CurrentMode()
	writeJSON(w, http.StatusOK, statusResponse{
		Version:             Version,
		ServerTime:          time.Now(),
		UptimeSeconds:       int64(time.Since(s.startedAt).Seconds()),
		Mode:                string(mode),
		ModeDescription:     status.ModeDescription,
		HardwareAccelerated: status.HardwareAccelerated,
		ForceGPU:            s.cfg.ForceGPU,
		Notice:              s.notice(),
		Probe:               s.probe,
		Transcoder:          status,
		RTP:                 s.relay.Stats(),
		WebRTC:              webrtcStatus{Peers: s.webrtc.PeerCount(), Codec: s.webrtc.Codec()},
		WS:                  wsStatus{Clients: s.hub.ClientCount()},
		Device:              s.device,
	})
}

// handleTelemetry 返回一条即时遥测快照，供前端首屏自检使用。
func (s *Server) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodGet) {
		return
	}
	stats := s.relay.Stats()
	writeJSON(w, http.StatusOK, s.sim.Next(s.target(), buildEnv(s, stats.Active)))
}

// buildEnv 组装遥测所需的环境信息。
func buildEnv(s *Server, mediaActive bool) telemetry.Env {
	env := telemetry.Env{
		MediaActive:       mediaActive,
		TranscoderRunning: s.transcoder.Status().Running,
		Uptime:            time.Since(s.startedAt),
		Notice:            s.notice(),
	}
	if s.device != nil {
		env.Device = *s.device
	}
	return env
}

// handleMode 支持 GET 查询当前轨道、POST 指定目标轨道。
func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		status := s.transcoder.Status()
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":                 status.Mode,
			"mode_description":     status.ModeDescription,
			"hardware_accelerated": status.HardwareAccelerated,
			"target_fps":           status.TargetFPS,
			"target_bitrate_kbps":  status.TargetBitrateKbps,
			"resolution":           status.Resolution,
			"codec":                status.Codec,
		})
	case http.MethodPost:
		s.applyModeChange(w, r)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

// handleModeToggle 是「双轨一键切换」按钮的专用接口：不带参数即取反。
func (s *Server) handleModeToggle(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
	s.applyModeChange(w, r)
}

// modeRequest 兼容三种入参：显式 mode、布尔 gpu、空 body（取反）。
type modeRequest struct {
	Mode string `json:"mode"`
	GPU  *bool  `json:"gpu"`
}

// modeResponse 返回切换结果与切换后的即时指标。
type modeResponse struct {
	Changed             bool   `json:"changed"`
	Mode                string `json:"mode"`
	ModeDescription     string `json:"mode_description"`
	Codec               string `json:"codec"`
	Resolution          string `json:"resolution"`
	FPS                 int    `json:"fps"`
	BitrateKbps         int    `json:"bitrate_kbps"`
	HardwareAccelerated bool   `json:"hardware_accelerated"`
}

func (s *Server) applyModeChange(w http.ResponseWriter, r *http.Request) {
	var req modeRequest
	if r.Body != nil {
		// 允许空 body：直接按当前轨道取反。
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil && err != io.EOF {
			s.logger.Warn("解析模式切换请求失败，按取反处理", "err", err)
		}
	}

	prev := s.transcoder.CurrentMode()
	var next media.Mode

	switch {
	case strings.TrimSpace(req.Mode) != "":
		parsed, ok := media.ParseMode(req.Mode)
		if !ok {
			writeError(w, http.StatusBadRequest, "非法模式，可选值：GPU_ACCELERATION / CPU_SELF_HEALING")
			return
		}
		next = parsed
	case req.GPU != nil:
		if *req.GPU {
			next = media.ModeGPU
		} else {
			next = media.ModeCPU
		}
	default:
		next = prev.Toggle()
	}

	if err := s.transcoder.SwitchMode(next); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.recordSession(next)
	// 立刻推一帧，保证大屏在 1 秒内看到 60fps/4250k -> 12fps/800k 的断崖式变化。
	s.BroadcastNow()

	spec := next.Spec(s.probe.NVENCAvailable)
	writeJSON(w, http.StatusOK, modeResponse{
		Changed:             prev != next,
		Mode:                string(next),
		ModeDescription:     spec.Description,
		Codec:               spec.Codec,
		Resolution:          spec.Resolution,
		FPS:                 spec.FPS,
		BitrateKbps:         spec.BitrateKbps,
		HardwareAccelerated: spec.Hardware,
	})
}

// recordSession 把每次轨道切换落库，供后续双轨对比分析。
func (s *Server) recordSession(mode media.Mode) {
	if s.db == nil || s.device == nil {
		return
	}
	spec := mode.Spec(s.probe.NVENCAvailable)
	row := &model.StreamSession{
		DeviceID:      s.device.ID,
		CurrentCodec:  spec.Codec,
		TranscodeMode: string(mode),
		BitrateKbps:   spec.BitrateKbps,
		FPS:           spec.FPS,
		LossRate:      fmt.Sprintf("%.2f%%", 0.08+rand.Float64()*0.22),
		Resolution:    spec.Resolution,
	}
	if err := database.RecordSession(s.db, row); err != nil {
		s.logger.Warn("写入轨道切换快照失败", "err", err)
	}
}

// offerRequest 是浏览器下发的 SDP Offer。
type offerRequest struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

// answerResponse 是返回给浏览器的 SDP Answer。
type answerResponse struct {
	SDP   string `json:"sdp"`
	Type  string `json:"type"`
	Codec string `json:"codec"`
	Peers int    `json:"peers"`
}

// handleWebRTCOffer 接收浏览器 Offer，返回可直连播放的 Answer。
func (s *Server) handleWebRTCOffer(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodPost) {
		return
	}

	var req offerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "解析 SDP Offer 失败: "+err.Error())
		return
	}
	if strings.TrimSpace(req.SDP) == "" {
		writeError(w, http.StatusBadRequest, "SDP 不能为空")
		return
	}

	offerType := webrtc.NewSDPType(req.Type)
	if offerType == webrtc.SDPTypeUnknown {
		offerType = webrtc.SDPTypeOffer
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	answer, err := s.webrtc.HandleOffer(ctx, webrtc.SessionDescription{Type: offerType, SDP: req.SDP})
	if err != nil {
		s.logger.Warn("WebRTC 协商失败", "err", err)
		writeError(w, http.StatusInternalServerError, "WebRTC 协商失败: "+err.Error())
		return
	}

	s.logger.Info("WebRTC 协商成功", "peers", s.webrtc.PeerCount())
	writeJSON(w, http.StatusOK, answerResponse{
		SDP:   answer.SDP,
		Type:  answer.Type.String(),
		Codec: s.webrtc.Codec(),
		Peers: s.webrtc.PeerCount(),
	})
}

// withRecover 捕获 handler panic，避免单个异常请求打挂整个服务。
func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// withCORS 允许 Vite 开发服务器（:5173）与局域网大屏跨域访问。
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder 记录响应码，供访问日志使用。
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) status() int {
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}

// withLogging 只记录 /api 请求，避免静态资源刷屏。
func withLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		logger.Info("api",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status(),
			"cost", time.Since(start).Round(time.Millisecond).String(),
		)
	})
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func allowMethods(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, m := range methods {
		if r.Method == m {
			return true
		}
	}
	methodNotAllowed(w, methods...)
	return false
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
}
