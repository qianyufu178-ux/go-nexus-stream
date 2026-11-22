package config

import (
	"errors"
	"strings"
	"testing"
)

// TestNVENCFailureHint 覆盖真实出现过的几类 NVENC 报错，确保探针结论不会
// 把「FFmpeg 版本过旧 / 驱动不兼容」误判成「宿主机没有显卡」。
func TestNVENCFailureHint(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   string
	}{
		{
			// 本机实测：FFmpeg 4.1.0（N-92722, 2018）+ RTX 3060 驱动 610.88。
			name:   "旧版 FFmpeg 与新驱动不兼容",
			output: "[h264_nvenc @ 000001] Cannot get the preset configuration: unsupported param (12)",
			want:   "FFmpeg 版本过旧",
		},
		{
			name:   "宿主机没有可用 N 卡",
			output: "[h264_nvenc @ 000001] No capable devices found",
			want:   "未找到可用的 NVIDIA 显卡",
		},
		{
			name:   "驱动文件缺失",
			output: "[h264_nvenc @ 000001] Cannot load nvcuda.dll",
			want:   "未找到可用的 NVIDIA 显卡",
		},
		{
			name:   "显存不足",
			output: "[h264_nvenc @ 000001] OpenEncodeSessionEx failed: out of memory (10)",
			want:   "显存不足",
		},
		{
			name:   "探针超时被杀",
			output: "signal: killed",
			want:   "10 秒内未完成",
		},
		{
			name:   "未识别的报错",
			output: "something unexpected happened",
			want:   "请查看启动日志",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NVENCFailureHint(tc.output)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("NVENCFailureHint(%q) = %q，期望包含 %q", tc.output, got, tc.want)
			}
		})
	}
}

// TestCompactTruncatesLongOutput 保证超长 stderr 被截断，避免撑爆日志与接口。
func TestCompactTruncatesLongOutput(t *testing.T) {
	long := strings.Repeat("x", 300)
	got := compact(long, errors.New("exit status 1"))

	if len(got) != 243 {
		t.Fatalf("期望截断为 240+3 字节，实际 %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("期望以 ... 结尾，实际 %q", got)
	}
}

// TestCompactFallsBackToError 保证 FFmpeg 没有任何输出时仍能给出原因。
func TestCompactFallsBackToError(t *testing.T) {
	got := compact("   ", errors.New("exit status 1"))
	if got != "exit status 1" {
		t.Fatalf("期望回退到 err，实际 %q", got)
	}
}
