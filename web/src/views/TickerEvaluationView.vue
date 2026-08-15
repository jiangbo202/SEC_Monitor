<template>
  <div class="page-container ticker-evaluation-page">
    <div class="page-header">
      <div>
        <h2>标的评估</h2>
        <p>输入股票或 ETF，按当前的小盘候选规则生成一份独立、可追溯的评估快照。</p>
      </div>
    </div>
    <el-alert type="warning" :closable="false" show-icon>
      <template #title>研究辅助工具，不构成投资建议或自动交易指令</template>
      <div>股票复用 SEC 基本面、Form 4、资本风险、趋势、动量、量价和交易纪律逻辑；ETF 不适用发行人基本面与 Form 4，页面会明确显示不适用。</div>
    </el-alert>

    <el-card shadow="never" class="query-card">
      <el-form inline @submit.prevent="evaluate">
        <el-form-item label="标的"><el-input v-model="ticker" placeholder="例如 NVDA / SPY" clearable @keyup.enter="evaluate" /></el-form-item>
        <el-form-item label="类型">
          <el-radio-group v-model="targetType">
            <el-radio-button label="">自动识别</el-radio-button>
            <el-radio-button label="stock">股票</el-radio-button>
            <el-radio-button label="etf">ETF</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-button type="primary" :loading="evaluating" @click="evaluate">开始评估</el-button>
      </el-form>
    </el-card>

    <template v-if="selected">
      <div class="result-heading">
        <div>
          <h3>{{ selected.ticker }} <small>{{ selected.company_name || '-' }}</small></h3>
          <p>评估时间：{{ formatDate(selected.evaluated_at) }} · {{ selected.target_type === 'etf' ? 'ETF' : '股票' }} · <el-tag size="small" :type="selected.status === 'ready' ? 'success' : 'warning'">{{ selected.status === 'ready' ? '完整' : '部分数据' }}</el-tag> <el-tag size="small" effect="plain" :type="selected.data_source === 'ad_hoc_evaluation' ? 'primary' : 'info'">{{ sourceLabel(selected.data_source) }}</el-tag></p>
        </div>
        <div class="ai-actions">
          <el-select v-model="selectedAIProvider" placeholder="选择 AI 模型" style="width: 210px" :disabled="!aiProviders.length">
            <el-option v-for="provider in aiProviders" :key="provider.id" :label="`${provider.name} · ${provider.model}`" :value="provider.id" />
          </el-select>
          <el-select v-model="selectedAIPromptTemplate" placeholder="选择模板" style="width: 190px" :disabled="!aiPromptTemplates.length"><el-option v-for="template in aiPromptTemplates" :key="template.id" :label="template.name" :value="template.id" /></el-select>
          <el-button type="primary" :loading="generatingAI" :disabled="!selectedAIProvider || !selectedAIPromptTemplate" @click="generateAIAnalysis">AI 研判（手动）</el-button>
          <el-button @click="loadHistory">刷新历史</el-button>
        </div>
      </div>
      <el-alert v-if="!aiProviders.length" type="info" :closable="false" class="warnings" title="尚未配置可用 AI 模型；请在“系统配置 → AI 分析”添加 DeepSeek 或其他 OpenAI 兼容供应商。" />
      <el-card v-if="aiAnalyses.length" shadow="never" class="discipline ai-analysis-card">
        <template #header><div class="ai-analysis-heading"><strong>AI 研判记录</strong><span>仅由手动点击生成；结果基于当次本地评估快照，不会改变评分或交易纪律。</span></div></template>
        <el-select v-model="selectedAnalysisID" size="small" class="ai-analysis-select"><el-option v-for="item in aiAnalyses" :key="item.id" :label="`${item.provider_name} · ${item.model} · ${item.template_name || '历史模板'} · ${formatDate(item.requested_at)}`" :value="item.id" /></el-select>
        <template v-if="activeAIAnalysis">
          <el-alert v-if="activeAIAnalysis.status === 'queued' || activeAIAnalysis.status === 'running'" type="warning" :closable="false" title="AI 研判正在后台处理，页面会自动刷新结果。" class="warnings" />
          <el-alert v-else-if="activeAIAnalysis.status === 'failed'" type="error" :closable="false" :title="activeAIAnalysis.error_message || 'AI 调用失败'" class="warnings" />
          <template v-else>
            <AIRequestPrompt :system-prompt="activeAIAnalysis.system_prompt" :user-prompt="activeAIAnalysis.user_prompt" />
            <div class="ai-analysis-content"><MarkdownContent :content="activeAIAnalysis.content" /></div>
          </template>
        </template>
      </el-card>
      <el-alert v-if="selected.warnings?.length" type="warning" :closable="false" class="warnings">
        <template #title>数据边界</template>
        <div v-for="warning in selected.warnings" :key="warning">{{ warning }}</div>
      </el-alert>
      <el-row :gutter="16">
        <el-col :xs="24" :md="8"><el-card shadow="never"><template #header><strong>基本面</strong></template><template v-if="selected.fundamental_status === 'not_applicable'"><el-empty description="ETF：不适用" :image-size="44" /></template><template v-else><div class="score"><strong>{{ selected.candidate_score.total_score }}</strong><span>/ 100</span><el-tag size="small" effect="plain">{{ selected.candidate_score.grade || '未分级' }}</el-tag></div><div class="metrics"><span>收入增长 <b>{{ selected.candidate_score.revenue_growth_score }}/30</b></span><span>现金储备 <b>{{ selected.candidate_score.cash_runway_score }}/20</b></span><span>内幕买入 <b>{{ selected.candidate_score.insider_score }}/20</b></span><span>毛利率 <b>{{ selected.candidate_score.gross_margin_score }}/10</b></span><span>稀释风险 <b>{{ selected.candidate_score.dilution_risk_score }}/10</b></span><span>赛道 <b>{{ selected.candidate_score.sector_score }}/10</b></span></div></template></el-card></el-col>
        <el-col :xs="24" :md="8"><el-card shadow="never"><template #header><strong>短线复核</strong></template><div class="score"><strong>{{ selected.candidate_score.review_priority_score }}</strong><span>/ 100</span></div><div class="metrics"><span v-for="reason in selected.candidate_score.review_priority_reasons || []" :key="reason.label">{{ reason.label }} <b :class="reason.points < 0 ? 'negative' : 'positive'">{{ reason.points > 0 ? '+' : '' }}{{ reason.points }}</b></span></div></el-card></el-col>
        <el-col :xs="24" :md="8"><el-card shadow="never"><template #header><strong>量价 / 流动性</strong></template><div class="metrics"><span>收盘价 <b>{{ price(selected.candidate_score.price_close_usd) }} USD</b></span><span>20 日均成交额 <b>{{ usd(selected.candidate_score.market_quality?.average_dollar_volume_usd) }}</b></span><span>20 日动量 <b>{{ pct(selected.candidate_score.market_quality?.momentum_pct) }}</b></span><span>波动率 <b>{{ pct(selected.candidate_score.market_quality?.volatility_pct) }}</b></span><span>量比 <b>{{ ratio(selected.candidate_score.technical?.volume_ratio_20) }}</b></span><span>流动性 <b>{{ selected.candidate_score.technical?.liquidity_status || '-' }}</b></span></div></el-card></el-col>
      </el-row>
      <el-card shadow="never" class="discipline"><template #header><strong>趋势 / 动量与交易纪律</strong></template><el-descriptions :column="3" border><el-descriptions-item label="技术状态">{{ selected.candidate_score.technical?.status || '-' }}</el-descriptions-item><el-descriptions-item label="距 MA20">{{ pct(selected.candidate_score.technical?.distance_to_ma20_pct) }}</el-descriptions-item><el-descriptions-item label="距 20 日高点">{{ pct(selected.candidate_score.technical?.distance_to_20d_high_pct) }}</el-descriptions-item><el-descriptions-item label="入场触发">{{ selected.candidate_score.technical?.trade_setup?.entry_trigger || '等待触发条件' }}</el-descriptions-item><el-descriptions-item label="止损">{{ price(selected.candidate_score.technical?.trade_setup?.stop_loss_usd) }} USD</el-descriptions-item><el-descriptions-item label="止盈区间">{{ price(selected.candidate_score.technical?.trade_setup?.take_profit_zone_low_usd) }} – {{ price(selected.candidate_score.technical?.trade_setup?.take_profit_zone_high_usd) }} USD</el-descriptions-item></el-descriptions></el-card>
    </template>

    <el-card shadow="never" class="history">
      <template #header>
        <div class="history-heading">
          <div><strong>历史评估记录</strong><span class="history-hint">展开每行可查看该次评估保存的档案、共识、估值及机构/基金持仓快照；不会重新请求第三方接口。</span></div>
          <div class="history-filters"><el-input v-model="historyTicker" placeholder="按标的筛选" clearable @change="applyHistoryFilters" @clear="applyHistoryFilters" /><el-select v-model="historyEntryTrigger" filterable clearable placeholder="选择入场触发" @change="applyHistoryFilters"><el-option v-for="option in historyEntryTriggerOptions" :key="option" :label="option" :value="option" /></el-select></div>
        </div>
      </template>
      <el-table :data="history" v-loading="historyLoading" border empty-text="暂无评估记录" :default-sort="{ prop: historySortBy, order: historySortOrder === 'asc' ? 'ascending' : 'descending' }" @sort-change="handleHistorySort">
        <el-table-column type="expand" width="48" fixed="left">
          <template #default="{ row }">
            <div class="research-details">
              <div class="research-title">{{ row.ticker }} 研究补充快照 <span>与本次评估同一时间保存</span></div>
              <el-alert v-if="!row.research" type="info" :closable="false" show-icon title="该历史记录生成于研究补充功能上线前，未保存扩展研究快照；重新评估一次即可生成。" />
              <template v-else>
                <el-row :gutter="14">
                  <el-col :xs="24" :lg="8"><section class="research-section"><h4>公司 / 基金档案</h4><el-descriptions :column="1" size="small" border><el-descriptions-item label="名称">{{ row.research.profile?.company_name || row.company_name || '-' }}</el-descriptions-item><el-descriptions-item label="行业">{{ row.research.profile?.sic_description || row.research.profile?.sector_category || '-' }}</el-descriptions-item><el-descriptions-item label="资料来源">{{ row.research.profile?.summary_source || '-' }}</el-descriptions-item><el-descriptions-item label="业务简介"><span class="business-summary">{{ row.research.profile?.business_summary || '暂无可核验的业务简介。' }}</span></el-descriptions-item></el-descriptions></section></el-col>
                  <el-col :xs="24" :lg="8"><section class="research-section"><h4>分析师共识</h4><template v-if="row.research.analyst_rating?.latest"><el-descriptions :column="2" size="small" border><el-descriptions-item label="建议">{{ row.research.analyst_rating.latest.recommendation || '-' }}</el-descriptions-item><el-descriptions-item label="覆盖数">{{ row.research.analyst_rating.latest.analyst_count }}</el-descriptions-item><el-descriptions-item label="平均目标价">{{ microsPrice(row.research.analyst_rating.latest.target_average_micros, row.research.analyst_rating.latest.currency) }}</el-descriptions-item><el-descriptions-item label="目标区间">{{ microsPrice(row.research.analyst_rating.latest.target_low_micros, row.research.analyst_rating.latest.currency) }} – {{ microsPrice(row.research.analyst_rating.latest.target_high_micros, row.research.analyst_rating.latest.currency) }}</el-descriptions-item><el-descriptions-item label="买入 / 持有" :span="2">{{ row.research.analyst_rating.latest.strong_buy_count + row.research.analyst_rating.latest.buy_count }} / {{ row.research.analyst_rating.latest.hold_count }}</el-descriptions-item></el-descriptions></template><el-empty v-else :description="row.research.analyst_rating?.message || '暂无分析师共识'" :image-size="40" /></section></el-col>
                  <el-col :xs="24" :lg="8"><section class="research-section"><h4>估值与 EPS 预期</h4><el-descriptions :column="2" size="small" border><el-descriptions-item label="PE">{{ decimal(row.research.valuation_research?.latest?.metrics?.pe?.current) }}</el-descriptions-item><el-descriptions-item label="PB">{{ decimal(row.research.valuation_research?.latest?.metrics?.pb?.current) }}</el-descriptions-item><el-descriptions-item label="PS">{{ decimal(row.research.valuation_research?.latest?.metrics?.ps?.current) }}</el-descriptions-item><el-descriptions-item label="EPS 均值">{{ decimal(row.research.market_research?.eps_forecast?.latest?.mean) }}</el-descriptions-item><el-descriptions-item label="EPS 区间" :span="2">{{ decimal(row.research.market_research?.eps_forecast?.latest?.low) }} – {{ decimal(row.research.market_research?.eps_forecast?.latest?.high) }}</el-descriptions-item></el-descriptions><p class="research-note">{{ row.research.valuation_research?.message || row.research.market_research?.eps_forecast?.message || '暂无估值或 EPS 覆盖。' }}</p></section></el-col>
                </el-row>
                <el-row :gutter="14" class="holding-row">
                  <el-col :xs="24" :lg="12"><section class="research-section"><h4>机构股东（最近披露）</h4><el-table :data="topRows(row.research.market_research?.institutional_holders, 10)" size="small" max-height="250" empty-text="暂无机构股东披露"><el-table-column prop="holder_name" label="机构" min-width="190" show-overflow-tooltip /><el-table-column prop="institution_type" label="类别" width="110" /><el-table-column label="持股比例" width="110" align="right"><template #default="{ row: holding }">{{ pct(holding.percent_of_shares) }}</template></el-table-column><el-table-column prop="report_date" label="报告日" width="120" /></el-table></section></el-col>
                  <el-col :xs="24" :lg="12"><section class="research-section"><h4>{{ row.target_type === 'etf' ? '持有该 ETF 的基金 / ETF' : '基金 / ETF 披露组合权重' }}</h4><el-table :data="topRows(row.research.market_research?.fund_holders, 10)" size="small" max-height="250" empty-text="暂无基金 / ETF 披露"><el-table-column prop="fund_symbol" label="简称" width="95" /><el-table-column prop="fund_name" label="基金 / ETF" min-width="180" show-overflow-tooltip /><el-table-column label="组合权重" width="110" align="right"><template #default="{ row: holding }">{{ pct(holding.position_ratio) }}</template></el-table-column><el-table-column prop="report_date" label="报告日" width="120" /></el-table><p class="research-note">{{ row.research.holdings_scope_note }}</p></section></el-col>
                </el-row>
                <section class="research-section option-summary"><h4>期权与空头研究（当次快照）</h4><template v-if="row.research.option_research?.latest"><el-descriptions :column="4" size="small" border><el-descriptions-item label="Call 成交量">{{ integer(row.research.option_research.latest.call_volume) }}</el-descriptions-item><el-descriptions-item label="Put 成交量">{{ integer(row.research.option_research.latest.put_volume) }}</el-descriptions-item><el-descriptions-item label="Put/Call">{{ decimal(row.research.option_research.latest.put_call_volume_ratio) }}</el-descriptions-item><el-descriptions-item label="空头比例">{{ pct(row.research.option_research.latest.short_ratio_pct) }}</el-descriptions-item><el-descriptions-item label="空头股数">{{ integer(row.research.option_research.latest.current_shares_short) }}</el-descriptions-item><el-descriptions-item label="days to cover">{{ decimal(row.research.option_research.latest.days_to_cover) }}</el-descriptions-item><el-descriptions-item label="空头报告日" :span="2">{{ row.research.option_research.latest.short_reported_at || '-' }}</el-descriptions-item></el-descriptions><p v-for="anomaly in row.research.option_research.latest.anomalies || []" :key="anomaly.kind" class="research-note">{{ anomaly.label }}：{{ anomaly.detail }}</p></template><el-empty v-else :description="row.research.option_research?.message || '暂无期权与空头研究快照'" :image-size="36" /></section>
                <div class="research-foot"><el-tag v-for="source in row.research.sources || []" :key="source.name" size="small" effect="plain" :type="source.status === 'available' ? 'success' : source.status === 'partial' ? 'warning' : 'info'">{{ source.name }}：{{ researchStatus(source.status) }}</el-tag><span v-for="note in row.research.refresh_notes || []" :key="note">{{ note }}</span></div>
              </template>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="ticker" label="标的" width="108" fixed="left" />
        <el-table-column prop="company_name" label="公司 / 基金" min-width="180" show-overflow-tooltip />
        <el-table-column prop="fundamental" label="基本面" width="100" align="right" sortable="custom"><template #default="{ row }"><el-tooltip :content="fundamentalTooltip(row)" placement="top"><el-tag effect="plain">{{ row.candidate_score?.total_score ?? '-' }}</el-tag></el-tooltip></template></el-table-column>
        <el-table-column prop="review" label="短线复核" width="110" align="right" sortable="custom"><template #default="{ row }"><el-tooltip :content="reviewTooltip(row)" placement="top"><el-tag type="warning" effect="plain">{{ row.candidate_score?.review_priority_score ?? '-' }}</el-tag></el-tooltip></template></el-table-column>
        <el-table-column prop="technical_status" label="技术状态" width="105" sortable="custom"><template #default="{ row }">{{ row.candidate_score?.technical?.status || '-' }}</template></el-table-column>
        <el-table-column label="收盘价" width="130" align="right"><template #default="{ row }"><el-tooltip :content="priceSnapshotTooltip(row)" placement="top"><span>{{ historicalClose(row) }}</span></el-tooltip></template></el-table-column>
        <el-table-column prop="distance_to_ma20" label="距 MA20" width="105" align="right" sortable="custom"><template #default="{ row }"><span :class="signedMetricClass(row.candidate_score?.technical?.distance_to_ma20_pct)">{{ pct(row.candidate_score?.technical?.distance_to_ma20_pct) }}</span></template></el-table-column>
        <el-table-column prop="distance_to_20d_high" label="距 20 日高点" width="120" align="right" sortable="custom"><template #default="{ row }"><span :class="signedMetricClass(row.candidate_score?.technical?.distance_to_20d_high_pct)">{{ pct(row.candidate_score?.technical?.distance_to_20d_high_pct) }}</span></template></el-table-column>
        <el-table-column label="入场触发" min-width="150" show-overflow-tooltip><template #default="{ row }">{{ row.candidate_score?.technical?.trade_setup?.entry_trigger || '等待触发条件' }}</template></el-table-column>
        <el-table-column label="止损" width="120" align="right"><template #default="{ row }">{{ price(row.candidate_score?.technical?.trade_setup?.stop_loss_usd) }} USD</template></el-table-column>
        <el-table-column label="止盈区间" width="178" align="right"><template #default="{ row }">{{ takeProfitZone(row) }}</template></el-table-column>
        <el-table-column prop="evaluated_at" label="评估时间" width="170" sortable="custom"><template #default="{ row }">{{ formatDate(row.evaluated_at) }}</template></el-table-column>
        <el-table-column label="操作" width="88" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="selectHistory(row)">查看</el-button></template></el-table-column>
      </el-table>
      <el-pagination v-model:current-page="historyPage" v-model:page-size="historyPageSize" :total="historyTotal" :page-sizes="[20, 50, 100]" layout="total, sizes, prev, pager, next" class="pagination" @current-change="loadHistory" @size-change="applyHistoryFilters" />
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import AIRequestPrompt from '@/components/AIRequestPrompt.vue'
import MarkdownContent from '@/components/MarkdownContent.vue'

type Evaluation = any
const ticker = ref('')
const targetType = ref('')
const evaluating = ref(false)
const selected = ref<Evaluation | null>(null)
const history = ref<Evaluation[]>([])
const historyTicker = ref('')
const historyEntryTrigger = ref('')
const historyEntryTriggerOptions = ref<string[]>([])
const historyLoading = ref(false)
const historyPage = ref(1)
const historyPageSize = ref(20)
const historyTotal = ref(0)
const historySortBy = ref('evaluated_at')
const historySortOrder = ref<'asc' | 'desc'>('desc')
type AIProvider = { id: string; name: string; model: string }
type AIPromptTemplate = { id: string; name: string }
type AIAnalysis = { id: number; provider_name: string; model: string; template_name?: string; content: string; status: string; error_message?: string; system_prompt?: string; user_prompt?: string; requested_at: string }
const aiProviders = ref<AIProvider[]>([])
const aiPromptTemplates = ref<AIPromptTemplate[]>([])
const selectedAIProvider = ref('')
const selectedAIPromptTemplate = ref('')
const generatingAI = ref(false)
const aiAnalyses = ref<AIAnalysis[]>([])
const selectedAnalysisID = ref<number | null>(null)
const activeAIAnalysis = computed(() => aiAnalyses.value.find((item) => item.id === selectedAnalysisID.value) || aiAnalyses.value[0])
let aiPollingTimer: number | undefined

async function evaluate() {
  const symbol = ticker.value.trim().toUpperCase()
  if (!symbol) { ElMessage.warning('请输入标的代码'); return }
  evaluating.value = true
  try {
    const response = await apiClient.post('/ticker-evaluations', { ticker: symbol, target_type: targetType.value }, { timeout: 120000 })
    selected.value = response.data.data
    ticker.value = symbol
    historyTicker.value = symbol
    historyPage.value = 1
    await Promise.all([loadHistory(), loadHistoryEntryTriggerOptions(), loadAIAnalyses()])
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '评估失败，请检查标的和数据源配置')
  } finally { evaluating.value = false }
}
async function loadAIProviders() {
  try {
    const [response, templateResponse] = await Promise.all([apiClient.get('/ai/providers'), apiClient.get('/ai/prompt-templates', { params: { scope: 'ticker_evaluation' } })])
    aiProviders.value = response.data.data || []; aiPromptTemplates.value = templateResponse.data.data || []
    if (!selectedAIProvider.value && aiProviders.value.length) selectedAIProvider.value = aiProviders.value[0].id
    if (!selectedAIPromptTemplate.value && aiPromptTemplates.value.length) selectedAIPromptTemplate.value = aiPromptTemplates.value[0].id
  } catch { aiProviders.value = []; aiPromptTemplates.value = [] }
}
async function loadAIAnalyses() {
  const symbol = selected.value?.ticker || historyTicker.value
  if (!symbol) { aiAnalyses.value = []; return }
  try {
    const response = await apiClient.get('/ai/analyses', { params: { ticker: symbol, page: 1, page_size: 20 } })
    aiAnalyses.value = response.data.data.items || []
    selectedAnalysisID.value = aiAnalyses.value[0]?.id || null
    if (aiAnalyses.value.some((item) => item.status === 'queued' || item.status === 'running')) scheduleAIPoll()
  } catch { aiAnalyses.value = [] }
}
function scheduleAIPoll() {
  if (aiPollingTimer !== undefined) return
  aiPollingTimer = window.setTimeout(() => { aiPollingTimer = undefined; void loadAIAnalyses() }, 2000)
}
async function generateAIAnalysis() {
  if (!selected.value || !selectedAIProvider.value || !selectedAIPromptTemplate.value) return
  generatingAI.value = true
  try {
    const response = await apiClient.post('/ai/ticker-evaluations', { provider_id: selectedAIProvider.value, template_id: selectedAIPromptTemplate.value, evaluation: selected.value }, { timeout: 315000 })
    ElMessage.success('AI 研判已提交，正在后台处理')
    await loadAIAnalyses()
    selectedAnalysisID.value = response.data.data.id
  } catch (err: any) { ElMessage.error(err?.response?.data?.message || 'AI 研判请求超时或失败；请检查供应商配置、额度或适当提高模型超时后手动重试') } finally { generatingAI.value = false }
}
function selectHistory(row: Evaluation) { selected.value = row; void loadAIAnalyses() }
async function loadHistory() {
  historyLoading.value = true
  try {
    const response = await apiClient.get('/ticker-evaluations', { params: { ticker: historyTicker.value.trim().toUpperCase(), entry_trigger: historyEntryTrigger.value.trim() || undefined, sort_by: historySortBy.value, sort_order: historySortOrder.value, page: historyPage.value, page_size: historyPageSize.value } })
    history.value = response.data.data.items || []
    historyTotal.value = response.data.data.total || 0
  } catch (err: any) { ElMessage.error(err?.response?.data?.message || '加载历史记录失败') } finally { historyLoading.value = false }
}
async function loadHistoryEntryTriggerOptions() {
  try { const response = await apiClient.get('/ticker-evaluations/entry-triggers', { params: { ticker: historyTicker.value.trim().toUpperCase() } }); historyEntryTriggerOptions.value = response.data.data || [] } catch { historyEntryTriggerOptions.value = [] }
}
function applyHistoryFilters() { historyPage.value = 1; void loadHistory(); void loadHistoryEntryTriggerOptions() }
function handleHistorySort({ prop, order }: { prop?: string, order?: 'ascending' | 'descending' | null }) { historySortBy.value = prop || 'evaluated_at'; historySortOrder.value = order === 'ascending' ? 'asc' : 'desc'; historyPage.value = 1; void loadHistory() }
function formatDate(value?: string) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false, timeZone: 'Asia/Shanghai' }) : '-' }
function price(value?: number) { return Number.isFinite(value) ? Number(value).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 }) : '-' }
function microsPrice(value?: number, currency?: string) { return Number.isFinite(value) && Number(value) !== 0 ? `${price(Number(value) / 1_000_000)} ${currency || 'USD'}` : '-' }
function decimal(value?: number) { return Number.isFinite(value) ? Number(value).toFixed(2) : '-' }
function integer(value?: number) { return Number.isFinite(value) ? Number(value).toLocaleString('en-US') : '-' }
function usd(value?: number) { return Number.isFinite(value) ? `$${Number(value).toLocaleString('en-US', { maximumFractionDigits: 0 })}` : '-' }
function pct(value?: number) { return Number.isFinite(value) ? `${Number(value) > 0 ? '+' : ''}${Number(value).toFixed(2)}%` : '-' }
function signedMetricClass(value?: number) { return Number(value) > 0 ? 'positive' : Number(value) < 0 ? 'negative' : '' }
function ratio(value?: number) { return Number.isFinite(value) ? `${Number(value).toFixed(2)}×` : '-' }
function topRows<T>(items: T[] | undefined, max: number) { return (items || []).slice(0, max) }
function researchStatus(value?: string) { return ({ available: '可用', partial: '部分', no_coverage: '暂无覆盖', not_synced: '未同步', unavailable: '不可用' } as Record<string, string>)[value || ''] || (value || '未标注') }
onUnmounted(() => { if (aiPollingTimer !== undefined) window.clearTimeout(aiPollingTimer) })
function takeProfitZone(row: Evaluation) { const setup = row.candidate_score?.technical?.trade_setup; return setup?.take_profit_zone_low_usd != null && setup?.take_profit_zone_high_usd != null ? `${price(setup.take_profit_zone_low_usd)} – ${price(setup.take_profit_zone_high_usd)} USD` : '-' }
function historicalClose(row: Evaluation) { const score = row.candidate_score || {}; return Number.isFinite(score.price_close_usd) ? `${price(score.price_close_usd)} ${score.price_currency || 'USD'}` : '-' }
function priceFreshnessLabel(value?: string) { return ({ current: '当日', previous_trading_day: '上一交易日', stale: '滞后', future: '日期异常', missing: '缺失' } as Record<string, string>)[value || ''] || (value || '未标注') }
function priceSnapshotTooltip(row: Evaluation) { const score = row.candidate_score || {}; if (!Number.isFinite(score.price_close_usd)) return '该次评估未保存可用收盘价。'; return [`该次评估使用的收盘价：${historicalClose(row)}`, `交易日：${score.price_trade_date ? String(score.price_trade_date).slice(0, 10) : '-'}`, `来源：${score.price_source || '-'}`, `新鲜度：${priceFreshnessLabel(score.price_freshness_status)}`].join('\n') }
function fundamentalTooltip(row: Evaluation) { const score = row.candidate_score || {}; if (row.fundamental_status === 'not_applicable') return 'ETF：不适用 SEC 发行人基本面与 Form 4 规则。'; return [`总分：${score.total_score ?? '-'} / 100 · ${score.grade || '未分级'}`, `收入增长：${score.revenue_growth_score ?? '-'} / 30（${pct(score.revenue_growth_pct)}）`, `现金储备：${score.cash_runway_score ?? '-'} / 20（${Number.isFinite(score.cash_runway_months) ? `${Number(score.cash_runway_months).toFixed(1)} 个月` : '-'}）`, `内幕买入：${score.insider_score ?? '-'} / 20${score.recent_qualified_insider ? '（近期合格）' : ''}`, `毛利率：${score.gross_margin_score ?? '-'} / 10`, `稀释风险：${score.dilution_risk_score ?? '-'} / 10`, `赛道：${score.sector_score ?? '-'} / 10`, score.reason_code ? `评分说明：${score.reason_code}` : ''].filter(Boolean).join('\n') }
function reviewTooltip(row: Evaluation) { const score = row.candidate_score || {}; const reasons = (score.review_priority_reasons || []).map((reason: { label?: string, points?: number }) => `${reason.label || '未命名项'}：${Number(reason.points) > 0 ? '+' : ''}${reason.points ?? 0}`).join('\n'); return [`短线复核：${score.review_priority_score ?? '-'} / 100`, reasons || '本次无可用的短线复核构成。', score.recent_anomaly_labels?.length ? `异动：${score.recent_anomaly_labels.join('、')}` : ''].filter(Boolean).join('\n') }
function sourceLabel(value?: string) { return ({ candidate_cache: '小盘候选缓存', watch_target_cache: '监控标的缓存', ad_hoc_evaluation: '即时评估快照', ad_hoc_evaluation_cooldown_cache: '即时评估缓存' } as Record<string, string>)[value || ''] || '本地数据' }
onMounted(() => { void loadHistory(); void loadHistoryEntryTriggerOptions(); void loadAIProviders() })
</script>

<style scoped>
.query-card,.discipline,.history,.warnings{margin-top:16px}.result-heading,.history-heading{display:flex;justify-content:space-between;align-items:center;gap:16px}.result-heading h3{margin:18px 0 4px}.result-heading small{font-weight:normal;color:var(--el-text-color-secondary)}.result-heading p{margin:0 0 14px;color:var(--el-text-color-secondary)}.score{display:flex;align-items:baseline;gap:8px;margin-bottom:14px}.score strong{font-size:34px}.score span{color:var(--el-text-color-secondary)}.metrics{display:grid;grid-template-columns:1fr 1fr;gap:10px;font-size:13px}.metrics span{display:flex;justify-content:space-between;gap:8px;color:var(--el-text-color-secondary)}.metrics b{color:var(--el-text-color-primary)}.positive{color:var(--el-color-success)!important}.negative{color:var(--el-color-danger)!important}.history-filters,.ai-actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap}.history-filters .el-input,.history-filters .el-select{width:190px}.history-hint{margin-left:12px;color:var(--el-text-color-secondary);font-size:12px;font-weight:normal}.pagination{margin-top:16px;justify-content:flex-end}.research-details{padding:4px 12px 16px;background:var(--el-fill-color-lighter)}.research-title{font-weight:600;margin:8px 0 14px}.research-title span,.research-note,.ai-analysis-heading span{font-size:12px;font-weight:normal;color:var(--el-text-color-secondary);margin-left:8px}.research-section{background:var(--el-bg-color);border:1px solid var(--el-border-color-lighter);border-radius:4px;padding:12px;height:100%;box-sizing:border-box}.research-section h4{margin:0 0 10px;font-size:14px}.business-summary,.ai-analysis-content{white-space:pre-wrap;line-height:1.6}.holding-row,.option-summary{margin-top:14px}.research-foot{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-top:12px;color:var(--el-text-color-secondary);font-size:12px}.research-foot span{max-width:100%;word-break:break-word}.ai-analysis-select{width:min(100%,460px);margin-bottom:12px}.ai-analysis-content{padding:12px;background:var(--el-fill-color-light);border-radius:4px}@media(max-width:768px){.result-heading,.history-heading{align-items:flex-start;flex-direction:column}.metrics{grid-template-columns:1fr}.history-hint{display:block;margin:6px 0 0}.history-filters{width:100%;flex-direction:column}.history-filters .el-input,.history-filters .el-select{width:100%}.research-details{padding:4px}}
</style>
