<template>
  <section class="page">
    <div class="page-header">
      <h1>{{ t('pages.filings.title') }}</h1>
      <el-button type="primary" :loading="refreshing" @click="refresh">{{ t('common.refreshData') }}</el-button>
    </div>
    <el-form :model="filters" class="toolbar filings-toolbar">
      <div class="filings-toolbar-top">
        <el-form-item :label="t('pages.filings.savedViews')" class="saved-view-item">
          <el-select fit-input-width v-model="activeSavedView" clearable @change="applySavedView">
            <el-option v-for="item in savedViews" :key="item.name" :label="item.name" :value="item.name" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('pages.filings.quickFilter')" class="quick-filters-item">
          <div class="quick-filter-row">
            <el-check-tag v-for="item in quickFilters" :key="item.label" :checked="activeQuickFilter === item.label" @change="applyQuickFilter(item)">
              {{ item.label }}
            </el-check-tag>
          </div>
        </el-form-item>
        <div class="filings-view-actions">
          <el-button @click="saveCurrentView">{{ t('pages.filings.saveView') }}</el-button>
          <el-button :disabled="!activeSavedView" @click="deleteSavedView">{{ t('pages.filings.deleteView') }}</el-button>
        </div>
      </div>
      <div class="filings-filter-grid">
        <el-form-item label="Ticker"><el-input v-model="filters.ticker" clearable /></el-form-item>
        <el-form-item :label="t('common.company')"><el-input v-model="filters.company_name" clearable /></el-form-item>
        <el-form-item>
          <template #label>
            <span class="filing-type-label">
              {{ t('common.type') }}
              <el-tooltip :content="t('pages.filings.typeTooltip')" placement="top">
                <el-button :icon="QuestionFilled" link class="help-button" @click="typeHelpVisible = true" />
              </el-tooltip>
            </span>
          </template>
          <el-select fit-input-width
            v-model="filters.filing_type"
            clearable
            filterable
            :filter-method="filterFilingTypes"
            :placeholder="t('pages.filings.typePlaceholder')"
            class="filing-type-select"
            @visible-change="onFilingTypeDropdownVisible"
          >
            <el-option
              v-for="item in visibleFilingTypes"
              :key="item.code"
              :label="`${item.code} - ${item.name}`"
              :value="item.code"
            >
              <div class="filing-option">
                <strong>{{ item.code }}</strong>
                <span>{{ item.name }}</span>
              </div>
            </el-option>
          </el-select>
        </el-form-item>
        <el-form-item :label="t('pages.filings.start')"><el-date-picker v-model="filters.date_from" type="date" value-format="YYYY-MM-DD" /></el-form-item>
        <el-form-item :label="t('pages.filings.end')"><el-date-picker v-model="filters.date_to" type="date" value-format="YYYY-MM-DD" /></el-form-item>
        <el-form-item :label="t('pages.filings.notification')">
          <el-select fit-input-width v-model="filters.notification_status" clearable>
            <el-option :label="t('status.success')" value="success" />
            <el-option :label="t('status.failed')" value="failed" />
            <el-option :label="t('status.unnotified')" value="unnotified" />
          </el-select>
        </el-form-item>
        <el-button class="filings-query-button" :loading="loading" @click="load">{{ t('common.query') }}</el-button>
      </div>
    </el-form>
    <el-table :data="rows" v-loading="loading" border :empty-text="t('pages.filings.empty')" @sort-change="onSortChange">
      <el-table-column prop="filing_type" :label="t('common.type')" width="140" sortable="custom">
        <template #default="{ row }">
          <div class="filing-type-cell">
            <strong>{{ row.filing_type }}</strong>
            <el-tag size="small" :type="filingImportance(row.filing_type).type" effect="plain">
              {{ filingImportance(row.filing_type).label }}
            </el-tag>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="ticker" label="Ticker" width="100" sortable="custom" />
      <el-table-column prop="company_name" :label="t('common.companyName')" min-width="180" show-overflow-tooltip />
      <el-table-column prop="filing_date" :label="t('common.filingDate')" width="140" sortable="custom">
        <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
      </el-table-column>
      <el-table-column prop="published_at" :label="t('pages.filings.publishedAt')" width="170" sortable="custom">
        <template #default="{ row }">{{ formatDateTime(row.published_at) }}</template>
      </el-table-column>
      <el-table-column prop="pulled_at" :label="t('common.syncTime')" width="170" sortable="custom">
        <template #default="{ row }">{{ formatDateTime(row.pulled_at) }}</template>
      </el-table-column>
      <el-table-column prop="title" :label="t('common.title')" min-width="260">
        <template #default="{ row }">
          <div class="filing-title-cell">
            <el-link class="filing-title-link" :href="row.filing_url" target="_blank" type="primary">
              {{ row.title || `${row.ticker} ${row.filing_type}` }}
            </el-link>
            <span>{{ row.company_name }} · {{ row.ticker }}</span>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="notification_status" :label="t('pages.filings.notification')" width="96" align="center">
        <template #default="{ row }">
          <el-tag v-if="row.notification_status" class="compact-status-tag" :type="notificationStatusType(row.notification_status)" effect="plain">
            {{ notificationStatusLabel(row.notification_status) }}
          </el-tag>
          <span v-else class="muted-text">{{ t('status.unnotified') }}</span>
        </template>
      </el-table-column>
      <el-table-column label="AI 研判" width="132" fixed="right" align="center">
        <template #default="{ row }">
          <div class="ai-action-cell">
            <el-button link type="primary" @click="openAIAnalysis(row)">AI 分析</el-button>
            <el-button link @click="viewAIHistory(row)">记录</el-button>
          </div>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />

    <el-dialog v-model="typeHelpVisible" :title="t('pages.filings.typeHelpTitle')" width="760px">
      <el-table :data="filingTypes" border height="460">
        <el-table-column prop="code" :label="t('common.type')" width="110" />
        <el-table-column prop="name" :label="t('pages.filings.typeName')" width="180" />
        <el-table-column prop="description" :label="t('pages.filings.typeDescription')" min-width="320" />
        <el-table-column prop="why" :label="t('pages.filings.typeWhy')" min-width="240" />
        <el-table-column prop="read" :label="t('pages.filings.typeRead')" min-width="240" />
      </el-table>
    </el-dialog>

    <el-dialog v-model="aiDialogVisible" :title="aiFiling ? `${aiFiling.ticker} · SEC 公告 AI 分析` : 'SEC 公告 AI 分析'" width="720px" destroy-on-close>
      <template v-if="aiFiling">
        <el-alert type="info" :closable="false" show-icon title="仅在确认后手动执行。后台将从已入库的 SEC 公告链接获取原文，连同链接和公告元数据发送给所选模型；本次请求及结果会留档。" />
        <el-descriptions :column="2" border size="small" style="margin-top:16px">
          <el-descriptions-item label="标的">{{ aiFiling.ticker }}</el-descriptions-item>
          <el-descriptions-item label="文件类型">{{ aiFiling.filing_type }}</el-descriptions-item>
          <el-descriptions-item label="公司" :span="2">{{ aiFiling.company_name }}</el-descriptions-item>
          <el-descriptions-item label="公告链接" :span="2"><el-link :href="aiFiling.filing_url" target="_blank" type="primary">查看 SEC 原文</el-link></el-descriptions-item>
        </el-descriptions>
        <el-form label-width="92px" style="margin-top:16px">
          <el-form-item label="模型"><el-select fit-input-width v-model="selectedAIProvider" placeholder="选择模型" style="width:100%"><el-option v-for="provider in aiProviders" :key="provider.id" :label="`${provider.name} · ${provider.model}`" :value="provider.id" /></el-select></el-form-item>
          <el-form-item label="提示词模板"><el-select fit-input-width v-model="selectedAIPromptTemplate" placeholder="选择 SEC 公告模板" style="width:100%"><el-option v-for="template in aiPromptTemplates" :key="template.id" :label="template.name" :value="template.id" /></el-select></el-form-item>
        </el-form>
      </template>
      <template #footer><el-button @click="aiDialogVisible = false">取消</el-button><el-button type="primary" :loading="generatingAI" :disabled="!selectedAIProvider || !selectedAIPromptTemplate" @click="generateAIAnalysis">开始分析</el-button></template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { QuestionFilled } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, Filing, PageResult } from '@/api/types'
import { useI18n } from '@/i18n'

const { store, t } = useI18n()
interface FilingTypeInfo {
  code: string
  name: string
  description: string
  why: string
  read: string
}

const filingTypeCatalog = [
  { code: '8-K', zhName: '重大事件报告', enName: 'Current Report', zhDescription: '公司发生重大事件时提交，例如并购、管理层变动、重大协议、业绩预告或退市风险。', enDescription: 'Filed when a company reports material events such as M&A, management changes, major agreements, guidance, or delisting risk.', zhWhy: '通常是最及时的重大事项披露。', enWhy: 'Often the fastest official signal for material events.', zhRead: '事件类型、影响金额、协议条款、管理层变化。', enRead: 'Event type, financial impact, agreement terms, management changes.' },
  { code: '10-K', zhName: '年度报告', enName: 'Annual Report', zhDescription: '公司每个财年提交的完整年度报告，包含业务、风险、财务报表和管理层讨论。', enDescription: 'A complete annual report covering business, risks, financial statements, and management discussion.', zhWhy: '适合做基本面和风险全景检查。', enWhy: 'Best for a full fundamental and risk review.', zhRead: '风险因素、收入结构、现金流、管理层讨论。', enRead: 'Risk factors, revenue mix, cash flow, MD&A.' },
  { code: '10-Q', zhName: '季度报告', enName: 'Quarterly Report', zhDescription: '公司每个季度提交的报告，包含季度财务数据、经营讨论和风险变化。', enDescription: 'A quarterly report with financial data, operating discussion, and risk updates.', zhWhy: '反映最新季度经营变化。', enWhy: 'Shows recent quarterly operating changes.', zhRead: '收入增速、利润率、现金变化、风险更新。', enRead: 'Revenue growth, margins, cash changes, risk updates.' },
  { code: 'S-1', zhName: '证券注册声明', enName: 'Registration Statement', zhDescription: '公司 IPO 或公开发行证券前提交的注册文件，披露业务、财务、股权和发行信息。', enDescription: 'Registration statement for IPOs or public offerings, covering business, financials, ownership, and offering details.', zhWhy: '常意味着 IPO 或融资动作。', enWhy: 'Often signals an IPO or financing event.', zhRead: '募资用途、稀释、风险、财务历史。', enRead: 'Use of proceeds, dilution, risks, financial history.' },
  { code: 'S-3', zhName: '简式注册声明', enName: 'Short Form Registration', zhDescription: '符合条件的上市公司用于后续发行证券的简化注册文件。', enDescription: 'Simplified registration form used by eligible public companies for follow-on offerings.', zhWhy: '可能预示后续融资。', enWhy: 'Can precede follow-on financing.', zhRead: '发行额度、证券类型、潜在稀释。', enRead: 'Shelf size, security type, potential dilution.' },
  { code: '424B', zhName: '招股书补充文件', enName: 'Prospectus Supplement', zhDescription: '证券发行相关的最终招股书或补充招股书，通常跟发行价格、规模和条款有关。', enDescription: 'Final prospectus or supplement for securities offerings, often including pricing, size, and terms.', zhWhy: '通常给出融资最终条款。', enWhy: 'Often contains final offering terms.', zhRead: '价格、数量、承销商、折价和摊薄。', enRead: 'Price, size, underwriters, discount, dilution.' },
  { code: 'DEF 14A', zhName: '正式委托书', enName: 'Definitive Proxy', zhDescription: '股东大会投票材料，包含董事选举、高管薪酬、股东提案等事项。', enDescription: 'Definitive shareholder voting materials, including director elections, executive compensation, and shareholder proposals.', zhWhy: '能看到治理和薪酬安排。', enWhy: 'Reveals governance and compensation structure.', zhRead: '董事选举、薪酬、股东提案。', enRead: 'Board votes, compensation, shareholder proposals.' },
  { code: 'PRE 14A', zhName: '初步委托书', enName: 'Preliminary Proxy', zhDescription: '正式委托书提交前的初稿版本，内容可能还会调整。', enDescription: 'Preliminary proxy materials submitted before the definitive version.', zhWhy: '提前暴露拟投票事项。', enWhy: 'Early view of upcoming voting matters.', zhRead: '重大议案、合并投票、治理变更。', enRead: 'Major proposals, merger votes, governance changes.' },
  { code: '13F-HR', zhName: '机构持仓报告', enName: 'Institutional Holdings', zhDescription: '大型机构投资者按季度披露的美股持仓报告。', enDescription: 'Quarterly US equity holdings report filed by large institutional investment managers.', zhWhy: '可观察机构持仓变化，但滞后。', enWhy: 'Shows institutional positioning, with delay.', zhRead: '新增、减持、集中度变化。', enRead: 'New stakes, reductions, concentration changes.' },
  { code: '13D', zhName: '主动持股披露', enName: 'Active Ownership Disclosure', zhDescription: '投资者持股超过 5% 且可能影响公司控制权或经营时提交。', enDescription: 'Filed when an investor owns over 5% and may influence control or operations.', zhWhy: '可能意味着激进投资或控制权变化。', enWhy: 'Can signal activism or control influence.', zhRead: '持股比例、目的、后续计划。', enRead: 'Ownership percentage, purpose, planned actions.' },
  { code: '13G', zhName: '被动持股披露', enName: 'Passive Ownership Disclosure', zhDescription: '投资者持股超过 5% 但通常为被动投资时提交。', enDescription: 'Filed for over-5% ownership that is generally passive.', zhWhy: '显示重要股东结构变化。', enWhy: 'Shows major shareholder structure changes.', zhRead: '持股比例、申报人、变动时间。', enRead: 'Ownership percent, filer, timing.' },
  { code: '3', zhName: '内幕人初始持股', enName: 'Initial Insider Ownership', zhDescription: '董事、高管或大股东成为报告义务人时披露初始持股。', enDescription: 'Initial ownership report for directors, officers, or major shareholders.', zhWhy: '建立内幕人持股基准。', enWhy: 'Establishes insider ownership baseline.', zhRead: '人员身份、直接/间接持股。', enRead: 'Insider role, direct/indirect ownership.' },
  { code: '4', zhName: '内幕人交易变动', enName: 'Insider Transaction', zhDescription: '董事、高管或大股东买卖、授予、行权等持股变化披露。', enDescription: 'Reports insider purchases, sales, grants, exercises, and other ownership changes.', zhWhy: '可快速看到高管/大股东交易。', enWhy: 'Fast view into insider transactions.', zhRead: '买卖方向、数量、价格、交易性质。', enRead: 'Buy/sell direction, shares, price, transaction code.' },
  { code: '5', zhName: '内幕人年度补充报告', enName: 'Annual Insider Statement', zhDescription: '内幕人年度补充披露未在 Form 4 中及时报告的交易。', enDescription: 'Annual insider report for transactions not timely reported on Form 4.', zhWhy: '补充遗漏的内幕交易披露。', enWhy: 'Catches insider transactions not reported on Form 4.', zhRead: '延迟披露交易和原因。', enRead: 'Late-reported transactions and context.' },
  { code: '6-K', zhName: '外国发行人报告', enName: 'Foreign Issuer Report', zhDescription: '外国上市公司向 SEC 提交的重大信息披露或当地市场披露材料。', enDescription: 'Material disclosures or local market materials furnished by foreign private issuers.', zhWhy: '外国发行人的重要信息入口。', enWhy: 'Key disclosure channel for foreign issuers.', zhRead: '本地公告、业绩、重大事项。', enRead: 'Local disclosures, results, material events.' },
  { code: '20-F', zhName: '外国发行人年报', enName: 'Foreign Issuer Annual Report', zhDescription: '外国上市公司提交的年度报告，类似美国公司的 10-K。', enDescription: 'Annual report for foreign private issuers, similar to a US company 10-K.', zhWhy: '外国发行人的年度基本面材料。', enWhy: 'Annual fundamental report for foreign issuers.', zhRead: '风险、财务、治理、地区披露。', enRead: 'Risks, financials, governance, regional disclosure.' },
  { code: 'SC 13D/A', zhName: '13D 修订', enName: '13D Amendment', zhDescription: '主动持股披露发生重要变化后的修订文件。', enDescription: 'Amendment to active ownership disclosure after material changes.', zhWhy: '显示主动股东意图变化。', enWhy: 'Signals changes in active shareholder intent.', zhRead: '持股变化、计划更新、沟通记录。', enRead: 'Ownership changes, plan updates, communications.' },
  { code: 'SC 13G/A', zhName: '13G 修订', enName: '13G Amendment', zhDescription: '被动持股披露发生重要变化后的修订文件。', enDescription: 'Amendment to passive ownership disclosure after material changes.', zhWhy: '显示被动大股东持仓变化。', enWhy: 'Shows passive large-holder changes.', zhRead: '最新比例、申报人、变化幅度。', enRead: 'Latest percent, filer, magnitude of change.' },
]

const filingTypes = computed<FilingTypeInfo[]>(() => filingTypeCatalog.map((item) => ({
  code: item.code,
  name: store.locale === 'en-US' ? item.enName : item.zhName,
  description: store.locale === 'en-US' ? item.enDescription : item.zhDescription,
  why: store.locale === 'en-US' ? item.enWhy : item.zhWhy,
  read: store.locale === 'en-US' ? item.enRead : item.zhRead
})))

const loading = ref(false)
const refreshing = ref(false)
const route = useRoute()
const router = useRouter()
const typeHelpVisible = ref(false)
const rows = ref<Filing[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const filters = reactive({ ticker: '', company_name: '', filing_type: '', date_from: '', date_to: '', notification_status: '' })
const sort = reactive({ sort_by: 'filing_date', sort_order: 'desc' })
const visibleFilingTypes = ref<FilingTypeInfo[]>([])
const activeQuickFilter = ref('')
const activeSavedView = ref('')
const savedViews = ref<Array<{ name: string, filters: typeof filters, sort: typeof sort }>>([])
type AIProvider = { id: string; name: string; model: string }
type AIPromptTemplate = { id: string; name: string; scopes?: string[] }
const aiProviders = ref<AIProvider[]>([])
const aiPromptTemplates = ref<AIPromptTemplate[]>([])
const aiFiling = ref<Filing | null>(null)
const aiDialogVisible = ref(false)
const selectedAIProvider = ref('')
const selectedAIPromptTemplate = ref('')
const generatingAI = ref(false)
const quickFilters = computed(() => [
  { label: t('pages.filings.filters.recent7Days'), dateDays: 7 },
  { label: t('pages.filings.filters.majorEvents'), filingType: '8-K' },
  { label: t('pages.filings.filters.annual10K'), filingType: '10-K' },
  { label: t('pages.filings.filters.quarterly10Q'), filingType: '10-Q' },
  { label: t('pages.filings.filters.insiderTrading'), filingType: '4' },
  { label: t('pages.filings.filters.financingS1'), filingType: 'S-1' }
])

function filterFilingTypes(query: string) {
  const value = query.trim().toLowerCase()
  if (!value) {
    visibleFilingTypes.value = filingTypes.value
    return
  }
  visibleFilingTypes.value = filingTypes.value
    .filter((item) => {
      const code = item.code.toLowerCase()
      const name = item.name.toLowerCase()
      const description = item.description.toLowerCase()
      return code.includes(value) || name.includes(value) || description.includes(value)
    })
    .sort((a, b) => Number(!a.code.toLowerCase().startsWith(value)) - Number(!b.code.toLowerCase().startsWith(value)))
}

function onFilingTypeDropdownVisible(visible: boolean) {
  if (visible) {
    visibleFilingTypes.value = filingTypes.value
  }
}

function formatDate(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toISOString().slice(0, 10)
}

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

function filingImportance(type: string) {
  const normalized = type.toUpperCase()
  if (['8-K', '10-K', '10-Q', 'S-1', 'S-3', '424B'].some((value) => normalized.startsWith(value))) {
    return { label: t('pages.filings.important'), type: 'danger' as const }
  }
  if (['4', '3', '5', '13F-HR', '13D', '13G'].some((value) => normalized === value || normalized.startsWith(value))) {
    return { label: t('pages.filings.watch'), type: 'warning' as const }
  }
  return { label: t('pages.filings.normal'), type: 'info' as const }
}

function notificationStatusType(status: string) {
  if (status === 'success') return 'success'
  if (status === 'failed') return 'danger'
  return 'info'
}

function notificationStatusLabel(status: string) {
  if (status === 'success') return t('status.success')
  if (status === 'failed') return t('status.failed')
  if (status === 'unnotified') return t('status.unnotified')
  return status || '-'
}

function applyQuickFilter(item: { label: string, dateDays?: number, filingType?: string }) {
  activeQuickFilter.value = activeQuickFilter.value === item.label ? '' : item.label
  filters.date_from = ''
  filters.date_to = ''
  filters.filing_type = ''
  if (activeQuickFilter.value) {
    if (item.dateDays) {
      const date = new Date()
      date.setDate(date.getDate() - item.dateDays)
      filters.date_from = date.toISOString().slice(0, 10)
    }
    if (item.filingType) {
      filters.filing_type = item.filingType
    }
  }
  page.value = 1
  load()
}

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<Filing>>>('/filings', { params: { ...filters, ...sort, page: page.value, page_size: pageSize } })
    rows.value = res.data.data.items
    total.value = res.data.data.total
  } finally {
    loading.value = false
  }
}

async function refresh() {
  refreshing.value = true
  try {
    const res = await apiClient.post<ApiResponse<{ new_filings: number }>>('/filings/refresh')
    ElMessage.success(t('messages.newFilingsAdded', { count: res.data.data.new_filings }))
    await load()
  } finally {
    refreshing.value = false
  }
}

async function loadAIOptions() {
  try {
    const [providerResponse, templateResponse] = await Promise.all([
      apiClient.get<ApiResponse<AIProvider[]>>('/ai/providers'),
      apiClient.get<ApiResponse<AIPromptTemplate[]>>('/ai/prompt-templates', { params: { scope: 'sec_filing' } })
    ])
    aiProviders.value = providerResponse.data.data || []
    aiPromptTemplates.value = templateResponse.data.data || []
    if (!selectedAIProvider.value && aiProviders.value.length) selectedAIProvider.value = aiProviders.value[0].id
    if (!selectedAIPromptTemplate.value && aiPromptTemplates.value.length) selectedAIPromptTemplate.value = aiPromptTemplates.value[0].id
  } catch (error: any) {
    ElMessage.error(error?.response?.data?.message || '加载 AI 模型或模板失败')
  }
}

function openAIAnalysis(filing: Filing) {
  aiFiling.value = filing
  aiDialogVisible.value = true
  void loadAIOptions()
}

function viewAIHistory(filing: Filing) {
  void router.push({ path: '/ai-analyses', query: { ticker: filing.ticker, scope: 'sec_filing' } })
}

async function generateAIAnalysis() {
  if (!aiFiling.value || !selectedAIProvider.value || !selectedAIPromptTemplate.value) return
  generatingAI.value = true
  try {
    await apiClient.post(`/ai/sec-filings/${aiFiling.value.id}`, { provider_id: selectedAIProvider.value, template_id: selectedAIPromptTemplate.value })
    ElMessage.success('已加入 AI 分析队列；完成后可在“AI 分析记录”查看结果。')
    aiDialogVisible.value = false
  } catch (error: any) {
    ElMessage.error(error?.response?.data?.message || '提交 SEC 公告 AI 分析失败')
  } finally {
    generatingAI.value = false
  }
}

function loadSavedViews() {
  try {
    savedViews.value = JSON.parse(localStorage.getItem('sec-monitor-filing-views') || '[]')
  } catch (error) {
    savedViews.value = []
  }
}

function persistSavedViews() {
  localStorage.setItem('sec-monitor-filing-views', JSON.stringify(savedViews.value))
}

async function saveCurrentView() {
  const name = await ElMessageBox.prompt(t('pages.filings.viewName'), t('pages.filings.saveView'), {
    inputValue: activeSavedView.value || ''
  }).then((res) => res.value).catch(() => '')
  if (!name.trim()) return
  const next = savedViews.value.filter((item) => item.name !== name.trim())
  next.push({ name: name.trim(), filters: { ...filters }, sort: { ...sort } })
  savedViews.value = next
  activeSavedView.value = name.trim()
  persistSavedViews()
  ElMessage.success(t('messages.savedViewAdded'))
}

function applySavedView(name: string) {
  const view = savedViews.value.find((item) => item.name === name)
  if (!view) return
  Object.assign(filters, view.filters)
  Object.assign(sort, view.sort)
  page.value = 1
  load()
}

function deleteSavedView() {
  if (!activeSavedView.value) return
  savedViews.value = savedViews.value.filter((item) => item.name !== activeSavedView.value)
  activeSavedView.value = ''
  persistSavedViews()
  ElMessage.success(t('messages.savedViewDeleted'))
}

function onSortChange({ prop, order }: { prop?: string, order?: string | null }) {
  sort.sort_by = prop || 'filing_date'
  sort.sort_order = order === 'ascending' ? 'asc' : 'desc'
  page.value = 1
  load()
}

onMounted(() => {
  loadSavedViews()
  visibleFilingTypes.value = filingTypes.value
  void loadAIOptions()
  const ticker = route.query.ticker
  if (typeof ticker === 'string') {
    filters.ticker = ticker
  }
  load()
})

watch(() => store.locale, () => {
  visibleFilingTypes.value = filingTypes.value
  activeQuickFilter.value = ''
})
</script>

<style scoped>
.filings-toolbar {
  display: grid;
  gap: 10px;
}

.ai-action-cell {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  white-space: nowrap;
}

.ai-action-cell :deep(.el-button + .el-button) {
  margin-left: 0;
}

.filings-toolbar-top {
  display: grid;
  grid-template-columns: 220px minmax(0, 1fr) auto;
  gap: 12px;
  align-items: center;
}

.filings-toolbar :deep(.el-form-item) {
  min-width: 0;
  margin: 0;
}

.filings-toolbar-top :deep(.el-form-item) {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
}

.filings-toolbar :deep(.el-form-item__content),
.filings-toolbar :deep(.el-input),
.filings-toolbar :deep(.el-select),
.filings-toolbar :deep(.el-date-editor) {
  min-width: 0;
  width: 100% !important;
}

.quick-filters-item {
  overflow: hidden;
}

.quick-filter-row {
  display: flex;
  min-width: 0;
  flex-wrap: nowrap;
  gap: 6px;
  max-width: none;
  overflow-x: auto;
  scrollbar-width: none;
}

.quick-filter-row::-webkit-scrollbar {
  display: none;
}

.quick-filter-row :deep(.el-check-tag) {
  flex: 1 0 auto;
  padding: 6px 12px;
  text-align: center;
}

.filings-view-actions {
  display: flex;
  gap: 8px;
}

.filings-filter-grid {
  display: grid;
  grid-template-columns: .75fr .9fr 1.2fr 1fr 1fr .8fr auto;
  gap: 8px 10px;
  align-items: end;
  padding-top: 10px;
  border-top: 1px solid var(--el-border-color-lighter);
}

.filings-filter-grid :deep(.el-form-item) {
  display: block;
}

.filings-filter-grid :deep(.el-form-item__label) {
  display: flex;
  height: 20px;
  justify-content: flex-start;
  padding: 0;
  line-height: 18px;
}

.filings-query-button {
  align-self: end;
}

@media (max-width: 1180px) {
  .filings-toolbar-top {
    grid-template-columns: 200px minmax(0, 1fr);
  }

  .filings-view-actions {
    grid-column: 1 / -1;
    justify-content: flex-end;
  }

  .filings-filter-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@media (max-width: 760px) {
  .filings-toolbar-top,
  .filings-filter-grid {
    grid-template-columns: 1fr;
  }

  .filings-toolbar-top :deep(.el-form-item) {
    display: block;
  }

  .filings-view-actions {
    justify-content: flex-start;
  }
}
</style>
