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
          <div class="task-description">{{ taskDescription(row.task_name) }}</div>
          <div class="cron-hint">{{ row.task_name }}</div>
        </template>
      </el-table-column>
      <el-table-column label="Cron" min-width="200">
        <template #default="{ row }">
          <div class="cron-editor">
            <el-select fit-input-width :placeholder="t('pages.scheduler.commonFrequency')" style="width: 150px" @change="(value: string) => applyCron(row, value)">
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
				<el-dropdown-item command="logs">日志</el-dropdown-item>
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
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { MoreFilled } from '@element-plus/icons-vue'
import { apiClient } from '@/api/client'
import type { ApiResponse, SystemConfig, TaskConfig } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const router = useRouter()
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
	  return
  }
	if (command === 'logs') viewLogs(row)
}

function viewLogs(row: TaskConfig) {
	if (row.task_name === 'small_cap_discovery_sync' || row.task_name === 'small_cap_discovery_full_sync') {
		router.push({ path: '/discovery-logs', query: { kind: row.task_name === 'small_cap_discovery_sync' ? 'incremental' : 'full' } })
		return
	}
	router.push({ path: '/sync-runs', query: { task_name: row.task_name } })
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
    ipo_radar_sync: 'IPO 新申报扫描',
    ipo_lifecycle_reconcile_sync: 'IPO 生命周期补查',
    ipo_offering_reconcile_sync: 'IPO 发行条款重解析',
    ipo_listing_reconcile_sync: 'IPO 上市状态核验',
    macro_calendar_sync: '宏观日历同步',
    market_trend_sync: '大盘趋势日线同步',
    us_futures_sync: '美股期货日线同步',
    longbridge_candidate_research_sync: 'Longbridge P1 候选市场研究',
    longbridge_candidate_valuation_sync: 'Longbridge P2 候选估值研究',
    longbridge_watch_target_valuation_sync: 'Longbridge 监控标的估值研究',
    longbridge_watch_target_research_sync: 'Longbridge 监控标的机构持仓研究',
  }
  return labels[value] || value
}

function taskDescription(value: string) {
  const descriptions: Record<string, string> = {
    sec_filing_sync: '轮询已启用标的的 SEC 最新申报；与 IPO 扫描错峰，优先保证公告时效。',
    ipo_radar_sync: '仅扫描 S-1/F-1 等当前 IPO 申报并更新官方 SEC 映射；补偿工作由独立任务完成。',
    ipo_lifecycle_reconcile_sync: '按最久未检查优先补查活跃 IPO 的 EFFECT、424B4、RW 等生命周期文件。',
    ipo_offering_reconcile_sync: '限量重试 424B4 发行价、发行数量和预计募资解析，不影响新申报扫描。',
    ipo_listing_reconcile_sync: '独立查询 Longbridge 上市资料与 IPO 日历；失败使用退避重试，不改变 SEC 原始事实。',
    small_cap_discovery_sync: '增量更新小盘候选的上市身份、财务与行情预筛；不触发 P1/P2 研究。',
    small_cap_discovery_full_sync: '每周全量校准 SEC/Nasdaq 候选宇宙，用于修复身份变化和遗漏。',
    watch_target_market_sync: '美股收盘后同步监控标的日线，供持仓与技术指标使用。',
    watch_target_earnings_sync: '同步监控标的及当前候选的 Longbridge 财报日历和市场预期。',
    market_trend_sync: '美股收盘后从 Longbridge 更新大盘、VIX 和板块 ETF 日线。',
    us_futures_sync: '更新美股指数、商品及国债连续期货日线；来源为 Yahoo Finance，失败不影响 Longbridge 数据。',
    macro_calendar_sync: '在美国宏观数据常见发布时间后刷新官方日历、实际值和 Longbridge 日历补充。',
    longbridge_candidate_research_sync: 'P1：独立更新 EPS 预期、异动、机构与基金持仓；按预算轮换候选。',
    longbridge_candidate_valuation_sync: 'P2：独立更新估值历史、行业分位和同行比较；使用单独预算与失败周期。',
    longbridge_watch_target_valuation_sync: '独立更新已启用股票监控标的的估值历史与同业比较；不占用候选 P2 预算。',
    longbridge_watch_target_research_sync: '独立更新已启用股票监控标的的机构股东、基金/ETF 持仓、EPS 预期及异动；不占用候选 P1 预算。',
    candidate_notification_sync: '按候选评分和通知设置生成去重提醒；通知功能关闭时不会发送。',
    trade_setup_notification_sync: '按交易计划状态生成入场、退出或失效提醒；通知功能关闭时不会发送。',
    notification_retry_sync: '重试到期但此前发送失败的通知，不重新拉取市场或 SEC 数据。',
    sqlite_backup: '备份主库与小盘研究库；安排在日常数据任务完成后，避免影响数据更新。',
    operation_history_cleanup: '清理过期运行、诊断和通知历史，不删除行情、候选或 SEC 数据。',
    operational_health_notification_sync: '在启用 Telegram 后发送去重的运行健康告警；默认关闭以避免无配置空跑。'
  }
  return descriptions[value] || '系统后台任务。'
}

onMounted(load)
</script>

<style scoped>
.scheduler-timezone {
  margin-bottom: 12px;
}

.task-description {
  margin-top: 4px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1.5;
}
</style>
