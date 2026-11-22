// Package media 实现双轨制 FFmpeg 转码调度、RTP 中转与 Pion WebRTC 分发。
package media

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Mode 是双轨制转码轨道标识，与前端 / WebSocket 协议严格对齐。
type Mode string

const (
	// ModeGPU GPU_ACCELERATION：显卡托管的高性能轨道（1080P60）。
	ModeGPU Mode = "GPU_ACCELERATION"
	// ModeCPU CPU_SELF_HEALING：无卡时的 CPU 抽帧自愈降级轨道（540P12）。
	ModeCPU Mode = "CPU_SELF_HEALING"
)

// Valid 判断模式字符串是否合法。
func (m Mode) Valid() bool { return m == ModeGPU || m == ModeCPU }

// ParseMode 宽松解析模式字符串（兼容 gpu / cpu 简写）。
func ParseMode(raw string) (Mode, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "gpu_acceleration", "gpu", "nvenc", "hardware":
		return ModeGPU, true
	case "cpu_self_healing", "cpu", "self_healing", "degraded":
		return ModeCPU, true
	default:
		return "", false
	}
}

// Toggle 返回另一条轨道的模式（用于演示按钮一键切换）。
func (m Mode) Toggle() Mode {
	if m == ModeGPU {
		return ModeCPU
	}
	return ModeGPU
}

// InitialMode 依据 FORCE_GPU 策略与硬件探针结果决定启动轨道。
//
// FORCE_GPU=true  时即便无卡也进 GPU_ACCELERATION（用 libx264 仿真高帧率流）；
// FORCE_GPU=false 时强制 CPU_SELF_HEALING；
// auto           时按探针结果自适应。
func InitialMode(policy string, hardwareAvailable bool) Mode {
	switch policy {
	case "true":
		return ModeGPU
	case "false":
		return ModeCPU
	default:
		if hardwareAvailable {
			return ModeGPU
		}
		return ModeCPU
	}
}

// Spec 描述某条轨道的输出规格（同时用于 FFmpeg 参数拼装与指标上报）。
type Spec struct {
	// Codec 上报给大屏的编码器名称，如 "H.264 (NVENC)"。
	Codec string
	// Encoder ffmpeg 的 -c:v 取值。
	Encoder string
	// Resolution 输出分辨率，如 1920x1080。
	Resolution string
	// FPS 输出帧率。
	FPS int
	// BitrateKbps 目标码率。
	BitrateKbps int
	// GOP 关键帧间隔（帧数），保持 1 秒一个 IDR 方便 WebRTC 秒开。
	GOP int
	// Hardware 是否真正跑在显卡上（false 表示演示容错用的 libx264 仿真）。
	Hardware bool
	// Description 人类可读描述。
	Description string
}

// Spec 返回该轨道在当前硬件条件下的输出规格。
func (m Mode) Spec(hardware bool) Spec {
	if m == ModeGPU {
		spec := Spec{
			Codec:       "H.264 (NVENC)",
			Encoder:     "h264_nvenc",
			Resolution:  "1920x1080",
			FPS:         60,
			BitrateKbps: 6000,
			GOP:         60,
			Hardware:    hardware,
			Description: "GPU 高性能硬转轨道 · 1080P60",
		}
		if !hardware {
			// 宿主机无 N 卡但处于 FORCE_GPU 演示模式：自动回退 libx264，
			// 保持 1080P60 输出与 GPU 指标口径，避免 FFmpeg 直接报错退出。
			spec.Encoder = "libx264"
			spec.Description = "GPU 高性能轨道（无卡仿真 · libx264 1080P60）"
		}
		return spec
	}

	return Spec{
		Codec:       "H.264 (libx264)",
		Encoder:     "libx264",
		Resolution:  "960x540",
		FPS:         12,
		BitrateKbps: 1200,
		GOP:         12,
		Hardware:    false,
		Description: "CPU 抽帧自愈降级轨道 · 540P12",
	}
}

// GPU 轨的码率不再写死，而是按「像素 × 帧率 × bpp」重算：
// 小源不浪费码率，1080P60 这类大画面也不会被固定 4 Mbps 压糊。
const (
	// gpuBitsPerPixelFrame 每像素每帧分配的目标比特数（0.12 ≈ NVENC p5 的准无损区间）。
	gpuBitsPerPixelFrame = 0.12
	gpuBitrateMinKbps    = 2500
	gpuBitrateMaxKbps    = 8000
)

// ParseResolution 解析 "1920x1080" 形式的规格串。
func ParseResolution(res string) (int, int, bool) {
	idx := strings.IndexAny(res, "xX")
	if idx <= 0 {
		return 0, 0, false
	}
	w, errW := strconv.Atoi(strings.TrimSpace(res[:idx]))
	h, errH := strconv.Atoi(strings.TrimSpace(res[idx+1:]))
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// FitToSource 用输入源的真实画面参数收敛轨道规格（画质第一原则）：
//
//   - 只降不升：源比轨道小时退到源分辨率，杜绝「720P 硬放大成 1080P」的假高清；
//   - 帧率同样只降不升，避免把 30fps 源复制成 60fps 白烧码率；
//   - 宽高按源等比重算，超宽 / 竖屏源不会被拉变形；
//   - GPU 轨码率按「像素 × 帧率 × 0.12 bit」重算，并夹在 2.5~8 Mbps。
//
// width/height 非法（ffprobe 探针失败）时原样返回标称规格。
func (s Spec) FitToSource(width, height, fps int) Spec {
	if width <= 0 || height <= 0 {
		return s
	}
	targetW, targetH, ok := ParseResolution(s.Resolution)
	if !ok {
		return s
	}

	scale := 1.0
	if ratio := float64(targetW) / float64(width); ratio < scale {
		scale = ratio
	}
	if ratio := float64(targetH) / float64(height); ratio < scale {
		scale = ratio
	}
	outW := evenDim(int(math.Round(float64(width) * scale)))
	outH := evenDim(int(math.Round(float64(height) * scale)))
	if outW <= 0 || outH <= 0 {
		return s
	}

	nominal := s
	s.Resolution = fmt.Sprintf("%dx%d", outW, outH)
	if fps > 0 && fps < s.FPS {
		s.FPS = fps
	}
	s.GOP = s.FPS
	if s.Encoder == "h264_nvenc" {
		kbps := int(math.Round(float64(outW) * float64(outH) * float64(s.FPS) * gpuBitsPerPixelFrame / 1000))
		s.BitrateKbps = clampInt(kbps, gpuBitrateMinKbps, gpuBitrateMaxKbps)
	}
	if s.Resolution != nominal.Resolution || s.FPS != nominal.FPS {
		// 描述里带上收敛后的真实规格，大屏上的「1080P60」不会再与实际画面打架。
		head, _, _ := strings.Cut(nominal.Description, " · ")
		s.Description = fmt.Sprintf("%s · %dP%d（按源自适应）", head, outH, s.FPS)
	}
	return s
}

// evenDim 向下对齐到偶数：H.264（yuv420p）要求宽高均为偶数。
func evenDim(v int) int {
	if v < 2 {
		return 0
	}
	return v - v%2
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
