<template>
  <div class="page-container market-trend-page">
    <div class="page-header">
      <div>
        <h2>大盘趋势</h2>
        <p>基于 Longbridge 已缓存的美股日线。用于宏观研究和相对强弱观察，不构成投资建议。</p>
      </div>
      <el-space>
        <el-select v-model="historyDays" style="width: 130px" @change="load">
          <el-option label="近 3 个月" :value="60" />
          <el-option label="近 6 个月" :value="120" />
          <el-option label="近 1 年" :value="250" />
        </el-select>
        <el-button :loading="loading" @click="load">刷新视图</el-button>
        <el-button type="primary" :loading="refreshing" @click="refresh">刷新 Longbridge 行情</el-button>
      </el-space>
    </div>

    <el-alert
      :title="`数据源：${data.source || 'Longbridge'}；最近同步：${formatDateTime(data.last_fetched_at)}`"
      description="每日自动任务会在美股收盘后同步完成日线；手动刷新仅更新本页覆盖的指数与行业 ETF。VIX 上升通常表示预期波动扩大，并非单独的涨跌信号。"
      type="info"
      :closable="false"
      show-icon
      class="market-trend-alert"
    />

    <section class="temperature-section">
      <div class="section-heading"><div><h3>美国市场温度</h3><p>Longbridge 0–100 综合读数，拆分为市场估值与情绪；用于环境观察，不构成交易信号。</p></div></div>
      <el-empty v-if="!loading && !data.temperature" description="暂无市场温度缓存；点击“刷新 Longbridge 行情”同步。" :image-size="56" />
      <article v-else-if="data.temperature" class="temperature-card">
        <div class="temperature-score"><small>综合温度</small><strong>{{ data.temperature.temperature }}</strong><span>/ 100</span><p>{{ data.temperature.description || 'Longbridge 美国市场综合读数' }}</p></div>
        <svg class="temperature-sparkline" viewBox="0 0 260 86" preserveAspectRatio="none" role="img" aria-label="美国市场温度趋势"><path :d="temperatureArea(data.temperature.history)" class="temperature-area" /><path :d="temperaturePath(data.temperature.history)" class="temperature-line" /></svg>
        <div class="temperature-metrics"><span><small>估值</small><strong>{{ data.temperature.valuation }}</strong></span><span><small>情绪</small><strong>{{ data.temperature.sentiment }}</strong></span><span><small>数据日</small><strong>{{ data.temperature.trade_date || '-' }}</strong></span></div>
      </article>
    </section>

    <section>
      <div class="section-heading">
        <div><h3>第一部分：美国大盘</h3><p>宽基指数与波动率的 1、5、20 个交易日变化。</p></div>
      </div>
      <el-empty v-if="!loading && !data.market.length" description="暂无缓存行情；请先刷新 Longbridge 行情。" :image-size="72" />
      <div v-else class="market-card-grid" v-loading="loading">
        <article v-for="item in data.market" :key="item.symbol" class="market-card">
          <div class="market-card-heading"><span>{{ item.label }}</span><small>{{ item.symbol }}</small></div>
          <div class="market-card-value">{{ formatPrice(item.close) }}</div>
          <div class="market-card-meta">收盘日 {{ item.trade_date || '-' }}</div>
          <svg class="market-sparkline" viewBox="0 0 200 56" preserveAspectRatio="none" role="img" :aria-label="`${item.label} 趋势`">
            <path v-if="sparklineArea(item.history)" :d="sparklineArea(item.history)" class="market-sparkline-area" />
            <path :d="sparklinePath(item.history)" :class="['market-sparkline-line', changeClass(item.change_20d_pct)]" />
          </svg>
          <div class="market-returns"><span><small>1日</small><strong :class="changeClass(item.change_1d_pct)">{{ formatPct(item.change_1d_pct) }}</strong></span><span><small>5日</small><strong :class="changeClass(item.change_5d_pct)">{{ formatPct(item.change_5d_pct) }}</strong></span><span><small>20日</small><strong :class="changeClass(item.change_20d_pct)">{{ formatPct(item.change_20d_pct) }}</strong></span></div>
        </article>
      </div>
    </section>

    <section class="sector-section">
      <div class="section-heading"><div><h3>第二部分：S&P 行业板块</h3><p>以 Select Sector SPDR ETF 代表板块强弱；可点击列标题排序。</p></div></div>
      <el-table :data="data.sectors" v-loading="loading" border empty-text="暂无缓存板块行情" :default-sort="{ prop: 'change_20d_pct', order: 'descending' }">
        <el-table-column prop="label" label="板块" min-width="150"><template #default="{ row }"><div class="sector-name"><strong>{{ row.label }}</strong><small>{{ row.symbol }}</small></div></template></el-table-column>
        <el-table-column prop="close" label="最新收盘" min-width="120" align="right" sortable><template #default="{ row }">{{ formatPrice(row.close) }}</template></el-table-column>
        <el-table-column prop="change_1d_pct" label="1日" min-width="105" align="right" sortable><template #default="{ row }"><strong :class="changeClass(row.change_1d_pct)">{{ formatPct(row.change_1d_pct) }}</strong></template></el-table-column>
        <el-table-column prop="change_5d_pct" label="5日" min-width="105" align="right" sortable><template #default="{ row }"><strong :class="changeClass(row.change_5d_pct)">{{ formatPct(row.change_5d_pct) }}</strong></template></el-table-column>
        <el-table-column prop="change_20d_pct" label="20日" min-width="105" align="right" sortable><template #default="{ row }"><strong :class="changeClass(row.change_20d_pct)">{{ formatPct(row.change_20d_pct) }}</strong></template></el-table-column>
        <el-table-column label="走势" min-width="210"><template #default="{ row }"><svg class="sector-sparkline" viewBox="0 0 200 42" preserveAspectRatio="none" role="img" :aria-label="`${row.label} 趋势`"><path :d="sparklinePath(row.history, 42)" :class="['market-sparkline-line', changeClass(row.change_20d_pct)]" /></svg></template></el-table-column>
        <el-table-column prop="trade_date" label="收盘日" width="115" sortable />
      </el-table>
    </section>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, MarketTrendRefreshResult, MarketTrendResponse, MarketTrendPoint, MarketTemperaturePoint } from '@/api/types'

const loading = ref(false)
const refreshing = ref(false)
const historyDays = ref(120)
const data = reactive<MarketTrendResponse>({ source: 'longbridge', market: [], sectors: [] })

async function load() {
  loading.value = true
  try {
    const response = await apiClient.get<ApiResponse<MarketTrendResponse>>('/market-trend', { params: { history_days: historyDays.value } })
    Object.assign(data, response.data.data)
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载大盘趋势失败')
  } finally {
    loading.value = false
  }
}

async function refresh() {
  refreshing.value = true
  try {
    const response = await apiClient.post<ApiResponse<MarketTrendRefreshResult>>('/market-trend/refresh')
    const result = response.data.data
    ElMessage.success(`已更新 ${result.symbols_updated}/${result.symbols_requested} 个指数和板块，保存 ${result.bars_saved} 条日线`)
    if (result.warnings.length) ElMessage.warning(`部分数据待重试：${result.warnings.join('；')}`)
    await load()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '刷新 Longbridge 大盘行情失败')
  } finally {
    refreshing.value = false
  }
}

function formatDateTime(value?: string | null) {
  if (!value) return '尚未同步'
  return new Date(value).toLocaleString('zh-CN', { hour12: false })
}

function formatPrice(value?: number | null) {
  if (!Number.isFinite(value)) return '-'
  return Number(value).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function formatPct(value?: number | null) {
  if (!Number.isFinite(value)) return '-'
  return `${Number(value) > 0 ? '+' : ''}${Number(value).toFixed(2)}%`
}

function changeClass(value?: number | null) {
  if (!Number.isFinite(value) || Number(value) === 0) return 'is-flat'
  return Number(value) > 0 ? 'is-up' : 'is-down'
}

function sparklinePath(points: MarketTrendPoint[], height = 56) {
  if (!points.length) return ''
  const values = points.map((point) => point.close)
  const min = Math.min(...values)
  const max = Math.max(...values)
  const range = max - min || 1
  return points.map((point, index) => {
    const x = points.length === 1 ? 100 : (index / (points.length - 1)) * 200
    const y = height - 5 - ((point.close - min) / range) * (height - 10)
    return `${index === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}`
  }).join(' ')
}

function sparklineArea(points: MarketTrendPoint[]) {
  const path = sparklinePath(points)
  if (!path) return ''
  return `${path} L200,56 L0,56 Z`
}

function temperaturePath(points: MarketTemperaturePoint[]) {
  if (!points.length) return ''
  return points.map((point, index) => {
    const x = points.length === 1 ? 130 : (index / (points.length - 1)) * 260
    const y = 78 - Math.max(0, Math.min(100, point.temperature)) / 100 * 70
    return `${index === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}`
  }).join(' ')
}

function temperatureArea(points: MarketTemperaturePoint[]) {
  const path = temperaturePath(points)
  return path ? `${path} L260,86 L0,86 Z` : ''
}

onMounted(load)
</script>

<style scoped>
.page-header, .section-heading { display: flex; justify-content: space-between; gap: 16px; align-items: flex-start; }
.page-header { margin-bottom: 16px; }
.page-header h2, .section-heading h3 { margin: 0; }
.page-header p, .section-heading p { margin: 7px 0 0; color: var(--el-text-color-secondary); }
.market-trend-alert { margin-bottom: 22px; }
.market-card-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(210px, 1fr)); gap: 14px; }
.market-card { min-height: 204px; padding: 15px; border: 1px solid var(--el-border-color-lighter); border-radius: 10px; background: linear-gradient(145deg, var(--el-fill-color-lighter), var(--el-bg-color)); }
.market-card-heading { display: flex; justify-content: space-between; align-items: baseline; gap: 8px; font-weight: 600; }.market-card-heading small, .market-card-meta, .sector-name small { color: var(--el-text-color-secondary); font-size: 12px; }
.market-card-value { margin-top: 12px; font-size: 25px; font-weight: 700; font-variant-numeric: tabular-nums; }.market-card-meta { margin-top: 3px; }
.market-sparkline { display: block; width: 100%; height: 58px; margin: 10px 0 6px; overflow: visible; }.sector-sparkline { display: block; width: 100%; height: 38px; }
.market-sparkline-area { fill: rgb(64 158 255 / 9%); }.market-sparkline-line { fill: none; stroke: var(--el-color-info); stroke-width: 2.2; vector-effect: non-scaling-stroke; stroke-linecap: round; stroke-linejoin: round; }.market-sparkline-line.is-up { stroke: var(--el-color-success); }.market-sparkline-line.is-down { stroke: var(--el-color-danger); }
.market-returns { display: grid; grid-template-columns: repeat(3, 1fr); gap: 5px; }.market-returns span { display: grid; gap: 2px; }.market-returns small { color: var(--el-text-color-secondary); font-size: 11px; }.market-returns strong { font-variant-numeric: tabular-nums; }
.sector-section { margin-top: 28px; }.section-heading { margin-bottom: 12px; }.sector-name { display: grid; gap: 2px; }.is-up { color: var(--el-color-success); }.is-down { color: var(--el-color-danger); }.is-flat { color: var(--el-text-color-secondary); }
.temperature-section { margin-top: 28px; }.temperature-card { display: grid; grid-template-columns: minmax(190px, .8fr) minmax(240px, 1.4fr) minmax(220px, .9fr); gap: 20px; align-items: center; padding: 18px; border: 1px solid var(--el-border-color-lighter); border-radius: 10px; background: linear-gradient(145deg, rgb(64 158 255 / 8%), var(--el-bg-color)); }.temperature-score small, .temperature-metrics small { display: block; color: var(--el-text-color-secondary); font-size: 12px; }.temperature-score strong { margin-top: 4px; font-size: 38px; line-height: 1; }.temperature-score span { margin-left: 5px; color: var(--el-text-color-secondary); }.temperature-score p { margin: 8px 0 0; color: var(--el-text-color-secondary); font-size: 13px; }.temperature-sparkline { width: 100%; height: 90px; }.temperature-area { fill: rgb(64 158 255 / 12%); }.temperature-line { fill: none; stroke: var(--el-color-primary); stroke-width: 2.4; vector-effect: non-scaling-stroke; }.temperature-metrics { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; }.temperature-metrics strong { display: block; margin-top: 3px; font-variant-numeric: tabular-nums; }
@media (max-width: 760px) { .page-header, .section-heading { display: grid; }.page-header :deep(.el-space) { flex-wrap: wrap; }.temperature-card { grid-template-columns: 1fr; } }
</style>
