<template>
  <section class="profit-history">
    <div class="profit-history-heading">
      <div>
        <div class="profit-history-title">近三年净利润（SEC）</div>
        <div class="profit-history-note">单位：百万美元；绿色代表盈利、红色代表亏损。季度优先采用 SEC 披露的三个月期间，必要时 Q4 由年度净利润减去前三季度推导。</div>
      </div>
      <el-radio-group v-model="mode" size="small" aria-label="净利润图表周期">
        <el-radio-button value="quarterly">季度</el-radio-button>
        <el-radio-button value="annual">年度</el-radio-button>
      </el-radio-group>
    </div>

    <template v-if="points.length">
      <div class="profit-history-legend">
        <span><i class="profit-history-profit" />盈利</span>
        <span><i class="profit-history-loss" />亏损</span>
        <span>共 {{ points.length }} 期</span>
        <span class="profit-history-hint">悬浮柱状查看明细</span>
      </div>

      <div class="profit-history-chart-wrap" @mouseleave="activePoint = null">
        <svg
          class="profit-history-chart"
          viewBox="0 0 900 360"
          role="img"
          :aria-label="`${history?.ticker || '标的'}近三年${mode === 'quarterly' ? '季度' : '年度'}净利润图表`"
        >
          <defs>
            <linearGradient id="profit-gradient" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stop-color="#67c23a" />
              <stop offset="100%" stop-color="#95d475" />
            </linearGradient>
            <linearGradient id="loss-gradient" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stop-color="#f89898" />
              <stop offset="100%" stop-color="#f56c6c" />
            </linearGradient>
            <filter id="profit-shadow" x="-20%" y="-20%" width="140%" height="150%">
              <feDropShadow dx="0" dy="5" stdDeviation="4" flood-color="#409eff" flood-opacity=".14" />
            </filter>
          </defs>

          <g v-for="tick in ticks" :key="tick.value">
            <line :x1="plot.left" :y1="tick.y" :x2="plot.right" :y2="tick.y" class="profit-history-grid" />
            <text :x="plot.left - 12" :y="tick.y + 4" text-anchor="end" class="profit-history-y-axis">{{ formatAxis(tick.value) }}</text>
          </g>
          <line :x1="plot.left" :y1="zeroY" :x2="plot.right" :y2="zeroY" class="profit-history-zero" />
          <text :x="plot.left - 12" :y="zeroY - 7" text-anchor="end" class="profit-history-zero-label">零</text>

          <g v-for="point in chartPoints" :key="point.key" class="profit-history-bar-group">
            <rect
              :x="point.x - point.width / 2"
              :y="point.y"
              :width="point.width"
              :height="point.height"
              rx="6"
              :fill="point.value >= 0 ? 'url(#profit-gradient)' : 'url(#loss-gradient)'"
              :class="{ 'is-active': activePoint?.key === point.key }"
              filter="url(#profit-shadow)"
            />
            <rect
              class="profit-history-hit-area"
              :x="point.x - Math.max(point.width / 2, 16)"
              :y="plot.top"
              :width="Math.max(point.width, 32)"
              :height="plot.height"
              tabindex="0"
              :aria-label="tooltip(point)"
              @mouseenter="activePoint = point"
              @focus="activePoint = point"
              @blur="activePoint = null"
            />
          </g>

          <g v-for="point in labelPoints" :key="point.key">
            <line :x1="point.x" :y1="plot.bottom + 4" :x2="point.x" :y2="plot.bottom + 9" class="profit-history-tick" />
            <text :x="point.x" :y="plot.bottom + 28" text-anchor="middle" class="profit-history-axis">{{ point.period }}</text>
          </g>
        </svg>

        <Transition name="profit-tooltip">
          <div v-if="activePoint" class="profit-history-tooltip" :class="tooltipPlacement" :style="tooltipStyle">
            <div class="profit-history-tooltip-title">{{ activePoint.period }}</div>
            <div class="profit-history-tooltip-value" :class="activePoint.value >= 0 ? 'is-profit' : 'is-loss'">
              {{ formatCurrency(activePoint.value) }}
            </div>
            <div class="profit-history-tooltip-row"><span>截至</span>{{ formatDate(activePoint.period_end) }}</div>
            <div class="profit-history-tooltip-row"><span>报表</span>{{ activePoint.form || '-' }}</div>
            <div class="profit-history-tooltip-row"><span>来源</span>{{ activePoint.derived ? '年度净利润减前三季度（推导）' : 'SEC 原始期间披露' }}</div>
          </div>
        </Transition>
      </div>
    </template>
    <el-empty v-else :image-size="64" description="暂无可用的 SEC 净利润数据" />
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { ProfitHistory, ProfitHistoryPoint } from '@/api/types'

type ChartPoint = ProfitHistoryPoint & {
  key: string
  value: number
  x: number
  y: number
  width: number
  height: number
}

const props = defineProps<{ history?: ProfitHistory | null }>()
const mode = ref<'quarterly' | 'annual'>('quarterly')
const activePoint = ref<ChartPoint | null>(null)
const plot = { left: 76, right: 866, top: 28, bottom: 282, height: 254, width: 790 }

watch(() => props.history, (history) => {
  activePoint.value = null
  if ((!history?.quarterly || history.quarterly.length === 0) && (history?.annual?.length || 0) > 0) mode.value = 'annual'
}, { immediate: true })

watch(mode, () => { activePoint.value = null })

const points = computed<ProfitHistoryPoint[]>(() => mode.value === 'quarterly'
  ? (props.history?.quarterly || [])
  : (props.history?.annual || []))

const extent = computed(() => {
  const values = points.value.map((item) => Number(item.net_income_usd) || 0)
  const rawMax = Math.max(0, ...values)
  const rawMin = Math.min(0, ...values)
  const baseSpan = Math.max(1, rawMax - rawMin)
  const padding = baseSpan * 0.12
  const max = rawMax > 0 ? rawMax + padding : padding
  const min = rawMin < 0 ? rawMin - padding : 0
  return { max, min, span: Math.max(1, max - min) }
})

const zeroY = computed(() => plot.top + (extent.value.max / extent.value.span) * plot.height)
const chartPoints = computed<ChartPoint[]>(() => points.value.map((point, index) => {
  const count = points.value.length
  const x = count <= 1 ? plot.left + plot.width / 2 : plot.left + index * (plot.width / (count - 1))
  const value = Number(point.net_income_usd) || 0
  const valueY = plot.top + ((extent.value.max - value) / extent.value.span) * plot.height
  const width = Math.max(14, Math.min(44, (plot.width / Math.max(1, count)) * 0.58))
  return {
    ...point,
    key: `${point.period_end}-${point.period}`,
    value,
    x,
    y: value >= 0 ? valueY : zeroY.value,
    width,
    height: Math.max(2, Math.abs(zeroY.value - valueY)),
  }
}))

const ticks = computed(() => Array.from({ length: 5 }, (_, index) => {
  const ratio = index / 4
  const value = extent.value.max - ratio * extent.value.span
  return { value, y: plot.top + ratio * plot.height }
}))

const labelPoints = computed(() => {
  const values = chartPoints.value
  if (values.length <= 4) return values
  const indexes = [...new Set([0, Math.round((values.length - 1) / 2), values.length - 1])]
  return indexes.map((index) => values[index])
})

const tooltipPlacement = computed(() => {
  if (!activePoint.value) return 'is-centered is-above'
  const horizontal = activePoint.value.x / 900
  const vertical = activePoint.value.value >= 0
    ? activePoint.value.y / 360
    : (activePoint.value.y + activePoint.value.height) / 360
  const side = horizontal < 0.2 ? 'is-right' : horizontal > 0.8 ? 'is-left' : 'is-centered'
  return `${side} ${vertical < 0.38 ? 'is-below' : 'is-above'}`
})

const tooltipStyle = computed(() => {
  if (!activePoint.value) return {}
  const left = (activePoint.value.x / 900) * 100
  const top = activePoint.value.value >= 0
    ? Math.max(8, (activePoint.value.y / 360) * 100)
    : Math.min(79, ((activePoint.value.y + activePoint.value.height) / 360) * 100)
  return { left: `${left}%`, top: `${top}%` }
})

function tooltip(point: ProfitHistoryPoint) {
  return `${point.period}，净利润 ${formatCurrency(Number(point.net_income_usd) || 0)}，截至 ${formatDate(point.period_end)}`
}

function formatAxis(value: number) {
  if (Math.abs(value) < 500_000) return '$0'
  return `${value < 0 ? '-' : ''}$${(Math.abs(value) / 1_000_000).toFixed(Math.abs(value) >= 100_000_000 ? 0 : 1)}M`
}

function formatCurrency(value: number) {
  const sign = value < 0 ? '-' : ''
  return `${sign}$${(Math.abs(value) / 1_000_000).toLocaleString('en-US', { maximumFractionDigits: 2 })}M`
}

function formatDate(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toISOString().slice(0, 10)
}
</script>

<style scoped>
.profit-history { display: grid; gap: 14px; }
.profit-history-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.profit-history-title { font-size: 17px; font-weight: 650; color: var(--el-text-color-primary); letter-spacing: .1px; }
.profit-history-note { max-width: 760px; margin-top: 5px; color: var(--el-text-color-secondary); font-size: 12px; line-height: 1.6; }
.profit-history-legend { display: flex; flex-wrap: wrap; gap: 14px; color: var(--el-text-color-secondary); font-size: 13px; }
.profit-history-legend span { display: inline-flex; align-items: center; gap: 5px; }
.profit-history-legend i { width: 15px; height: 9px; border-radius: 3px; display: inline-block; }
.profit-history-profit { background: linear-gradient(135deg, #67c23a, #95d475); }
.profit-history-loss { background: linear-gradient(135deg, #f89898, #f56c6c); }
.profit-history-hint { color: #a8abb2; }
.profit-history-chart-wrap { position: relative; overflow: hidden; border: 1px solid var(--el-border-color-lighter); border-radius: 10px; background: linear-gradient(180deg, #fcfdff 0%, #fff 100%); }
.profit-history-chart { display: block; width: 100%; min-height: 270px; }
.profit-history-grid { stroke: #e9eef7; stroke-dasharray: 4 5; }
.profit-history-zero { stroke: #8d99a8; stroke-width: 1.5; }
.profit-history-zero-label, .profit-history-y-axis { fill: #a8abb2; font-size: 12px; }
.profit-history-zero-label { fill: #7c8795; font-weight: 600; }
.profit-history-tick { stroke: #c7d0dd; }
.profit-history-axis { fill: #909399; font-size: 12px; }
.profit-history-bar-group rect:not(.profit-history-hit-area) { transition: opacity .18s ease, transform .18s ease; transform-origin: center; }
.profit-history-bar-group .is-active { opacity: 1; }
.profit-history-hit-area { fill: transparent; cursor: crosshair; outline: none; }
.profit-history-hit-area:focus { fill: rgba(64, 158, 255, .06); }
.profit-history-tooltip { position: absolute; z-index: 2; box-sizing: border-box; width: 230px; max-width: calc(100% - 24px); padding: 11px 13px; border: 1px solid rgba(255, 255, 255, .3); border-radius: 9px; background: rgba(35, 42, 52, .96); box-shadow: 0 12px 30px rgba(24, 39, 58, .22); color: #f4f8ff; font-size: 12px; line-height: 1.55; pointer-events: none; }
.profit-history-tooltip.is-centered.is-above { transform: translate(-50%, -108%); }
.profit-history-tooltip.is-left.is-above { transform: translate(calc(-100% - 10px), -108%); }
.profit-history-tooltip.is-right.is-above { transform: translate(10px, -108%); }
.profit-history-tooltip.is-centered.is-below { transform: translate(-50%, 10px); }
.profit-history-tooltip.is-left.is-below { transform: translate(calc(-100% - 10px), 10px); }
.profit-history-tooltip.is-right.is-below { transform: translate(10px, 10px); }
.profit-history-tooltip-title { color: #cfd8e6; font-weight: 600; }
.profit-history-tooltip-value { margin: 2px 0 5px; font-size: 18px; font-weight: 700; letter-spacing: .1px; }
.profit-history-tooltip-value.is-profit { color: #9ce680; }
.profit-history-tooltip-value.is-loss { color: #ff9c9c; }
.profit-history-tooltip-row { display: grid; grid-template-columns: 32px minmax(0, 1fr); gap: 8px; }
.profit-history-tooltip-row span { color: #aeb9c7; }
.profit-tooltip-enter-active, .profit-tooltip-leave-active { transition: opacity .12s ease; }
.profit-tooltip-enter-from, .profit-tooltip-leave-to { opacity: 0; }
@media (max-width: 640px) {
  .profit-history-heading { align-items: stretch; flex-direction: column; }
  .profit-history-chart { min-height: 220px; }
  .profit-history-tooltip { min-width: 180px; }
}
</style>
