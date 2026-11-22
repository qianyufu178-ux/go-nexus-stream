package media

import (
	"strings"
	"testing"
)

// TestFitToSourceNeverUpscales 覆盖本机真实场景：采集端只有 720P30 时，
// 转码器不能把画面硬放大成 1080P60（这是「看起来发糊」的头号原因）。
func TestFitToSourceNeverUpscales(t *testing.T) {
	got := ModeGPU.Spec(true).FitToSource(1280, 720, 30)

	if got.Resolution != "1280x720" {
		t.Fatalf("分辨率期望 1280x720，实际 %s", got.Resolution)
	}
	if got.FPS != 30 || got.GOP != 30 {
		t.Fatalf("帧率/GOP 期望 30/30，实际 %d/%d", got.FPS, got.GOP)
	}
	// 1280×720×30×0.12 bpp ≈ 3318 kbps
	if got.BitrateKbps != 3318 {
		t.Fatalf("码率期望 3318 kbps，实际 %d", got.BitrateKbps)
	}
	if !strings.Contains(got.Description, "720P30") || !strings.Contains(got.Description, "按源自适应") {
		t.Fatalf("描述未体现收敛后的真实规格：%q", got.Description)
	}
	if !got.Hardware {
		t.Fatalf("收敛不应改变硬件标记")
	}
}

// TestFitToSourceDownscalesLargeSource 保证大源仍按轨道上限降采样，且码率不超过上限。
func TestFitToSourceDownscalesLargeSource(t *testing.T) {
	got := ModeGPU.Spec(true).FitToSource(2560, 1440, 60)

	if got.Resolution != "1920x1080" || got.FPS != 60 {
		t.Fatalf("期望 1920x1080@60，实际 %s@%d", got.Resolution, got.FPS)
	}
	if got.BitrateKbps != gpuBitrateMaxKbps {
		t.Fatalf("码率应被夹到上限 %d，实际 %d", gpuBitrateMaxKbps, got.BitrateKbps)
	}
	if strings.Contains(got.Description, "按源自适应") {
		t.Fatalf("与标称一致时不应改写描述：%q", got.Description)
	}
}

// TestFitToSourceKeepsAspect 覆盖超宽源：不能为了填满 16:9 把画面拉变形。
func TestFitToSourceKeepsAspect(t *testing.T) {
	got := ModeGPU.Spec(true).FitToSource(3840, 1600, 60)

	if got.Resolution != "1920x800" {
		t.Fatalf("期望等比缩放为 1920x800，实际 %s", got.Resolution)
	}
	w, h, ok := ParseResolution(got.Resolution)
	if !ok || w*1600 != h*3840 {
		t.Fatalf("缩放后宽高比失真：%s", got.Resolution)
	}
}

// TestFitToSourceFallsBackWithoutCaps 保证 ffprobe 探针失败时行为与升级前一致。
func TestFitToSourceFallsBackWithoutCaps(t *testing.T) {
	nominal := ModeGPU.Spec(true)
	got := nominal.FitToSource(0, 0, 0)

	if got != nominal {
		t.Fatalf("无源参数时应原样返回标称规格：%+v", got)
	}
}

// TestFitToSourceLeavesCPUTrackBitrate CPU 轨是故意压低的降级轨，
// 只跟随分辨率/帧率收敛，不参与 GPU 轨的 bpp 码率重算。
func TestFitToSourceLeavesCPUTrackBitrate(t *testing.T) {
	got := ModeCPU.Spec(false).FitToSource(1280, 720, 30)

	if got.Resolution != "960x540" || got.FPS != 12 {
		t.Fatalf("期望 960x540@12，实际 %s@%d", got.Resolution, got.FPS)
	}
	if got.BitrateKbps != ModeCPU.Spec(false).BitrateKbps {
		t.Fatalf("CPU 轨码率不应被改写，实际 %d", got.BitrateKbps)
	}
}

// TestFitToSourceAlignsEvenDimensions H.264 yuv420p 要求宽高为偶数。
func TestFitToSourceAlignsEvenDimensions(t *testing.T) {
	got := ModeGPU.Spec(true).FitToSource(1279, 719, 30)
	if got.Resolution != "1278x718" {
		t.Fatalf("期望对齐为 1278x718，实际 %s", got.Resolution)
	}
}

// TestParseResolution 覆盖规格串解析的成功/失败分支。
func TestParseResolution(t *testing.T) {
	cases := []struct {
		in     string
		w, h   int
		wantOK bool
	}{
		{"1920x1080", 1920, 1080, true},
		{"1280X720", 1280, 720, true},
		{" 960x540 ", 960, 540, true},
		{"1920", 0, 0, false},
		{"x1080", 0, 0, false},
		{"0x1080", 0, 0, false},
		{"abcxdef", 0, 0, false},
		{"", 0, 0, false},
	}

	for _, tc := range cases {
		w, h, ok := ParseResolution(tc.in)
		if ok != tc.wantOK || w != tc.w || h != tc.h {
			t.Fatalf("ParseResolution(%q) = (%d,%d,%v)，期望 (%d,%d,%v)", tc.in, w, h, ok, tc.w, tc.h, tc.wantOK)
		}
	}
}

// TestParseProbeCSV 解析 ffprobe `-of csv=p=0` 的真实输出。
func TestParseProbeCSV(t *testing.T) {
	caps := parseProbeCSV("1280,720,30/1\r\n")
	if !caps.valid() || caps.width != 1280 || caps.height != 720 || caps.fps != 30 {
		t.Fatalf("解析结果错误：%+v", caps)
	}

	// 29.97fps 摄像机：四舍五入到 30，避免把 60fps 轨道留成半速。
	caps = parseProbeCSV("1920,1080,30000/1001\n")
	if caps.fps != 30 {
		t.Fatalf("29.97fps 期望归整为 30，实际 %d", caps.fps)
	}

	// 缺字段 / 脏输出时返回零值，调用方回退标称规格。
	if got := parseProbeCSV("N/A\n"); got.valid() {
		t.Fatalf("脏输出不应产出有效源参数：%+v", got)
	}
	if got := parseProbeCSV(""); got.valid() {
		t.Fatalf("空输出不应产出有效源参数：%+v", got)
	}
}

// TestParseFrameRate 覆盖分数帧率与异常输入。
func TestParseFrameRate(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"30/1", 30},
		{"30000/1001", 30},
		{"60000/1001", 60},
		{"0/0", 0},
		{"", 0},
		{"abc", 0},
	}

	for _, tc := range cases {
		if got := parseFrameRate(tc.in); got != tc.want {
			t.Fatalf("parseFrameRate(%q) = %d，期望 %d", tc.in, got, tc.want)
		}
	}
}
