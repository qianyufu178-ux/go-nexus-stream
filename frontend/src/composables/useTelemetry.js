import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

// 图表保留最近 30 个采样点，与 PRD「最近 30 秒」一致。
const MAX_POINTS = 30

/**
 * useTelemetry：封装 WebSocket 实时遥测 + REST 兜底。
 *
 * - 首屏先拉一次 /api/telemetry，避免等待 1 秒心跳出现空白；
 * - WebSocket 断线后 2 秒自动重连（演示时刷新后端也不掉线）；
 * - 每 5 秒刷新 /api/status，同步探针与转码进程细节。
 */
export function useTelemetry() {
  const latest = ref(null)
  const history = ref([])
  const connected = ref(false)
  const status = ref(null)
  const error = ref('')

  const mode = computed(() => latest.value?.transcode_mode || status.value?.mode || 'GPU_ACCELERATION')
  const isGPU = computed(() => mode.value === 'GPU_ACCELERATION')

  let socket = null
  let retryTimer = null
  let pollTimer = null
  let disposed = false

  function wsURL() {
    const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
    return `${scheme}://${window.location.host}/ws/telemetry`
  }

  function pushSnapshot(snap) {
    if (!snap || typeof snap.fps !== 'number') return
    latest.value = snap
    const point = {
      label: snap.timestamp,
      at: snap.generated_at || Date.now(),
      fps: snap.fps,
      bitrate: snap.bitrate_kbps,
      mode: snap.transcode_mode,
    }
    history.value = history.value.concat(point).slice(-MAX_POINTS)
  }

  async function fetchSnapshot() {
    try {
      const res = await fetch('/api/telemetry', { cache: 'no-store' })
      if (res.ok) pushSnapshot(await res.json())
    } catch (err) {
      error.value = String(err)
    }
  }

  async function fetchStatus() {
    try {
      const res = await fetch('/api/status', { cache: 'no-store' })
      if (res.ok) status.value = await res.json()
    } catch (err) {
      error.value = String(err)
    }
  }

  function scheduleRetry() {
    if (disposed) return
    clearTimeout(retryTimer)
    retryTimer = setTimeout(connect, 2000)
  }

  function connect() {
    if (disposed) return
    try {
      socket = new WebSocket(wsURL())
    } catch (err) {
      error.value = String(err)
      scheduleRetry()
      return
    }

    socket.onopen = () => {
      connected.value = true
      error.value = ''
    }
    socket.onmessage = (event) => {
      try {
        pushSnapshot(JSON.parse(event.data))
      } catch {
        /* 忽略非法报文，保持大屏稳定 */
      }
    }
    socket.onerror = () => {
      error.value = 'WebSocket 连接异常，正在自动重连'
    }
    socket.onclose = () => {
      connected.value = false
      socket = null
      scheduleRetry()
    }
  }

  onMounted(async () => {
    await Promise.all([fetchSnapshot(), fetchStatus()])
    connect()
    pollTimer = setInterval(fetchStatus, 5000)
  })

  onBeforeUnmount(() => {
    disposed = true
    clearTimeout(retryTimer)
    clearInterval(pollTimer)
    if (socket) {
      socket.onclose = null
      socket.close()
    }
  })

  return { latest, history, connected, status, error, mode, isGPU, fetchStatus }
}
