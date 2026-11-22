# Nexus-Stream · 实时双轨转码网关 (MVP)

FFmpeg 双轨转码 → 本地 RTP 中转 → Pion WebRTC 分发 → WebSocket 遥测大屏。
全部能力可在**单机、无 GPU、无 FFmpeg** 的环境下跑通（自动降级为演示指标）。

技术栈：Go 1.24 + [Pion WebRTC v4](https://github.com/pion/webrtc)（H.264 分发）/ FFmpeg（NVENC 或 libx264）/ `glebarez/sqlite`（pure Go，无需 CGO）/ `gorilla/websocket`；前端 Vue3 + Vite + Tailwind + ECharts。

```
浏览器 ──WebRTC(H.264)──▶ nexus-core ──RTP──▶ Pion Track
                              ▲
        FFmpeg(GPU/CPU 轨道) ──┘  rtp://127.0.0.1:5004
                              │
   大屏 ◀──WebSocket /ws/telemetry── 遥测 1Hz
```

## 目录

| 路径 | 说明 |
| --- | --- |
| `backend/cmd/nexus-core` | 服务入口（装配各层） |
| `backend/cmd/rtcprobe` | 自检工具：模拟浏览器完成 Offer/Answer，统计真实 RTP 收包速率 |
| `backend/internal/config` | 环境变量、`ffmpeg -encoders` 硬件探针 |
| `backend/internal/media` | 转码调度器、RTP 中转、Pion WebRTC |
| `backend/internal/telemetry` | 指标模拟器（含 60→12 fps 断崖） |
| `backend/internal/ws` | WebSocket 广播中心（慢客户端直接丢弃） |
| `backend/internal/api` | REST、静态资源、SPA 回退 |
| `backend/internal/database` | pure-Go SQLite（glebarez），自动建表 |
| `frontend` | Vue3 + Vite + Tailwind + ECharts 大屏 |

## 运行

```powershell
# 1. 后端
cd backend
$env:FFMPEG_PATH = 'E:\path\to\ffmpeg.exe'   # 没有就留空，服务仍可启动
go build ./...
go run ./cmd/nexus-core
# 大屏 http://127.0.0.1:8080/

# 2. 前端（开发热更，可选；生产直接 npm run build 到 frontend/dist）
cd frontend
npm install
npm run dev      # http://127.0.0.1:5173，已代理 /api 与 /ws 到 :8080
npm run build    # 产物供 nexus-core 直接托管
```

前端产物查找顺序：`$WEB_DIST` → `web/dist` → `frontend/dist` → 可执行文件同级 `web/dist`，
找不到时返回内置的降级页（提示如何构建前端），不会 404。

### 从网关直播接入面拉流（端到端串联）

```powershell
$env:NEXUS_INPUT_URL    = 'http://127.0.0.1:8082/ingest/live/cam0'
$env:NEXUS_INPUT_FORMAT = 'mpegts'
go run ./cmd/nexus-core
```

此时 FFmpeg 以「低延迟 + 失败自动重连」的方式拉 HTTP 直播流
（`-fflags nobuffer -reconnect* -rw_timeout`），再走双轨转码。
完整链路（摄像头 → 网关 → 转码器 → 网页）由工作区内的另外两个仓库共同组成：
`go-agent-collect`（摄像头采集推流）与 `go-apisix-extend`（网关 `/ingest/live/{name}` 接入面），本仓库负责拉流、转码与分发。

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NEXUS_ADDR` | `:8080` | HTTP 监听地址 |
| `NEXUS_DB` | `data/nexus.db` | SQLite 路径 |
| `WEB_DIST` | `web/dist` | 前端构建产物目录 |
| `FFMPEG_PATH` | `ffmpeg` | ffmpeg 可执行文件 |
| `NEXUS_SAMPLE` | `testdata/sample.mp4` | 无 RTSP 时循环的本地样片 |
| `NEXUS_INPUT_URL` | 空 | 直播输入源覆盖（优先级高于设备表 `rtsp_url`），支持 `http(s)://`（网关直播接入面）、`rtsp://`、本地文件 |
| `NEXUS_INPUT_FORMAT` | 空 | 强制输入封装格式，例如 `mpegts`；留空自动探测 |
| `NEXUS_RTP_HOST` / `NEXUS_RTP_PORT` | `127.0.0.1` / `5004` | 本地 RTP 中转口 |
| `FORCE_GPU` | `auto` | `auto` / `true`(强制 GPU 轨，无卡用 libx264 仿真) / `false`(强制 CPU 轨) |
| `NEXUS_STUN` | 空 | 逗号分隔 STUN，例如 `stun:stun.l.google.com:19302` |
| `NEXUS_TELEMETRY_MS` | `1000` | 遥测推送间隔，最小 200ms |
| `NEXUS_AUTO_RESTART` | `true` | 转码进程异常退出自动拉起 |

## 双轨参数

| 轨道 | 分辨率/帧率 | 码率 | 编码器 |
| --- | --- | --- | --- |
| `GPU_ACCELERATION` | 1920×1080 @ 60 | 4250k | `h264_nvenc`，探针不可用时自动回落 `libx264` |
| `CPU_SELF_HEALING` | 960×540 @ 12 | 800k | `libx264 -preset ultrafast` |

切换轨道时会重建 FFmpeg 进程，RTP 中转层同步重写 SSRC/序号/时间戳，
浏览器 jitter buffer 不会被旧流卡死（画面**不黑屏**，只掉清晰度）。

## HTTP / WS 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 存活探针 |
| GET | `/api/status` | 探针 + 转码 + RTP + WebRTC + WS 汇总 |
| GET | `/api/telemetry` | 单帧遥测快照（首屏自检用） |
| GET | `/api/device` | 接入设备资产 |
| GET | `/api/mode` | 当前轨道 |
| POST | `/api/mode` | 指定轨道：`{"mode":"GPU_ACCELERATION"}` |
| POST | `/api/mode/toggle` | 一键反向切换，返回切换后轨道 |
| POST | `/api/webrtc/offer` | `{"sdp":...,"type":"offer"}` → Answer |
| WS | `/ws/telemetry` | 1Hz 遥测推送 |

## 冒烟测试

```powershell
curl.exe http://127.0.0.1:8080/api/health
curl.exe http://127.0.0.1:8080/api/status
curl.exe -X POST http://127.0.0.1:8080/api/mode/toggle
```

没有浏览器时，可用 `cmd/rtcprobe` 走一次真实的 WebRTC 收流自检：

```powershell
cd backend
go run ./cmd/rtcprobe                       # 默认打 http://127.0.0.1:8080/api/webrtc/offer
# 自定义地址：go run ./cmd/rtcprobe http://127.0.0.1:8099/api/webrtc/offer
$env:PROBE_WINDOW_MS = '5000'               # 可选，统计窗口，默认 5 秒
# 输出示例：{"codec":"H.264","pc_state":"connected","rtp_pps":117,"payload_kbps":796.3,...}
#           切到 GPU 轨后 rtp_pps ≈ 590、payload_kbps ≈ 4480
```

打开大屏后：视频区出现 WebRTC 画面（无 FFmpeg 时显示占位提示），
点击「双轨一键切换」应看到 ECharts 里 **fps 60 → 12 断崖**、码率同步下降，
状态条与日志面板 1 秒内联动。

## 实测数据（本机 RTX 显卡 + i5-13400F / Windows 11）

### 连续 11.2 小时长跑（`nexus-core` 进程未重启，转码子进程累计重启 1 次）

| 指标 | 值 |
| --- | --- |
| 运行时长 | 40,212 秒（≈11.2 小时），`running=true` |
| 转码重启 | `restarts=1`（修复前：45 秒内 31 次） |
| 网关推流输入 | `publishing=true`，`bytes_in=26,883,756,540`（≈26.9 GB），`chunks_in=1,107,566`，`dropped=0` |
| RTP 中转 | `packets_received = packets_forwarded = 15,880,248`，`packets_dropped=0`，`payload_bytes≈16.7 GB` |
| 输出规格 | 1280×720@30，3318 kbps（源 1280×720@30，按源收敛、不放大） |
| 编码参数 | `h264_nvenc`：`p5 / tune hq / -bf 0 / CBR / profile high / spatial-aq 1`；缩放用 `lanczos+accurate_rnd+full_chroma_int` |
| 探针结论 | `nvenc_compiled=true`、`nvenc_available=true`，自动选择 GPU 轨 |

> `restarts=1` 是 11 小时累计值（发生在长时间运行后的输入重连窗口），不是崩溃风暴。

### 质量门禁

```powershell
cd backend
go vet ./...      # 0 告警
go test ./...     # internal/config、internal/media 两个包通过
go build ./...    # 构建 nexus-core / rtcprobe
```

## 已知边界

- **单实例单机部署**为主；多实例横向扩展需要把「流 → 实例」的映射交给调度层（当前未内置）。
- 浏览器侧依赖 H.264（WebRTC 内建支持）；H.265 需自行扩展编解码与 SDP 协商。
- 采集侧不在本工程范围：Windows 用 `go-agent-collect/cmd/cam-push` 采集推流，Linux/macOS 建议直接给 RTSP（`NEXUS_INPUT_URL`）。
- 遥测大屏在没有 FFmpeg 时展示的是 `internal/telemetry` 合成指标（用于演示与前端联调），真实链路上以 FFmpeg 输出与 RTP 计数为准。
- 生产部署需自行补鉴权、多设备接入与前端 `npm run build` 产物托管。

## 文档

- [`doc/tec.md`](doc/tec.md) 技术设计（双轨转码、RTP 中转、SSRC 重写、WebRTC 分发）
- [`doc/requment.md`](doc/requment.md) 需求与边界
- [`doc/smoke-report.md`](doc/smoke-report.md) 冒烟与验收记录（编译、REST/WS、真实收流、双轨切换）

## 说明

- SQLite 使用 `github.com/glebarez/sqlite`（pure Go，无需 CGO）。
- 演示指标由 `internal/telemetry` 合成，真实链路上以 FFmpeg 输出与 RTP 计数为准。
- 强杀 `nexus-core` 时 FFmpeg 子进程可能残留，重启前执行
  `Get-Process ffmpeg | Stop-Process -Force`，否则两个进程会同时写 `127.0.0.1:5004`。
- 本机验收记录（编译、REST/WS、真实 WebRTC 收流、双轨切换）见 `doc/smoke-report.md`。
- MVP 不含鉴权与多设备接入；`doc/requment.md`、`doc/tec.md` 为完整需求与设计参考。

## 许可证

[MIT](LICENSE) © 2026 qianyufu178-ux
