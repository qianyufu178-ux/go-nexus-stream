# 冒烟测试报告（nexus-core MVP）

> 测试时间：2026-11-21 19:33–19:43（本机 Windows，无 NVIDIA 显卡，FFmpeg 由 npm 包
> `@ffmpeg-installer/win32-x64@4.1.0` 提供）  
> 结论：**REST / WS / WebRTC / 双轨切换 / 静态大屏 / SQLite 全链路通过，0 个 500，0 丢包。**

## 1. 环境

| 项 | 值 |
| --- | --- |
| OS | Windows 10/11 x64 |
| Go | 本地 GOROOT `.tools\goroot\go`，`GOTOOLCHAIN=local` |
| FFmpeg | `E:\project\other-job\.tools\ffmpeg\bin\ffmpeg.exe`（`ffmpeg version N-92722-gf22fcd4483`） |
| 硬件加速 | `h264_nvenc` 已编译但无卡 → 探针自动降级 `libx264` |
| 前端产物 | `frontend\dist`（`npm run build`，Vite 单文件 JS 1126668 B） |
| 服务端口 | `NEXUS_ADDR=:8099`（避免与本机 8080 冲突） |
| 数据库 | `data/e2e.db`（启动自动建表，24576 B） |

## 2. 编译与静态检查

```
gofmt -l .              -> 无输出（全部文件已格式化）
go vet ./...            -> exit 0
go build -o bin/nexus-core.exe ./cmd/nexus-core -> exit 0
go build ./...          -> exit 0   # 含 cmd/rtcprobe
```

## 3. HTTP / WS 断言

| 用例 | 期望 | 实测 |
| --- | --- | --- |
| `GET /api/health` | 200 + `ok` | 200 |
| `GET /api/status` | 200，含 probe/transcoder/rtp/webrtc/ws | 200 |
| `GET /api/telemetry` | 200，CPU 轨 `fps=12` | 200 |
| `GET /api/mode` | 200，`CPU_SELF_HEALING` | 200 |
| `POST /api/mode/toggle` | 200，`changed=true` 切到 GPU 轨 | 200，`fps=60 / 4250k` |
| `POST /api/mode`（重复指定 GPU） | 200，`changed=false` 幂等 | 200 |
| `POST /api/switch {"mode":"bogus"}` | 400 非法模式 | 400 |
| `ws://…/ws/telemetry` | 1Hz 连续帧 | 3 帧连续到达 |
| `GET /` | 200 且含 JS 资源引用 | 200, 496 B |
| `GET /assets/index-*.js` | 200 | 200, 1126668 B |
| 服务端日志 | 无 500 | 全 200/400 |

## 4. WebRTC 真实收流（`cmd/rtcprobe`）

`rtcprobe` 用 Pion 扮演浏览器：recvonly Offer → 服务端 Answer → 收包 5 秒统计。

| 轨道 | 协商结果 | RTP 包速率 | 负载码率 | 推算帧率 |
| --- | --- | --- | --- | --- |
| `CPU_SELF_HEALING` 960×540@12 / 800k | `pc_state=connected`，`codec=H.264` | 117.0 pkt/s | 796.3 kbps | ≈12 fps |
| `GPU_ACCELERATION` 1920×1080@60 / 4250k | `pc_state=connected`，`codec=H.264` | 590.3 pkt/s | 4479.8 kbps | ≈60 fps |

切换过程中（GPU 轨重启 FFmpeg）：

```
rtp: packets_received=7172  packets_forwarded=7172  packets_dropped=0
     payload_bytes=6572972  stream_count=2  silence_ms=20  active=true
```

- `stream_count=2` 是「检测到的流代数」（首启 + 1 次切换），不是并发数；
- 中转层重写 SSRC/序号/时间戳后，**同一 PeerConnection 跨轨道连续收包**，
  无需重新协商、无需重建 Peer（jitter buffer 不会卡死）；
- 5 倍帧率差（12 ↔ 60）与 5.6 倍码率差（796k ↔ 4480k）在真实 RTP 上成立，
  与 PRD 的「60→12 断崖」一致。

## 5. 已知限制

1. 无 NVIDIA 显卡：`h264_nvenc` 探针失败（`Cannot get the preset configuration`），
   GPU 轨用 `libx264` 仿真 1080P60，`hardware_accelerated=false`。
2. `NEXUS_SAMPLE` 指向的 `testdata/sample.mp4` 未提供，FFmpeg 自动回落
   `lavfi testsrc2` 测试图（`input_kind=lavfi`），不影响链路验证。
3. 浏览器端画面与 ECharts 联动建议人工再确认一次（本报告只覆盖协议层）。
4. `cmd/rtcprobe` 为自检工具，不参与生产构建。
5. 若上一轮测试残留了 `ffmpeg.exe` 进程（服务被强杀时子进程可能存活），
   它会与新进程同时往 `127.0.0.1:5004` 写不同的 SSRC，导致 `stream_count`
   异常增长（现象：10 秒内从 1 涨到 189）。功能不受影响（序号仍被改写为单调），
   复测前执行 `Get-Process ffmpeg | Stop-Process -Force` 即可恢复 `stream_count=1`。
