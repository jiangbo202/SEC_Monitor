<template>
  <section class="page">
    <div class="page-header">
      <div>
        <h1>{{ t('pages.ipoRadar.title') }}</h1>
        <p class="page-subtitle">{{ t('pages.ipoRadar.subtitle') }}</p>
      </div>
      <el-button type="primary" :loading="refreshing" @click="refresh">{{ t('pages.ipoRadar.refresh') }}</el-button>
    </div>

    <el-form :inline="true" :model="filters" class="toolbar">
      <el-form-item :label="t('common.company')"><el-input v-model="filters.company_name" clearable /></el-form-item>
      <el-form-item label="CIK"><el-input v-model="filters.cik" clearable /></el-form-item>
      <el-form-item :label="t('common.status')">
        <el-select v-model="filters.status" clearable style="width: 160px">
          <el-option v-for="item in ipoStatuses" :key="item.value" :label="item.label" :value="item.value" />
        </el-select>
      </el-form-item>
      <el-form-item><el-button :loading="loading" @click="load">{{ t('common.query') }}</el-button></el-form-item>
    </el-form>

    <el-table :data="rows" v-loading="loading" border :empty-text="t('pages.ipoRadar.empty')" @expand-change="onExpandChange">
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
          <div class="filing-title-cell">
            <el-link class="filing-title-link" :href="row.latest_filing_url" target="_blank" type="primary">
              {{ row.latest_title || `${row.company_name} ${row.latest_filing_type}` }}
            </el-link>
            <span>{{ row.matched_ticker ? `${row.matched_ticker} · ` : '' }}{{ row.company_name }}</span>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="notified" :label="t('pages.filings.notification')" width="110" align="center">
        <template #default="{ row }">
          <el-tag v-if="row.notified" class="compact-status-tag" type="success" effect="plain">{{ t('status.success') }}</el-tag>
          <span v-else class="muted-text">{{ t('status.unnotified') }}</span>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />
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
  { value: 'priced', label: t('pages.ipoRadar.statuses.priced') },
  { value: 'listed', label: t('pages.ipoRadar.statuses.listed') },
  { value: 'withdrawn', label: t('pages.ipoRadar.statuses.withdrawn') },
  { value: 'stale', label: t('pages.ipoRadar.statuses.stale') }
]
const loading = ref(false)
const refreshing = ref(false)
const rows = ref<IPOCompany[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const filters = reactive({ company_name: '', cik: '', status: '' })
const filingDetails = ref<Record<string, IPOFiling[]>>({})

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<IPOCompany>>>('/ipo-companies', { params: { ...filters, page: page.value, page_size: pageSize } })
    rows.value = res.data.data.items
    total.value = res.data.data.total
  } finally {
    loading.value = false
  }
}

async function onExpandChange(row: IPOCompany) {
  if (filingDetails.value[row.cik]) return
  const res = await apiClient.get<ApiResponse<PageResult<IPOFiling>>>('/ipo-filings', { params: { cik: row.cik, page: 1, page_size: 100 } })
  filingDetails.value = { ...filingDetails.value, [row.cik]: res.data.data.items }
}

function ipoStatusLabel(status: string) {
  return t(`pages.ipoRadar.statuses.${status}`)
}

function ipoStatusType(status: string) {
  if (status === 'new') return 'success'
  if (status === 'updating') return 'primary'
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
    page.value = 1
    await load()
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

onMounted(load)
</script>
