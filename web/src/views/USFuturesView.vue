<template>
  <div class="page-container us-futures-page">
    <div class="page-header">
      <div><h2>美股期货</h2><p>主要美股指数、能源、贵金属和美国国债连续合约的日线走势。</p></div>
      <el-space>
        <el-select fit-input-width v-model="historyDays" style="width:130px" @change="load"><el-option label="近 3 个月" :value="60" /><el-option label="近 6 个月" :value="120" /><el-option label="近 1 年" :value="250" /></el-select>
        <el-button :loading="loading" @click="load">刷新视图</el-button>
        <el-button type="primary" :loading="refreshing" @click="refresh">刷新期货日线</el-button>
      </el-space>
    </div>
    <el-alert :title="`数据源：Yahoo Finance 连续合约；最近同步：${formatDateTime(data.last_fetched_at)}`" description="Longbridge 当前不提供期货市场代码，因此本页单独使用 Yahoo Finance 连续合约。连续合约会受换月拼接影响，适合趋势观察，不应用于精确结算或交易执行。" type="warning" :closable="false" show-icon class="futures-source-alert" />
    <el-table :data="data.futures" v-loading="loading" border empty-text="暂无期货日线；请先刷新。" :default-sort="{ prop: 'change_20d_pct', order: 'descending' }">
      <el-table-column prop="label" label="连续合约" min-width="175"><template #default="{ row }"><div class="futures-contract"><strong>{{ row.label }}</strong><small>{{ row.symbol }}</small></div></template></el-table-column>
      <el-table-column prop="close" label="收盘" min-width="108" align="right" sortable><template #default="{ row }">{{ formatPrice(row.close) }}</template></el-table-column>
      <el-table-column prop="change_1d_pct" label="1日" width="105" align="right" sortable><template #default="{ row }"><strong :class="changeClass(row.change_1d_pct)">{{ formatPct(row.change_1d_pct) }}</strong></template></el-table-column>
      <el-table-column prop="change_5d_pct" label="5日" width="105" align="right" sortable><template #default="{ row }"><strong :class="changeClass(row.change_5d_pct)">{{ formatPct(row.change_5d_pct) }}</strong></template></el-table-column>
      <el-table-column prop="change_20d_pct" label="20日" width="108" align="right" sortable><template #default="{ row }"><strong :class="changeClass(row.change_20d_pct)">{{ formatPct(row.change_20d_pct) }}</strong></template></el-table-column>
      <el-table-column label="当日区间" min-width="180" align="right"><template #default="{ row }"><el-tooltip placement="top"><template #content><div>开盘：{{ formatPrice(row.open) }}</div><div>最高：{{ formatPrice(row.high) }}</div><div>最低：{{ formatPrice(row.low) }}</div><div>成交量：{{ formatVolume(row.volume) }}</div></template><span>{{ formatPrice(row.low) }} — {{ formatPrice(row.high) }}</span></el-tooltip></template></el-table-column>
      <el-table-column label="趋势" min-width="180"><template #default="{ row }"><svg class="futures-sparkline" viewBox="0 0 180 40" preserveAspectRatio="none" role="img" :aria-label="`${row.label} 趋势`"><path :d="sparklinePath(row.history)" :class="['futures-line', changeClass(row.change_20d_pct)]" /></svg></template></el-table-column>
      <el-table-column prop="trade_date" label="收盘日" width="115" sortable />
    </el-table>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, MarketTrendPoint, USFuturesRefreshResult, USFuturesResponse } from '@/api/types'

const loading = ref(false)
const refreshing = ref(false)
const historyDays = ref(120)
const data = reactive<USFuturesResponse>({ source: 'yahoo_finance', futures: [] })

async function load() { loading.value = true; try { const response = await apiClient.get<ApiResponse<USFuturesResponse>>('/us-futures', { params: { history_days: historyDays.value } }); Object.assign(data, response.data.data) } catch (err: any) { ElMessage.error(err?.response?.data?.message || '加载美股期货失败') } finally { loading.value = false } }
async function refresh() { refreshing.value = true; try { const response = await apiClient.post<ApiResponse<USFuturesRefreshResult>>('/us-futures/refresh'); const result = response.data.data; ElMessage.success(`已更新 ${result.symbols_updated}/${result.symbols_requested} 个连续合约，保存 ${result.bars_saved} 条日线`); if (result.warnings.length) ElMessage.warning(`部分数据待重试：${result.warnings.join('；')}`); await load() } catch (err: any) { ElMessage.error(err?.response?.data?.message || '刷新美股期货失败') } finally { refreshing.value = false } }
function formatDateTime(value?: string | null) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚未同步' }
function formatPrice(value?: number | null) { return Number.isFinite(value) ? Number(value).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 }) : '-' }
function formatPct(value?: number | null) { return Number.isFinite(value) ? `${Number(value) > 0 ? '+' : ''}${Number(value).toFixed(2)}%` : '-' }
function formatVolume(value?: number | null) { return Number.isFinite(value) ? Number(value).toLocaleString('en-US') : '-' }
function changeClass(value?: number | null) { if (!Number.isFinite(value) || Number(value) === 0) return 'is-flat'; return Number(value) > 0 ? 'is-up' : 'is-down' }
function sparklinePath(points: MarketTrendPoint[]) { if (!points.length) return ''; const values = points.map((point) => point.close); const min = Math.min(...values); const range = Math.max(...values) - min || 1; return points.map((point, index) => { const x = points.length === 1 ? 90 : (index / (points.length - 1)) * 180; const y = 35 - ((point.close - min) / range) * 30; return `${index === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}` }).join(' ') }
onMounted(load)
</script>

<style scoped>
.page-header { display:flex; justify-content:space-between; gap:12px; align-items:flex-start; margin-bottom:12px; }.page-header h2 { margin:0; }.page-header p { margin:4px 0 0; color:var(--el-text-color-secondary); font-size:12px; }.futures-source-alert { margin-bottom:12px; }.futures-contract { display:grid; gap:2px; }.futures-contract small { color:var(--el-text-color-secondary); font-size:11px; }.futures-sparkline { display:block; width:100%; height:32px; }.futures-line { fill:none; stroke:var(--el-color-info); stroke-width:2.2; vector-effect:non-scaling-stroke; stroke-linecap:round; stroke-linejoin:round; }.is-up { color:var(--el-color-success); stroke:var(--el-color-success); }.is-down { color:var(--el-color-danger); stroke:var(--el-color-danger); }.is-flat { color:var(--el-text-color-secondary); stroke:var(--el-text-color-secondary); } @media (max-width:760px) { .page-header { display:grid; }.page-header :deep(.el-space) { flex-wrap:wrap; } }
</style>
