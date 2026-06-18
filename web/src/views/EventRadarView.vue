<template>
  <section class="page">
    <div class="page-header">
      <div>
        <h1>{{ t('pages.eventRadar.title') }}</h1>
        <p class="page-subtitle">{{ t('pages.eventRadar.subtitle') }}</p>
      </div>
      <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
    </div>
    <el-form :inline="true" :model="filters" class="toolbar">
      <el-form-item label="Ticker"><el-input v-model="filters.ticker" clearable /></el-form-item>
      <el-form-item :label="t('pages.eventRadar.filterMajor')">
        <el-select v-model="filters.filing_type" clearable style="width: 160px">
          <el-option v-for="item in majorTypes" :key="item" :label="item" :value="item" />
        </el-select>
      </el-form-item>
      <el-form-item><el-button :loading="loading" @click="load">{{ t('common.query') }}</el-button></el-form-item>
    </el-form>
    <el-table :data="rows" v-loading="loading" border :empty-text="t('pages.eventRadar.empty')">
      <el-table-column prop="filing_type" :label="t('common.type')" width="110">
        <template #default="{ row }"><el-tag type="danger" effect="plain">{{ row.filing_type }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="ticker" label="Ticker" width="100" />
      <el-table-column prop="company_name" :label="t('common.companyName')" min-width="180" show-overflow-tooltip />
      <el-table-column prop="filing_date" :label="t('common.filingDate')" width="130">
        <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
      </el-table-column>
      <el-table-column prop="title" :label="t('common.title')" min-width="280">
        <template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">{{ row.title || row.filing_type }}</el-link></template>
      </el-table-column>
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { apiClient } from '@/api/client'
import type { ApiResponse, Filing, PageResult } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const majorTypes = ['8-K', 'S-1', 'S-3', '424B', '13D', 'SC 13D/A']
const loading = ref(false)
const rows = ref<Filing[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const filters = reactive({ ticker: '', filing_type: '' })

async function load() {
  loading.value = true
  try {
    if (filters.filing_type) {
      const res = await apiClient.get<ApiResponse<PageResult<Filing>>>('/filings', { params: { ...filters, page: page.value, page_size: pageSize, sort_by: 'filing_date', sort_order: 'desc' } })
      rows.value = res.data.data.items
      total.value = res.data.data.total
      return
    }
    const batches = await Promise.all(majorTypes.map((type) => apiClient.get<ApiResponse<PageResult<Filing>>>('/filings', { params: { ticker: filters.ticker, filing_type: type, page: 1, page_size: 20, sort_by: 'filing_date', sort_order: 'desc' } })))
    const merged = batches.flatMap((res) => res.data.data.items).sort((a, b) => new Date(b.filing_date).getTime() - new Date(a.filing_date).getTime())
    rows.value = merged.slice((page.value - 1) * pageSize, page.value * pageSize)
    total.value = merged.length
  } finally {
    loading.value = false
  }
}

function formatDate(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toISOString().slice(0, 10)
}

onMounted(load)
</script>
