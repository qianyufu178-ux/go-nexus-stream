// Package telemetry 生成推送给 Vue 3 大屏的实时转码指标。
//
// MVP 阶段指标由「目标规格 + 随机游走」模拟，保证曲线自然抖动，
// 后续接入 FFmpeg 真实 -progress 解析时只需替换 Simulator.Next 的实现。
package telemetry

import (
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/nexus-stream/nexus-core/internal/model"
)

// Snapshot 是一条 WebSocket 遥测数据包。
//
// 前 8 个字段为《技术规划 3.5》约定协议，顺序与命名保持完全一致；
// 其余为向后兼容的扩展字段，前端可选用。
type Snapshot struct {
	Timestamp     string `json:"timestamp"`
	DeviceName    string `json:"device_name"`
	TranscodeMode string `json:"transcode_mode"`
	CurrentCodec  string `json:"current_codec"`
	Resolution    string `json:"resolution"`
	FPS           int    `json:"fps"`
	BitrateKbps   int    `json:"bitrate_kbps"`
	LossRate      string `json:"loss_rate"`

	StreamKey           string `json:"stream_key,omitempty"`
	DirectionType       string `json:"direction_type,omitempty"`
	Source              string `json:"source,omitempty"`
	HardwareAccelerated bool   `json:"hardware_accelerated"`
	TranscoderRunning   bool   `json:"transcoder_running"`
	MediaActive         bool   `json:"media_active"`
	UptimeSeconds       int64  `json:"uptime_seconds"`
	Notice              string `json:"notice,omitempty"`
	GeneratedAt         int64  `json:"generated_at"`
}

// Target 描述当前轨道应该收敛到的指标中心值。
type Target struct {
	Mode                string
	Codec               string
	Resolution          string
	FPS                 int
	BitrateKbps         int
	HardwareAccelerated bool
}

// Env 为生成一条快照所需的运行环境信息。
type Env struct {
	Device            model.Device
	MediaActive       bool
	TranscoderRunning bool
	Uptime            time.Duration
	Notice            string
}

// Simulator 负责让指标在合理区间内平滑抖动，制造真实感。
type Simulator struct {
	mu      sync.Mutex
	mode    string
	fps     float64
	bitrate float64
	loss    float64
}

// NewSimulator 构造指标模拟器。
func NewSimulator() *Simulator {
	return &Simulator{
		mode: "",
		loss: 0.22,
	}
}

// Next 生成下一条遥测快照。
func (s *Simulator) Next(target Target, env Env) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 轨道切换时立刻收敛到新规格，保证大屏 1 秒内看到 60 -> 12 的断崖式下跌。
	if s.mode != target.Mode {
		s.mode = target.Mode
		s.fps = float64(target.FPS)
		s.bitrate = float64(target.BitrateKbps)
	}

	fpsBand := band{min: float64(target.FPS) - 2, max: float64(target.FPS)}
	if target.FPS <= 15 {
		fpsBand = band{min: float64(target.FPS) - 1, max: float64(target.FPS)}
	}

	bitrateBand := band{min: float64(target.BitrateKbps) - 250, max: float64(target.BitrateKbps) + 250}
	if target.BitrateKbps < 1500 {
		bitrateBand = band{min: float64(target.BitrateKbps) - 50, max: float64(target.BitrateKbps) + 50}
	}

	// 随机游走 + 均值回归，既抖动又不飘出区间。
	s.fps = walk(s.fps, float64(target.FPS), 0.35, fpsBand)
	s.bitrate = walk(s.bitrate, float64(target.BitrateKbps), 90, bitrateBand)
	s.loss = walk(s.loss, 0.22, 0.05, band{min: 0.05, max: 0.45})

	now := time.Now()
	return Snapshot{
		Timestamp:           now.Format("15:04:05"),
		DeviceName:          env.Device.DeviceName,
		TranscodeMode:       target.Mode,
		CurrentCodec:        target.Codec,
		Resolution:          target.Resolution,
		FPS:                 int(math.Round(s.fps)),
		BitrateKbps:         int(math.Round(s.bitrate)),
		LossRate:            fmt.Sprintf("%.2f%%", s.loss),
		StreamKey:           env.Device.StreamKey,
		DirectionType:       env.Device.DirectionType,
		Source:              env.Device.RTSPURL,
		HardwareAccelerated: target.HardwareAccelerated,
		TranscoderRunning:   env.TranscoderRunning,
		MediaActive:         env.MediaActive,
		UptimeSeconds:       int64(env.Uptime.Seconds()),
		Notice:              env.Notice,
		GeneratedAt:         now.UnixMilli(),
	}
}

// band 是取值区间。
type band struct {
	min float64
	max float64
}

// walk 执行一次带均值回归的随机游走，并裁剪到区间内。
func walk(current, center, step float64, b band) float64 {
	next := current + (rand.Float64()*2-1)*step
	next += (center - next) * 0.25
	if next < b.min {
		next = b.min
	}
	if next > b.max {
		next = b.max
	}
	return next
}
