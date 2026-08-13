<template>
  <div class="page-container strategy-pool-page">
    <div class="page-header">
      <div>
        <h2>策略观察池</h2>
        <p>把小盘候选的基本面、趋势、动量、量价与交易纪律，放在大盘、行业 ETF 和宏观事件环境中一起复核。</p>
      </div>
      <el-space>
        <el-segmented v-model="candidateOrder" :options="candidateOrderOptions" @change="loadCandidates" />
        <el-button :loading="loading" @click="load">刷新视图</el-button>
      </el-space>
    </div>

    <el-alert type="warning" :closable="false" show-icon class="strategy-alert">
      <template #title>研究观察工具，不构成投资建议或自动交易指令</template>
      <div>候选基本面总分仍是默认排序；“短线复核”只用于提示近期趋势、量价和风险的复核优先级。交易计划中的入场、止损和止盈仅为规则化纪律参考，须结合流动性、财报与事件风险独立判断。</div>
    </el-alert>

    <section>
      <div class="section-heading">
        <div>
          <h3>市场环境</h3>
          <p>数据源：{{ market.source || 'Longbridge' }} 缓存日线；最近同步：{{ formatDateTime(market.last_fetched_at) }}。</p>
        </div>
        <el-button link type="primary" @click="router.push('/market-trend')">查看大盘趋势</el-button>
      </div>
      <el-row :gutter="16" v-loading="loading">
        <el-col v-for="item in marketCards" :key="item.symbol" :xs="24" :sm="12" :lg="8">
          <el-card shadow="never" class="market-card">
            <div class="market-card-heading"><strong>{{ item.label }}</strong><small>{{ item.symbol }}</small></div>
            <div class="market-price">{{ formatPrice(item.close) }}</div>
            <div class="market-card-meta">收盘日 {{ item.trade_date || '-' }}</div>
            <div class="return-grid">
              <span><small>1 日</small><strong :class="changeClass(item.change_1d_pct)">{{ formatPct(item.change_1d_pct) }}</strong></span>
              <span><small>5 日</small><strong :class="changeClass(item.change_5d_pct)">{{ formatPct(item.change_5d_pct) }}</strong></span>
              <span><small>20 日</small><strong :class="changeClass(item.change_20d_pct)">{{ formatPct(item.change_20d_pct) }}</strong></span>
            </div>
          </el-card>
        </el-col>
        <el-col v-if="market.temperature" :xs="24" :sm="12" :lg="8">
          <el-card shadow="never" class="market-card temperature-card">
            <div class="market-card-heading"><strong>美国市场温度</strong><small>{{ market.temperature.trade_date || '-' }}</small></div>
            <div class="market-price">{{ market.temperature.temperature }}<span>/ 100</span></div>
            <div class="temperature-grid"><span>估值 <strong>{{ market.temperature.valuation }}</strong></span><span>情绪 <strong>{{ market.temperature.sentiment }}</strong></span></div>
            <p>{{ market.temperature.description || 'Longbridge 美国市场综合读数，仅用于环境观察。' }}</p>
          </el-card>
        </el-col>
      </el-row>
      <el-empty v-if="!loading && !marketCards.length && !market.temperature" description="暂无大盘缓存；请先在“大盘趋势”同步 Longbridge 日线。" :image-size="52" />
    </section>

    <section class="two-column-section">
      <el-card shadow="never">
        <template #header>
          <div class="card-header"><div><strong>行业 ETF 强弱</strong><p>按 20 日变化排序；用于候选赛道的相对环境观察。</p></div><el-button link type="primary" @click="router.push('/sector-breadth')">查看广度</el-button></div>
        </template>
        <el-table :data="sectorLeaders" size="small" border empty-text="暂无行业 ETF 缓存">
          <el-table-column prop="label" label="行业 ETF" min-width="130"><template #default="{ row }"><div class="instrument"><strong>{{ row.label }}</strong><small>{{ row.symbol }}</small></div></template></el-table-column>
          <el-table-column label="1 日" width="84" align="right"><template #default="{ row }"><strong :class="changeClass(row.change_1d_pct)">{{ formatPct(row.change_1d_pct) }}</strong></template></el-table-column>
          <el-table-column label="20 日" width="92" align="right"><template #default="{ row }"><strong :class="changeClass(row.change_20d_pct)">{{ formatPct(row.change_20d_pct) }}</strong></template></el-table-column>
          <el-table-column prop="trade_date" label="收盘日" width="106" />
        </el-table>
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="card-header"><div><strong>近期宏观事件</strong><p>按公布时间（上海）排序，优先显示待公布事件。</p></div><el-button link type="primary" @click="router.push('/macro-calendar')">查看日历</el-button></div>
        </template>
        <el-table :data="macroEvents" size="small" border empty-text="暂无待公布宏观事件">
          <el-table-column prop="title" label="事件" min-width="190" show-overflow-tooltip />
          <el-table-column label="重要性" width="80" align="center"><template #default="{ row }"><el-tag size="small" :type="row.market_importance ? 'warning' : 'info'" effect="plain">{{ importanceLabel(row) }}</el-tag></template></el-table-column>
          <el-table-column label="公布时间" width="150"><template #default="{ row }">{{ formatDateTime(row.scheduled_at) }}</template></el-table-column>
        </el-table>
      </el-card>
    </section>

    <section class="candidate-section">
      <div class="section-heading">
        <div>
          <h3>监控标的技术复核</h3>
          <p>已接入 {{ watchTargetTotal }} 个已启用监控标的；这些标的保留原有 SEC 同步状态，价格、技术和交易纪律来自本地行情缓存。</p>
        </div>
        <el-button link type="primary" @click="router.push('/targets')">查看监控标的</el-button>
      </div>
      <el-table :data="watchTargets" v-loading="watchTargetsLoading" border class="candidate-table" empty-text="暂无已启用监控标的。">
        <el-table-column prop="ticker" label="标的" width="108"><template #default="{ row }"><strong>{{ row.ticker }}</strong></template></el-table-column>
        <el-table-column prop="company_name" label="公司 / 基金" min-width="190" show-overflow-tooltip />
        <el-table-column label="类型" width="78" align="center"><template #default="{ row }"><el-tag size="small" :type="row.target_type === 'etf' ? 'warning' : 'info'" effect="plain">{{ row.target_type === 'etf' ? 'ETF' : '股票' }}</el-tag></template></el-table-column>
        <el-table-column label="价格 / 量价" min-width="150" align="right"><template #default="{ row }"><el-tooltip placement="top" effect="dark"><template #content><div class="tooltip-lines"><div>价格日期：{{ row.technical?.trade_date || '-' }}</div><div>收盘价：{{ formatPrice(row.technical?.close_usd) }} USD</div><div>当日估算成交额：{{ formatUSD(row.technical?.dollar_volume_usd) }}</div><div>20 日平均成交额：{{ formatUSD(row.technical?.average_dollar_volume_20) }}</div><div>20 日量比：{{ formatRatio(row.technical?.volume_ratio_20) }}</div></div></template><span>{{ formatPrice(row.technical?.close_usd) }} · {{ formatRatio(row.technical?.volume_ratio_20) }}</span></el-tooltip></template></el-table-column>
        <el-table-column label="技术状态" min-width="184"><template #default="{ row }"><el-tooltip placement="top" effect="dark"><template #content><div class="tooltip-lines"><div>距 MA20：{{ formatPct(row.technical?.distance_to_ma20_pct) }}</div><div>距 20 日高点：{{ formatPct(row.technical?.distance_to_20d_high_pct) }}</div><div>流动性：{{ liquidityLabel(row.technical?.liquidity_status) }}</div><div>状态：{{ technicalStatusLabel(row.technical?.status) }}</div></div></template><el-space wrap :size="4"><el-tag v-for="signal in row.technical?.signals || []" :key="signal.kind" type="success" size="small" effect="plain">{{ signal.label }}</el-tag><el-tag v-if="!(row.technical?.signals || []).length" size="small" type="info" effect="plain">{{ technicalStatusLabel(row.technical?.status) }}</el-tag></el-space></el-tooltip></template></el-table-column>
        <el-table-column label="交易纪律" min-width="150"><template #default="{ row }"><el-tooltip placement="top" effect="dark"><template #content><div class="tooltip-lines"><div>入场触发：{{ row.technical?.trade_setup?.entry_trigger || '等待触发条件' }}</div><div>止损：{{ formatPrice(row.technical?.trade_setup?.stop_loss_usd) }} USD（风险 {{ formatPct(row.technical?.trade_setup?.risk_pct) }}）</div><div>止盈区间：{{ formatPrice(row.technical?.trade_setup?.take_profit_zone_low_usd) }} – {{ formatPrice(row.technical?.trade_setup?.take_profit_zone_high_usd) }} USD</div><div>离场规则：{{ row.technical?.trade_setup?.exit_reason || '-' }}</div><div>当前状态开始：{{ row.technical?.trade_setup?.status_since ? formatDateTime(row.technical.trade_setup.status_since) : '下次日线同步建立基线' }}</div></div></template><el-tag :type="tradeSetupTagType(row.technical?.trade_setup?.status)" effect="plain">{{ tradeSetupLabel(row.technical?.trade_setup?.status) }}</el-tag></el-tooltip></template></el-table-column>
        <el-table-column label="SEC 同步" width="150"><template #default="{ row }"><div class="sync-cell"><el-tag size="small" :type="syncTagType(row.last_sync_status)" effect="plain">{{ syncLabel(row.last_sync_status) }}</el-tag><small>{{ formatDateTime(row.last_sync_at) }}</small></div></template></el-table-column>
        <el-table-column prop="last_new_filings" label="新增公告" width="92" align="right"><template #default="{ row }">{{ row.last_new_filings ?? 0 }}</template></el-table-column>
      </el-table>
    </section>

    <section class="candidate-section">
      <div class="section-heading">
        <div>
          <h3>小盘候选复核</h3>
          <p>共 {{ candidateTotal }} 个 A/B 候选；{{ candidateOrder === 'fundamental' ? '按基本面总分排序' : '按短线复核优先级排序' }}。评分有效日以每行悬浮说明为准。</p>
        </div>
        <el-button link type="primary" @click="router.push('/discovery-candidates')">查看全部候选</el-button>
      </div>
      <el-table :data="candidates" v-loading="candidateLoading" border class="candidate-table" empty-text="暂无候选评分；请先执行小盘股扫描。">
        <el-table-column prop="ticker" label="标的" width="104">
          <template #default="{ row }"><strong>{{ row.ticker }}</strong></template>
        </el-table-column>
        <el-table-column label="基本面" width="112" align="right">
          <template #default="{ row }">
            <el-tooltip placement="top" effect="dark">
              <template #content><div class="tooltip-lines"><div>收入增长：{{ row.revenue_growth_score }} / 30</div><div>现金储备：{{ row.cash_runway_score }} / 20</div><div>内幕增持：{{ row.insider_score }} / 20</div><div>毛利率：{{ row.gross_margin_score }} / 10</div><div>稀释风险：{{ row.dilution_risk_score }} / 10</div><div>赛道空间：{{ row.sector_score }} / 10</div><strong>合计：{{ row.total_score }} / 100</strong></div></template>
              <el-tag :type="gradeTagType(row.grade)" effect="plain">{{ row.total_score }} 分 · {{ row.grade }}</el-tag>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="短线复核" width="104" align="right">
          <template #default="{ row }"><el-tooltip :content="reviewPriorityTooltip(row)" placement="top"><span class="metric-help">{{ row.review_priority_score ?? '-' }}</span></el-tooltip></template>
        </el-table-column>
        <el-table-column label="趋势 / 动量" min-width="180">
          <template #default="{ row }">
            <el-tooltip placement="top" effect="dark">
              <template #content><div class="tooltip-lines"><div>20 日动量：{{ formatPct(row.market_quality?.momentum_pct) }}</div><div>相对 IWM（20 日）：{{ formatPct(row.technical?.relative_strength?.excess_return_20d_pct) }}</div><div>相对 IWM（60 日）：{{ formatPct(row.technical?.relative_strength?.excess_return_60d_pct) }}</div><div>距 MA20：{{ formatPct(row.technical?.distance_to_ma20_pct) }}</div><div>距 20 日高点：{{ formatPct(row.technical?.distance_to_20d_high_pct) }}</div></div></template>
              <el-space wrap :size="4"><el-tag v-for="signal in row.technical?.signals || []" :key="signal.kind" type="success" size="small" effect="plain">{{ signal.label }}</el-tag><el-tag v-if="!(row.technical?.signals || []).length" size="small" type="info" effect="plain">{{ technicalLabel(row) }}</el-tag></el-space>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="量价 / 流动性" min-width="150" align="right">
          <template #default="{ row }"><el-tooltip placement="top" effect="dark"><template #content><div class="tooltip-lines"><div>收盘价：{{ formatPrice(row.price_close_usd) }} USD</div><div>当日成交量：{{ formatVolume(row.price_volume) }}</div><div>20 日平均成交额：{{ formatUSD(row.market_quality?.average_dollar_volume_usd) }}</div><div>20 日量比：{{ formatRatio(row.technical?.volume_ratio_20) }}</div><div>流动性：{{ liquidityLabel(row.technical?.liquidity_status) }}</div></div></template><span>{{ formatPrice(row.price_close_usd) }} · {{ formatRatio(row.technical?.volume_ratio_20) }}</span></el-tooltip></template>
        </el-table-column>
        <el-table-column label="交易纪律" min-width="172">
          <template #default="{ row }"><el-tooltip placement="top" effect="dark"><template #content><div class="tooltip-lines"><div>入场触发：{{ row.technical?.trade_setup?.entry_trigger || '等待触发条件' }}</div><div>止损：{{ formatPrice(row.technical?.trade_setup?.stop_loss_usd) }} USD（风险 {{ formatPct(row.technical?.trade_setup?.risk_pct) }}）</div><div>止盈区间：{{ formatPrice(row.technical?.trade_setup?.take_profit_zone_low_usd) }} – {{ formatPrice(row.technical?.trade_setup?.take_profit_zone_high_usd) }} USD</div><div>离场规则：{{ row.technical?.trade_setup?.exit_reason || '-' }}</div><div>当前状态开始：{{ row.technical?.trade_setup?.status_since ? formatDateTime(row.technical.trade_setup.status_since) : '下次日线同步建立基线' }}</div></div></template><el-tag :type="tradeSetupTagType(row.technical?.trade_setup?.status)" effect="plain">{{ tradeSetupLabel(row.technical?.trade_setup?.status) }}</el-tag></el-tooltip></template>
        </el-table-column>
        <el-table-column label="操作" width="84" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="router.push(`/discovery-candidates?ticker=${encodeURIComponent(row.ticker)}`)">复核</el-button></template></el-table-column>
      </el-table>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiClient } from '@/api/client'
import type { ApiResponse, CandidateScore, MacroRelease, MarketTrendResponse, PageResult, WatchTarget } from '@/api/types'

const router = useRouter()
const loading = ref(false)
const candidateLoading = ref(false)
const candidateOrder = ref<'fundamental' | 'shortTerm'>('fundamental')
const candidateOrderOptions = [
  { label: '基本面总分', value: 'fundamental' },
  { label: '短线复核', value: 'shortTerm' },
]
const candidates = ref<CandidateScore[]>([])
const candidateTotal = ref(0)
const watchTargets = ref<WatchTarget[]>([])
const watchTargetTotal = ref(0)
const watchTargetsLoading = ref(false)
const macroEvents = ref<MacroRelease[]>([])
const market = reactive<MarketTrendResponse>({ source: 'Longbridge', market: [], sectors: [] })

const marketCards = computed(() => market.market.slice(0, 3))
const sectorLeaders = computed(() => [...market.sectors].sort((left, right) => (right.change_20d_pct ?? -Infinity) - (left.change_20d_pct ?? -Infinity)).slice(0, 6))

async function loadCandidates() {
  candidateLoading.value = true
  try {
    const response = await apiClient.get<ApiResponse<PageResult<CandidateScore>>>('/discovery/candidates', {
      params: {
        page: 1,
        page_size: 10,
        sort_by: candidateOrder.value === 'fundamental' ? 'total_score' : 'review_priority_score',
        sort_order: 'desc',
        exclude_research_readiness: 'blocked',
      },
    })
    candidates.value = response.data.data.items || []
    candidateTotal.value = response.data.data.total || 0
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载小盘候选失败')
  } finally {
    candidateLoading.value = false
  }
}

async function loadWatchTargets() {
  watchTargetsLoading.value = true
  try {
    const response = await apiClient.get<ApiResponse<PageResult<WatchTarget>>>('/watch-targets', { params: { status: 'enabled', page: 1, page_size: 20 } })
    watchTargets.value = response.data.data.items || []
    watchTargetTotal.value = response.data.data.total || 0
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载监控标的失败')
  } finally {
    watchTargetsLoading.value = false
  }
}

async function load() {
  loading.value = true
  try {
    const [marketResponse, macroResponse] = await Promise.all([
      apiClient.get<ApiResponse<MarketTrendResponse>>('/market-trend', { params: { history_days: 30 } }),
      apiClient.get<ApiResponse<PageResult<MacroRelease>>>('/macro/releases', { params: { status: 'scheduled', from: todayShanghai(), page: 1, page_size: 6, sort: 'asc' } }),
      loadCandidates(),
      loadWatchTargets(),
    ])
    Object.assign(market, marketResponse.data.data)
    macroEvents.value = macroResponse.data.data.items || []
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载策略观察池失败')
  } finally {
    loading.value = false
  }
}

function formatDateTime(value?: string | null) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false, timeZone: 'Asia/Shanghai' }) : '尚未同步' }
function todayShanghai() { return new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date()) }
function formatPrice(value?: number | null) { return Number.isFinite(value) ? Number(value).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 }) : '-' }
function formatUSD(value?: number | null) { return Number.isFinite(value) ? `$${Number(value).toLocaleString('en-US', { maximumFractionDigits: 0 })}` : '-' }
function formatPct(value?: number | null) { return Number.isFinite(value) ? `${Number(value) > 0 ? '+' : ''}${Number(value).toFixed(2)}%` : '-' }
function formatRatio(value?: number | null) { return Number.isFinite(value) ? `${Number(value).toFixed(2)}×` : '-' }
function formatVolume(value?: number | null) { return Number.isFinite(value) ? Number(value).toLocaleString('en-US', { maximumFractionDigits: 0 }) : '-' }
function changeClass(value?: number | null) { return Number(value) > 0 ? 'is-up' : Number(value) < 0 ? 'is-down' : 'is-flat' }
function gradeTagType(grade: string) { return grade === 'A' ? 'success' : grade === 'B' ? 'warning' : 'info' }
function technicalLabel(row: CandidateScore) { return technicalStatusLabel(row.technical?.status) }
function technicalStatusLabel(value?: string) { return value === 'ready' ? '暂无突破' : value === 'data_insufficient' ? '技术历史不足' : '技术数据待补' }
function liquidityLabel(value?: string) { return ({ normal: '正常', limited: '受限', low: '低流动性', unknown: '待评估' } as Record<string, string>)[value || 'unknown'] || value || '待评估' }
function tradeSetupLabel(value?: string) { return ({ entry_candidate: '可关注入场', watching: '等待观察', exit_warning: '离场预警', invalidated: '计划失效', unavailable: '计划待生成' } as Record<string, string>)[value || 'unavailable'] || value || '计划待生成' }
function tradeSetupTagType(value?: string) { return value === 'entry_candidate' ? 'success' : value === 'exit_warning' || value === 'invalidated' ? 'danger' : value === 'watching' ? 'warning' : 'info' }
function importanceLabel(row: MacroRelease) { return row.market_importance ? `${row.market_importance} 星` : '常规' }
function syncLabel(value?: string) { return ({ success: '成功', failed: '失败', partial: '部分成功', running: '同步中' } as Record<string, string>)[value || ''] || '未同步' }
function syncTagType(value?: string) { return value === 'success' ? 'success' : value === 'failed' ? 'danger' : value === 'partial' ? 'warning' : 'info' }
function reviewPriorityTooltip(row: CandidateScore) {
  const reasons = row.review_priority_reasons || []
  const detail = reasons.length ? reasons.map(reason => `${reason.label} ${reason.points >= 0 ? '+' : ''}${reason.points}`).join('；') : '暂无近期变化或交易条件加减分'
  return `短线复核 ${row.review_priority_score ?? 0} / 100：质量调整分 + 近期变化/交易条件 − 风险。${detail}`
}

onMounted(load)
</script>

<style scoped>
.page-header,.section-heading,.card-header{display:flex;justify-content:space-between;gap:16px;align-items:flex-start}.page-header{margin-bottom:16px}.page-header h2,.section-heading h3{margin:0}.page-header p,.section-heading p,.card-header p,.temperature-card p{margin:7px 0 0;color:var(--el-text-color-secondary)}.strategy-alert{margin-bottom:24px}.section-heading{margin-bottom:12px}.market-card{height:100%;margin-bottom:16px}.market-card-heading,.instrument{display:flex;justify-content:space-between;gap:10px}.market-card-heading small,.instrument small{color:var(--el-text-color-secondary)}.market-price{font-size:27px;font-weight:650;margin-top:13px;font-variant-numeric:tabular-nums}.market-price span{font-size:13px;font-weight:400;color:var(--el-text-color-secondary);margin-left:4px}.market-card-meta{margin-top:4px;font-size:12px;color:var(--el-text-color-secondary)}.return-grid,.temperature-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin-top:16px}.return-grid small{display:block;color:var(--el-text-color-secondary);font-size:11px}.return-grid strong{display:block;margin-top:3px}.temperature-grid{grid-template-columns:repeat(2,1fr);font-size:13px;color:var(--el-text-color-secondary)}.temperature-grid strong{margin-left:4px;color:var(--el-text-color-primary)}.two-column-section{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px;margin-top:10px}.card-header p{font-size:12px}.candidate-section{margin-top:28px}.candidate-table :deep(.el-table__cell){vertical-align:middle}.metric-help{font-weight:650;cursor:help}.tooltip-lines{display:grid;gap:5px;max-width:340px}.tooltip-lines strong{margin-top:3px}.sync-cell{display:grid;gap:4px}.sync-cell small{color:var(--el-text-color-secondary);font-size:11px}.is-up{color:var(--el-color-success)}.is-down{color:var(--el-color-danger)}.is-flat{color:var(--el-text-color-secondary)}@media (max-width:900px){.two-column-section{grid-template-columns:1fr}}@media (max-width:760px){.page-header,.section-heading,.card-header{display:grid}.page-header :deep(.el-space){flex-wrap:wrap}}
</style>
