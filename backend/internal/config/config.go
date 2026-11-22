// Package config 负责环境变量解析与运行期配置。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// FORCE_GPU 的三种取值语义。
const (
	GPUAuto    = "auto"  // 由硬件探针自动判定
	GPUForce   = "true"  // 强制走 GPU_ACCELERATION 轨道（无卡时用 libx264 仿真）
	GPUDisable = "false" // 强制走 CPU_SELF_HEALING 轨道
)

// Config 为 Nexus-Core 的全局运行配置。
type Config struct {
	// Addr HTTP 监听地址（默认 :8080）。
	Addr string
	// DBPath SQLite 文件路径。
	DBPath string
	// WebDist 前端构建产物目录（Vue3 大屏）。
	WebDist string
	// FFmpegPath ffmpeg 可执行文件（可为绝对路径）。
	FFmpegPath string
	// SamplePath MVP 默认循环播放的本地样片。
	SamplePath string
	// SourceURL 直播输入源覆盖（优先级高于设备表里的 rtsp_url）。
	// 支持 http(s)://（网关直播接入面）、rtsp:// 以及本地文件路径。
	SourceURL string
	// InputFormat 强制指定输入封装格式（如 mpegts），留空由 FFmpeg 自动探测。
	InputFormat string
	// RTPHost / RTPPort 本地 RTP 中转口，FFmpeg 输出到这里，Pion 再读走。
	RTPHost string
	RTPPort int
	// ForceGPU 取值见 GPUAuto / GPUForce / GPUDisable。
	ForceGPU string
	// STUNServers 可选 STUN 列表（局域网演示可留空）。
	STUNServers []string
	// TelemetryInterval WebSocket 指标推送间隔。
	TelemetryInterval time.Duration
	// AutoRestart 转码进程异常退出后是否自动拉起。
	AutoRestart bool
}

// Load 从环境变量装载配置，未提供时使用可直接跑通的 MVP 默认值。
func Load() *Config {
	cfg := &Config{
		Addr:       env("NEXUS_ADDR", ":8080"),
		DBPath:     env("NEXUS_DB", "data/nexus.db"),
		WebDist:    env("WEB_DIST", "web/dist"),
		FFmpegPath: env("FFMPEG_PATH", "ffmpeg"),
		SamplePath: env("NEXUS_SAMPLE", "testdata/sample.mp4"),
		// NEXUS_INPUT_URL 是端到端串联的入口：指向网关直播接入面
		//   例如 http://127.0.0.1:8082/ingest/live/cam0
		SourceURL:         env("NEXUS_INPUT_URL", env("NEXUS_SOURCE", "")),
		InputFormat:       env("NEXUS_INPUT_FORMAT", ""),
		RTPHost:           env("NEXUS_RTP_HOST", "127.0.0.1"),
		RTPPort:           envInt("NEXUS_RTP_PORT", 5004),
		ForceGPU:          normalizeGPU(os.Getenv("FORCE_GPU")),
		STUNServers:       splitCSV(os.Getenv("NEXUS_STUN")),
		TelemetryInterval: time.Duration(envInt("NEXUS_TELEMETRY_MS", 1000)) * time.Millisecond,
		AutoRestart:       envBool("NEXUS_AUTO_RESTART", true),
	}
	if cfg.TelemetryInterval < 200*time.Millisecond {
		cfg.TelemetryInterval = time.Second
	}
	return cfg
}

// RTPAddr 返回 FFmpeg 的 RTP 推流目标地址（不含 scheme）。
func (c *Config) RTPAddr() string {
	return fmt.Sprintf("%s:%d", c.RTPHost, c.RTPPort)
}

// ForceGPUExplicit 返回是否显式指定了 GPU 策略（用于状态展示）。
func (c *Config) ForceGPUExplicit() bool {
	return c.ForceGPU != GPUAuto
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func envBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

func normalizeGPU(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return GPUAuto
	case "1", "true", "yes", "on", "gpu":
		return GPUForce
	case "0", "false", "no", "off", "cpu":
		return GPUDisable
	default:
		return GPUAuto
	}
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
