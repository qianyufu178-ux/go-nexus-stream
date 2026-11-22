<script setup>
import { computed } from 'vue'

const props = defineProps({
  mode: { type: String, default: 'GPU_ACCELERATION' },
  latest: { type: Object, default: null },
  status: { type: Object, default: null },
  connected: { type: Boolean, default: false },
})

const isCPU = computed(() => props.mode === 'CPU_SELF_HEALING')

const title = computed(() =>
  isCPU.value
    ? '系统未检测到显卡，已自适应切换为云端 CPU 抽帧自愈降级轨道'
    : '边缘 / 云端显卡托管高性能转码中',
)

const subtitle = computed(() => {
  const spec = props.latest
  if (spec) return `${spec.current_codec} · ${spec.resolution} · ${spec.fps} FPS · ${spec.bitrate_kbps} kbps`
  // 遥测还没到时，直接用后端快照里的真实轨道规格兜底（已按源收敛，不再是写死的 1080P60）。
  const target = props.status?.transcoder
  if (target) return `${target.codec} · ${target.resolution} · ${target.target_fps} FPS · ${target.target_bitrate_kbps} kbps`
  return isCPU.value ? 'CPU_SELF_HEALING · libx264 · 960x540@12' : 'GPU_ACCELERATION · NVENC · 1920x1080@60'
})

const notice = computed(() => props.latest?.notice || props.status?.notice || '')
const source = computed(() => props.latest?.source || props.status?.device?.rtsp_url || '')
</script>

<template>
  <div class="pointer-events-none flex justify-center px-4">
    <div
      class="pointer-events-auto flex w-full max-w-5xl items-center gap-4 rounded-2xl border px-6 py-3 backdrop-blur-md transition-all duration-500"
      :class="
        isCPU
          ? 'border-nexus-warn/60 bg-nexus-warn/[0.12] animate-breathe-warn'
          : 'border-nexus-green/50 bg-nexus-green/[0.08] animate-breathe'
      "
    >
      <span class="text-2xl leading-none">{{ isCPU ? '⚠️' : '🟢' }}</span>
      <div class="min-w-0 flex-1">
        <p
          class="truncate font-mono text-sm font-semibold tracking-wide"
          :class="isCPU ? 'text-nexus-warn animate-blink' : 'text-nexus-green'"
        >
          {{ title }}
        </p>
        <p class="mt-0.5 truncate font-mono text-[11px] text-nexus-dim">{{ subtitle }}</p>
      </div>
      <div class="hidden shrink-0 flex-col items-end gap-1 md:flex">
        <span class="chip" :class="isCPU ? 'chip-warn' : 'chip-green'">
          {{ isCPU ? 'CPU_SELF_HEALING' : 'GPU_ACCELERATION' }}
        </span>
        <span class="font-mono text-[10px] text-nexus-dim">
          {{ connected ? 'WS 实时' : 'WS 重连中' }} · {{ source || 'local sample' }}
        </span>
      </div>
    </div>
  </div>

  <div v-if="notice" class="mt-2 flex justify-center px-4">
    <p class="rounded-full border border-nexus-cyan/30 bg-nexus-cyan/10 px-4 py-1 font-mono text-[11px] text-nexus-cyan">
      {{ notice }}
    </p>
  </div>
</template>
