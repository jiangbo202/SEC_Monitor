<template>
  <section class="page">
    <div class="page-header">
      <h1>{{ t('pages.scheduler.title') }}</h1>
      <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
    </div>
    <el-alert
      class="scheduler-timezone"
      type="info"
      :closable="false"
      show-icon
      :title="t('pages.scheduler.timezoneTitle', { timezone: schedulerTimezone })"
      :description="t('pages.scheduler.timezoneDescription')"
    />
    <el-table :data="rows" v-loading="loading" border :empty-text="t('pages.scheduler.empty')">
      <el-table-column :label="t('common.task')" min-width="220" show-overflow-tooltip>
        <template #default="{ row }">
          <div>{{ taskLabel(row.task_name) }}</div>
          <div class="cron-hint">{{ row.task_name }}</div>
        </template>
      </el-table-column>
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
      <el-table-column :label="t('pages.scheduler.nextRun')" width="170">
        <template #default="{ row }">
          <span v-if="row.enabled && row.next_run_at">{{ formatDateTime(row.next_run_at) }}</span>
          <el-tag v-else type="info" effect="plain">{{ t('pages.scheduler.notScheduled') }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column :label="t('pages.scheduler.runStatus')" width="125">
        <template #default="{ row }">
          <el-tooltip v-if="row.last_error_message" :content="row.last_error_message" placement="top">
            <el-tag :type="taskStatusType(row.last_status)" effect="plain">{{ taskStatusLabel(row.last_status) }}</el-tag>
          </el-tooltip>
          <el-tag v-else :type="taskStatusType(row.last_status)" effect="plain">{{ taskStatusLabel(row.last_status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column :label="t('pages.scheduler.consecutiveFailures')" width="110" align="center">
        <template #default="{ row }">
          <el-tag :type="row.consecutive_failures >= 3 ? 'danger' : row.consecutive_failures > 0 ? 'warning' : 'info'" effect="plain">{{ row.consecutive_failures || 0 }}</el-tag>
        </template>
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
import type { ApiResponse, SystemConfig, TaskConfig } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const loading = ref(false)
const running = ref(false)
const rows = ref<TaskConfig[]>([])
const schedulerTimezone = ref('UTC')
const cronPresets = computed(() => [
  { label: t('pages.scheduler.presets.every5'), value: '*/5 * * * *' },
  { label: t('pages.scheduler.presets.every30'), value: '*/30 * * * *' },
  { label: t('pages.scheduler.presets.hourly'), value: '0 * * * *' },
  { label: t('pages.scheduler.presets.daily9'), value: '0 9 * * *' }
])

async function load() {
  loading.value = true
  try {
    const [tasksRes, configsRes] = await Promise.all([
      apiClient.get<ApiResponse<TaskConfig[]>>('/task-configs'),
      apiClient.get<ApiResponse<SystemConfig[]>>('/system-configs?category=scheduler')
    ])
    rows.value = tasksRes.data.data
    schedulerTimezone.value = configsRes.data.data.find((item) => item.config_key === 'scheduler.timezone')?.config_value || 'UTC'
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
  const [minute, hour, dayOfMonth, month, dayOfWeek] = parts
  if (minute.startsWith('*/') && hour === '*' && dayOfMonth === '*' && month === '*' && dayOfWeek === '*') {
    return t('pages.scheduler.cronEveryMinutes', { minutes: minute.slice(2) })
  }
  if (/^\d+$/.test(minute) && hour === '*' && dayOfMonth === '*' && month === '*' && dayOfWeek === '*') {
    return t('pages.scheduler.cronHourlyMinute', { minute })
  }
  if (/^\d+$/.test(minute) && /^\d+(,\d+)*$/.test(hour) && dayOfMonth === '*' && month === '*') {
    const times = hour.split(',').map((value) => `${value.padStart(2, '0')}:${minute.padStart(2, '0')}`).join('、')
    if (dayOfWeek === '*') return t('pages.scheduler.cronDailyAt', { time: times })
    const days = describeWeekdays(dayOfWeek)
    if (days) return t('pages.scheduler.cronWeekdaysAt', { days, time: times })
  }
  return t('pages.scheduler.cronCustom')
}

function describeWeekdays(value: string) {
  const labels: Record<string, string> = {
    '0': t('pages.scheduler.weekdays.sun'),
    '1': t('pages.scheduler.weekdays.mon'),
    '2': t('pages.scheduler.weekdays.tue'),
    '3': t('pages.scheduler.weekdays.wed'),
    '4': t('pages.scheduler.weekdays.thu'),
    '5': t('pages.scheduler.weekdays.fri'),
    '6': t('pages.scheduler.weekdays.sat'),
  }
  return value.split(',').map((part) => {
    const range = part.match(/^(\d)-(\d)$/)
    if (range && labels[range[1]] && labels[range[2]]) return `${labels[range[1]]}–${labels[range[2]]}`
    return labels[part] || ''
  }).filter(Boolean).join('、')
}

async function run(row: TaskConfig) {
  running.value = true
  try {
    await apiClient.post(`/task-configs/${row.id}/run`)
    ElMessage.success(t('messages.taskTriggered'))
    await load()
  } catch (error: any) {
    const message = error?.response?.data?.message || error?.message || t('messages.taskTriggerFailed')
    ElMessage.error(message)
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
  try {
    return new Intl.DateTimeFormat(undefined, {
      timeZone: schedulerTimezone.value,
      year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
    }).format(date)
  } catch {
    return date.toLocaleString()
  }
}

function taskStatusType(value: string) {
  if (value === 'success') return 'success'
  if (value === 'partial') return 'warning'
  if (value === 'skipped') return 'info'
  if (value === 'failed') return 'danger'
  if (value === 'running') return 'primary'
  if (value === 'interrupted') return 'warning'
  return 'info'
}

function taskStatusLabel(value: string) {
  return t(`pages.scheduler.status.${value || 'idle'}`)
}

function taskLabel(value: string) {
  const labels: Record<string, string> = {
    watch_target_market_sync: '监控标的每日行情同步',
    watch_target_earnings_sync: '监控标的财报预告同步',
    small_cap_discovery_sync: '小盘候选每日同步',
    small_cap_discovery_full_sync: '小盘候选全量校准',
    sec_filing_sync: 'SEC 公告同步',
    ipo_radar_sync: 'IPO 监控同步',
    macro_calendar_sync: '宏观日历同步',
  }
  return labels[value] || value
}

onMounted(load)
</script>

<style scoped>
.scheduler-timezone {
  margin-bottom: 16px;
}
</style>
