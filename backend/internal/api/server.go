// Package api 暴露 Nexus-Core 的 HTTP / WebSocket 接口，并托管前端静态资源。
package api

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/nexus-stream/nexus-core/internal/config"
	"github.com/nexus-stream/nexus-core/internal/database"
	"github.com/nexus-stream/nexus-core/internal/media"
	"github.com/nexus-stream/nexus-core/internal/model"
	"github.com/nexus-stream/nexus-core/internal/telemetry"
	"github.com/nexus-stream/nexus-core/internal/ws"
)

// Version 当前 MVP 版本号。
const Version = "0.1.0"

// Deps 是 API 层依赖注入集合。
type Deps struct {
	Config     *config.Config
	DB         *gorm.DB
	Probe      config.ProbeResult
	Transcoder *media.Transcoder
	WebRTC     *media.WebRTCPeer
	Relay      *media.RTPRelay
	Hub        *ws.Hub
	Simulator  *telemetry.Simulator
	Device     *model.Device
	Logger     *slog.Logger
	StartedAt  time.Time
}

// Server 聚合所有 HTTP 处理器与运行期状态。
type Server struct {
	cfg        *config.Config
	db         *gorm.DB
	probe      config.ProbeResult
	transcoder *media.Transcoder
	webrtc     *media.WebRTCPeer
	relay      *media.RTPRelay
	hub        *ws.Hub
	sim        *telemetry.Simulator
	device     *model.Device
	logger     *slog.Logger
	startedAt  time.Time
}

// New 构造 Server。
func New(deps Deps) *Server {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	startedAt := deps.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	return &Server{
		cfg:        deps.Config,
		db:         deps.DB,
		probe:      deps.Probe,
		transcoder: deps.Transcoder,
		webrtc:     deps.WebRTC,
		relay:      deps.Relay,
		hub:        deps.Hub,
		sim:        deps.Simulator,
		device:     deps.Device,
		logger:     logger,
		startedAt:  startedAt,
	}
}

// Handler 组装路由与中间件。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/device", s.handleDevice)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/telemetry", s.handleTelemetry)
	mux.HandleFunc("/api/mode", s.handleMode)
	mux.HandleFunc("/api/mode/toggle", s.handleModeToggle)
	// 兼容早期接口命名，前端与脚本都可直接调用。
	mux.HandleFunc("/api/switch", s.handleModeToggle)
	mux.HandleFunc("/api/webrtc/offer", s.handleWebRTCOffer)
	mux.Handle("/ws/telemetry", s.hub.Handler())
	mux.Handle("/", s.staticHandler())

	return withRecover(withCORS(withLogging(s.logger, mux)))
}

// RunTelemetry 每秒向所有大屏广播一次指标，并每 10 秒刷新一次设备心跳。
func (s *Server) RunTelemetry(ctx context.Context) {
	interval := s.cfg.TelemetryInterval
	if interval <= 0 {
		interval = time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	heartbeat := 0
	s.broadcastSnapshot()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.broadcastSnapshot()
			heartbeat++
			if heartbeat >= 10 {
				heartbeat = 0
				s.persistHeartbeat()
			}
		}
	}
}

// BroadcastNow 立即推送一条快照（模式切换后调用，保证大屏 1 秒内联动）。
func (s *Server) BroadcastNow() {
	s.broadcastSnapshot()
}

func (s *Server) broadcastSnapshot() {
	if s.hub == nil || s.sim == nil {
		return
	}
	stats := s.relay.Stats()
	s.hub.BroadcastJSON(s.sim.Next(s.target(), buildEnv(s, stats.Active)))
}

// target 把当前轨道规格映射为指标中心值。
func (s *Server) target() telemetry.Target {
	mode := s.transcoder.CurrentMode()
	spec := s.transcoder.CurrentSpec()
	return telemetry.Target{
		Mode:                string(mode),
		Codec:               spec.Codec,
		Resolution:          spec.Resolution,
		FPS:                 spec.FPS,
		BitrateKbps:         spec.BitrateKbps,
		HardwareAccelerated: spec.Hardware,
	}
}

// notice 生成大屏控制台的提示文案（无 FFmpeg / 异常退出时给出可读原因）。
func (s *Server) notice() string {
	status := s.transcoder.Status()
	if !s.probe.FFmpegAvailable {
		return "未检测到 FFmpeg，转码管线未启动，当前指标为演示模拟数据"
	}
	if !status.Running && status.LastError != "" {
		return "转码进程异常：" + status.LastError
	}
	if !status.Running {
		return "转码进程未运行，等待自动拉起"
	}
	if s.relay.Stats().Streams == 0 {
		return "FFmpeg 已启动，等待首批 RTP 包"
	}
	return ""
}

// persistHeartbeat 刷新 devices.last_active_at，避免每秒写库。
func (s *Server) persistHeartbeat() {
	if s.db == nil || s.device == nil {
		return
	}
	status := model.StatusOnline
	if !s.relay.Stats().Active {
		status = model.StatusDegraded
	}
	if err := database.TouchDevice(s.db, s.device.ID, status, time.Now()); err != nil {
		s.logger.Warn("刷新设备心跳失败", "err", err)
	}
}

// staticHandler 托管前端构建产物，并做 SPA 路由回退。
func (s *Server) staticHandler() http.Handler {
	abs := resolveWebDist(s.cfg.WebDist)
	if s.logger != nil {
		s.logger.Info("前端静态目录", "dir", abs, "fallback", !hasIndex(abs))
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		clean := path.Clean("/" + r.URL.Path)
		if clean == "/" {
			s.serveIndexOrFallback(w, r, abs)
			return
		}

		full := filepath.Join(abs, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
		if !strings.HasPrefix(full, abs) {
			http.NotFound(w, r)
			return
		}
		if st, statErr := os.Stat(full); statErr == nil && !st.IsDir() {
			http.ServeFile(w, r, full)
			return
		}
		// 单页应用路由回退到 index.html。
		s.serveIndexOrFallback(w, r, abs)
	})
}

func (s *Server) serveIndexOrFallback(w http.ResponseWriter, r *http.Request, dist string) {
	index := filepath.Join(dist, "index.html")
	if _, err := os.Stat(index); err == nil {
		http.ServeFile(w, r, index)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fallbackPage))
}

// fallbackPage 在还没执行 `npm run build` 时给出清晰的引导页，
// 保证后端单独启动也能看到接口探活入口，而不是一片 404。
const fallbackPage = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8" />
<title>Nexus-Stream · 前端待构建</title>
<style>
  body{margin:0;height:100vh;display:flex;align-items:center;justify-content:center;
    background:#05070d;color:#d7e3ff;font-family:"Segoe UI",system-ui,sans-serif}
  .card{max-width:640px;padding:32px 40px;border:1px solid rgba(0,255,157,.35);border-radius:16px;
    background:rgba(10,18,32,.85);box-shadow:0 0 40px rgba(0,255,157,.12)}
  h1{margin:0 0 12px;font-size:22px;color:#00ff9d;letter-spacing:.08em}
  code{display:block;margin:8px 0;padding:10px 14px;border-radius:8px;background:#0b1220;
    border:1px solid rgba(120,180,255,.25);color:#9ad7ff}
  a{color:#00ff9d}
</style>
</head>
<body>
  <div class="card">
    <h1>NEXUS-STREAM · 大屏尚未构建</h1>
    <p>后端已正常运行，但未找到前端构建产物。请执行：</p>
    <code>cd frontend &amp;&amp; npm install &amp;&amp; npm run build</code>
    <p>或开发模式下单独启动 Vite：<code>cd frontend &amp;&amp; npm run dev</code></p>
    <p>接口自检：<a href="/api/health">/api/health</a> ·
       <a href="/api/status">/api/status</a> ·
       <a href="/api/telemetry">/api/telemetry</a></p>
  </div>
</body>
</html>`

// resolveWebDist 兼容「从 backend/ 启动」与「从仓库根目录启动」两种方式：
// 依次尝试配置值、父级同名目录以及常见的前端产物目录。
func resolveWebDist(raw string) string {
	if raw == "" {
		raw = "web/dist"
	}

	candidates := []string{raw, filepath.Join("..", raw)}
	candidates = append(candidates,
		"web/dist", filepath.Join("..", "web", "dist"),
		"frontend/dist", filepath.Join("..", "frontend", "dist"),
	)

	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if hasIndex(abs) {
			return abs
		}
	}

	// 都没找到时回退到配置值，由 serveIndexOrFallback 给出引导页。
	abs, err := filepath.Abs(raw)
	if err != nil {
		return raw
	}
	return abs
}

// hasIndex 判断目录下是否存在 index.html。
func hasIndex(dir string) bool {
	if dir == "" {
		return false
	}
	st, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !st.IsDir()
}
