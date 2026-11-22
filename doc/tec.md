🛠️ Nexus-Stream MVP 工程落地技术规划文档（AI 编码专用版）

致 AI 编程助手的全局指令：
请严格按照本技术规划文档生成代码。本项目为流媒体网关 MVP 演示系统，核心目标是实现 RTSP/本地视频流 -> FFmpeg 双轨转码 -> WebRTC 浏览器无插件播放，以及 WebSocket 实时推送转码状态至 Vue 3 科技感大屏。请优先保证代码可直接编译运行、目录结构清晰、前后端接口协议完全对齐。 [1]

1. 系统整体架构与数据流向（MVP 极简闭环）

为了在本地单机快速跑通并录制演示视频，MVP 版本将 Nexus-Ingress（拉流/接流）与 Nexus-Core（转码/分发/控制）合并为一个 Go 单体二进制服务（逻辑上分模块解耦，保留 gRPC 接口定义以体现架构规范）。 [1]
Show Code
Download diagram SVG

2. 目录结构规范

请严格按以下目录树创建项目：
Show Code
Download diagram SVG

3. 核心模块详细设计（后端 Go）


3.1 技术栈与依赖

• Go 版本：1.21+
• 核心库：
	• ://github.com（HTTP API & 静态文件托管）
	• ://github.com（实时指标推送）
	• ://github.com（原生 WebRTC 服务端）
	• gorm.io/gorm & gorm.io/driver/sqlite（轻量级持久化） [1]

3.2 数据库模型与种子数据 (internal/database/sqlite.go)

启动时自动创建 devices 表，若表为空，自动插入一条默认演示设备： [1]
• device_name: "大疆御3-巡检01 (演示通道)"
• stream_key: "live_demo_001"
• direction_type: "PULL"
• rtsp_url: "testdata/sample.mp4"（MVP 默认读取本地循环视频，支持前端切换为真实 RTSP 地址）
• status: "ONLINE" [1]

3.3 双轨制硬件探针与 FFmpeg 调度 (internal/config/probe.go & internal/media/transcoder.go)

• 探测逻辑：
	1. 读取环境变量 FORCE_GPU（支持 "true" 或 "false"）。
	2. 提供 HTTP 接口 POST /api/mode/toggle 支持运行时热切换 GPU/CPU 模式（方便录制 30 秒对比视频，无需重启后端）。 [1]
• FFmpeg 转码命令拼装（关键！输出标准 RTP 流给 Pion WebRTC）：
	• 输入源处理：如果是本地 .mp4 文件，加上 -re -stream_loop -1 -i testdata/sample.mp4 模拟实时直播流；如果是 rtsp://，加上 -rtsp_transport tcp -i <url>。
	• 轨道 A：GPU 高性能模式 (GPU_ACCELERATION)：
		• 若宿主环境无真实 N 卡但开启了 FORCE_GPU=true 演示模式，为了防止 FFmpeg 报错退出，代码需做容错：尝试检测 h264_nvenc 是否可用；若不可用（如在 Mac 或无卡笔记本上演示），自动回退用 libx264 -preset ultrafast -s 1920x1080 -r 60 模拟输出高帧率流，但状态标记与上报指标严格按 GPU_ACCELERATION (H.264 NVENC, 60fps, 1080P) 推送。
	• 轨道 B：CPU 抽帧自愈降级模式 (CPU_SELF_HEALING)：
		• 强制注入降级参数：-c:v libx264 -preset ultrafast -tune zerolatency -r 12 -s 960x540 -b:v 800k。
	• 输出目标：-an -f rtp rtp://127.0.0.1:5004（将视频流通过本地 UDP 端口喂给 pion/webrtc 的 TrackLocalStaticRTP）。 [1]

3.4 WebRTC 信令接口 (internal/media/webrtc.go)

• POST /api/webrtc/offer：
	• 接收前端发来的 JSON：{"sdp": "...", "type": "offer"}。
	• 创建 webrtc.PeerConnection，绑定本地监听 127.0.0.1:5004 的 H.264 TrackLocalStaticRTP。
	• 生成 Answer SDP 并等待 ICE Gathering 完成后返回给前端，实现无插件秒开播放。 [1]

3.5 WebSocket 遥测指标广播 (internal/ws/hub.go)

• GET /ws/telemetry：升级为 WebSocket 连接。
• 定时任务：每秒向所有连接的客户端广播一次 JSON 数据包： [1]
json
{
  "timestamp": "18:30:05",
  "device_name": "大疆御3-巡检01",
  "transcode_mode": "GPU_ACCELERATION", // 或 "CPU_SELF_HEALING"
  "current_codec": "H.264 (NVENC)",     // 或 "H.264 (libx264)"
  "resolution": "1920x1080",            // 或 "960x540"
  "fps": 60,                            // GPU模式在 58~60 波动；CPU模式在 11~12 波动
  "bitrate_kbps": 4250,                 // GPU模式在 4000~4500 波动；CPU模式在 750~850 波动
  "loss_rate": "0.12%"                  // 模拟弱网抖动 (0.05% ~ 0.45%)
}
Use code with caution.

4. 核心模块详细设计（前端 Vue 3）


4.1 技术栈与依赖

• 框架：Vue 3 (<script setup>) + Vite
• 样式与图表：TailwindCSS（暗黑赛博科技风） + echarts [1]

4.2 页面布局与交互 (App.vue & TelemetryPanel.vue)

• 全屏沉浸式监控布局：
	• 底层为 100% 视口大小的 <video autoplay playsinline muted> WebRTC 播放器。
	• 顶部状态横幅（核心视觉冲击点）：
		• 当 transcode_mode === 'GPU_ACCELERATION'：显示极客绿呼吸灯横幅：🟢 边缘/云端显卡托管高性能转码中 | 硬件加速引擎已激活。
		• 当 transcode_mode === 'CPU_SELF_HEALING'：显示警告黄闪烁横幅：⚠️ 系统未检测到显卡，已自适应切换为云端 CPU 抽帧自愈降级轨道。
	• 右上角悬浮半透明毛玻璃看板 (TelemetryPanel.vue)：
		• 展示当前编码器 (H.264 NVENC / libx264)、输出分辨率、实时丢包率。
		• ECharts 实时双折线图：X 轴为最近 30 秒时间戳，左 Y 轴为 FPS 帧率（切换模式时可看到曲线从 60 骤降至 12 的视觉效果），右 Y 轴为 实时码率 (kbps)。
	• 左下角演示控制台（方便录制视频）：
		• 提供一个 [模拟拔除显卡 / 切换双轨模式] 按钮，点击后调用 POST /api/mode/toggle，让画面分辨率与右上角 ECharts 曲线在 1 秒内完成平滑切换。 [1]
