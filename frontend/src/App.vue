<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ControlConsole from './components/ControlConsole.vue'
import StatusBanner from './components/StatusBanner.vue'
import TelemetryPanel from './components/TelemetryPanel.vue'
import VideoStage from './components/VideoStage.vue'
import { useTelemetry } from './composables/useTelemetry.js'

const { latest, history, connected, status, error, mode, isGPU, fetchStatus } = useTelemetry()

const stageRef = ref(null)
const clock = ref(new Date())
let clockTimer = null

onMounted(() => {
  clockTimer = setInterval(() => {
    clock.value = new Date()
  }, 1000)
})

onBeforeUnmount(() => {
  if (clockTimer) clearInterval(clockTimer)
})

const version = computed(() => status.value?.version || '0.1.0')
const clockText = computed(() => clock.value.toLocaleTimeString('zh-CN', { hour12: false }))
const uptimeText = computed(() => formatUptime(status.value?.uptime_seconds ?? latest.value?.uptime_seconds ?? 0))
const peers = computed(() => status.value?.webrtc?.peers ?? 0)
const wsClients = computed(() => status.value?.ws?.clients ?? 0)
const rtpPackets = computed(() => status.value?.rtp?.packets_received ?? 0)

function formatUptime(total) {
  const seconds = Math.max(0, Number(total) || 0)
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = seconds % 60
  const pad = (v) => String(v).padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${pad(m)}:${pad(s)}`
}

function reconnectStage() {
  stageRef.value?.connect()
}

const tickerItems = computed(() => [
  `DEVICE ${latest.value?.device_name || '大疆御3-巡检01'}`,
  `MODE ${mode.value}`,
  `RTP PACKETS ${rtpPackets.value}`,
  `WEBRTC PEERS ${peers.value}`,
  `WS CLIENTS ${wsClients.value}`,
  'UPLINK 4G/5G SIMULATED LOSS 0.05% ~ 0.45%',
  `NEXUS-CORE v${version.value}`,
])
</script>

<template>
  <div class="relative h-screen w-screen overflow-hidden bg-nexus-bg">
    <VideoStage ref="stageRef" :mode="mode" :latest="latest" :connected="connected" />

    <!-- 顶部：品牌条 + 双轨状态横幅 -->
    <div class="absolute inset-x-0 top-0 z-20">
      <div class="flex items-center justify-between px-6 py-4">
        <div class="flex items-center gap-3">
          <div
            class="flex h-10 w-10 items-center justify-center rounded-xl border font-mono text-lg font-bold"
            :class="isGPU ? 'border-nexus-green/60 bg-nexus-green/10 text-nexus-green' : 'border-nexus-warn/60 bg-nexus-warn/10 text-nexus-warn'"
          >
            N
          </div>
          <div>
            <h1 class="font-mono text-base tracking-[0.3em] text-white">NEXUS-STREAM</h1>
            <p class="font-mono text-[10px] tracking-[0.18em] text-nexus-dim">
              全场景高性能流媒体转码网关 · MVP DEMO
            </p>
          </div>
        </div>

        <div class="flex items-center gap-2">
          <span class="chip chip-dim">v{{ version }}</span>
          <span class="chip chip-dim">UPTIME {{ uptimeText }}</span>
          <span class="chip" :class="connected ? (isGPU ? 'chip-green' : 'chip-warn') : 'chip-dim'">
            <span
              class="h-1.5 w-1.5 rounded-full"
              :class="connected ? (isGPU ? 'bg-nexus-green' : 'bg-nexus-warn') : 'bg-nexus-dim'"
            ></span>
            {{ connected ? 'TELEMETRY LIVE' : 'RECONNECTING' }}
          </span>
          <span class="chip chip-dim">{{ clockText }}</span>
        </div>
      </div>

      <StatusBanner :mode="mode" :latest="latest" :status="status" :connected="connected" />
    </div>

    <!-- 右上：指标看板 -->
    <div class="absolute right-6 top-[188px] z-20">
      <TelemetryPanel :latest="latest" :history="history" :mode="mode" :connected="connected" :status="status" />
    </div>

    <!-- 左下：演示控制台 -->
    <div class="absolute bottom-12 left-6 z-20">
      <ControlConsole
        :status="status"
        :latest="latest"
        :mode="mode"
        :is-gpu="isGPU"
        @reconnect="reconnectStage"
        @refresh="fetchStatus"
      />
    </div>

    <!-- 底部：滚动信息条 -->
    <div class="absolute inset-x-0 bottom-0 z-20 border-t border-white/10 bg-black/50 py-1.5 backdrop-blur">
      <div class="flex w-max ticker-track gap-10 whitespace-nowrap px-6 font-mono text-[10px] tracking-[0.2em] text-nexus-dim">
        <span v-for="(item, index) in [...tickerItems, ...tickerItems]" :key="index">▸ {{ item }}</span>
      </div>
    </div>

    <p v-if="error" class="absolute bottom-14 right-6 z-20 font-mono text-[10px] text-nexus-danger">{{ error }}</p>
  </div>
</template>
