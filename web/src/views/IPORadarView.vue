<template>
  <section class="page">
    <div class="page-header">
      <div>
        <h1>{{ t('pages.ipoRadar.title') }}</h1>
        <p class="page-subtitle">{{ t('pages.ipoRadar.subtitle') }}</p>
      </div>
      <el-button type="primary" :loading="refreshing" @click="refresh">{{ t('pages.ipoRadar.refresh') }}</el-button>
    </div>

    <div class="quality-strip">
      <el-alert :title="qualitySummary" type="info" :closable="false" show-icon />
      <div class="export-actions">
        <el-button @click="download('/api/exports/ipo-companies.csv')">{{ t('pages.ipoRadar.exportCompanies') }}</el-button>
        <el-button @click="download('/api/exports/ipo-filings.csv')">{{ t('pages.ipoRadar.exportFilings') }}</el-button>
      </div>
    </div>

    <el-tabs v-model="activeTab" class="content-tabs" @tab-change="handleTabChange">
      <el-tab-pane :label="t('pages.ipoRadar.tabs.companies')" name="companies">
        <el-form :inline="true" :model="companyFilters" class="toolbar">
          <el-form-item :label="t('common.company')"><el-input v-model="companyFilters.company_name" clearable /></el-form-item>
          <el-form-item label="CIK"><el-input v-model="companyFilters.cik" clearable /></el-form-item>
          <el-form-item :label="t('common.status')">
            <el-select v-model="companyFilters.status" clearable style="width: 160px">
              <el-option v-for="item in ipoStatuses" :key="item.value" :label="item.label" :value="item.value" />
            </el-select>
          </el-form-item>
          <el-form-item><el-button :loading="companiesLoading" @click="loadCompanies">{{ t('common.query') }}</el-button></el-form-item>
        </el-form>

        <el-table
          :data="companies"
          v-loading="companiesLoading"
          border
          :empty-text="t('pages.ipoRadar.emptyCompanies')"
          :default-sort="{ prop: 'latest_update', order: 'descending' }"
          @expand-change="onExpandChange"
          @sort-change="onCompanySortChange"
        >
          <el-table-column type="expand">
            <template #default="{ row }">
              <el-table :data="filingDetails[row.cik] || []" border class="sync-detail-table">
                <el-table-column prop="filing_type" :label="t('common.type')" width="110">
                  <template #default="{ row: filing }"><el-tag type="warning" effect="plain">{{ filing.filing_type }}</el-tag></template>
                </el-table-column>
                <el-table-column prop="filing_date" :label="t('common.filingDate')" width="130">
                  <template #default="{ row: filing }">{{ formatDate(filing.filing_date) }}</template>
                </el-table-column>
                <el-table-column prop="accepted_at" :label="t('pages.ipoRadar.acceptedAt')" width="170">
                  <template #default="{ row: filing }">{{ formatDateTime(filing.accepted_at) }}</template>
                </el-table-column>
                <el-table-column prop="created_at" :label="t('common.syncTime')" width="170">
                  <template #default="{ row: filing }">{{ formatDateTime(filing.created_at) }}</template>
                </el-table-column>
                <el-table-column prop="title" :label="t('common.title')" min-width="280">
                  <template #default="{ row: filing }"><el-link :href="filing.filing_url" target="_blank" type="primary">{{ filing.title || filing.filing_type }}</el-link></template>
                </el-table-column>
                <el-table-column prop="notified_at" :label="t('pages.filings.notification')" width="110" align="center">
                  <template #default="{ row: filing }">
                    <el-tag v-if="filing.notified_at" class="compact-status-tag" type="success" effect="plain">{{ t('status.success') }}</el-tag>
                    <span v-else class="muted-text">{{ t('status.unnotified') }}</span>
                  </template>
                </el-table-column>
              </el-table>
            </template>
          </el-table-column>
          <el-table-column prop="status" :label="t('common.status')" width="130" sortable="custom">
            <template #default="{ row }">
              <el-tooltip :content="statusReasonText(row)" placement="top">
                <el-tag :type="ipoStatusType(row.status)" effect="plain">{{ ipoStatusLabel(row.status) }}</el-tag>
              </el-tooltip>
            </template>
          </el-table-column>
          <el-table-column prop="company_name" :label="t('common.companyName')" min-width="220" show-overflow-tooltip />
          <el-table-column prop="cik" label="CIK" width="130" />
          <el-table-column prop="latest_filing_type" :label="t('pages.ipoRadar.latestType')" width="120">
            <template #default="{ row }"><el-tag effect="plain">{{ row.latest_filing_type }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="filing_count" :label="t('pages.ipoRadar.filingCount')" width="90" align="right" />
          <el-table-column prop="first_filing_date" :label="t('pages.ipoRadar.firstFiling')" width="130">
            <template #default="{ row }">{{ formatDate(row.first_filing_date) }}</template>
          </el-table-column>
          <el-table-column prop="latest_update" :label="t('pages.ipoRadar.latestUpdate')" width="170" sortable="custom">
            <template #default="{ row }">{{ formatDateTime(row.latest_accepted_at || row.latest_filing_date) }}</template>
          </el-table-column>
          <el-table-column prop="latest_title" :label="t('common.title')" min-width="260">
            <template #default="{ row }">
              <el-link class="filing-title-link" :href="row.latest_filing_url" target="_blank" type="primary">
                {{ row.latest_title || `${row.company_name} ${row.latest_filing_type}` }}
              </el-link>
            </template>
          </el-table-column>
          <el-table-column prop="notified" :label="t('pages.filings.notification')" width="110" align="center">
            <template #default="{ row }">
              <el-tag v-if="row.notified" class="compact-status-tag" type="success" effect="plain">{{ t('status.success') }}</el-tag>
              <span v-else class="muted-text">{{ t('status.unnotified') }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.actions')" width="100" fixed="right">
            <template #default="{ row }">
              <el-button size="small" @click="openCompanyDetail(row)">{{ t('common.details') }}</el-button>
            </template>
          </el-table-column>
        </el-table>
        <el-pagination class="pagination" layout="total, prev, pager, next" :total="companiesTotal" :page-size="pageSize" v-model:current-page="companiesPage" @current-change="loadCompanies" />
      </el-tab-pane>

      <el-tab-pane :label="t('pages.ipoRadar.tabs.filings')" name="filings">
        <el-form :inline="true" :model="filingFilters" class="toolbar">
          <el-form-item :label="t('common.company')"><el-input v-model="filingFilters.company_name" clearable /></el-form-item>
          <el-form-item label="CIK"><el-input v-model="filingFilters.cik" clearable /></el-form-item>
          <el-form-item :label="t('common.type')"><el-input v-model="filingFilters.filing_type" clearable placeholder="S-1, EFFECT, 424B4" /></el-form-item>
          <el-form-item :label="t('pages.filings.notification')">
            <el-select v-model="filingFilters.notified" clearable style="width: 150px">
              <el-option :label="t('status.success')" value="yes" />
              <el-option :label="t('status.unnotified')" value="no" />
            </el-select>
          </el-form-item>
          <el-form-item><el-button :loading="filingsLoading" @click="loadFilings">{{ t('common.query') }}</el-button></el-form-item>
        </el-form>

        <el-table :data="filings" v-loading="filingsLoading" border :empty-text="t('pages.ipoRadar.emptyFilings')">
          <el-table-column prop="filing_type" :label="t('common.type')" width="110">
            <template #default="{ row }"><el-tag type="warning" effect="plain">{{ row.filing_type }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="company_name" :label="t('common.companyName')" min-width="210" show-overflow-tooltip />
          <el-table-column prop="cik" label="CIK" width="130" />
          <el-table-column prop="filing_date" :label="t('common.filingDate')" width="130">
            <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
          </el-table-column>
          <el-table-column prop="accepted_at" :label="t('pages.ipoRadar.acceptedAt')" width="170">
            <template #default="{ row }">{{ formatDateTime(row.accepted_at) }}</template>
          </el-table-column>
          <el-table-column prop="created_at" :label="t('common.syncTime')" width="170">
            <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
          </el-table-column>
          <el-table-column prop="title" :label="t('common.title')" min-width="300">
            <template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">{{ row.title || row.filing_type }}</el-link></template>
          </el-table-column>
          <el-table-column prop="notified_at" :label="t('pages.filings.notification')" width="110" align="center">
            <template #default="{ row }">
              <el-tag v-if="row.notified_at" class="compact-status-tag" type="success" effect="plain">{{ t('status.success') }}</el-tag>
              <span v-else class="muted-text">{{ t('status.unnotified') }}</span>
            </template>
          </el-table-column>
        </el-table>
        <el-pagination class="pagination" layout="total, prev, pager, next" :total="filingsTotal" :page-size="pageSize" v-model:current-page="filingsPage" @current-change="loadFilings" />
      </el-tab-pane>
    </el-tabs>

    <el-drawer v-model="detailVisible" :title="selectedCompany?.company_name || t('pages.ipoRadar.companyDetail')" size="640px">
      <div v-if="selectedCompany" class="detail-drawer-body">
        <el-descriptions :column="1" border>
          <el-descriptions-item label="CIK">{{ selectedCompany.cik }}</el-descriptions-item>
          <el-descriptions-item :label="t('common.status')">
            <el-tag :type="ipoStatusType(selectedCompany.status)" effect="plain">{{ ipoStatusLabel(selectedCompany.status) }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.statusReason')">{{ statusReasonText(selectedCompany) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.statusSource')">{{ statusSourceLabel(selectedCompany.status_source) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.finalTicker')">{{ selectedCompany.final_ticker || selectedCompany.matched_ticker || '-' }}</el-descriptions-item>
        </el-descriptions>

        <el-divider>{{ t('pages.ipoRadar.manualOverride') }}</el-divider>
        <el-form :model="overrideForm" label-width="120px">
          <el-form-item :label="t('common.status')">
            <el-select v-model="overrideForm.status_override" clearable>
              <el-option v-for="item in ipoStatuses" :key="item.value" :label="item.label" :value="item.value" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('pages.ipoRadar.finalTicker')">
            <el-input v-model="overrideForm.final_ticker" />
          </el-form-item>
          <el-form-item :label="t('pages.ipoRadar.overrideNote')">
            <el-input v-model="overrideForm.note" type="textarea" :rows="2" />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" :loading="savingOverride" @click="saveOverride">{{ t('common.save') }}</el-button>
          </el-form-item>
        </el-form>

        <el-divider>{{ t('pages.ipoRadar.timeline') }}</el-divider>
        <el-table :data="selectedCompany ? filingDetails[selectedCompany.cik] || [] : []" border>
          <el-table-column prop="filing_type" :label="t('common.type')" width="100">
            <template #default="{ row }"><el-tag type="warning" effect="plain">{{ row.filing_type }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="accepted_at" :label="t('pages.ipoRadar.acceptedAt')" width="170">
            <template #default="{ row }">{{ formatDateTime(row.accepted_at) }}</template>
          </el-table-column>
          <el-table-column prop="title" :label="t('common.title')" min-width="220">
            <template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">{{ row.title || row.filing_type }}</el-link></template>
          </el-table-column>
        </el-table>
      </div>
    </el-drawer>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, IPOCompany, IPOFiling, IPORadarRefreshResult, PageResult } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const ipoStatuses = [
  { value: 'new', label: t('pages.ipoRadar.statuses.new') },
  { value: 'updating', label: t('pages.ipoRadar.statuses.updating') },
  { value: 'effective', label: t('pages.ipoRadar.statuses.effective') },
  { value: 'priced', label: t('pages.ipoRadar.statuses.priced') },
  { value: 'listed', label: t('pages.ipoRadar.statuses.listed') },
  { value: 'withdrawn', label: t('pages.ipoRadar.statuses.withdrawn') },
  { value: 'stale', label: t('pages.ipoRadar.statuses.stale') }
]
const refreshing = ref(false)
const savingOverride = ref(false)
const activeTab = ref('companies')
const filingsLoading = ref(false)
const companiesLoading = ref(false)
const filings = ref<IPOFiling[]>([])
const companies = ref<IPOCompany[]>([])
const filingsTotal = ref(0)
const companiesTotal = ref(0)
const filingsPage = ref(1)
const companiesPage = ref(1)
const pageSize = 20
const filingFilters = reactive({ company_name: '', cik: '', filing_type: '', notified: '' })
const companyFilters = reactive({ company_name: '', cik: '', status: '' })
const companySort = reactive({ sort_by: 'latest_update', sort_order: 'desc' })
const filingDetails = ref<Record<string, IPOFiling[]>>({})
const selectedCompany = ref<IPOCompany | null>(null)
const detailVisible = ref(false)
const overrideForm = reactive({ status_override: '', final_ticker: '', note: '' })

const qualitySummary = computed(() => {
  const total = companiesTotal.value
  const incomplete = companies.value.filter((item) => item.status_confidence === 'medium').length
  return t('pages.ipoRadar.qualitySummary', { total, incomplete })
})

async function loadFilings() {
  filingsLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<IPOFiling>>>('/ipo-filings', { params: { ...filingFilters, page: filingsPage.value, page_size: pageSize } })
    filings.value = res.data.data.items
    filingsTotal.value = res.data.data.total
  } finally {
    filingsLoading.value = false
  }
}

async function loadCompanies() {
  companiesLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<IPOCompany>>>('/ipo-companies', { params: { ...companyFilters, ...companySort, page: companiesPage.value, page_size: pageSize } })
    companies.value = res.data.data.items
    companiesTotal.value = res.data.data.total
  } finally {
    companiesLoading.value = false
  }
}

function onCompanySortChange({ prop, order }: { prop?: string, order?: string | null }) {
  companySort.sort_by = prop === 'status' ? 'status' : 'latest_update'
  companySort.sort_order = order === 'ascending' ? 'asc' : 'desc'
  companiesPage.value = 1
  loadCompanies()
}

async function handleTabChange() {
  if (activeTab.value === 'companies' && companies.value.length === 0) {
    await loadCompanies()
  }
  if (activeTab.value === 'filings' && filings.value.length === 0) {
    await loadFilings()
  }
}

async function onExpandChange(row: IPOCompany) {
  if (filingDetails.value[row.cik]) return
  const res = await apiClient.get<ApiResponse<PageResult<IPOFiling>>>('/ipo-filings', { params: { cik: row.cik, sort: 'timeline', page: 1, page_size: 100 } })
  filingDetails.value = { ...filingDetails.value, [row.cik]: res.data.data.items }
}

async function openCompanyDetail(row: IPOCompany) {
  selectedCompany.value = row
  overrideForm.status_override = row.status_source === 'manual' ? row.status : ''
  overrideForm.final_ticker = row.final_ticker || row.matched_ticker || ''
  overrideForm.note = row.override_note || ''
  detailVisible.value = true
  await onExpandChange(row)
}

async function saveOverride() {
  if (!selectedCompany.value) return
  savingOverride.value = true
  try {
    await apiClient.put(`/ipo-companies/${encodeURIComponent(selectedCompany.value.cik)}/override`, overrideForm)
    ElMessage.success(t('messages.saved'))
    await loadCompanies()
    const updated = companies.value.find((item) => item.cik === selectedCompany.value?.cik)
    if (updated) {
      selectedCompany.value = updated
    }
  } finally {
    savingOverride.value = false
  }
}

function ipoStatusLabel(status: string) {
  return t(`pages.ipoRadar.statuses.${status}`)
}

function ipoStatusType(status: string) {
  if (status === 'new') return 'success'
  if (status === 'updating') return 'primary'
  if (status === 'effective') return 'warning'
  if (status === 'priced') return 'warning'
  if (status === 'listed') return 'success'
  if (status === 'withdrawn') return 'danger'
  return 'info'
}

function statusReasonText(row: IPOCompany) {
  const confidence = row.status_confidence ? t(`pages.ipoRadar.confidence.${row.status_confidence}`) : ''
  return confidence ? `${row.status_reason || '-'} · ${confidence}` : row.status_reason || '-'
}

function statusSourceLabel(source?: string) {
  return source === 'manual' ? t('pages.ipoRadar.sources.manual') : t('pages.ipoRadar.sources.system')
}

function download(url: string) {
  window.location.href = url
}

async function refresh() {
  refreshing.value = true
  try {
    const res = await apiClient.post<ApiResponse<IPORadarRefreshResult>>('/ipo-filings/refresh', null, { timeout: 120000 })
    ElMessage.success(t('messages.ipoRefreshDone', { count: res.data.data.new_filings, notified: res.data.data.notified }))
    filingDetails.value = {}
    filingsPage.value = 1
    companiesPage.value = 1
    if (activeTab.value === 'companies') {
      await loadCompanies()
    } else {
      await loadFilings()
    }
  } finally {
    refreshing.value = false
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

onMounted(loadCompanies)
</script>
