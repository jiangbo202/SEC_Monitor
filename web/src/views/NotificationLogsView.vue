<template>
  <section class="page">
    <div class="page-header">
      <h1>通知日志</h1>
      <el-button :loading="loading" @click="load">刷新</el-button>
    </div>
    <el-table :data="rows" v-loading="loading" border empty-text="暂无通知日志">
      <el-table-column prop="created_at" label="时间" width="170">
        <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column prop="filing_id" label="Filing ID" min-width="180" show-overflow-tooltip />
      <el-table-column prop="channel" label="渠道" width="100">
        <template #default="{ row }"><el-tag type="info" effect="plain">{{ row.channel }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="target" label="目标" min-width="150" show-overflow-tooltip />
      <el-table-column prop="status" label="状态" width="120">
        <template #default="{ row }">
          <el-tag class="status-tag" :type="notificationStatusType(row.status)" effect="plain">{{ notificationStatusLabel(row.status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="retry_count" label="重试" width="80" align="right" />
      <el-table-column prop="error_message" label="错误" min-width="220" show-overflow-tooltip />
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { apiClient } from '@/api/client'
import type { ApiResponse, NotificationLog, PageResult } from '@/api/types'

const loading = ref(false)
const rows = ref<NotificationLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<NotificationLog>>>('/notification-logs', { params: { page: page.value, page_size: pageSize } })
    rows.value = res.data.data.items
    total.value = res.data.data.total
  } finally {
    loading.value = false
  }
}

function notificationStatusType(status?: string) {
  if (status === 'success') return 'success'
  if (status === 'failed') return 'danger'
  return 'info'
}

function notificationStatusLabel(status?: string) {
  if (status === 'success') return '成功'
  if (status === 'failed') return '失败'
  return status || '-'
}

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

onMounted(load)
</script>
