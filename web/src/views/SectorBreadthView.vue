<template>
  <div class="page-container sector-breadth-page">
    <div class="page-header"><div><h2>板块广度</h2><p>以 Longbridge Select Sector SPDR ETF 日线为代理，观察行业层面的涨跌扩散与变动集中度。</p></div><el-button :loading="loading" @click="load">刷新视图</el-button></div>
    <el-alert type="warning" :closable="false" show-icon title="当前 Longbridge 美国指数/ETF成分股接口返回空列表" description="因此本页是“行业 ETF 代理广度”，不是成分股上涨家数或个股集中度。待数据源提供可用美国成分股覆盖后，会替换为成分股口径。" />
    <el-row :gutter="16" class="summary-row" v-loading="loading">
      <el-col v-for="item in summaryCards" :key="item.label" :xs="24" :sm="12" :lg="6"><el-card shadow="never"><small>{{ item.label }}</small><strong :class="item.className">{{ item.value }}</strong><p>{{ item.note }}</p></el-card></el-col>
    </el-row>
    <el-card shadow="never"><template #header>行业 ETF 广度明细</template><el-table :data="rows" border :default-sort="{ prop: 'change_1d_pct', order: 'descending' }" empty-text="暂无行业 ETF 日线；请先在“大盘趋势”刷新 Longbridge 行情。"><el-table-column prop="label" label="板块" min-width="160" /><el-table-column prop="symbol" label="ETF" width="100" /><el-table-column label="1日变化" prop="change_1d_pct" width="120" align="right" sortable><template #default="{ row }"><strong :class="changeClass(row.change_1d_pct)">{{ formatPct(row.change_1d_pct) }}</strong></template></el-table-column><el-table-column label="5日变化" prop="change_5d_pct" width="120" align="right" sortable><template #default="{ row }"><strong :class="changeClass(row.change_5d_pct)">{{ formatPct(row.change_5d_pct) }}</strong></template></el-table-column><el-table-column label="20日变化" prop="change_20d_pct" width="120" align="right" sortable><template #default="{ row }"><strong :class="changeClass(row.change_20d_pct)">{{ formatPct(row.change_20d_pct) }}</strong></template></el-table-column><el-table-column prop="trade_date" label="收盘日" width="115" /></el-table></el-card>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, MarketTrendSeries, MarketTrendResponse } from '@/api/types'
const loading = ref(false); const rows = ref<MarketTrendSeries[]>([])
const up = computed(() => rows.value.filter(item => (item.change_1d_pct || 0) > 0).length); const down = computed(() => rows.value.filter(item => (item.change_1d_pct || 0) < 0).length)
const concentration = computed(() => { const total = rows.value.reduce((sum, item) => sum + Math.abs(item.change_1d_pct || 0), 0); if (!total) return 0; return Math.max(...rows.value.map(item => Math.abs(item.change_1d_pct || 0))) / total * 100 })
const leader = computed(() => [...rows.value].sort((a,b) => (b.change_1d_pct || 0) - (a.change_1d_pct || 0))[0])
const laggard = computed(() => [...rows.value].sort((a,b) => (a.change_1d_pct || 0) - (b.change_1d_pct || 0))[0])
const summaryCards = computed(() => [{ label: '上涨 / 下跌板块', value: `${up.value} / ${down.value}`, note: `共 ${rows.value.length} 个行业 ETF`, className: '' }, { label: '领涨', value: leader.value ? `${leader.value.label} ${formatPct(leader.value.change_1d_pct)}` : '-', note: '按当日 ETF 涨跌幅', className: 'is-up' }, { label: '领跌', value: laggard.value ? `${laggard.value.label} ${formatPct(laggard.value.change_1d_pct)}` : '-', note: '按当日 ETF 涨跌幅', className: 'is-down' }, { label: '变动集中度', value: `${concentration.value.toFixed(1)}%`, note: '最大绝对变动占全部行业变动', className: '' }])
async function load() { loading.value = true; try { const response = await apiClient.get<ApiResponse<MarketTrendResponse>>('/market-trend', { params: { history_days: 30 } }); rows.value = response.data.data.sectors || [] } catch (err:any) { ElMessage.error(err?.response?.data?.message || '加载板块广度失败') } finally { loading.value = false } }
function formatPct(value?: number | null) { if (!Number.isFinite(value)) return '-'; return `${Number(value) > 0 ? '+' : ''}${Number(value).toFixed(2)}%` }
function changeClass(value?: number | null) { return Number(value) > 0 ? 'is-up' : Number(value) < 0 ? 'is-down' : '' }
onMounted(load)
</script>
<style scoped>
.page-header { display:flex; justify-content:space-between; gap:12px; align-items:flex-start; margin-bottom:12px }.page-header h2{margin:0}.page-header p,.summary-row p{color:var(--el-text-color-secondary);margin:4px 0 0;font-size:12px}.summary-row{margin:12px 0}.summary-row small{display:block;color:var(--el-text-color-secondary)}.summary-row strong{display:block;margin-top:4px;font-size:22px}.is-up{color:var(--el-color-success)}.is-down{color:var(--el-color-danger)}
</style>
