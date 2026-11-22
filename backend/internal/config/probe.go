package config

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/nexus-stream/nexus-core/internal/executil"
)

// ProbeResult 记录双轨制硬件探针的结论。
type ProbeResult struct {
	// FFmpegPath 实际使用的 ffmpeg 可执行文件。
	FFmpegPath string `json:"ffmpeg_path"`
	// FFmpegAvailable 是否能在当前宿主找到并执行 ffmpeg。
	FFmpegAvailable bool `json:"ffmpeg_available"`
	// FFmpegVersion 版本首行信息。
	FFmpegVersion string `json:"ffmpeg_version,omitempty"`
	// NVENCCompiled ffmpeg 是否编译进了 h264_nvenc 编码器。
	NVENCCompiled bool `json:"nvenc_compiled"`
	// NVENCAvailable 是否真的能用 N 卡跑通一次最小硬编码（Mac / 无卡笔记本为 false）。
	NVENCAvailable bool `json:"nvenc_available"`
	// Detail 人类可读的探测结论，直接展示在大屏控制台。
	Detail string `json:"detail"`
	// Error 探测过程中的错误摘要。
	Error string `json:"error,omitempty"`
	// ProbedAt 探测时间。
	ProbedAt time.Time `json:"probed_at"`
}

// ProbeFFmpeg 执行「双轨制硬件探针」：
//  1. 检测 ffmpeg 是否存在；
//  2. 检测是否编译了 h264_nvenc；
//  3. 真机跑一次 5 帧 NVENC 最小硬编码，失败即判定无可用 GPU。
//
// 整个过程最多阻塞 10 秒，超时后按「无 GPU」处理，绝不阻塞服务启动。
func ProbeFFmpeg(parent context.Context, ffmpegPath string) ProbeResult {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	res := ProbeResult{
		FFmpegPath: ffmpegPath,
		ProbedAt:   time.Now(),
	}

	versionOut, err := run(ctx, ffmpegPath, "-hide_banner", "-version")
	if err != nil {
		res.Detail = "未检测到 FFmpeg，转码管线将不会启动（大屏指标仍可演示）"
		res.Error = err.Error()
		return res
	}
	res.FFmpegAvailable = true
	res.FFmpegVersion = firstLine(versionOut)

	encodersOut, err := run(ctx, ffmpegPath, "-hide_banner", "-encoders")
	if err != nil {
		res.Detail = "FFmpeg 可用，但读取编码器列表失败，按无 GPU 处理"
		res.Error = err.Error()
		return res
	}
	if !strings.Contains(encodersOut, "h264_nvenc") {
		res.Detail = "FFmpeg 未编译 h264_nvenc，走 CPU 抽帧自愈轨道"
		return res
	}
	res.NVENCCompiled = true

	testOut, err := run(ctx, ffmpegPath,
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=256x144:r=10",
		"-frames:v", "5",
		"-c:v", "h264_nvenc",
		"-f", "null", "-",
	)
	if err != nil {
		// 注意：这里不能直接写成「无可用 NVIDIA 显卡」。宿主机可能有卡，
		// 只是 FFmpeg 的 NVENC 封装与当前驱动不兼容（旧版本 FFmpeg 常见），
		// 必须把原始报错翻译出来，否则会被误读成「这台机器没有显卡」。
		res.Detail = "NVENC 硬编码测试失败，自动降级为 CPU 自愈轨道（" + NVENCFailureHint(testOut) + "）"
		res.Error = compact(testOut, err)
		return res
	}

	res.NVENCAvailable = true
	res.Detail = "检测到可用 NVIDIA NVENC 硬编码引擎，已激活 GPU 双轨加速"
	return res
}

// run 执行命令并返回合并输出。
func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	executil.Prepare(cmd)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	out := strings.TrimSpace(buf.String())
	if err != nil {
		if out == "" {
			out = err.Error()
		}
		return out, fmt.Errorf("%w: %s", err, out)
	}
	return out, nil
}

func firstLine(s string) string {
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}

// NVENCFailureHint 把 FFmpeg 的 NVENC 原始报错翻译成可执行的排查结论，
// 用于区分「宿主机没有 NVIDIA 显卡」「驱动不兼容」「显存不足」等不同原因。
func NVENCFailureHint(output string) string {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "cannot get the preset configuration"),
		strings.Contains(lower, "unsupported param"):
		return "FFmpeg 的 NVENC 封装与当前 NVIDIA 驱动不兼容，通常说明 FFmpeg 版本过旧，请升级 FFmpeg"
	case strings.Contains(lower, "no capable devices"),
		strings.Contains(lower, "no cuda-capable"),
		strings.Contains(lower, "cannot load nvcuda"),
		strings.Contains(lower, "cannot open nvcuda"),
		strings.Contains(lower, "nvenc is not supported"):
		return "未找到可用的 NVIDIA 显卡或驱动，请确认驱动已安装、显卡未被独占"
	case strings.Contains(lower, "out of memory"),
		strings.Contains(lower, "insufficient"):
		return "显存不足，请释放显卡占用后重试"
	case strings.Contains(lower, "unknown encoder"):
		return "当前 FFmpeg 未启用 h264_nvenc，请更换带 NVENC 的构建"
	case strings.Contains(lower, "deadline exceeded"),
		strings.Contains(lower, "killed"):
		return "NVENC 测试 10 秒内未完成，可能是显卡被占用或驱动无响应"
	default:
		return "请查看启动日志中的 FFmpeg 原始报错"
	}
}

func compact(out string, err error) string {
	msg := firstLine(strings.TrimSpace(out))
	if msg == "" {
		msg = err.Error()
	}
	if len(msg) > 240 {
		msg = msg[:240] + "..."
	}
	return msg
}
