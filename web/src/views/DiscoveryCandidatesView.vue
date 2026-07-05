<template>
  <div>
    <div class="page-header">
      <div>
        <h1>小盘股候选</h1>
        <p>基于公开 SEC 文件、财务指标、内幕交易和融资风险生成的研究候选列表。</p>
      </div>
      <el-space>
        <el-button :loading="workflowLoading" type="primary" plain @click="runWorkflow">刷新候选工作流</el-button>
        <el-button :loading="reportLoading" @click="openReport">查看日报</el-button>
        <el-button :loading="summaryLoading" @click="previewSummary">预检通知摘要</el-button>
        <el-button :loading="loading" @click="load">刷新</el-button>
      </el-space>
    </div>

    <el-alert
      v-if="health"
      :type="health.status === 'ok' ? 'success' : health.status === 'missing' ? 'warning' : 'error'"
      :closable="false"
      show-icon
      class="health-alert"
      :title="`数据健康：${healthStatusLabel(health.status)}｜候选 ${health.total_candidates}｜财务指标不可用 ${health.missing_financials}｜无合格内幕增持 ${health.missing_insiders}｜缺市值 ${health.missing_market_cap}｜活跃风险 ${health.active_risk_events}`"
      :description="health.issues.length ? health.issues.map(formatHealthIssue).join('；') : '当前候选证据链完整度正常。'"
    />

    <el-card shadow="never" class="filter-card">
      <el-form :inline="true" :model="filters">
        <el-form-item label="等级">
          <el-select v-model="filters.grade" clearable style="width: 120px">
            <el-option label="A级" value="A" />
            <el-option label="B级" value="B" />
            <el-option label="排除" value="excluded" />
          </el-select>
        </el-form-item>
        <el-form-item label="Ticker">
          <el-input v-model="filters.ticker" clearable placeholder="ACME" style="width: 140px" @keyup.enter="search" />
        </el-form-item>
        <el-form-item label="A级合格">
          <el-select v-model="filters.eligible_a" clearable style="width: 120px">
            <el-option label="是" value="true" />
            <el-option label="否" value="false" />
          </el-select>
        </el-form-item>
        <el-form-item label="B级合格">
          <el-select v-model="filters.eligible_b" clearable style="width: 120px">
            <el-option label="是" value="true" />
            <el-option label="否" value="false" />
          </el-select>
        </el-form-item>
        <el-form-item label="赛道分类">
          <el-select v-model="filters.sector_category" clearable filterable style="width: 180px">
            <el-option v-for="category in sectorCategoryOptions" :key="category" :label="category" :value="category" />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="search">查询</el-button>
          <el-button @click="reset">重置</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-table :data="rows" v-loading="loading" border empty-text="暂无候选" @sort-change="onSortChange">
      <el-table-column prop="grade" label="等级" width="90" align="center">
        <template #default="{ row }">
          <el-tag :type="gradeTagType(row.grade)" effect="dark">{{ gradeLabel(row.grade) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="ticker" label="Ticker" width="110" sortable="custom" />
      <el-table-column prop="total_score" label="总分" width="90" align="right" sortable="custom" />
      <el-table-column prop="market_cap_usd" label="市值" width="130" align="right" sortable="custom">
        <template #default="{ row }">{{ formatUSD(row.market_cap_usd) }}</template>
      </el-table-column>
      <el-table-column prop="price_close_usd" label="价格" width="100" align="right" sortable="custom">
        <template #default="{ row }">{{ formatPrice(row.price_close_usd, row.price_currency) }}</template>
      </el-table-column>
      <el-table-column prop="price_volume" label="成交量" width="120" align="right" sortable="custom">
        <template #default="{ row }">{{ formatVolume(row.price_volume) }}</template>
      </el-table-column>
      <el-table-column prop="price_trade_date" label="价格日期" width="110" sortable="custom">
        <template #default="{ row }">{{ formatDate(row.price_trade_date) }}</template>
      </el-table-column>
      <el-table-column prop="sector_category" label="赛道分类" min-width="150">
        <template #default="{ row }">
          <el-space wrap>
            <span>{{ row.sector_category || '-' }}</span>
            <el-tag v-if="Number.isFinite(row.sector_rating_score)" size="small" :type="sectorTagType(row.sector_rating_score)" effect="plain">
              {{ row.sector_rating_score }}/10
            </el-tag>
          </el-space>
        </template>
      </el-table-column>
      <el-table-column prop="quarterly_revenue_yoy_pct" label="季度同比" width="110" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <div v-for="line in revenueGrowthCalculationTooltipLines(row, 'quarterly_yoy')" :key="line">{{ line }}</div>
              </div>
            </template>
            <span class="metric-help">{{ formatPct(revenueGrowthQuarterly(row)) }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="quarterly_revenue_qoq_pct" label="季度环比" width="110" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <div v-for="line in revenueGrowthCalculationTooltipLines(row, 'quarterly_qoq')" :key="line">{{ line }}</div>
              </div>
            </template>
            <span class="metric-help">{{ formatPct(revenueGrowthQuarterlyQoQ(row)) }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="annual_revenue_yoy_pct" label="年度同比" width="110" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <div v-for="line in revenueGrowthCalculationTooltipLines(row, 'annual_yoy')" :key="line">{{ line }}</div>
              </div>
            </template>
            <span class="metric-help">{{ formatPct(revenueGrowthAnnual(row)) }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="annual_revenue_qoq_pct" label="年度环比" width="110" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <div v-for="line in revenueGrowthCalculationTooltipLines(row, 'annual_qoq')" :key="line">{{ line }}</div>
              </div>
            </template>
            <span class="metric-help">{{ formatPct(revenueGrowthAnnualQoQ(row)) }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="cash_runway_months" label="现金 runway" width="120" align="right" sortable="custom">
        <template #default="{ row }">{{ formatMonths(row.cash_runway_months) }}</template>
      </el-table-column>
      <el-table-column label="核心信号" min-width="220">
        <template #default="{ row }">
          <el-space wrap>
            <el-tag v-if="row.recent_qualified_insider" type="success" effect="plain">内部人买入</el-tag>
            <el-tooltip v-if="row.active_blocks_a" placement="top" effect="dark">
              <template #content>
                <div class="metric-tooltip">
                  <div v-for="line in capitalRiskTooltipLines(row, 'A')" :key="line">{{ line }}</div>
                </div>
              </template>
              <el-tag type="danger" effect="plain" class="risk-tag">阻断A</el-tag>
            </el-tooltip>
            <el-tooltip v-if="row.active_blocks_b" placement="top" effect="dark">
              <template #content>
                <div class="metric-tooltip">
                  <div v-for="line in capitalRiskTooltipLines(row, 'B')" :key="line">{{ line }}</div>
                </div>
              </template>
              <el-tag type="danger" effect="plain" class="risk-tag">阻断B</el-tag>
            </el-tooltip>
            <el-tag v-if="!row.active_blocks_a && !row.active_blocks_b" type="info" effect="plain">无阻断风险</el-tag>
          </el-space>
        </template>
      </el-table-column>
      <el-table-column label="分项" min-width="260">
        <template #default="{ row }">
          增长 {{ row.revenue_growth_score }} / 现金 {{ row.cash_runway_score }} / 内幕 {{ row.insider_score }} / 稀释 {{ row.dilution_risk_score }}
        </template>
      </el-table-column>
      <el-table-column prop="reason_code" label="原因" min-width="140" />
      <el-table-column label="操作" width="110" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" :loading="detailLoadingTicker === row.ticker" @click="openDetail(row)">详情</el-button>
        </template>
      </el-table-column>
    </el-table>

    <div class="pagination-row">
      <el-pagination
        background
        layout="prev, pager, next, total"
        :page-size="pageSize"
        :current-page="page"
        :total="total"
        @current-change="onPageChange"
      />
    </div>

    <el-dialog v-model="summaryVisible" title="小盘候选通知 dry-run 预检" width="760px">
      <el-alert
        :type="notificationPreview?.enabled ? 'success' : 'warning'"
        :closable="false"
        show-icon
        :title="notificationPreviewStatus"
        class="summary-alert"
      />
      <div v-if="summary" class="summary-dialog">
        <el-descriptions :column="3" border size="small">
          <el-descriptions-item label="批次">{{ summary.batch_id || '-' }}</el-descriptions-item>
          <el-descriptions-item label="A级候选">{{ summary.total_a }}</el-descriptions-item>
          <el-descriptions-item label="B级候选">{{ summary.total_b }}</el-descriptions-item>
          <el-descriptions-item v-if="notificationPreview" label="通知等级">
            A: {{ notificationPreview.settings.notify_a ? '开' : '关' }} / B: {{ notificationPreview.settings.notify_b ? '开' : '关' }}
          </el-descriptions-item>
          <el-descriptions-item v-if="notificationPreview" label="发送时间">{{ notificationPreview.settings.send_time }}</el-descriptions-item>
          <el-descriptions-item v-if="notificationPreview" label="每级最多">{{ notificationPreview.settings.max_per_grade }}</el-descriptions-item>
        </el-descriptions>
        <el-input
          :model-value="summary.message"
          type="textarea"
          :autosize="{ minRows: 8, maxRows: 16 }"
          readonly
          class="summary-message"
        />
        <el-tabs>
          <el-tab-pane :label="`A级候选 (${summary.items_a.length})`">
            <el-table :data="summary.items_a" border size="small" empty-text="暂无A级候选">
              <el-table-column prop="ticker" label="Ticker" width="100" />
              <el-table-column prop="total_score" label="总分" width="80" align="right" />
              <el-table-column prop="market_cap_usd" label="市值" width="120" align="right">
                <template #default="{ row }">{{ formatUSD(row.market_cap_usd) }}</template>
              </el-table-column>
              <el-table-column prop="revenue_growth_pct" label="收入增长" width="110" align="right">
                <template #default="{ row }">{{ formatPct(row.revenue_growth_pct) }}</template>
              </el-table-column>
              <el-table-column prop="cash_runway_months" label="现金 runway" width="120" align="right">
                <template #default="{ row }">{{ formatMonths(row.cash_runway_months) }}</template>
              </el-table-column>
              <el-table-column label="信号" min-width="160">
                <template #default="{ row }">
                  <el-tag v-if="row.recent_qualified_insider" type="success" effect="plain">内部人买入</el-tag>
                  <el-tag v-else type="info" effect="plain">无内部人买入</el-tag>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>
          <el-tab-pane :label="`B级候选 (${summary.items_b.length})`">
            <el-table :data="summary.items_b" border size="small" empty-text="暂无B级候选">
              <el-table-column prop="ticker" label="Ticker" width="100" />
              <el-table-column prop="total_score" label="总分" width="80" align="right" />
              <el-table-column prop="market_cap_usd" label="市值" width="120" align="right">
                <template #default="{ row }">{{ formatUSD(row.market_cap_usd) }}</template>
              </el-table-column>
              <el-table-column prop="revenue_growth_pct" label="收入增长" width="110" align="right">
                <template #default="{ row }">{{ formatPct(row.revenue_growth_pct) }}</template>
              </el-table-column>
              <el-table-column prop="cash_runway_months" label="现金 runway" width="120" align="right">
                <template #default="{ row }">{{ formatMonths(row.cash_runway_months) }}</template>
              </el-table-column>
              <el-table-column label="风险" min-width="160">
                <template #default="{ row }">
                  <el-tag v-if="row.active_blocks_a || row.active_blocks_b" type="danger" effect="plain">有阻断风险</el-tag>
                  <el-tag v-else type="info" effect="plain">无阻断风险</el-tag>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>
        </el-tabs>
      </div>
      <template #footer>
        <el-checkbox v-model="forceCandidateNotification" class="force-resend-checkbox">
          强制重发（会重复推送）
        </el-checkbox>
        <el-button
          type="primary"
          :loading="sendingNotification"
          :disabled="!notificationPreview || !notificationPreview.enabled || !!notificationPreview.suppressed_reason"
          @click="sendNotification"
        >
          确认发送 Telegram
        </el-button>
        <el-button @click="summaryVisible = false">关闭</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="reportVisible" title="小盘候选日报" width="820px">
      <div v-if="report" class="summary-dialog">
        <el-descriptions :column="3" border size="small">
          <el-descriptions-item label="日期">{{ report.date }}</el-descriptions-item>
          <el-descriptions-item label="批次">{{ report.batch.batch_id || '-' }}</el-descriptions-item>
          <el-descriptions-item label="状态">{{ report.batch.status || '-' }}</el-descriptions-item>
          <el-descriptions-item label="A级">{{ report.summary.total_a }}</el-descriptions-item>
          <el-descriptions-item label="B级">{{ report.summary.total_b }}</el-descriptions-item>
          <el-descriptions-item label="健康">{{ healthStatusLabel(report.health.status) }}</el-descriptions-item>
        </el-descriptions>
        <el-input
          :model-value="report.summary.message"
          type="textarea"
          :autosize="{ minRows: 8, maxRows: 16 }"
          readonly
          class="summary-message"
        />
      </div>
      <template #footer>
        <el-button @click="reportVisible = false">关闭</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="detailVisible" title="候选证据链" size="720px">
      <div v-if="candidateDetail" class="candidate-detail">
        <el-descriptions :column="2" border>
          <el-descriptions-item label="Ticker">{{ candidateDetail.score.ticker }}</el-descriptions-item>
          <el-descriptions-item label="公司">{{ candidateDetail.security.company_name }}</el-descriptions-item>
          <el-descriptions-item label="等级">{{ gradeLabel(candidateDetail.score.grade) }}</el-descriptions-item>
          <el-descriptions-item label="总分">{{ candidateDetail.score.total_score }}</el-descriptions-item>
          <el-descriptions-item label="市值">{{ formatUSD(candidateDetail.score.market_cap_usd) }}</el-descriptions-item>
          <el-descriptions-item label="SIC">{{ candidateDetail.security.sic || '-' }}</el-descriptions-item>
        </el-descriptions>

        <el-card shadow="never">
          <template #header>评分拆解</template>
          <el-descriptions :column="3" border size="small">
            <el-descriptions-item label="收入增长">{{ candidateDetail.score.revenue_growth_score }}</el-descriptions-item>
            <el-descriptions-item label="现金储备">{{ candidateDetail.score.cash_runway_score }}</el-descriptions-item>
            <el-descriptions-item label="内幕增持">{{ candidateDetail.score.insider_score }}</el-descriptions-item>
            <el-descriptions-item label="毛利率">{{ candidateDetail.score.gross_margin_score }}</el-descriptions-item>
            <el-descriptions-item label="稀释风险">{{ candidateDetail.score.dilution_risk_score }}</el-descriptions-item>
            <el-descriptions-item label="赛道空间">{{ candidateDetail.score.sector_score }}</el-descriptions-item>
          </el-descriptions>
        </el-card>

        <el-card shadow="never">
          <template #header>赛道解释</template>
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="分类">{{ candidateDetail.sector.category }}</el-descriptions-item>
            <el-descriptions-item label="标签">{{ candidateDetail.sector.label }}</el-descriptions-item>
            <el-descriptions-item label="SIC">{{ candidateDetail.sector.sic || '-' }}</el-descriptions-item>
            <el-descriptions-item label="赛道分">{{ candidateDetail.sector.score }}/10</el-descriptions-item>
            <el-descriptions-item label="说明" :span="2">{{ candidateDetail.sector.rationale }}</el-descriptions-item>
          </el-descriptions>
        </el-card>

        <el-card shadow="never">
          <template #header>数据质量</template>
          <el-space wrap>
            <el-tag v-for="(value, key) in candidateDetail.data_quality" :key="key" :type="value === 'valid' ? 'success' : 'warning'" effect="plain">
              {{ key }}: {{ value }}
            </el-tag>
          </el-space>
        </el-card>

        <el-card shadow="never">
          <template #header>财务证据</template>
          <el-descriptions v-if="candidateDetail.financial" :column="2" border size="small">
            <el-descriptions-item label="季度收入 YoY">{{ formatPct(candidateDetail.financial.quarterly_revenue_yoy_pct) }}</el-descriptions-item>
            <el-descriptions-item label="季度收入 QoQ">{{ formatPct(candidateDetail.financial.quarterly_revenue_qoq_pct) }}</el-descriptions-item>
            <el-descriptions-item label="年度收入 YoY">{{ formatPct(candidateDetail.financial.annual_revenue_yoy_pct) }}</el-descriptions-item>
            <el-descriptions-item label="年度收入环比">{{ formatPct(candidateDetail.financial.annual_revenue_qoq_pct) }}</el-descriptions-item>
            <el-descriptions-item label="现金 Runway">{{ formatMonths(candidateDetail.financial.cash_runway_months) }}</el-descriptions-item>
            <el-descriptions-item label="质量标记">{{ candidateDetail.financial.quality_flags_json || '-' }}</el-descriptions-item>
          </el-descriptions>
          <el-empty v-else description="暂无财务证据" />
        </el-card>

        <el-card shadow="never">
          <template #header>近期 SEC 公告</template>
          <el-table :data="candidateDetail.recent_filings || []" size="small" border empty-text="暂无近期公告">
            <el-table-column prop="filing_date" label="日期" width="120">
              <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
            </el-table-column>
            <el-table-column prop="filing_type" label="类型" width="90" />
            <el-table-column prop="title" label="标题" min-width="220" show-overflow-tooltip />
            <el-table-column label="链接" width="80">
              <template #default="{ row }">
                <el-link v-if="row.filing_url" :href="row.filing_url" target="_blank" type="primary">打开</el-link>
                <span v-else>-</span>
              </template>
            </el-table-column>
          </el-table>
        </el-card>

        <el-card shadow="never">
          <template #header>内幕交易</template>
          <el-table :data="candidateDetail.insiders" size="small" border empty-text="暂无内幕交易">
            <el-table-column prop="transaction_date" label="日期" width="120"><template #default="{ row }">{{ formatDate(row.transaction_date) }}</template></el-table-column>
            <el-table-column prop="owner_name" label="人员" min-width="120" />
            <el-table-column prop="role" label="角色" width="90" />
            <el-table-column prop="transaction_code" label="代码" width="70" />
            <el-table-column prop="qualified" label="合格" width="70"><template #default="{ row }"><el-tag :type="row.qualified ? 'success' : 'info'" effect="plain">{{ row.qualified ? '是' : '否' }}</el-tag></template></el-table-column>
          </el-table>
        </el-card>

        <el-card shadow="never">
          <template #header>融资/稀释风险</template>
          <el-table :data="candidateDetail.capital_risks" size="small" border empty-text="暂无融资风险">
            <el-table-column prop="kind" label="类型" width="140" />
            <el-table-column prop="severity" label="严重度" width="90" />
            <el-table-column prop="reason" label="原因" min-width="220" show-overflow-tooltip />
            <el-table-column label="阻断" width="120"><template #default="{ row }">A: {{ row.blocks_a ? '是' : '否' }} / B: {{ row.blocks_b ? '是' : '否' }}</template></el-table-column>
          </el-table>
        </el-card>

        <el-card shadow="never">
          <template #header>原始证据字段</template>
          <el-table :data="candidateDetail.evidence" size="small" border>
            <el-table-column prop="field" label="字段" width="170" />
            <el-table-column prop="value" label="值" width="140" />
            <el-table-column prop="source" label="来源" min-width="180" />
          </el-table>
        </el-card>
      </div>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient } from '@/api/client'
import type {
  ApiResponse,
  CandidateDetail,
  CandidateHealth,
  CandidateNotificationPreview,
  CandidateNotificationSendInput,
  CandidateNotificationSendResult,
  CandidateReport,
  CandidateScore,
  CandidateSummary,
  DiscoveryWorkflowResult,
  PageResult,
} from '@/api/types'

const rows = ref<CandidateScore[]>([])
const loading = ref(false)
const workflowLoading = ref(false)
const reportLoading = ref(false)
const detailVisible = ref(false)
const detailLoadingTicker = ref('')
const candidateDetail = ref<CandidateDetail | null>(null)
const health = ref<CandidateHealth | null>(null)
const report = ref<CandidateReport | null>(null)
const reportVisible = ref(false)
const summaryLoading = ref(false)
const sendingNotification = ref(false)
const summaryVisible = ref(false)
const summary = ref<CandidateSummary | null>(null)
const notificationPreview = ref<CandidateNotificationPreview | null>(null)
const notificationPreviewStatus = ref('仅 dry-run 预检，不会自动发送 Telegram。')
const forceCandidateNotification = ref(false)
const page = ref(1)
const pageSize = 20
const total = ref(0)
const filters = reactive({ grade: '', ticker: '', eligible_a: '', eligible_b: '', sector_category: '' })
const sortState = reactive({ sort_by: '', sort_order: '' })
const sectorCategoryOptions = [
  '生物医药',
  '软件与数据服务',
  '电子/半导体',
  '医疗器械',
  '通信服务',
  '医疗服务',
  '能源',
  '矿业/资源',
  '化工/生命科学材料',
  '工业制造',
  '计算机硬件',
  '消费/零售',
  '商业服务',
  '消费服务',
  '教育服务',
  '专业服务',
  '其他已分类赛道',
  '赛道数据缺失',
]

function requestParams() {
  const params: Record<string, string | number> = { page: page.value, page_size: pageSize }
  if (filters.grade) params.grade = filters.grade
  if (filters.ticker) params.ticker = filters.ticker.trim().toUpperCase()
  if (filters.eligible_a) params.eligible_a = filters.eligible_a
  if (filters.eligible_b) params.eligible_b = filters.eligible_b
  if (filters.sector_category) params.sector_category = filters.sector_category
  if (sortState.sort_by) params.sort_by = sortState.sort_by
  if (sortState.sort_order) params.sort_order = sortState.sort_order
  return params
}

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<CandidateScore>>>('/discovery/candidates', { params: requestParams() })
    rows.value = res.data.data.items || []
    total.value = res.data.data.total || 0
    await loadHealth()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载候选失败')
  } finally {
    loading.value = false
  }
}

async function loadHealth() {
  const res = await apiClient.get<ApiResponse<CandidateHealth>>('/discovery/candidates/health')
  health.value = res.data.data
}

async function runWorkflow() {
  workflowLoading.value = true
  try {
    const res = await apiClient.post<ApiResponse<DiscoveryWorkflowResult>>('/discovery/candidates/refresh')
    health.value = res.data.data.health
    summary.value = res.data.data.summary
    await load()
    if (res.data.data.status === 'published') {
      ElMessage.success('小盘候选真实同步已完成')
    } else if (res.data.data.status === 'market_failed') {
      ElMessage.warning('证券与 SEC 数据已同步，行情候选阶段失败，请检查行情源配置')
    } else {
      ElMessage.info(`小盘候选同步状态：${res.data.data.status}`)
    }
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '刷新候选工作流失败')
  } finally {
    workflowLoading.value = false
  }
}

async function openReport() {
  reportLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<CandidateReport>>('/discovery/candidates/report')
    report.value = res.data.data
    reportVisible.value = true
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载候选日报失败')
  } finally {
    reportLoading.value = false
  }
}

async function openDetail(row: CandidateScore) {
  detailLoadingTicker.value = row.ticker
  try {
    const res = await apiClient.get<ApiResponse<CandidateDetail>>(`/discovery/candidates/${row.ticker}/detail`)
    candidateDetail.value = res.data.data
    detailVisible.value = true
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载候选详情失败')
  } finally {
    detailLoadingTicker.value = ''
  }
}

async function previewSummary() {
  summaryLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<CandidateNotificationPreview>>('/discovery/candidates/notification-preview')
    notificationPreview.value = res.data.data
    summary.value = res.data.data.summary
    notificationPreviewStatus.value = res.data.data.suppressed_reason
      ? `通知被抑制：${res.data.data.suppressed_reason}；本次不会发送 Telegram。`
      : '配置已启用；本次仅 dry-run 预检，不会发送 Telegram。'
    summaryVisible.value = true
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '预检候选通知失败')
  } finally {
    summaryLoading.value = false
  }
}

async function sendNotification() {
  if (!notificationPreview.value || !notificationPreview.value.enabled || notificationPreview.value.suppressed_reason) return
  const warning = forceCandidateNotification.value
    ? '确认强制重发当前小盘候选摘要到 Telegram？这会绕过当天同批次防重复保护。'
    : '确认发送当前小盘候选摘要到 Telegram？发送后会记录通知批次。'
  await ElMessageBox.confirm(warning, forceCandidateNotification.value ? '确认强制重发' : '确认发送', { type: 'warning' })
  sendingNotification.value = true
  try {
    const payload: CandidateNotificationSendInput = { confirm: true, force: forceCandidateNotification.value }
    const res = await apiClient.post<ApiResponse<CandidateNotificationSendResult>>('/discovery/candidates/notification-send', payload)
    ElMessage.success(`候选通知已发送，批次 #${res.data.data.batch.id}`)
    notificationPreview.value = res.data.data.preview
    summary.value = res.data.data.preview.summary
    forceCandidateNotification.value = false
    summaryVisible.value = false
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '发送候选通知失败')
  } finally {
    sendingNotification.value = false
  }
}

function search() {
  page.value = 1
  load()
}

function reset() {
  filters.grade = ''
  filters.ticker = ''
  filters.eligible_a = ''
  filters.eligible_b = ''
  filters.sector_category = ''
  search()
}

function onPageChange(next: number) {
  page.value = next
  load()
}

function onSortChange({ prop, order }: { prop?: string; order?: 'ascending' | 'descending' | null }) {
  sortState.sort_by = order && prop ? prop : ''
  sortState.sort_order = order === 'ascending' ? 'asc' : order === 'descending' ? 'desc' : ''
  page.value = 1
  load()
}

function gradeLabel(grade: string) {
  if (grade === 'A') return 'A级'
  if (grade === 'B') return 'B级'
  return '排除'
}

function gradeTagType(grade: string) {
  if (grade === 'A') return 'success'
  if (grade === 'B') return 'warning'
  return 'info'
}

function sectorTagType(score?: number) {
  if (!Number.isFinite(score)) return 'info'
  if (Number(score) >= 7) return 'success'
  if (Number(score) >= 5) return 'warning'
  return 'info'
}

function healthStatusLabel(status: string) {
  if (status === 'ok') return '正常'
  if (status === 'degraded') return '降级'
  if (status === 'missing') return '缺少批次'
  return status || '-'
}

function formatHealthIssue(issue: string) {
  const [code, count] = issue.split(':')
  if (code === 'missing_financials') return `财务指标不可用：${count || 0}`
  if (code === 'missing_insiders') return `无合格内幕增持：${count || 0}`
  if (code === 'missing_market_cap') return `缺市值：${count || 0}`
  if (code === 'no_current_published_prescreen_batch') return '暂无已发布的小盘候选批次'
  return issue
}

function formatUSD(value: number) {
	if (!value) return '-'
	if (value >= 1_000_000_000) return `$${(value / 1_000_000_000).toFixed(2)}B`
	return `$${(value / 1_000_000).toFixed(1)}M`
}

function formatPrice(value?: number, currency?: string) {
  if (!Number.isFinite(value)) return '-'
  const prefix = currency === 'USD' || !currency ? '$' : `${currency} `
  return `${prefix}${Number(value).toFixed(2)}`
}

function formatVolume(value?: number) {
  if (!Number.isFinite(value)) return '-'
  if (Number(value) >= 1_000_000) return `${(Number(value) / 1_000_000).toFixed(1)}M`
  if (Number(value) >= 1_000) return `${(Number(value) / 1_000).toFixed(1)}K`
  return String(value)
}

function formatPct(value: number) {
	return Number.isFinite(value) ? `${value.toFixed(1)}%` : '-'
}

function formatPlainUSD(value?: number) {
  if (!Number.isFinite(value)) return '-'
  return `$${Number(value).toLocaleString()}`
}

function revenueGrowthCalculationTooltipLines(row: CandidateScore, period: 'quarterly_yoy' | 'quarterly_qoq' | 'annual_yoy' | 'annual_qoq') {
  const info = row.revenue_growth_explanation
  if (!info) {
    return ['来源：candidate_score_snapshots.revenue_growth_pct；详情数据未返回，无法展开原始收入计算。']
  }
  const isQuarterly = period.startsWith('quarterly')
  const isQoQ = period.endsWith('qoq')
  const currentRevenue = isQuarterly ? info.latest_quarter_revenue_usd : info.latest_annual_revenue_usd
  const priorRevenue = isQuarterly
    ? (isQoQ ? info.previous_quarter_revenue_usd : info.prior_year_quarter_revenue_usd)
    : info.prior_annual_revenue_usd
  const resultPct = period === 'quarterly_yoy'
    ? info.quarterly_revenue_yoy_pct
    : period === 'quarterly_qoq'
      ? info.quarterly_revenue_qoq_pct
      : period === 'annual_yoy'
        ? info.annual_revenue_yoy_pct
        : info.annual_revenue_qoq_pct
  const title = period === 'quarterly_yoy'
    ? '季度收入同比 YoY'
    : period === 'quarterly_qoq'
      ? '季度收入环比 QoQ'
      : period === 'annual_yoy'
        ? '年度收入同比 YoY'
        : '年度收入环比'
  const currentLabel = isQuarterly ? '最新季度收入' : '最新年度收入'
  const priorLabel = period === 'quarterly_yoy'
    ? '去年同期季度收入'
    : period === 'quarterly_qoq'
      ? '上一季度收入'
      : '上一年度收入'
  return [
    `${title}`,
    `来源：${info.source || 'SEC companyfacts / financial_metric_snapshots'}`,
    `${currentLabel}：${formatPlainUSD(currentRevenue)}`,
    `${priorLabel}：${formatPlainUSD(priorRevenue)}`,
    `公式：（${currentLabel} - ${priorLabel}）/ ${priorLabel} × 100%`,
    `代入：（${formatPlainUSD(currentRevenue)} - ${formatPlainUSD(priorRevenue)}）/ ${formatPlainUSD(priorRevenue)} × 100% = ${formatPct(resultPct)}`,
    `质量标记：${info.quality_flags_json || '-'}`,
  ]
}

function revenueGrowthQuarterly(row: CandidateScore) {
  return row.revenue_growth_explanation?.revenue_growth_available
    ? row.revenue_growth_explanation.quarterly_revenue_yoy_pct
    : row.revenue_growth_pct
}

function revenueGrowthAnnual(row: CandidateScore) {
  return row.revenue_growth_explanation?.revenue_growth_available
    ? row.revenue_growth_explanation.annual_revenue_yoy_pct
    : row.revenue_growth_pct
}

function revenueGrowthQuarterlyQoQ(row: CandidateScore) {
  return row.revenue_growth_explanation?.revenue_growth_available
    ? row.revenue_growth_explanation.quarterly_revenue_qoq_pct
    : NaN
}

function revenueGrowthAnnualQoQ(row: CandidateScore) {
  return row.revenue_growth_explanation?.revenue_growth_available
    ? row.revenue_growth_explanation.annual_revenue_qoq_pct
    : NaN
}

function capitalRiskTooltipLines(row: CandidateScore, grade: 'A' | 'B') {
  const risks = (row.capital_risk_summaries || []).filter((risk) => grade === 'A' ? risk.blocks_a : risk.blocks_b)
  if (!risks.length) {
    return [`${grade}级阻断：当前列表未返回具体风险原因，请打开详情查看融资/稀释风险。`]
  }
  const lines = [`${grade}级阻断原因：`]
  risks.forEach((risk, index) => {
    lines.push(`${index + 1}. ${capitalRiskKindLabel(risk.kind)}｜${capitalRiskSeverityLabel(risk.severity)}｜${formatDate(risk.effective_at)}`)
    lines.push(`   ${risk.reason || '-'}`)
  })
  return lines
}

function capitalRiskKindLabel(kind: string) {
  const labels: Record<string, string> = {
    registered_financing: '注册融资',
    atm_program: 'ATM 发行计划',
    reverse_split: '反向拆股',
    going_concern: '持续经营警告',
    warrants: '认股权证/稀释',
    shelf_registration: 'Shelf 注册',
    offering: '发行/融资',
  }
  return labels[kind] || kind || '-'
}

function capitalRiskSeverityLabel(severity: string) {
  if (severity === 'high') return '高风险'
  if (severity === 'medium') return '中风险'
  if (severity === 'low') return '低风险'
  return severity || '-'
}

function formatMonths(value: number) {
  return Number.isFinite(value) && value > 0 ? `${value.toFixed(1)} 月` : '-'
}

function formatDate(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString()
}

onMounted(load)
</script>

<style scoped>
.health-alert {
  margin-bottom: 12px;
}

.summary-alert {
  margin-bottom: 12px;
}

.summary-dialog {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.summary-message :deep(textarea) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', monospace;
  line-height: 1.5;
}

.force-resend-checkbox {
  margin-right: auto;
}

.candidate-detail {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.metric-help {
  cursor: help;
  border-bottom: 1px dotted var(--el-text-color-secondary);
}

.risk-tag {
  cursor: help;
}

.metric-tooltip {
  max-width: 520px;
  line-height: 1.6;
}
</style>
