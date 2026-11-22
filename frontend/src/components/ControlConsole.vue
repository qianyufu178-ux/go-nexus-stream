<script setup>
import { computed, ref } from 'vue'

const props = defineProps({
  status: { type: Object, default: null },
  latest: { type: Object, default: null },
  mode: { type: String, default: 'GPU_ACCELERATION' },
  isGPU: { type: Boolean, default: true },
})

const emit = defineEmits(['reconnect', 'refresh'])

const busy = ref(false)
const log = ref([{ at: new Date().toLocaleTimeString('zh-CN', { hour12: false }), text: '控制台就绪，等待演练指令', tone: 'dim' }])

const targetMode = computed(() => (props.isGPU ? 'CPU_SELF_HEALING' : 'GPU_ACCELERATION'))
const actionLabel = computed(() =>
  props.isGPU ? '模拟拔除显卡 / 切换至 CPU 抽帧自愈轨道' : '恢复显卡算力 / 切换至 GPU 硬转轨道',
)
const device = computed(() => props.status?.device || null)
const probe = computed(() => props.status?.probe || null)

function note(text, tone = 'ok') {
  const at = new Date().toLocaleTimeString('zh-CN', { hour12: false })
  log.value = [{ at, text, tone }, ...log.value].slice(0, 4)
}

async function toggleMode() {
  if (busy.value) return
  busy.value = true
  const requested = targetMode.value
  try {
    const res = await fetch('/api/mode/toggle', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ mode: requested }),
    })
    const data = await res.json().catch(() => ({}))
    if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`)
    note(`轨道已切换 → ${data.mode} · ${data.codec} · ${data.resolution}@${data.fps}`, 'ok')
    emit('refresh')
  } catch (err) {
    note(`切换失败：${err instanceof Error ? err.message : String(err)}`, 'err')
  } finally {
    busy.value = false
  }
}

function refresh() {
  emit('refresh')
  note('已请求刷新运行状态', 'dim')
}
</script>


<template>
  <section class="glass-panel w-[420px] p-4">
    <header class="flex items-center justify-between">
      <div>
        <p class="label-tech">Demo Console</p>
        <h2 class="font-mono text-sm text-white">双轨切换演练台</h2>
      </div>
      <span class="chip chip-dim">{{ device?.direction_type || 'PULL' }} · {{ device?.stream_key || 'live_demo_001' }}</span>
    </header>

    <p class="mt-2 truncate font-mono text-[11px] text-nexus-dim">
      {{ device?.device_name || '大疆御3-巡检01 (演示通道)' }}
    </p>

    <button
      class="console-btn mt-3"
      :class="isGPU ? 'console-btn-warn' : 'console-btn-green'"
      :disabled="busy"
      @click="toggleMode"
    >
      <span class="flex items-center gap-2">
        <span class="text-base">{{ busy ? '⏳' : isGPU ? '⚡' : '🟢' }}</span>
        <span class="flex-1">{{ busy ? '正在重建转码管道…' : actionLabel }}</span>
      </span>
      <span class="mt-1 block text-[10px] tracking-widest text-nexus-dim">
        POST /api/mode/toggle → {{ targetMode }}
      </span>
    </button>

    <div class="mt-2 grid grid-cols-2 gap-2">
      <button class="console-btn console-btn-ghost py-2 text-xs" @click="emit('reconnect')">重连 WebRTC 画面</button>
      <button class="console-btn console-btn-ghost py-2 text-xs" @click="refresh">刷新运行状态</button>
    </div>

    <dl class="mt-3 grid grid-cols-2 gap-x-3 gap-y-1 font-mono text-[11px]">
      <div class="flex justify-between">
        <dt class="text-nexus-dim">FFmpeg</dt>
        <dd
          :class="probe?.ffmpeg_available ? 'text-nexus-green' : 'text-nexus-danger'"
          :title="probe?.ffmpeg_version || ''"
        >
          {{ probe?.ffmpeg_available ? 'READY' : 'MISSING' }}
        </dd>
      </div>
      <div class="flex justify-between">
        <dt class="text-nexus-dim">NVENC</dt>
        <dd
          :class="probe?.nvenc_available ? 'text-nexus-green' : 'text-nexus-warn'"
          :title="probe?.detail || ''"
        >
          {{ probe?.nvenc_available ? 'AVAILABLE' : 'FALLBACK' }}
        </dd>
      </div>
      <div class="flex justify-between">
        <dt class="text-nexus-dim">转码进程</dt>
        <dd :class="status?.transcoder?.running ? 'text-nexus-green' : 'text-nexus-warn'">
          {{ status?.transcoder?.running ? 'RUNNING' : 'STOPPED' }}
        </dd>
      </div>
      <div class="flex justify-between">
        <dt class="text-nexus-dim">RTP 包量</dt>
        <dd class="text-white">{{ status?.rtp?.packets_received ?? 0 }}</dd>
      </div>
      <div class="flex justify-between">
        <dt class="text-nexus-dim">WebRTC Peers</dt>
        <dd class="text-white">{{ status?.webrtc?.peers ?? 0 }}</dd>
      </div>
      <div class="flex justify-between">
        <dt class="text-nexus-dim">大屏连接</dt>
        <dd class="text-white">{{ status?.ws?.clients ?? 0 }}</dd>
      </div>
    </dl>

    <!-- NVENC 不可用时直接给出探针结论：区分「没显卡」与「FFmpeg 版本过旧 / 驱动不兼容」 -->
    <p
      v-if="probe?.detail && !probe?.nvenc_available"
      class="mt-2 border-l-2 border-nexus-warn/60 pl-2 text-[10px] leading-relaxed text-nexus-warn/80"
    >
      {{ probe.detail }}
    </p>

    <ul class="mt-3 space-y-1 border-t border-white/10 pt-2 font-mono text-[10px]">
      <li v-for="item in log" :key="item.at + item.text" class="flex gap-2">
        <span class="text-nexus-dim">{{ item.at }}</span>
        <span
          class="flex-1 truncate"
          :class="item.tone === 'err' ? 'text-nexus-danger' : item.tone === 'dim' ? 'text-nexus-dim' : 'text-nexus-green'"
        >
          {{ item.text }}
        </span>
      </li>
    </ul>
  </section>
</template>
