<template>
  <section class="page">
    <div class="page-header">
      <h1>调度任务</h1>
      <el-button :loading="loading" @click="load">刷新</el-button>
    </div>
    <el-table :data="rows" v-loading="loading" border empty-text="暂无调度任务">
      <el-table-column prop="task_name" label="任务" min-width="180" show-overflow-tooltip />
      <el-table-column label="Cron" min-width="200">
        <template #default="{ row }">
          <div class="cron-editor">
            <el-select placeholder="常用频率" style="width: 150px" @change="(value: string) => applyCron(row, value)">
              <el-option v-for="item in cronPresets" :key="item.value" :label="item.label" :value="item.value" />
            </el-select>
            <el-input v-model="row.cron_expr" />
          </div>
          <div class="cron-hint">{{ explainCron(row.cron_expr) }}</div>
        </template>
      </el-table-column>
      <el-table-column label="启用" width="90">
        <template #default="{ row }"><el-switch v-model="row.enabled" /></template>
      </el-table-column>
      <el-table-column prop="last_run_at" label="上次运行" width="170">
        <template #default="{ row }">{{ formatDateTime(row.last_run_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="150" fixed="right">
        <template #default="{ row }">
          <el-button size="small" type="primary" @click="save(row)">保存</el-button>
          <el-dropdown trigger="click" @command="(command: string) => handleTaskCommand(command, row)">
            <el-button size="small" :icon="MoreFilled" />
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="run">立即执行</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </template>
      </el-table-column>
    </el-table>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { MoreFilled } from '@element-plus/icons-vue'
import { apiClient } from '@/api/client'
import type { ApiResponse, TaskConfig } from '@/api/types'

const loading = ref(false)
const running = ref(false)
const rows = ref<TaskConfig[]>([])
const cronPresets = [
  { label: '每 5 分钟', value: '*/5 * * * *' },
  { label: '每 30 分钟', value: '*/30 * * * *' },
  { label: '每小时', value: '0 * * * *' },
  { label: '每天 09:00', value: '0 9 * * *' }
]

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<TaskConfig[]>>('/task-configs')
    rows.value = res.data.data
  } finally {
    loading.value = false
  }
}

async function save(row: TaskConfig) {
  await apiClient.put(`/task-configs/${row.id}`, { cron_expr: row.cron_expr, enabled: row.enabled })
  ElMessage.success('调度配置已保存')
  await load()
}

function applyCron(row: TaskConfig, value: string) {
  row.cron_expr = value
}

function explainCron(value: string) {
  const normalized = value.trim()
  const known = cronPresets.find((item) => item.value === normalized)
  if (known) return known.label
  const parts = normalized.split(/\s+/)
  if (parts.length !== 5) return 'Cron 需要 5 段：分钟 小时 日期 月份 星期'
  if (/^\d+$/.test(parts[0])) return `每小时第 ${parts[0]} 分钟执行`
  if (parts[0].startsWith('*/')) return `每 ${parts[0].slice(2)} 分钟执行`
  return '自定义 Cron 表达式'
}

async function run(row: TaskConfig) {
  running.value = true
  try {
    await apiClient.post(`/task-configs/${row.id}/run`)
    ElMessage.success('任务已触发')
    await load()
  } finally {
    running.value = false
  }
}

async function handleTaskCommand(command: string, row: TaskConfig) {
  if (command === 'run') {
    await run(row)
  }
}

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

onMounted(load)
</script>
