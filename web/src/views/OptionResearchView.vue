<template>
  <div class="page-container">
    <div class="page-header"><div><h2>期权与多空研究</h2><p>保存 Longbridge 的 Call/Put 汇总成交量与空头持仓快照，用于观察多空指标，不代表真实全市场净仓位。</p></div></div>
    <div class="availability-strip">
      <div><span>数据能力</span><strong>{{ research ? (research.latest ? statusLabel(research.latest.status) : '尚未同步') : listLoaded ? `已同步 ${list.total} 个标的` : listError ? '读取失败' : '读取中' }}</strong><small>{{ selectedTicker ? `${selectedTicker} · Longbridge 日级快照` : 'Longbridge 本地快照' }}</small></div>
      <div><span>期权覆盖</span><strong>{{ research ? (research.latest ? hasOptions(research.latest) ? '可用' : '未覆盖' : '待查询') : listLoaded ? `${list.summary.option_covered} 个标的可用` : '待查询' }}</strong><small>{{ research?.latest?.option_volume_as_of || 'Call / Put 汇总成交量' }}</small></div>
      <div><span>空头覆盖</span><strong>{{ research ? (research.latest ? hasShort(research.latest) ? '可用' : '未覆盖' : '待查询') : listLoaded ? `${list.summary.short_covered} 个标的可用` : '待查询' }}</strong><small>{{ research?.latest?.short_reported_at || '提供方报告期持仓' }}</small></div>
      <div><span>{{ research ? '历史深度' : '最近同步' }}</span><strong :class="{ 'sync-time': !research }">{{ research ? research.history.length : formatDate(list.summary.last_fetched_at) }}</strong><small>{{ research ? '最多展示 30 个日级快照' : '本地同步时间，与报告期分开' }}</small></div>
    </div>
    <el-alert type="info" :closable="false" show-icon title="打开页面和刷新列表只读取本地数据。日级研究快照不保存完整期权链；未覆盖不等于数值为零。" />
    <el-card shadow="never" class="query-card">
      <el-form inline @submit.prevent="load(true)">
        <el-form-item label="标的"><el-input v-model="ticker" placeholder="例如 NVDA / SPY" clearable /></el-form-item>
        <el-button :loading="detailLoading" @click="load(true)">查询本地快照</el-button>
        <el-button type="primary" :loading="refreshing" @click="refresh">刷新 Longbridge 数据</el-button>
      </el-form>
    </el-card>
    <el-card ref="listCard" shadow="never" class="snapshot-list">
      <template #header><div class="list-heading"><div><strong>已同步标的</strong><p>每个标的显示最新快照，按本地同步时间从新到旧排序。点击标的查看历史。</p></div><el-button :loading="listLoading" @click="loadList">刷新列表</el-button></div></template>
      <el-alert v-if="listError" :title="listError" type="error" :closable="false" class="message" />
      <el-table :data="list.items" v-loading="listLoading" border :empty-text="listError ? '列表读取失败，请点击刷新列表重试。' : '暂无本地快照；输入标的后可刷新 Longbridge 数据。'">
        <el-table-column prop="ticker" label="标的" width="95"><template #default="{ row }"><el-button link type="primary" @click="selectTicker(row.ticker)">{{ row.ticker }}</el-button></template></el-table-column>
        <el-table-column label="数据状态" width="105"><template #default="{ row }"><el-tag :type="row.status === 'available' ? 'success' : row.status === 'partial' ? 'warning' : 'info'">{{ statusLabel(row.status) }}</el-tag></template></el-table-column>
        <el-table-column label="Call" min-width="100" align="right"><template #default="{ row }">{{ integer(row.call_volume) }}</template></el-table-column>
        <el-table-column label="Put" min-width="100" align="right"><template #default="{ row }">{{ integer(row.put_volume) }}</template></el-table-column>
        <el-table-column label="Put / Call" width="110" align="right"><template #default="{ row }">{{ decimal(row.put_call_volume_ratio) }}</template></el-table-column>
        <el-table-column label="空头比例" width="105" align="right"><template #default="{ row }">{{ pct(row.short_ratio_pct) }}</template></el-table-column>
        <el-table-column label="days to cover" width="120" align="right"><template #default="{ row }">{{ decimal(row.days_to_cover) }}</template></el-table-column>
        <el-table-column prop="observed_date" label="快照日" width="115" />
        <el-table-column label="本地同步时间 ↓" min-width="185"><template #default="{ row }">{{ formatDate(row.fetched_at) }}</template></el-table-column>
      </el-table>
      <el-pagination v-if="list.total > 0" v-model:current-page="page" :page-size="pageSize" :total="list.total" layout="total, prev, pager, next" @current-change="loadList" class="list-pagination" />
    </el-card>
    <section v-if="research" ref="detailSection" class="detail-section">
      <div class="detail-heading"><h3>{{ selectedTicker }} · 期权与多空详情</h3><el-button @click="returnToList">返回列表</el-button></div>
    <template v-if="research">
      <el-alert v-if="research.message" type="info" :closable="false" class="message" :title="research.message" />
      <el-empty v-if="!research.latest" description="暂无快照；可点击“刷新 Longbridge 数据”，或等待已开启的候选 / 监控标的任务。" />
      <template v-else>
        <el-row :gutter="16" class="summary"><el-col :xs="24" :md="8"><el-card shadow="never"><template #header>期权成交量</template><el-descriptions :column="1"><el-descriptions-item label="Call">{{ integer(research.latest.call_volume) }}</el-descriptions-item><el-descriptions-item label="Put">{{ integer(research.latest.put_volume) }}</el-descriptions-item><el-descriptions-item label="Put / Call">{{ decimal(research.latest.put_call_volume_ratio) }}</el-descriptions-item><el-descriptions-item label="期权数据日">{{ research.latest.option_volume_as_of || research.latest.observed_date }}</el-descriptions-item></el-descriptions></el-card></el-col><el-col :xs="24" :md="8"><el-card shadow="never"><template #header>空头持仓</template><el-descriptions :column="1"><el-descriptions-item label="空头比例">{{ pct(research.latest.short_ratio_pct) }}</el-descriptions-item><el-descriptions-item label="空头股数">{{ integer(research.latest.current_shares_short) }}</el-descriptions-item><el-descriptions-item label="日均成交量">{{ integer(research.latest.avg_daily_share_volume) }}</el-descriptions-item><el-descriptions-item label="days to cover">{{ decimal(research.latest.days_to_cover) }}</el-descriptions-item></el-descriptions></el-card></el-col><el-col :xs="24" :md="8"><el-card shadow="never"><template #header>研究提示</template><el-empty v-if="!(research.latest.anomalies || []).length" description="当前无显著异常标签" :image-size="44" /><div v-else class="anomalies"><el-tag v-for="item in research.latest.anomalies" :key="item.kind" :type="item.severity === 'warning' ? 'warning' : 'info'" effect="plain">{{ item.label }}</el-tag><p v-for="item in research.latest.anomalies" :key="item.kind + '-detail'">{{ item.detail }}</p></div></el-card></el-col></el-row>
        <el-card shadow="never" class="history"><template #header><strong>历史快照</strong></template><el-table :data="research.history || []" border><el-table-column prop="observed_date" label="快照日" width="120" /><el-table-column label="Call" align="right"><template #default="{ row }">{{ integer(row.call_volume) }}</template></el-table-column><el-table-column label="Put" align="right"><template #default="{ row }">{{ integer(row.put_volume) }}</template></el-table-column><el-table-column label="Put/Call" align="right"><template #default="{ row }">{{ decimal(row.put_call_volume_ratio) }}</template></el-table-column><el-table-column label="空头比例" align="right"><template #default="{ row }">{{ pct(row.short_ratio_pct) }}</template></el-table-column><el-table-column label="days to cover" align="right"><template #default="{ row }">{{ decimal(row.days_to_cover) }}</template></el-table-column><el-table-column label="提示" min-width="220"><template #default="{ row }">{{ anomalyLabels(row) }}</template></el-table-column><el-table-column label="同步时间" width="170"><template #default="{ row }">{{ formatDate(row.fetched_at) }}</template></el-table-column></el-table></el-card>
      </template>
    </template>
    </section>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, OptionResearchList, OptionResearchSnapshot, OptionResearchView } from '@/api/types'
const ticker = ref('')
const selectedTicker = ref('')
const route = useRoute()
const research = ref<OptionResearchView | null>(null)
const refreshing = ref(false)
const detailLoading = ref(false)
const listLoading = ref(false)
const listLoaded = ref(false)
const listError = ref('')
const page = ref(1)
const pageSize = 10
const list = reactive<OptionResearchList>({ items: [], total: 0, page: 1, page_size: pageSize, summary: {total: 0, option_covered: 0, short_covered: 0} })
const detailSection = ref<HTMLElement | null>(null)
const listCard = ref<{ $el: HTMLElement } | null>(null)
let detailRequest = 0
let listRequest = 0
function symbol() { return ticker.value.trim().toUpperCase() }
async function loadList() {
  const request = ++listRequest
  listLoading.value = true
  listError.value = ''
  try {
    const res = await apiClient.get<ApiResponse<OptionResearchList>>('/discovery/options', { params: {page: page.value, page_size: pageSize} })
    if (request !== listRequest) return
    Object.assign(list, res.data.data)
    listLoaded.value = true
  } catch (err: any) {
    if (request === listRequest) listError.value = err?.response?.data?.message || '读取期权快照列表失败，请重试'
  } finally { if (request === listRequest) listLoading.value = false }
}
async function scrollToDetail() { await nextTick(); detailSection.value?.scrollIntoView({behavior: 'smooth', block: 'start'}) }
async function load(scroll = false) {
  const value = symbol()
  if (!value) { ElMessage.warning('请输入标的代码'); return }
  const request = ++detailRequest
  detailLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<OptionResearchView>>(`/discovery/options/${encodeURIComponent(value)}`)
    if (request !== detailRequest) return
    research.value = res.data.data
    selectedTicker.value = value
    if (scroll) await scrollToDetail()
  } catch (err: any) { if (request === detailRequest) ElMessage.error(err?.response?.data?.message || '查询期权研究失败') }
  finally { if (request === detailRequest) detailLoading.value = false }
}
async function selectTicker(value: string) { ticker.value = value; await load(true) }
function returnToList() { ++detailRequest; detailLoading.value = false; research.value = null; selectedTicker.value = ''; listCard.value?.$el.scrollIntoView({behavior: 'smooth', block: 'start'}) }
async function refresh() {
  const value = symbol()
  if (!value) { ElMessage.warning('请输入标的代码'); return }
  if (refreshing.value) return
  refreshing.value = true
  detailLoading.value = false
  const request = ++detailRequest
  try {
    const res = await apiClient.post<ApiResponse<{research: OptionResearchView; refresh?: {message: string}}>>(`/discovery/options/${encodeURIComponent(value)}/refresh`, {}, { timeout: 60000 })
    if (request === detailRequest) { research.value = res.data.data.research; selectedTicker.value = value }
    ElMessage.success(res.data.data.refresh?.message || '已刷新')
    page.value = 1
    await loadList()
    if (request === detailRequest) await scrollToDetail()
  } catch (err: any) { ElMessage.error(err?.response?.data?.message || '刷新失败，请检查 Longbridge 权限与配置') }
  finally { refreshing.value = false }
}
function anomalyLabels(row: OptionResearchSnapshot) { return (row.anomalies || []).map(item => item.label).join('；') || '-' }
function hasOptions(row: OptionResearchSnapshot) { return row.call_volume != null || row.put_volume != null }
function hasShort(row: OptionResearchSnapshot) { return row.short_ratio_pct != null || row.current_shares_short != null }
function integer(value?: number | null) { return Number.isFinite(value) ? Number(value).toLocaleString('en-US') : '-' }
function decimal(value?: number | null) { return Number.isFinite(value) ? Number(value).toFixed(2) : '-' }
function pct(value?: number | null) { return Number.isFinite(value) ? `${Number(value).toFixed(2)}%` : '-' }
function formatDate(value?: string) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false, timeZone: 'Asia/Hong_Kong' }) : '-' }
function statusLabel(value?: string) { if (value === 'available') return '完整可用'; if (value === 'partial') return '部分可用'; if (value === 'unavailable') return '暂不可用'; return value || '未知' }
onMounted(async () => {
  const value = route.query.ticker
  if (typeof value === 'string' && value.trim()) ticker.value = value.toUpperCase()
  await Promise.all([loadList(), ticker.value ? load(true) : Promise.resolve()])
})
</script>

<style scoped>
.availability-strip{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));border:1px solid var(--el-border-color-lighter);border-radius:8px;background:var(--el-bg-color);margin-bottom:12px}.availability-strip>div{padding:10px 14px;display:grid;gap:2px;border-right:1px solid var(--el-border-color-lighter)}.availability-strip>div:last-child{border-right:0}.availability-strip span,.availability-strip small{font-size:12px;color:var(--el-text-color-secondary)}.availability-strip strong{font-size:18px}.query-card,.message,.summary,.history,.snapshot-list{margin-top:12px}.anomalies{display:flex;flex-direction:column;align-items:flex-start;gap:6px}.anomalies p{font-size:12px;color:var(--el-text-color-secondary);margin:0}@media(max-width:760px){.availability-strip{grid-template-columns:repeat(2,1fr)}}
.list-heading,.detail-heading{display:flex;align-items:center;justify-content:space-between;gap:12px}.list-heading p{margin:5px 0 0;color:var(--el-text-color-secondary);font-size:12px}.list-pagination{margin-top:16px;justify-content:flex-end}.detail-section{margin-top:24px;scroll-margin-top:16px}.detail-heading h3{margin:0}.availability-strip .sync-time{font-size:14px}.snapshot-list :deep(.el-card__header){padding:14px 20px}@media(max-width:760px){.list-heading{align-items:flex-start}.list-pagination{justify-content:flex-start}}
</style>
