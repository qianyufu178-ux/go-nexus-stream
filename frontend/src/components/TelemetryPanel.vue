<script setup>
import * as echarts from 'echarts'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps({
  latest: { type: Object, default: null },
  history: { type: Array, default: () => [] },
  mode: { type: String, default: 'GPU_ACCELERATION' },
  connected: { type: Boolean, default: false },
  status: { type: Object, default: null },
})

const chartEl = ref(null)
let chart = null
let observer = null

const isCPU = computed(() => props.mode === 'CPU_SELF_HEALING')
const accent = computed(() => (isCPU.value ? '#ffc53d' : '#00ff9d'))
const accentFade = computed(() => (isCPU.value ? 'rgba(255,197,61,0.32)' : 'rgba(0,255,157,0.32)'))
const lossRate = computed(() => props.latest?.loss_rate || '0.00%')
const fps = computed(() => props.latest?.fps ?? 0)
const bitrate = computed(() => props.latest?.bitrate_kbps ?? 0)
const codec = computed(() => props.latest?.current_codec || '—')
const resolution = computed(() => props.latest?.resolution || '—')
const streamKey = computed(() => props.latest?.stream_key || props.status?.device?.stream_key || '—')
// 目标线跟随后端真实轨道规格：源只有 720P30 时，不会再把虚线画在 1080P60 上。
const targetFps = computed(() => props.status?.transcoder?.target_fps || (isCPU.value ? 12 : 60))
const targetBitrate = computed(() => props.status?.transcoder?.target_bitrate_kbps || 0)
const mono = 'Consolas, "Courier New", monospace'

function axisStyle(name) {
  return {
    type: 'value',
    name,
    splitNumber: 4,
    nameTextStyle: { color: '#7c8db5', fontSize: 10, fontFamily: mono },
    axisLabel: { color: '#7c8db5', fontSize: 10, fontFamily: mono },
  }
}

function lineSeries(name, data, extra = {}) {
  return { name, type: 'line', smooth: true, symbol: 'none', data, ...extra }
}

function buildOption() {
  const labels = props.history.map((point) => point.label)
  const fpsData = props.history.map((point) => point.fps)
  const bitrateData = props.history.map((point) => point.bitrate)
  const fpsCeiling = Math.max(68, targetFps.value + 8)
  const kbpsCeiling = Math.max(5200, Math.ceil((targetBitrate.value * 1.25) / 500) * 500)

  return {
    backgroundColor: 'transparent',
    animationDuration: 320,
    animationEasing: 'cubicOut',
    grid: { left: 36, right: 42, top: 24, bottom: 24 },
    tooltip: {
      trigger: 'axis',
      backgroundColor: 'rgba(8,14,26,0.94)',
      borderColor: 'rgba(120,180,255,0.28)',
      textStyle: { color: '#d7e3ff', fontSize: 11, fontFamily: mono },
      axisPointer: { type: 'line', lineStyle: { color: 'rgba(120,180,255,0.35)' } },
    },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: labels,
      axisLine: { lineStyle: { color: 'rgba(120,180,255,0.22)' } },
      axisTick: { show: false },
      axisLabel: {
        color: '#7c8db5',
        fontSize: 10,
        fontFamily: mono,
        interval: Math.max(0, Math.floor(labels.length / 5) - 1),
      },
    },
    yAxis: [
      { ...axisStyle('FPS'), min: 0, max: fpsCeiling, splitLine: { lineStyle: { color: 'rgba(120,180,255,0.09)' } } },
      { ...axisStyle('kbps'), min: 0, max: kbpsCeiling, splitLine: { show: false } },
    ],
    series: [
      lineSeries('FPS', fpsData, {
        lineStyle: { width: 2, color: accent.value, shadowBlur: 12, shadowColor: accent.value },
        areaStyle: {
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: accentFade.value },
            { offset: 1, color: 'rgba(0,0,0,0)' },
          ]),
        },
        markLine: {
          silent: true,
          symbol: 'none',
          lineStyle: { color: accent.value, type: 'dashed', opacity: 0.45 },
          label: {
            formatter: `目标 ${targetFps.value}`,
            color: '#7c8db5',
            fontSize: 10,
            fontFamily: mono,
            position: 'insideEndTop',
          },
          data: [{ yAxis: targetFps.value }],
        },
      }),
      lineSeries('码率', bitrateData, {
        yAxisIndex: 1,
        lineStyle: { width: 1.5, color: '#38bdf8', type: 'dashed' },
        areaStyle: {
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: 'rgba(56,189,248,0.18)' },
            { offset: 1, color: 'rgba(0,0,0,0)' },
          ]),
        },
      }),
    ],
  }
}

function render() {
  if (!chart) return
  chart.setOption(buildOption(), { notMerge: true })
}

onMounted(() => {
  chart = echarts.init(chartEl.value, null, { renderer: 'canvas' })
  render()
  observer = new ResizeObserver(() => {
    if (chart) chart.resize()
  })
  observer.observe(chartEl.value)
})

onBeforeUnmount(() => {
  if (observer) observer.disconnect()
  if (chart) chart.dispose()
  chart = null
})

watch(() => [props.history, props.mode], render, { deep: true })
</script>

<template>
  <section class="glass-panel w-[380px] p-4">
    <header class="flex items-center justify-between">
      <div>
        <p class="label-tech">Real-time Telemetry</p>
        <h2 class="font-mono text-sm text-white">双轨转码实时指标</h2>
      </div>
      <span class="chip" :class="connected ? (isCPU ? 'chip-warn' : 'chip-green') : 'chip-dim'">
        <span
          class="h-1.5 w-1.5 rounded-full"
          :class="connected ? (isCPU ? 'bg-nexus-warn' : 'bg-nexus-green') : 'bg-nexus-dim'"
        ></span>
        {{ connected ? 'LIVE' : 'OFFLINE' }}
      </span>
    </header>

    <div class="mt-4 grid grid-cols-2 gap-2">
      <div class="rounded-xl border border-white/10 bg-black/25 px-3 py-2">
        <p class="label-tech">实时帧率</p>
        <p class="font-mono text-3xl font-semibold leading-tight" :class="isCPU ? 'text-nexus-warn' : 'text-nexus-green'">
          {{ fps }}<span class="ml-1 text-xs text-nexus-dim">fps</span>
        </p>
      </div>
      <div class="rounded-xl border border-white/10 bg-black/25 px-3 py-2">
        <p class="label-tech">实时码率</p>
        <p class="font-mono text-3xl font-semibold leading-tight text-nexus-cyan">
          {{ bitrate }}<span class="ml-1 text-xs text-nexus-dim">kbps</span>
        </p>
      </div>
    </div>

    <div class="mt-3 space-y-1.5 font-mono text-[11px]">
      <div class="flex items-center justify-between border-b border-white/5 pb-1">
        <span class="text-nexus-dim">编码器 / CODEC</span>
        <span class="text-white">{{ codec }}</span>
      </div>
      <div class="flex items-center justify-between border-b border-white/5 pb-1">
        <span class="text-nexus-dim">输出分辨率</span>
        <span class="text-white">{{ resolution }}</span>
      </div>
      <div class="flex items-center justify-between border-b border-white/5 pb-1">
        <span class="text-nexus-dim">弱网丢包率</span>
        <span class="text-nexus-warn">{{ lossRate }}</span>
      </div>
      <div class="flex items-center justify-between">
        <span class="text-nexus-dim">STREAM KEY</span>
        <span class="text-nexus-cyan">{{ streamKey }}</span>
      </div>
    </div>

    <div class="mt-3 rounded-xl border border-white/5 bg-black/20 p-2">
      <div class="mb-1 flex items-center justify-between px-1">
        <span class="label-tech">最近 30 秒曲线</span>
        <span class="font-mono text-[10px] text-nexus-dim">
          {{ `${targetFps} fps / ${targetBitrate || '—'} kbps` }}
        </span>
      </div>
      <div ref="chartEl" class="h-44 w-full"></div>
    </div>
  </section>
</template>


