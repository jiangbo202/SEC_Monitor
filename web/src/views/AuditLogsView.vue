<template>
  <section class="page">
    <div class="page-header">
      <h1>审计日志</h1>
      <el-button :loading="loading" @click="load">刷新</el-button>
    </div>
    <el-table :data="rows" v-loading="loading" border empty-text="暂无审计日志">
      <el-table-column prop="operated_at" label="时间" width="170">
        <template #default="{ row }">{{ formatDateTime(row.operated_at) }}</template>
      </el-table-column>
      <el-table-column prop="operator" label="用户" width="110" show-overflow-tooltip />
      <el-table-column prop="action" label="操作" width="130">
        <template #default="{ row }"><el-tag :type="auditActionType(row.action)" effect="plain">{{ auditActionLabel(row.action) }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="object_type" label="对象" width="130">
        <template #default="{ row }"><el-tag type="info" effect="plain">{{ row.object_type }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="object_id" label="对象 ID" width="100" show-overflow-tooltip />
      <el-table-column prop="before_data" label="操作前" min-width="220" show-overflow-tooltip />
      <el-table-column prop="after_data" label="操作后" min-width="220" show-overflow-tooltip />
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { apiClient } from '@/api/client'
import type { ApiResponse, OperationLog, PageResult } from '@/api/types'

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
  if (action === 'create') return '新增'
  if (action === 'update') return '更新'
  if (action === 'delete') return '删除'
  if (action === 'status') return '状态'
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
