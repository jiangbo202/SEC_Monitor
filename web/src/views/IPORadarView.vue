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
      <el-form-item :label="t('common.type')">
        <el-select v-model="filters.filing_type" clearable filterable style="width: 150px">
          <el-option v-for="item in filingTypes" :key="item" :label="item" :value="item" />
        </el-select>
      </el-form-item>
      <el-form-item :label="t('pages.filings.notification')">
        <el-select v-model="filters.notified" clearable style="width: 140px">
          <el-option :label="t('status.success')" value="yes" />
          <el-option :label="t('status.unnotified')" value="no" />
        </el-select>
      </el-form-item>
      <el-form-item><el-button :loading="loading" @click="load">{{ t('common.query') }}</el-button></el-form-item>
    </el-form>

    <el-table :data="rows" v-loading="loading" border :empty-text="t('pages.ipoRadar.empty')">
      <el-table-column prop="filing_type" :label="t('common.type')" width="110">
        <template #default="{ row }"><el-tag type="warning" effect="plain">{{ row.filing_type }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="company_name" :label="t('common.companyName')" min-width="220" show-overflow-tooltip />
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
      <el-table-column prop="title" :label="t('common.title')" min-width="280">
        <template #default="{ row }">
          <div class="filing-title-cell">
            <el-link class="filing-title-link" :href="row.filing_url" target="_blank" type="primary">
              {{ row.title || `${row.company_name} ${row.filing_type}` }}
            </el-link>
            <span>{{ row.company_name }} · {{ row.cik || '-' }}</span>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="notified_at" :label="t('pages.filings.notification')" width="110" align="center">
        <template #default="{ row }">
          <el-tag v-if="row.notified_at" class="compact-status-tag" type="success" effect="plain">{{ t('status.success') }}</el-tag>
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
import type { ApiResponse, IPOFiling, IPORadarRefreshResult, PageResult } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const filingTypes = ['S-1', 'S-1/A', 'F-1', 'F-1/A', '424B', '424B1', '424B2', '424B3', '424B4', '424B5', 'RW']
const loading = ref(false)
const refreshing = ref(false)
const rows = ref<IPOFiling[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const filters = reactive({ company_name: '', cik: '', filing_type: '', notified: '' })

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<IPOFiling>>>('/ipo-filings', { params: { ...filters, page: page.value, page_size: pageSize } })
    rows.value = res.data.data.items
    total.value = res.data.data.total
  } finally {
    loading.value = false
  }
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
