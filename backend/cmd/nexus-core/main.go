// Command nexus-core 是 Nexus-Stream MVP 的核心服务：
// 双轨 FFmpeg 转码 -> 本地 RTP 中转 -> Pion WebRTC 分发 -> WebSocket 遥测大屏。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nexus-stream/nexus-core/internal/api"
	"github.com/nexus-stream/nexus-core/internal/config"
	"github.com/nexus-stream/nexus-core/internal/database"
	"github.com/nexus-stream/nexus-core/internal/media"
	"github.com/nexus-stream/nexus-core/internal/telemetry"
	"github.com/nexus-stream/nexus-core/internal/ws"
)

func main() {
	if err := run(); err != nil {
		slog.Error("nexus-core 启动失败", "err", err)
		os.Exit(1)
	}
}

func run() error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.Load()
	logger.Info("Nexus-Core 启动中",
		"addr", cfg.Addr,
		"db", cfg.DBPath,
		"ffmpeg", cfg.FFmpegPath,
		"sample", cfg.SamplePath,
		"rtp", cfg.RTPAddr(),
		"force_gpu", cfg.ForceGPU,
	)

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. 数据库：自动建表 + 演示种子数据。
	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	device, err := database.PrimaryDevice(db)
	if err != nil {
		return err
	}
	logger.Info("设备资产已装载", "device", device.DeviceName, "stream_key", device.StreamKey)

	// 2. 双轨制硬件探针：最多阻塞 10 秒，失败按无 GPU 处理。
	probe := config.ProbeFFmpeg(rootCtx, cfg.FFmpegPath)
	logger.Info("硬件探针完成",
		"ffmpeg", probe.FFmpegAvailable,
		"ffmpeg_version", probe.FFmpegVersion,
		"nvenc_compiled", probe.NVENCCompiled,
		"nvenc_available", probe.NVENCAvailable,
		"detail", probe.Detail,
		"error", probe.Error,
	)

	// 3. RTP 中转层：先占住 UDP 端口，FFmpeg 才能往里推。
	relay, err := media.NewRTPRelay(cfg.RTPHost, cfg.RTPPort)
	if err != nil {
		return err
	}
	defer relay.Close()
	go relay.Run(rootCtx)

	// 4. WebRTC 分发层：所有浏览器共用一条 H.264 Track。
	peer, err := media.NewWebRTCPeer(cfg.STUNServers)
	if err != nil {
		return err
	}
	defer peer.Close()
	relay.SetSink(peer.WriteRTP)

	// 5. 转码调度器：按探针结果选择初始轨道，并开启异常自愈。
	initial := media.InitialMode(cfg.ForceGPU, probe.NVENCAvailable)
	input := device.RTSPURL
	if cfg.SourceURL != "" {
		// NEXUS_INPUT_URL 优先：端到端串联时指向网关直播接入面
		input = cfg.SourceURL
	}
	logger.Info("转码输入源已确定", "input", input, "format", cfg.InputFormat)

	transcoder := media.NewTranscoder(
		cfg.FFmpegPath,
		input,
		cfg.SamplePath,
		cfg.RTPAddr(),
		probe.NVENCAvailable,
		initial,
		cfg.AutoRestart,
	)
	transcoder.SetInputFormat(cfg.InputFormat)
	// 每次重建管道都重置中转层时间戳基准，保证前端播放不中断。
	transcoder.SetRestartHook(relay.Reset)
	transcoder.AddModeListener(func(mode media.Mode) {
		logger.Info("转码轨道已切换", "mode", mode, "spec", mode.Spec(probe.NVENCAvailable).Description)
	})
	defer transcoder.Stop()

	if probe.FFmpegAvailable {
		transcoder.Start(rootCtx)
		logger.Info("双轨转码管线已启动", "mode", initial)
	} else {
		logger.Warn("未检测到 FFmpeg，转码管线保持待命；大屏指标仍按演示模式推送")
	}

	// 6. 遥测广播。
	hub := ws.NewHub(logger)
	defer hub.Close()
	sim := telemetry.NewSimulator()

	// 7. HTTP / WebSocket / 静态资源。
	server := api.New(api.Deps{
		Config:     cfg,
		DB:         db,
		Probe:      probe,
		Transcoder: transcoder,
		WebRTC:     peer,
		Relay:      relay,
		Hub:        hub,
		Simulator:  sim,
		Device:     device,
		Logger:     logger,
		StartedAt:  time.Now(),
	})
	go server.RunTelemetry(rootCtx)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		if listenErr := httpServer.ListenAndServe(); listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			serveErr <- listenErr
		}
	}()

	printBanner(cfg, device.DeviceName, initial)

	select {
	case <-rootCtx.Done():
		logger.Info("收到退出信号，开始优雅关闭")
	case err := <-serveErr:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	transcoder.Stop()
	_ = relay.Close()
	hub.Close()
	peer.Close()
	logger.Info("Nexus-Core 已退出")
	return nil
}

// printBanner 打印演示所需的入口地址，方便录屏时直接打开浏览器。
func printBanner(cfg *config.Config, deviceName string, mode media.Mode) {
	addr := cfg.Addr
	if addr != "" && addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	os.Stdout.WriteString("\n==============================================\n" +
		"  NEXUS-STREAM 实时双轨转码网关 (MVP)\n" +
		"----------------------------------------------\n" +
		"  大屏地址 : http://" + addr + "/\n" +
		"  健康检查 : http://" + addr + "/api/health\n" +
		"  运行状态 : http://" + addr + "/api/status\n" +
		"  遥测推送 : ws://" + addr + "/ws/telemetry\n" +
		"  接入设备 : " + deviceName + "\n" +
		"  当前轨道 : " + string(mode) + "\n" +
		"==============================================\n\n")
}
