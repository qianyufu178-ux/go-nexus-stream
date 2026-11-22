<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useWebRTC } from '../composables/useWebRTC.js'

const props = defineProps({
  mode: { type: String, default: 'GPU_ACCELERATION' },
  latest: { type: Object, default: null },
  connected: { type: Boolean, default: false },
})

const videoEl = ref(null)
const { state, codec, error, latencyHint, connect, resumePlayback } = useWebRTC(videoEl)

const isCPU = computed(() => props.mode === 'CPU_SELF_HEALING')
const playing = computed(() => state.value === 'playing')
const statusText = computed(() => {
  if (state.value === 'connecting') return '正在与 Nexus-Core 协商 WebRTC 通道…'
  if (state.value === 'error') return error.value || 'WebRTC 协商失败'
  return '等待 WebRTC 媒体流接入'
})

// 首次挂载即协商；坐标轴上的模式变化不会重连，避免热切换时画面闪断。
onMounted(() => {
  connect()
})

// 视频标签被重新挂载时补一次 play，规避自动播放策略。
watch(videoEl, (el) => {
  if (el && playing.value) resumePlayback()
})

defineExpose({ connect, state })
</script>

<template>
  <div class="absolute inset-0 overflow-hidden">
    <video
      ref="videoEl"
      class="h-full w-full object-contain"
      autoplay
      playsinline
      muted
      :class="playing ? 'opacity-100' : 'opacity-0'"
    ></video>

    <!-- 暗场底纹：网格 + 扫描线 + 上下渐隐，突出科技感；
         叠层透明度刻意压低，避免把视频盖成「发灰发糊」的观感。 -->
    <div class="pointer-events-none absolute inset-0 grid-overlay opacity-15"></div>
    <div class="pointer-events-none absolute inset-x-0 top-0 h-1/3 scanline animate-scan opacity-40"></div>
    <div class="pointer-events-none absolute inset-0 bg-gradient-to-b from-nexus-bg/25 via-transparent to-nexus-bg/45"></div>
    <div
      class="pointer-events-none absolute inset-0 transition-colors duration-500"
      :class="isCPU ? 'bg-nexus-warn/[0.06]' : 'bg-nexus-green/[0.03]'"
    ></div>

    <!-- 未出画面时的等待层（点击可恢复播放） -->
    <div
      v-if="!playing"
      class="absolute inset-0 flex cursor-pointer flex-col items-center justify-center gap-6"
      @click="resumePlayback"
    >
      <div class="relative flex h-32 w-32 items-center justify-center">
        <span class="absolute h-24 w-24 rounded-full border border-nexus-green/40 animate-radar"></span>
        <span class="absolute h-24 w-24 rounded-full border border-nexus-green/30 animate-radar [animation-delay:0.9s]"></span>
        <span class="absolute h-24 w-24 rounded-full border border-nexus-green/20 animate-radar [animation-delay:1.8s]"></span>
        <span class="font-mono text-2xl" :class="isCPU ? 'text-nexus-warn' : 'text-nexus-green'">▶</span>
      </div>
      <div class="text-center">
        <p class="font-mono text-xs uppercase tracking-[0.4em] text-nexus-dim">NEXUS-STREAM MEDIA STAGE</p>
        <p class="mt-2 text-sm" :class="isCPU ? 'text-nexus-warn' : 'text-nexus-green'">{{ statusText }}</p>
        <p v-if="state === 'error'" class="mt-1 font-mono text-[11px] text-nexus-dim">点击画面可重新拉流</p>
      </div>
    </div>

    <!-- 左上角通道角标 -->
    <div class="absolute left-6 top-6 flex items-center gap-3">
      <span class="flex items-center gap-2 rounded-full border border-nexus-danger/40 bg-nexus-danger/10 px-3 py-1">
        <span class="h-2 w-2 rounded-full bg-nexus-danger animate-blink"></span>
        <span class="font-mono text-[11px] tracking-[0.2em] text-nexus-danger">LIVE</span>
      </span>
      <span class="chip chip-dim">CH-01 · {{ props.latest?.direction_type || 'PULL' }}</span>
      <span v-if="playing" class="chip chip-green">
        <span class="h-1.5 w-1.5 rounded-full bg-nexus-green"></span>
        WebRTC · {{ codec || 'H264' }}
      </span>
      <span v-else-if="props.connected" class="chip chip-dim">WS 在线</span>
    </div>

    <!-- 画面四角装饰 -->
    <div class="pointer-events-none absolute inset-6">
      <span class="absolute left-0 top-0 h-8 w-8 border-l border-t" :class="isCPU ? 'border-nexus-warn/60' : 'border-nexus-green/60'"></span>
      <span class="absolute right-0 top-0 h-8 w-8 border-r border-t" :class="isCPU ? 'border-nexus-warn/60' : 'border-nexus-green/60'"></span>
      <span class="absolute bottom-0 left-0 h-8 w-8 border-b border-l" :class="isCPU ? 'border-nexus-warn/60' : 'border-nexus-green/60'"></span>
      <span class="absolute bottom-0 right-0 h-8 w-8 border-b border-r" :class="isCPU ? 'border-nexus-warn/60' : 'border-nexus-green/60'"></span>
    </div>

    <div class="absolute right-6 top-24 flex flex-col items-end gap-1 font-mono text-[11px] text-nexus-dim">
      <span v-if="latencyHint">{{ latencyHint }}</span>
      <span v-if="latest">{{ latest.resolution }} · {{ latest.fps }} FPS</span>
    </div>
  </div>
</template>
