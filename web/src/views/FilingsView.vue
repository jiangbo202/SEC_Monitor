<template>
  <section class="page">
    <div class="page-header">
      <h1>{{ t('pages.filings.title') }}</h1>
      <el-button type="primary" :loading="refreshing" @click="refresh">{{ t('common.refreshData') }}</el-button>
    </div>
    <el-form :inline="true" :model="filters" class="toolbar">
      <el-form-item :label="t('pages.filings.quickFilter')">
        <div class="quick-filter-row">
          <el-check-tag v-for="item in quickFilters" :key="item.label" :checked="activeQuickFilter === item.label" @change="applyQuickFilter(item)">
            {{ item.label }}
          </el-check-tag>
        </div>
      </el-form-item>
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
        <el-select
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
        <el-select v-model="filters.notification_status" clearable style="width: 140px">
          <el-option :label="t('status.success')" value="success" />
          <el-option :label="t('status.failed')" value="failed" />
          <el-option :label="t('status.unnotified')" value="unnotified" />
        </el-select>
      </el-form-item>
      <el-form-item><el-button :loading="loading" @click="load">{{ t('common.query') }}</el-button></el-form-item>
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
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />

    <el-dialog v-model="typeHelpVisible" :title="t('pages.filings.typeHelpTitle')" width="760px">
      <el-table :data="filingTypes" border height="460">
        <el-table-column prop="code" :label="t('common.type')" width="110" />
        <el-table-column prop="name" :label="t('pages.filings.typeName')" width="180" />
        <el-table-column prop="description" :label="t('pages.filings.typeDescription')" min-width="320" />
      </el-table>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { QuestionFilled } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, Filing, PageResult } from '@/api/types'
import { useI18n } from '@/i18n'

const { store, t } = useI18n()
interface FilingTypeInfo {
  code: string
  name: string
  description: string
}

const filingTypeCatalog = [
  { code: '8-K', zhName: '重大事件报告', enName: 'Current Report', zhDescription: '公司发生重大事件时提交，例如并购、管理层变动、重大协议、业绩预告或退市风险。', enDescription: 'Filed when a company reports material events such as M&A, management changes, major agreements, guidance, or delisting risk.' },
  { code: '10-K', zhName: '年度报告', enName: 'Annual Report', zhDescription: '公司每个财年提交的完整年度报告，包含业务、风险、财务报表和管理层讨论。', enDescription: 'A complete annual report covering business, risks, financial statements, and management discussion.' },
  { code: '10-Q', zhName: '季度报告', enName: 'Quarterly Report', zhDescription: '公司每个季度提交的报告，包含季度财务数据、经营讨论和风险变化。', enDescription: 'A quarterly report with financial data, operating discussion, and risk updates.' },
  { code: 'S-1', zhName: '证券注册声明', enName: 'Registration Statement', zhDescription: '公司 IPO 或公开发行证券前提交的注册文件，披露业务、财务、股权和发行信息。', enDescription: 'Registration statement for IPOs or public offerings, covering business, financials, ownership, and offering details.' },
  { code: 'S-3', zhName: '简式注册声明', enName: 'Short Form Registration', zhDescription: '符合条件的上市公司用于后续发行证券的简化注册文件。', enDescription: 'Simplified registration form used by eligible public companies for follow-on offerings.' },
  { code: '424B', zhName: '招股书补充文件', enName: 'Prospectus Supplement', zhDescription: '证券发行相关的最终招股书或补充招股书，通常跟发行价格、规模和条款有关。', enDescription: 'Final prospectus or supplement for securities offerings, often including pricing, size, and terms.' },
  { code: 'DEF 14A', zhName: '正式委托书', enName: 'Definitive Proxy', zhDescription: '股东大会投票材料，包含董事选举、高管薪酬、股东提案等事项。', enDescription: 'Definitive shareholder voting materials, including director elections, executive compensation, and shareholder proposals.' },
  { code: 'PRE 14A', zhName: '初步委托书', enName: 'Preliminary Proxy', zhDescription: '正式委托书提交前的初稿版本，内容可能还会调整。', enDescription: 'Preliminary proxy materials submitted before the definitive version.' },
  { code: '13F-HR', zhName: '机构持仓报告', enName: 'Institutional Holdings', zhDescription: '大型机构投资者按季度披露的美股持仓报告。', enDescription: 'Quarterly US equity holdings report filed by large institutional investment managers.' },
  { code: '13D', zhName: '主动持股披露', enName: 'Active Ownership Disclosure', zhDescription: '投资者持股超过 5% 且可能影响公司控制权或经营时提交。', enDescription: 'Filed when an investor owns over 5% and may influence control or operations.' },
  { code: '13G', zhName: '被动持股披露', enName: 'Passive Ownership Disclosure', zhDescription: '投资者持股超过 5% 但通常为被动投资时提交。', enDescription: 'Filed for over-5% ownership that is generally passive.' },
  { code: '3', zhName: '内幕人初始持股', enName: 'Initial Insider Ownership', zhDescription: '董事、高管或大股东成为报告义务人时披露初始持股。', enDescription: 'Initial ownership report for directors, officers, or major shareholders.' },
  { code: '4', zhName: '内幕人交易变动', enName: 'Insider Transaction', zhDescription: '董事、高管或大股东买卖、授予、行权等持股变化披露。', enDescription: 'Reports insider purchases, sales, grants, exercises, and other ownership changes.' },
  { code: '5', zhName: '内幕人年度补充报告', enName: 'Annual Insider Statement', zhDescription: '内幕人年度补充披露未在 Form 4 中及时报告的交易。', enDescription: 'Annual insider report for transactions not timely reported on Form 4.' },
  { code: '6-K', zhName: '外国发行人报告', enName: 'Foreign Issuer Report', zhDescription: '外国上市公司向 SEC 提交的重大信息披露或当地市场披露材料。', enDescription: 'Material disclosures or local market materials furnished by foreign private issuers.' },
  { code: '20-F', zhName: '外国发行人年报', enName: 'Foreign Issuer Annual Report', zhDescription: '外国上市公司提交的年度报告，类似美国公司的 10-K。', enDescription: 'Annual report for foreign private issuers, similar to a US company 10-K.' },
  { code: 'SC 13D/A', zhName: '13D 修订', enName: '13D Amendment', zhDescription: '主动持股披露发生重要变化后的修订文件。', enDescription: 'Amendment to active ownership disclosure after material changes.' },
  { code: 'SC 13G/A', zhName: '13G 修订', enName: '13G Amendment', zhDescription: '被动持股披露发生重要变化后的修订文件。', enDescription: 'Amendment to passive ownership disclosure after material changes.' },
]

const filingTypes = computed<FilingTypeInfo[]>(() => filingTypeCatalog.map((item) => ({
  code: item.code,
  name: store.locale === 'en-US' ? item.enName : item.zhName,
  description: store.locale === 'en-US' ? item.enDescription : item.zhDescription
})))

const loading = ref(false)
const refreshing = ref(false)
const route = useRoute()
const typeHelpVisible = ref(false)
const rows = ref<Filing[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const filters = reactive({ ticker: '', company_name: '', filing_type: '', date_from: '', date_to: '', notification_status: '' })
const sort = reactive({ sort_by: 'filing_date', sort_order: 'desc' })
const visibleFilingTypes = ref<FilingTypeInfo[]>([])
const activeQuickFilter = ref('')
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

function onSortChange({ prop, order }: { prop?: string, order?: string | null }) {
  sort.sort_by = prop || 'filing_date'
  sort.sort_order = order === 'ascending' ? 'asc' : 'desc'
  page.value = 1
  load()
}

onMounted(() => {
  visibleFilingTypes.value = filingTypes.value
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
