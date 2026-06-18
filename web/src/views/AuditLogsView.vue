<template>
  <section class="page">
    <div class="page-header">
      <h1>{{ t('pages.auditLogs.title') }}</h1>
      <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
    </div>
    <el-table :data="rows" v-loading="loading" border :empty-text="t('pages.auditLogs.empty')">
      <el-table-column prop="operated_at" :label="t('common.time')" width="170">
        <template #default="{ row }">{{ formatDateTime(row.operated_at) }}</template>
      </el-table-column>
      <el-table-column prop="operator" :label="t('common.user')" width="110" show-overflow-tooltip />
      <el-table-column prop="action" :label="t('common.actions')" width="130">
        <template #default="{ row }"><el-tag :type="auditActionType(row.action)" effect="plain">{{ auditActionLabel(row.action) }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="object_type" :label="t('pages.auditLogs.object')" width="130">
        <template #default="{ row }"><el-tag type="info" effect="plain">{{ row.object_type }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="object_id" :label="t('pages.auditLogs.objectId')" width="100" show-overflow-tooltip />
      <el-table-column prop="before_data" :label="t('pages.auditLogs.before')" min-width="220" show-overflow-tooltip />
      <el-table-column prop="after_data" :label="t('pages.auditLogs.after')" min-width="220" show-overflow-tooltip />
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { apiClient } from '@/api/client'
import type { ApiResponse, OperationLog, PageResult } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const loading = ref(false)
const rows = ref<OperationLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<OperationLog>>>('/operation-logs', { params: { page: page.value, page_size: pageSize } })
    rows.value = res.data.data.items
    total.value = res.data.data.total
  } finally {
    loading.value = false
  }
}

function auditActionType(action?: string) {
  if (action === 'delete') return 'danger'
  if (action === 'update' || action === 'status') return 'warning'
  if (action === 'create') return 'success'
  return 'info'
}

function auditActionLabel(action?: string) {
  if (action === 'create') return t('common.add')
  if (action === 'update') return t('common.update')
  if (action === 'delete') return t('common.delete')
  if (action === 'status') return t('common.status')
  return action || '-'
}

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

onMounted(load)
</script>
