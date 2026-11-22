package media

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nexus-stream/nexus-core/internal/executil"
)

// lavfiFallback 在既没有样片也没有 RTSP 地址时，用 FFmpeg 内置测试图卡兜底，
// 保证「无素材」也能录出完整演示视频。
const lavfiFallback = "testsrc2=size=1280x720:rate=30"

// Transcoder 是 FFmpeg 双轨转码调度器：负责进程生命周期、热切换、异常自愈。
type Transcoder struct {
	mu sync.RWMutex

	ffmpegPath  string
	input       string
	samplePath  string
	inputFormat string
	rtpAddr     string
	hardware    bool
	mode        Mode
	autoRestart bool

	started  bool
	running  bool
	cmd      *exec.Cmd
	restarts int

	lastStart time.Time
	lastExit  time.Time
	lastError string
	lastArgs  []string

	// srcCaps 缓存输入源的真实分辨率/帧率，用于把轨道规格收敛成「只降不升」。
	srcCaps sourceCaps

	restartCh chan struct{}
	stopCh    chan struct{}
	stopOnce  sync.Once

	onRestart func()
	onMode    []func(Mode)

	logs *logRing
}

// Status 是转码器的对外快照（/api/status 使用）。
type Status struct {
	Mode                string    `json:"mode"`
	ModeDescription     string    `json:"mode_description"`
	Running             bool      `json:"running"`
	Started             bool      `json:"started"`
	HardwareAccelerated bool      `json:"hardware_accelerated"`
	Encoder             string    `json:"encoder"`
	Codec               string    `json:"codec"`
	Resolution          string    `json:"resolution"`
	TargetFPS           int       `json:"target_fps"`
	TargetBitrateKbps   int       `json:"target_bitrate_kbps"`
	Restarts            int       `json:"restarts"`
	LastError           string    `json:"last_error,omitempty"`
	LastStartAt         time.Time `json:"last_start_at,omitempty"`
	LastExitAt          time.Time `json:"last_exit_at,omitempty"`
	FFmpegPath          string    `json:"ffmpeg_path"`
	Input               string    `json:"input"`
	InputKind           string    `json:"input_kind"`
	SourceCaps          string    `json:"source_caps,omitempty"`
	Args                []string  `json:"args,omitempty"`
	LogTail             string    `json:"log_tail,omitempty"`
}

// NewTranscoder 构造转码调度器。
//
// input 为设备配置里的 rtsp_url 字段：rtsp:// 前缀走真实拉流，其余按本地样片处理。
func NewTranscoder(ffmpegPath, input, samplePath, rtpAddr string, hardware bool, initial Mode, autoRestart bool) *Transcoder {
	return &Transcoder{
		ffmpegPath:  ffmpegPath,
		input:       input,
		samplePath:  samplePath,
		rtpAddr:     rtpAddr,
		hardware:    hardware,
		mode:        initial,
		autoRestart: autoRestart,
		restartCh:   make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
		logs:        newLogRing(60),
	}
}

// SetRestartHook 注册「新进程拉起前」的回调（用于重置 RTP 中转的时间戳基准）。
func (t *Transcoder) SetRestartHook(fn func()) {
	t.mu.Lock()
	t.onRestart = fn
	t.mu.Unlock()
}

// SetInputFormat 强制指定输入封装格式（如 mpegts），留空表示由 FFmpeg 自动探测。
func (t *Transcoder) SetInputFormat(format string) {
	t.mu.Lock()
	t.inputFormat = strings.TrimSpace(format)
	t.mu.Unlock()
}

// AddModeListener 注册模式变更回调。
func (t *Transcoder) AddModeListener(fn func(Mode)) {
	t.mu.Lock()
	t.onMode = append(t.onMode, fn)
	t.mu.Unlock()
}

// CurrentMode 返回当前轨道。
func (t *Transcoder) CurrentMode() Mode {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.mode
}

// CurrentSpec 返回当前轨道规格（已按输入源收敛：只降不升）。
func (t *Transcoder) CurrentSpec() Spec {
	t.mu.RLock()
	mode, hw, caps := t.mode, t.hardware, t.srcCaps
	t.mu.RUnlock()
	return mode.Spec(hw).FitToSource(caps.width, caps.height, caps.fps)
}

// HardwareAccelerated 表示是否真的跑在 N 卡上。
func (t *Transcoder) HardwareAccelerated() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.hardware
}

// Start 启动转码守护协程（幂等）。
func (t *Transcoder) Start(ctx context.Context) {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return
	}
	t.started = true
	t.mu.Unlock()

	go t.loop(ctx)
	go func() {
		// 服务退出时兜底 Kill，避免 ffmpeg 进程泄漏。
		select {
		case <-ctx.Done():
		case <-t.stopCh:
		}
		t.mu.RLock()
		cmd := t.cmd
		t.mu.RUnlock()
		if cmd != nil {
			executil.Kill(cmd)
		}
	}()
}

// Stop 停止转码进程与守护协程。
func (t *Transcoder) Stop() {
	t.stopOnce.Do(func() {
		close(t.stopCh)
	})
	t.mu.RLock()
	cmd := t.cmd
	t.mu.RUnlock()
	if cmd != nil {
		executil.Kill(cmd)
	}
}

// SwitchMode 运行时热切换双轨模式：杀死旧进程 -> 守护协程按新参数重新拉起。
func (t *Transcoder) SwitchMode(mode Mode) error {
	if !mode.Valid() {
		return fmt.Errorf("非法转码模式: %s", mode)
	}

	t.mu.Lock()
	if t.mode == mode {
		t.mu.Unlock()
		return nil
	}
	t.mode = mode
	cmd := t.cmd
	listeners := append([]func(Mode){}, t.onMode...)
	t.mu.Unlock()

	for _, fn := range listeners {
		fn(mode)
	}

	// 唤醒守护协程，立即用新参数重建管道。
	select {
	case t.restartCh <- struct{}{}:
	default:
	}

	if cmd != nil {
		executil.Kill(cmd)
	}
	return nil
}

// ToggleMode 在两条轨道之间切换，返回切换后的模式。
func (t *Transcoder) ToggleMode() Mode {
	next := t.CurrentMode().Toggle()
	_ = t.SwitchMode(next)
	return next
}

// Status 返回当前转码器快照。
func (t *Transcoder) Status() Status {
	t.mu.RLock()
	defer t.mu.RUnlock()

	spec := t.mode.Spec(t.hardware).FitToSource(t.srcCaps.width, t.srcCaps.height, t.srcCaps.fps)
	kind, input := resolveInput(t.input, t.samplePath)
	return Status{
		Mode:                string(t.mode),
		ModeDescription:     spec.Description,
		Running:             t.running,
		Started:             t.started,
		HardwareAccelerated: t.hardware && t.mode == ModeGPU,
		Encoder:             spec.Encoder,
		Codec:               spec.Codec,
		Resolution:          spec.Resolution,
		TargetFPS:           spec.FPS,
		TargetBitrateKbps:   spec.BitrateKbps,
		Restarts:            t.restarts,
		LastError:           t.lastError,
		LastStartAt:         t.lastStart,
		LastExitAt:          t.lastExit,
		FFmpegPath:          t.ffmpegPath,
		Input:               input,
		InputKind:           kind,
		SourceCaps:          t.srcCaps.String(),
		Args:                append([]string{}, t.lastArgs...),
		LogTail:             t.logs.tail(12),
	}
}

// loop 是转码守护协程：拉起进程 -> 等待退出 -> 判定是否需要重启。
func (t *Transcoder) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stopCh:
			return
		default:
		}

		args := t.buildArgs(t.CurrentMode())

		t.mu.Lock()
		t.lastArgs = args
		hook := t.onRestart
		t.mu.Unlock()

		if hook != nil {
			// 新进程会重新生成 SSRC / 时间戳基准，通知中转层重置。
			hook()
		}

		err := t.runOnce(ctx, args)
		if ctx.Err() != nil {
			return
		}
		select {
		case <-t.stopCh:
			return
		default:
		}

		if err != nil {
			t.setLastError(err.Error())
		}

		// 模式热切换：立刻重启，不再等待。
		select {
		case <-t.restartCh:
			continue
		default:
		}

		if !t.autoRestart {
			return
		}

		t.mu.Lock()
		t.restarts++
		count := t.restarts
		t.mu.Unlock()
		t.logs.add(fmt.Sprintf("[supervisor] 转码进程退出，2 秒后自动拉起（第 %d 次）", count))

		select {
		case <-ctx.Done():
			return
		case <-t.stopCh:
			return
		case <-t.restartCh:
			continue
		case <-time.After(2 * time.Second):
		}
	}
}

// runOnce 启动一次 ffmpeg 并阻塞到进程退出。
func (t *Transcoder) runOnce(ctx context.Context, args []string) error {
	select {
	case <-t.restartCh: // 清空上一轮遗留的切换信号
	default:
	}

	cmd := exec.CommandContext(ctx, t.ffmpegPath, args...)
	executil.Prepare(cmd)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("创建 stderr 管道失败: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建 stdout 管道失败: %w", err)
	}

	if err := cmd.Start(); err != nil {
		t.mu.Lock()
		t.running = false
		t.cmd = nil
		t.lastError = fmt.Sprintf("启动 FFmpeg 失败: %v", err)
		t.mu.Unlock()
		t.logs.add("[supervisor] 启动 FFmpeg 失败: " + err.Error())
		return err
	}

	t.mu.Lock()
	t.cmd = cmd
	t.running = true
	t.lastStart = time.Now()
	t.lastError = ""
	t.mu.Unlock()
	t.logs.add(fmt.Sprintf("[supervisor] FFmpeg 已启动 (mode=%s, pid=%d)", t.CurrentMode(), cmd.Process.Pid))

	var wg sync.WaitGroup
	wg.Add(2)
	go t.drain(&wg, stderr, true)
	go t.drain(&wg, stdout, false)

	waitErr := cmd.Wait()
	wg.Wait()

	t.mu.Lock()
	t.running = false
	t.cmd = nil
	t.lastExit = time.Now()
	t.mu.Unlock()

	if waitErr != nil && ctx.Err() == nil {
		return fmt.Errorf("FFmpeg 退出: %v", waitErr)
	}
	return nil
}

// drain 读取 ffmpeg 输出并写入环形日志。
func (t *Transcoder) drain(wg *sync.WaitGroup, r io.Reader, keep bool) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if keep {
			t.logs.add(line)
		}
	}
}

// buildArgs 拼装 ffmpeg 参数：输入源处理 + 双轨编码参数 + 标准 RTP 输出。
func (t *Transcoder) buildArgs(mode Mode) []string {
	t.mu.RLock()
	hardware := t.hardware
	input := t.input
	sample := t.samplePath
	inputFormat := t.inputFormat
	rtpAddr := t.rtpAddr
	t.mu.RUnlock()

	kind, src := resolveInput(input, sample)
	spec := mode.Spec(hardware)
	if kind != "lavfi" {
		// 用 ffprobe 读到的源参数收敛规格：只降不升，避免 720P 源被硬放大成 1080P 糊图。
		caps := t.ensureSourceCaps(src)
		spec = spec.FitToSource(caps.width, caps.height, caps.fps)
	}

	args := []string{"-hide_banner", "-nostdin", "-loglevel", "warning"}

	switch kind {
	case "rtsp":
		args = append(args, "-rtsp_transport", "tcp", "-i", src)
	case "http":
		// 网关直播接入面 / 任意 HTTP 直播源：
		// 关闭输入缓冲 + 失败自动重连，摄像头或网关抖动时转码进程不会直接退出。
		args = append(args,
			"-fflags", "nobuffer",
			"-flags", "low_delay",
			"-reconnect", "1",
			"-reconnect_streamed", "1",
			"-reconnect_delay_max", "2",
			"-rw_timeout", "15000000",
			"-analyzeduration", "0",
			"-probesize", "500000",
		)
		if inputFormat != "" {
			args = append(args, "-f", inputFormat)
		}
		args = append(args, "-i", src)
	case "file":
		// 本地样片循环播放，-re 让读取速度贴合实时，模拟直播流。
		args = append(args, "-re", "-stream_loop", "-1", "-i", src)
	default:
		args = append(args, "-re", "-f", "lavfi", "-i", lavfiFallback)
	}

	if spec.Encoder == "h264_nvenc" {
		args = append(args,
			"-c:v", "h264_nvenc",
			// p1 是「速度优先」预设，画面明显发糊；hq 调优在 720P30 上 GPU 占用依然很低。
			"-preset", "p5",
			"-tune", "hq",
			// 低延迟靠 -bf 0（帧序不重排），不要再加 -zerolatency 1：
			// FFmpeg 8 的 nvenc 在 tune=hq 与 zerolatency 同时存在时，首帧即报
			// "Failed unlocking input buffer: generic error (20)" 并退出（exit 0xB1B4B1AB），
			// 转码器会陷入无限重启。实测 tune=hq + bf=0 可稳定出帧且画质无损。
			"-bf", "0",
			"-rc", "cbr",
			// baseline 压缩效率差、同码率下细节丢得多；high profile 浏览器与 Pion 均支持。
			"-profile:v", "high",
			"-pix_fmt", "yuv420p",
			// 空间自适应量化：暗场与平坦区域少花码率，纹理区域多留细节。
			"-spatial-aq", "1",
			"-aq-strength", "8",
		)
	} else {
		args = append(args,
			"-c:v", "libx264",
			"-preset", "veryfast",
			"-tune", "zerolatency",
			"-profile:v", "main",
			"-pix_fmt", "yuv420p",
			"-sc_threshold", "0",
		)
	}

	args = append(args,
		"-an", "-sn", "-dn",
		"-b:v", strconv.Itoa(spec.BitrateKbps)+"k",
		"-maxrate", strconv.Itoa(spec.BitrateKbps)+"k",
		"-bufsize", strconv.Itoa(spec.BitrateKbps*2)+"k",
		"-r", strconv.Itoa(spec.FPS),
		// lanczos 比默认双线性锐利：真需要缩放时（大源降到轨道分辨率）细节损失更小。
		"-sws_flags", "lanczos+accurate_rnd+full_chroma_int",
		"-s", spec.Resolution,
		"-g", strconv.Itoa(spec.GOP),
		"-keyint_min", strconv.Itoa(spec.GOP),
		// dump_extra 保证每个 IDR 前都带 SPS/PPS，浏览器可随时秒开解码。
		"-bsf:v", "dump_extra=freq=keyframe",
		"-f", "rtp",
		"-payload_type", "96",
		"-muxdelay", "0",
		"-muxpreload", "0",
		"-flush_packets", "1",
		"rtp://"+rtpAddr+"?pkt_size=1200",
	)
	return args
}

func (t *Transcoder) setLastError(msg string) {
	if len(msg) > 400 {
		msg = msg[:400] + "..."
	}
	t.mu.Lock()
	t.lastError = msg
	t.mu.Unlock()
	t.logs.add("[supervisor] " + msg)
}

// sourceCaps 是输入源的真实画面参数（由 ffprobe 探针得到）。
type sourceCaps struct {
	width  int
	height int
	fps    int
}

func (c sourceCaps) valid() bool { return c.width > 0 && c.height > 0 }

func (c sourceCaps) String() string {
	if !c.valid() {
		return ""
	}
	if c.fps > 0 {
		return fmt.Sprintf("%dx%d@%dfps", c.width, c.height, c.fps)
	}
	return fmt.Sprintf("%dx%d", c.width, c.height)
}

// ensureSourceCaps 懒加载输入源参数：首次成功探到后缓存，探不到就等下次重启再试。
func (t *Transcoder) ensureSourceCaps(src string) sourceCaps {
	t.mu.RLock()
	caps := t.srcCaps
	t.mu.RUnlock()
	if caps.valid() {
		return caps
	}

	caps = t.probeSource(src)
	if !caps.valid() {
		return sourceCaps{}
	}

	t.mu.Lock()
	t.srcCaps = caps
	t.mu.Unlock()
	t.logs.add(fmt.Sprintf("[supervisor] 输入源 %s，输出规格按源收敛（只降不升）", caps))
	return caps
}

// probeSource 用 ffprobe 读取输入源首路视频的分辨率与帧率。
//
// 探针失败（源还没推上来 / 环境里没有 ffprobe）时返回零值，调用方回退标称规格，
// 不影响转码主流程 —— 画质优化不能成为链路可用性的风险。
func (t *Transcoder) probeSource(src string) sourceCaps {
	probe := t.ffprobePath()
	if probe == "" {
		return sourceCaps{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	args := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,avg_frame_rate",
		"-of", "csv=p=0",
		"-analyzeduration", "3000000",
		src,
	}
	out, err := exec.CommandContext(ctx, probe, args...).Output()
	if err != nil {
		return sourceCaps{}
	}
	return parseProbeCSV(string(out))
}

// ffprobePath 定位 ffprobe：优先环境变量，其次与 ffmpeg 同目录（发布包自带），最后查 PATH。
func (t *Transcoder) ffprobePath() string {
	if custom := strings.TrimSpace(os.Getenv("FFPROBE_PATH")); custom != "" {
		return custom
	}
	dir := filepath.Dir(t.ffmpegPath)
	for _, name := range []string{"ffprobe", "ffprobe.exe"} {
		candidate := filepath.Join(dir, name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	if found, err := exec.LookPath("ffprobe"); err == nil {
		return found
	}
	return ""
}

// parseProbeCSV 解析 `-of csv=p=0` 输出的一行 "1280,720,30000/1001"。
func parseProbeCSV(raw string) sourceCaps {
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Split(strings.TrimSpace(line), ",")
		if len(fields) < 2 {
			continue
		}
		width, errW := strconv.Atoi(strings.TrimSpace(fields[0]))
		height, errH := strconv.Atoi(strings.TrimSpace(fields[1]))
		if errW != nil || errH != nil || width <= 0 || height <= 0 {
			continue
		}
		caps := sourceCaps{width: width, height: height}
		if len(fields) >= 3 {
			caps.fps = parseFrameRate(fields[2])
		}
		return caps
	}
	return sourceCaps{}
}

// parseFrameRate 把 "30000/1001" 解析成整数帧率（四舍五入，限制在 1~240）。
func parseFrameRate(raw string) int {
	parts := strings.Split(strings.TrimSpace(raw), "/")
	num, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil || num <= 0 {
		return 0
	}
	den := 1.0
	if len(parts) > 1 {
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil && parsed > 0 {
			den = parsed
		}
	}
	fps := int(math.Round(num / den))
	if fps < 1 {
		fps = 1
	}
	if fps > 240 {
		fps = 240
	}
	return fps
}

// resolveInput 判定输入源类型：
//
//	http(s)://           -> 网关直播接入面 / 任意 HTTP 直播流
//	rtsp:// 或 rtsps://  -> 真实安防 / 无人机拉流
//	存在的本地文件        -> MVP 默认循环样片
//	都不满足             -> lavfi 内置测试图卡兜底
func resolveInput(input, sample string) (kind string, value string) {
	trimmed := strings.TrimSpace(input)
	lower := strings.ToLower(trimmed)

	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return "http", trimmed
	}

	if strings.HasPrefix(lower, "rtsp://") || strings.HasPrefix(lower, "rtsps://") {
		return "rtsp", trimmed
	}

	for _, candidate := range []string{strings.TrimPrefix(trimmed, "file://"), sample} {
		if candidate == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if st, err := os.Stat(abs); err == nil && !st.IsDir() {
			return "file", abs
		}
	}
	return "lavfi", ""
}

// logRing 是一个只保留最近 N 行的环形日志缓冲。
type logRing struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func newLogRing(max int) *logRing {
	if max <= 0 {
		max = 40
	}
	return &logRing{lines: make([]string, 0, max), max: max}
}

func (l *logRing) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
	if len(l.lines) > l.max {
		l.lines = l.lines[len(l.lines)-l.max:]
	}
}

func (l *logRing) tail(n int) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.lines) == 0 {
		return ""
	}
	start := len(l.lines) - n
	if start < 0 {
		start = 0
	}
	return strings.Join(l.lines[start:], "\n")
}
