<template>
  <section class="technical-history">
    <div class="technical-history-heading">
      <div>
        <strong>本地日线历史</strong>
        <span class="technical-history-meta">当前显示 {{ displayRows.length }} / {{ rows.length }} 个交易日；历史回填数据保存在本地</span>
      </div>
      <div class="technical-history-controls">
        <el-radio-group v-model="range" size="small" aria-label="价格历史时间范围">
          <el-radio-button value="1w">近1周</el-radio-button>
          <el-radio-button value="1m">近1月</el-radio-button>
          <el-radio-button value="3m">近3月</el-radio-button>
          <el-radio-button value="6m">近半年</el-radio-button>
          <el-radio-button value="1y">近1年</el-radio-button>
          <el-radio-button value="all">全部</el-radio-button>
        </el-radio-group>
        <el-radio-group v-model="view" size="small" aria-label="价格历史展示方式">
          <el-radio-button value="chart">图表</el-radio-button>
          <el-radio-button value="table">列表</el-radio-button>
        </el-radio-group>
      </div>
    </div>

    <template v-if="view === 'chart'">
      <div v-if="chart.points.length" class="technical-chart" role="img" :aria-label="`${ticker} 本地日线价格和成交量图表`">
        <div class="technical-chart-legend">
          <span><i class="line price" />收盘价</span>
          <span><i class="line ma20" />20 日均线</span>
          <span><i class="line ma50" />50 日均线</span>
          <span><i class="line ma200" />200 日均线</span>
          <span><i class="volume-key" />每日估算成交额</span>
          <span>价格范围 {{ formatPrice(chart.minClose) }} – {{ formatPrice(chart.maxClose) }}</span>
        </div>
        <svg class="technical-chart-svg" viewBox="0 0 720 270" preserveAspectRatio="none">
          <line v-for="y in [24, 76, 128, 180]" :key="`grid-${y}`" x1="0" :y1="y" x2="720" :y2="y" class="grid" />
          <rect v-for="point in chart.points" :key="`volume-${point.tradeDate}`" :x="point.x - chart.barWidth / 2" :y="point.volumeY" :width="chart.barWidth" :height="240 - point.volumeY" class="volume">
            <title>{{ `${point.tradeDate}｜每日估算成交额 ${formatNotional(point.dollarVolume)}` }}</title>
          </rect>
          <polyline :points="chart.pricePolyline" fill="none" class="price-line" />
          <polyline v-if="chart.ma20Polyline" :points="chart.ma20Polyline" fill="none" class="ma20-line" />
          <polyline v-if="chart.ma50Polyline" :points="chart.ma50Polyline" fill="none" class="ma50-line" />
          <polyline v-if="chart.ma200Polyline" :points="chart.ma200Polyline" fill="none" class="ma200-line" />
          <circle v-for="point in chart.points" :key="`price-${point.tradeDate}`" :cx="point.x" :cy="point.priceY" r="3" class="point">
            <title>{{ `${point.tradeDate}｜收盘 ${formatPrice(point.close)}｜MA20 ${point.ma20 == null ? '历史不足' : formatPrice(point.ma20)}｜MA50 ${point.ma50 == null ? '历史不足' : formatPrice(point.ma50)}｜MA200 ${point.ma200 == null ? '历史不足' : formatPrice(point.ma200)}｜每日估算成交额 ${formatNotional(point.dollarVolume)}｜成交量 ${formatVolume(point.volume)}｜${point.backfilled ? '历史回填' : '日常同步'} / ${point.source || '-'}` }}</title>
          </circle>
          <text x="0" y="264" class="axis-label">{{ chart.startDate }}</text>
          <text x="360" y="264" text-anchor="middle" class="axis-label">{{ chart.middleDate }}</text>
          <text x="720" y="264" text-anchor="end" class="axis-label">{{ chart.endDate }}</text>
        </svg>
        <div class="technical-chart-note">成交额按收盘价 × 当日成交量估算，不是盘中 VWAP 成交额；MA20/MA50/MA200 分别需累计满 20/50/200 个有效交易日后开始绘制。</div>
      </div>
      <el-empty v-else :image-size="72" description="暂无本地日线数据，请先回填价格历史" />
    </template>

    <el-table v-else :data="displayRows" size="small" border max-height="360" empty-text="该时间范围暂无本地日线数据">
      <el-table-column prop="trade_date" label="日期" width="120" />
      <el-table-column label="收盘价" width="120" align="right"><template #default="{ row }">{{ formatPrice(row.close_usd) }}</template></el-table-column>
      <el-table-column label="每日估算成交额" width="145" align="right"><template #default="{ row }">{{ formatNotional(row.dollar_volume_usd) }}</template></el-table-column>
      <el-table-column label="成交量" width="120" align="right"><template #default="{ row }">{{ formatVolume(row.volume) }}</template></el-table-column>
      <el-table-column label="数据来源" min-width="160"><template #default="{ row }"><el-tag :type="row.backfilled ? 'warning' : 'info'" effect="plain">{{ row.backfilled ? '历史回填' : '日常同步' }}</el-tag> {{ row.source || '-' }}</template></el-table-column>
    </el-table>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { CandidateTechnicalHistoryRow } from '@/api/types'

const props = defineProps<{ ticker: string, rows: CandidateTechnicalHistoryRow[] }>()
const view = ref<'chart' | 'table'>('chart')
const range = ref<TechnicalHistoryRange>('1y')
type TechnicalHistoryRange = '1w' | '1m' | '3m' | '6m' | '1y' | 'all'

type Point = { tradeDate: string, close: number, ma20: number | null, ma50: number | null, ma200: number | null, volume: number, dollarVolume: number, source: string, backfilled: boolean, x: number, priceY: number, volumeY: number, ma20Y: number | null, ma50Y: number | null, ma200Y: number | null }
type Chart = { points: Point[], pricePolyline: string, ma20Polyline: string, ma50Polyline: string, ma200Polyline: string, barWidth: number, minClose: number, maxClose: number, startDate: string, middleDate: string, endDate: string }

const normalizedHistory = computed(() => [...(props.rows || [])].filter((row) => Number.isFinite(row.close_usd) && row.close_usd > 0).sort((a, b) => a.trade_date.localeCompare(b.trade_date)))
const displayRows = computed(() => filterTechnicalHistoryRange(normalizedHistory.value, range.value))

const chart = computed<Chart>(() => {
  const history = normalizedHistory.value
  if (!history.length) return { points: [], pricePolyline: '', ma20Polyline: '', ma50Polyline: '', ma200Polyline: '', barWidth: 0, minClose: 0, maxClose: 0, startDate: '', middleDate: '', endDate: '' }
  const closes = history.map((row) => row.close_usd)
  const average = (index: number, period: number) => index < period - 1 ? null : closes.slice(index - period + 1, index + 1).reduce((sum, value) => sum + value, 0) / period
  const ma20 = history.map((_, index) => average(index, 20))
  const ma50 = history.map((_, index) => average(index, 50))
  const ma200 = history.map((_, index) => average(index, 200))
  const visible = history.map((row, index) => ({ row, index })).filter(({ row }) => displayRows.value.some((displayed) => displayed.trade_date === row.trade_date))
  if (!visible.length) return { points: [], pricePolyline: '', ma20Polyline: '', ma50Polyline: '', ma200Polyline: '', barWidth: 0, minClose: 0, maxClose: 0, startDate: '', middleDate: '', endDate: '' }
  const prices = visible.flatMap(({ index }) => [closes[index], ma20[index], ma50[index], ma200[index]].filter((value): value is number => value != null))
  const minClose = Math.min(...prices)
  const maxClose = Math.max(...prices)
  const range = Math.max(maxClose - minClose, Math.max(maxClose * 0.02, 0.01))
  const dollarVolume = (row: CandidateTechnicalHistoryRow) => Math.max(row.dollar_volume_usd || row.close_usd * (row.volume || 0), 0)
  const maxDollarVolume = Math.max(...visible.map(({ row }) => dollarVolume(row)), 1)
  const count = visible.length
  const priceY = (value: number) => 174 - ((value - minClose) / range) * 142
  const points = visible.map(({ row, index }, displayIndex) => ({ tradeDate: row.trade_date, close: row.close_usd, ma20: ma20[index], ma50: ma50[index], ma200: ma200[index], volume: row.volume, dollarVolume: dollarVolume(row), source: row.source, backfilled: row.backfilled, x: count === 1 ? 360 : (displayIndex / (count - 1)) * 720, priceY: priceY(row.close_usd), volumeY: 240 - (dollarVolume(row) / maxDollarVolume) * 42, ma20Y: ma20[index] == null ? null : priceY(ma20[index]!), ma50Y: ma50[index] == null ? null : priceY(ma50[index]!), ma200Y: ma200[index] == null ? null : priceY(ma200[index]!) }))
  const polyline = (key: 'priceY' | 'ma20Y' | 'ma50Y' | 'ma200Y') => points.filter((point) => point[key] != null).map((point) => `${point.x},${point[key]}`).join(' ')
  return { points, pricePolyline: polyline('priceY'), ma20Polyline: polyline('ma20Y'), ma50Polyline: polyline('ma50Y'), ma200Polyline: polyline('ma200Y'), barWidth: Math.max(2, Math.min(18, 540 / count)), minClose, maxClose, startDate: visible[0].row.trade_date, middleDate: visible[Math.floor((count - 1) / 2)].row.trade_date, endDate: visible[count - 1].row.trade_date }
})

function formatPrice(value: number) { return Number.isFinite(value) && value > 0 ? `$${value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}` : '-' }
function formatVolume(value: number) { return new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(value || 0) }
function formatNotional(value: number) { return `$${new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 2 }).format(value || 0)}` }

function filterTechnicalHistoryRange(rows: CandidateTechnicalHistoryRow[], selected: TechnicalHistoryRange) {
  if (selected === 'all' || !rows.length) return rows
  const latest = new Date(`${rows[rows.length - 1].trade_date}T12:00:00Z`)
  if (selected === '1w') latest.setUTCDate(latest.getUTCDate() - 6)
  if (selected === '1m') latest.setUTCMonth(latest.getUTCMonth() - 1)
  if (selected === '3m') latest.setUTCMonth(latest.getUTCMonth() - 3)
  if (selected === '6m') latest.setUTCMonth(latest.getUTCMonth() - 6)
  if (selected === '1y') latest.setUTCFullYear(latest.getUTCFullYear() - 1)
  const cutoff = latest.toISOString().slice(0, 10)
  return rows.filter((row) => row.trade_date >= cutoff)
}
</script>

<style scoped>
.technical-history-heading { display:flex; align-items:center; justify-content:space-between; gap:12px; margin-bottom:12px; }
.technical-history-controls { display:flex; align-items:center; justify-content:flex-end; gap:8px; flex-wrap:wrap; }
.technical-history-meta { color:var(--el-text-color-secondary); font-size:13px; margin-left:10px; }
.technical-chart { border:1px solid var(--el-border-color-lighter); border-radius:10px; padding:14px; }
.technical-chart-legend { display:flex; flex-wrap:wrap; gap:10px 16px; color:var(--el-text-color-secondary); font-size:13px; margin-bottom:8px; }
.technical-chart-legend span { display:inline-flex; align-items:center; gap:5px; }
.line { width:22px; height:0; border-top:4px solid; border-radius:3px; }.price{border-color:#409eff}.ma20{border-color:#e6a23c}.ma50{border-color:#67c23a}.ma200{border-color:#f56c6c}.volume-key{width:22px;height:12px;background:#c6e2ff;border-radius:3px;}
.technical-chart-svg { width:100%; height:300px; display:block; overflow:visible; }.grid{stroke:#ebeef5;stroke-dasharray:3 4}.volume{fill:#c6e2ff}.price-line{stroke:#409eff;stroke-width:3;stroke-linejoin:round}.ma20-line{stroke:#e6a23c;stroke-width:2.5;stroke-dasharray:5 4}.ma50-line{stroke:#67c23a;stroke-width:2.5;stroke-dasharray:5 4}.ma200-line{stroke:#f56c6c;stroke-width:2.5;stroke-dasharray:5 4}.point{fill:#409eff;stroke:#fff;stroke-width:1.5}.axis-label{fill:#909399;font-size:12px}.technical-chart-note{margin-top:4px;color:var(--el-text-color-secondary);font-size:12px;line-height:1.5}
@media (max-width: 640px) { .technical-history-heading{align-items:flex-start;flex-direction:column}.technical-history-controls{justify-content:flex-start}.technical-history-meta{display:block;margin:4px 0 0}.technical-chart-svg{height:220px} }
</style>
