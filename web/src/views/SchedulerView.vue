<template>
  <section class="page">
    <div class="page-header">
      <h1>{{ t('pages.scheduler.title') }}</h1>
      <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
    </div>
    <el-table :data="rows" v-loading="loading" border :empty-text="t('pages.scheduler.empty')">
      <el-table-column prop="task_name" :label="t('common.task')" min-width="180" show-overflow-tooltip />
      <el-table-column label="Cron" min-width="200">
        <template #default="{ row }">
          <div class="cron-editor">
            <el-select :placeholder="t('pages.scheduler.commonFrequency')" style="width: 150px" @change="(value: string) => applyCron(row, value)">
              <el-option v-for="item in cronPresets" :key="item.value" :label="item.label" :value="item.value" />
            </el-select>
            <el-input v-model="row.cron_expr" />
          </div>
          <div class="cron-hint">{{ explainCron(row.cron_expr) }}</div>
        </template>
      </el-table-column>
      <el-table-column :label="t('common.enabled')" width="90">
        <template #default="{ row }"><el-switch v-model="row.enabled" /></template>
      </el-table-column>
      <el-table-column prop="last_run_at" :label="t('pages.scheduler.lastRun')" width="170">
        <template #default="{ row }">{{ formatDateTime(row.last_run_at) }}</template>
      </el-table-column>
      <el-table-column :label="t('common.actions')" width="150" fixed="right">
        <template #default="{ row }">
          <el-button size="small" type="primary" @click="save(row)">{{ t('common.save') }}</el-button>
          <el-dropdown trigger="click" @command="(command: string) => handleTaskCommand(command, row)">
            <el-button size="small" :icon="MoreFilled" />
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="run">{{ t('pages.scheduler.runNow') }}</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </template>
      </el-table-column>
    </el-table>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { MoreFilled } from '@element-plus/icons-vue'
import { apiClient } from '@/api/client'
import type { ApiResponse, TaskConfig } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const loading = ref(false)
const running = ref(false)
const rows = ref<TaskConfig[]>([])
const cronPresets = computed(() => [
  { label: t('pages.scheduler.presets.every5'), value: '*/5 * * * *' },
  { label: t('pages.scheduler.presets.every30'), value: '*/30 * * * *' },
  { label: t('pages.scheduler.presets.hourly'), value: '0 * * * *' },
  { label: t('pages.scheduler.presets.daily9'), value: '0 9 * * *' }
])

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
  ElMessage.success(t('messages.taskSaved'))
  await load()
}

function applyCron(row: TaskConfig, value: string) {
  row.cron_expr = value
}

function explainCron(value: string) {
  const normalized = value.trim()
  const known = cronPresets.value.find((item) => item.value === normalized)
  if (known) return known.label
  const parts = normalized.split(/\s+/)
  if (parts.length !== 5) return t('pages.scheduler.cronInvalid')
  if (/^\d+$/.test(parts[0])) return t('pages.scheduler.cronHourlyMinute', { minute: parts[0] })
  if (parts[0].startsWith('*/')) return t('pages.scheduler.cronEveryMinutes', { minutes: parts[0].slice(2) })
  return t('pages.scheduler.cronCustom')
}

async function run(row: TaskConfig) {
  running.value = true
  try {
    await apiClient.post(`/task-configs/${row.id}/run`)
    ElMessage.success(t('messages.taskTriggered'))
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
