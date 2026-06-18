<template>
  <section class="page">
    <div class="page-header">
      <div>
        <h1>{{ t('pages.ipoRadar.title') }}</h1>
        <p class="page-subtitle">{{ t('pages.ipoRadar.subtitle') }}</p>
      </div>
      <el-button type="primary" :loading="refreshing" @click="refresh">{{ t('pages.ipoRadar.refresh') }}</el-button>
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

        <el-table :data="companies" v-loading="companiesLoading" border :empty-text="t('pages.ipoRadar.emptyCompanies')" @expand-change="onExpandChange">
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
          <el-table-column prop="status" :label="t('common.status')" width="130">
            <template #default="{ row }"><el-tag :type="ipoStatusType(row.status)" effect="plain">{{ ipoStatusLabel(row.status) }}</el-tag></template>
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
          <el-table-column prop="latest_accepted_at" :label="t('pages.ipoRadar.latestUpdate')" width="170">
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
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
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
const filingDetails = ref<Record<string, IPOFiling[]>>({})

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
    const res = await apiClient.get<ApiResponse<PageResult<IPOCompany>>>('/ipo-companies', { params: { ...companyFilters, page: companiesPage.value, page_size: pageSize } })
    companies.value = res.data.data.items
    companiesTotal.value = res.data.data.total
  } finally {
    companiesLoading.value = false
  }
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
