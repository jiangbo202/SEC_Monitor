<template>
  <section class="page dashboard-page">
    <div class="page-header">
      <div>
        <h1>总览</h1>
        <p class="page-subtitle">SEC 监控状态、最近同步和最新公告</p>
      </div>
      <div class="dashboard-actions">
        <el-button :loading="refreshing" type="primary" @click="refreshFilings">刷新公告</el-button>
        <el-button :loading="loading" @click="load">刷新面板</el-button>
      </div>
    </div>

    <div class="health-alert-grid">
      <el-alert
        v-for="item in healthAlerts"
        :key="item.title"
        :title="item.title"
        :description="item.description"
        :type="item.type"
        :closable="false"
        show-icon
      />
    </div>

    <div class="kpi-grid">
      <el-card v-for="item in metrics" :key="item.label" shadow="never" class="kpi-card">
        <div class="kpi-card-inner">
          <component :is="item.icon" class="kpi-icon" />
          <div class="metric">
            <span>{{ item.label }}</span>
            <strong>{{ item.value }}</strong>
            <small>{{ item.hint }}</small>
          </div>
        </div>
      </el-card>
    </div>

    <div class="dashboard-grid">
      <el-card shadow="never" class="dashboard-panel panel-wide">
        <template #header>
          <div class="panel-header">
            <span>最新 SEC 公告</span>
            <el-link type="primary" @click="$router.push('/filings')">查看全部</el-link>
          </div>
        </template>
        <el-table :data="recentFilings" v-loading="loading" border>
          <el-table-column prop="filing_type" label="类型" width="100">
            <template #default="{ row }"><el-tag effect="plain">{{ row.filing_type }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="ticker" label="Ticker" width="90" />
          <el-table-column prop="company_name" label="公司" min-width="160" show-overflow-tooltip />
          <el-table-column prop="filing_date" label="Filing Date" width="130">
            <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
          </el-table-column>
          <el-table-column prop="pulled_at" label="同步时间" width="180">
            <template #default="{ row }">{{ formatDateTime(row.pulled_at) }}</template>
          </el-table-column>
          <el-table-column label="链接" width="80">
            <template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">打开</el-link></template>
          </el-table-column>
        </el-table>
      </el-card>

      <el-card shadow="never" class="dashboard-panel">
        <template #header>
          <div class="panel-header">
            <span>同步状态</span>
            <el-link type="primary" @click="$router.push('/sync-runs')">历史</el-link>
          </div>
        </template>
        <div v-if="latestSync" class="status-block">
          <el-tag :type="syncStatusType(latestSync.status)" effect="plain">{{ latestSync.status }}</el-tag>
          <strong>{{ latestSync.new_filings }} 条新增公告</strong>
          <span>检查 {{ latestSync.targets_checked }} 个标的，失败 {{ latestSync.failed_targets }} 个</span>
          <span>开始：{{ formatDateTime(latestSync.started_at) }}</span>
          <span>结束：{{ formatDateTime(latestSync.finished_at) }}</span>
        </div>
        <el-empty v-else description="暂无同步记录" />
      </el-card>

      <el-card shadow="never" class="dashboard-panel">
        <template #header>
          <div class="panel-header">
            <span>标的健康</span>
            <el-link type="primary" @click="$router.push('/targets')">管理</el-link>
          </div>
        </template>
        <div class="target-health">
          <div class="health-row"><span>启用标的</span><strong>{{ enabledTargetTotal }}</strong></div>
          <div class="health-row"><span>同步成功</span><strong>{{ successfulTargets }}</strong></div>
          <div class="health-row danger"><span>同步失败</span><strong>{{ failedTargets }}</strong></div>
        </div>
      </el-card>

      <el-card shadow="never" class="dashboard-panel">
        <template #header>
          <div class="panel-header">
            <span>活跃标的</span>
            <el-link type="primary" @click="$router.push('/filings')">公告</el-link>
          </div>
        </template>
        <div v-if="activeTargets.length" class="rank-list">
          <div v-for="item in activeTargets" :key="item.ticker" class="rank-row">
            <div>
              <strong>{{ item.ticker }}</strong>
              <span>{{ item.latestType }}</span>
            </div>
            <el-tag effect="plain">{{ item.count }} 条</el-tag>
          </div>
        </div>
        <el-empty v-else description="暂无活跃标的" />
      </el-card>

      <el-card shadow="never" class="dashboard-panel">
        <template #header>
          <div class="panel-header">
            <span>失败标的</span>
            <el-link type="primary" @click="$router.push('/targets?status=enabled')">处理</el-link>
          </div>
        </template>
        <div v-if="failedTargetItems.length" class="issue-list">
          <div v-for="item in failedTargetItems" :key="item.id" class="issue-row">
            <div>
              <strong>{{ item.ticker }}</strong>
              <span>{{ item.last_sync_error || '同步失败，暂无错误详情' }}</span>
            </div>
            <el-button size="small" @click="$router.push(`/targets?ticker=${encodeURIComponent(item.ticker)}`)">查看</el-button>
          </div>
        </div>
        <el-empty v-else description="暂无失败标的" />
      </el-card>

      <el-card shadow="never" class="dashboard-panel panel-wide">
        <template #header>
          <div class="panel-header">
            <span>最近通知</span>
            <div class="panel-header-actions">
              <el-tag :type="notificationRateType" effect="plain">成功率 {{ notificationSuccessRate }}%</el-tag>
              <el-link type="primary" @click="$router.push('/notification-logs')">查看日志</el-link>
            </div>
          </div>
        </template>
        <el-table :data="recentNotifications" v-loading="loading" border>
          <el-table-column prop="created_at" label="时间" width="180">
            <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
          </el-table-column>
          <el-table-column prop="filing_id" label="Filing ID" min-width="180" show-overflow-tooltip />
          <el-table-column prop="status" label="状态" width="110">
            <template #default="{ row }">
              <el-tag class="status-tag" :type="notificationStatusType(row.status)" effect="plain">{{ notificationStatusLabel(row.status) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="error_message" label="错误" min-width="180" show-overflow-tooltip />
        </el-table>
      </el-card>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Aim, Bell, DataAnalysis, Document } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, Filing, NotificationLog, PageResult, SyncRun, SystemConfig, TaskConfig, WatchTarget } from '@/api/types'

const loading = ref(false)
const refreshing = ref(false)
const targetTotal = ref(0)
const enabledTargetTotal = ref(0)
const filingTotal = ref(0)
const notificationTotal = ref(0)
const syncTotal = ref(0)
const recentFilings = ref<Filing[]>([])
const dashboardFilings = ref<Filing[]>([])
const recentNotifications = ref<NotificationLog[]>([])
const latestSync = ref<SyncRun | null>(null)
const successfulTargets = ref(0)
const failedTargets = ref(0)
const failedTargetItems = ref<WatchTarget[]>([])
const telegramEnabled = ref(false)
const schedulerEnabled = ref(false)

const metrics = computed(() => [
  { label: '监控标的', value: targetTotal.value, hint: `${enabledTargetTotal.value} 个启用`, icon: Aim },
  { label: 'SEC 公告', value: filingTotal.value, hint: '已入库公告总数', icon: Document },
  { label: '同步批次', value: syncTotal.value, hint: latestSync.value ? `最近 ${latestSync.value.status}` : '暂无记录', icon: DataAnalysis },
  { label: '通知日志', value: notificationTotal.value, hint: 'Telegram 发送记录', icon: Bell }
])

const healthAlerts = computed(() => {
  const alerts: Array<{ title: string, description: string, type: 'success' | 'warning' | 'error' | 'info' }> = []
  if (failedTargets.value > 0) {
    alerts.push({
      title: `${failedTargets.value} 个标的同步失败`,
      description: '进入监控标的页查看错误，或对失败标的单独重试同步。',
      type: 'error'
    })
  }
  if (!latestSync.value) {
    alerts.push({ title: '还没有同步记录', description: '新增标的后可以手动刷新公告或等待调度执行。', type: 'warning' })
  } else if (latestSyncAgeHours.value >= 6) {
    alerts.push({
      title: `最近同步已超过 ${latestSyncAgeHours.value} 小时`,
      description: '建议检查调度任务是否启用，或手动刷新公告。',
      type: 'warning'
    })
  }
  if (!schedulerEnabled.value) {
    alerts.push({ title: '调度任务未启用', description: '当前不会自动周期拉取 SEC 公告。', type: 'warning' })
  }
  if (!telegramEnabled.value) {
    alerts.push({ title: 'Telegram 通知未启用', description: '新公告会入库，但不会主动推送提醒。', type: 'info' })
  }
  if (alerts.length === 0) {
    alerts.push({ title: '系统运行正常', description: '同步、调度和通知配置当前没有明显异常。', type: 'success' })
  }
  return alerts.slice(0, 3)
})

const latestSyncAgeHours = computed(() => {
  if (!latestSync.value?.started_at) return 0
  const started = new Date(latestSync.value.started_at)
  if (Number.isNaN(started.getTime())) return 0
  return Math.floor((Date.now() - started.getTime()) / 36e5)
})

const activeTargets = computed(() => {
  const stats = new Map<string, { ticker: string, count: number, latestType: string }>()
  for (const filing of dashboardFilings.value) {
    const current = stats.get(filing.ticker) || { ticker: filing.ticker, count: 0, latestType: filing.filing_type }
    current.count++
    if (!current.latestType) {
      current.latestType = filing.filing_type
    }
    stats.set(filing.ticker, current)
  }
  return Array.from(stats.values()).sort((a, b) => b.count - a.count).slice(0, 5)
})

const notificationSuccessRate = computed(() => {
  if (!recentNotifications.value.length) return 100
  const success = recentNotifications.value.filter((item) => item.status === 'success').length
  return Math.round((success / recentNotifications.value.length) * 100)
})

const notificationRateType = computed(() => {
  if (notificationSuccessRate.value >= 90) return 'success'
  if (notificationSuccessRate.value >= 70) return 'warning'
  return 'danger'
})

async function load() {
  loading.value = true
  try {
    const [targets, enabledTargets, filings, syncRuns, notifications, telegramConfigs, taskConfigs] = await Promise.all([
      apiClient.get<ApiResponse<PageResult<WatchTarget>>>('/watch-targets', { params: { page: 1, page_size: 10 } }),
      apiClient.get<ApiResponse<PageResult<WatchTarget>>>('/watch-targets', { params: { status: 'enabled', page: 1, page_size: 200 } }),
      apiClient.get<ApiResponse<PageResult<Filing>>>('/filings', { params: { page: 1, page_size: 100, sort_by: 'pulled_at', sort_order: 'desc' } }),
      apiClient.get<ApiResponse<PageResult<SyncRun>>>('/sync-runs', { params: { page: 1, page_size: 1 } }),
      apiClient.get<ApiResponse<PageResult<NotificationLog>>>('/notification-logs', { params: { page: 1, page_size: 5 } }),
      apiClient.get<ApiResponse<SystemConfig[]>>('/telegram/config'),
      apiClient.get<ApiResponse<TaskConfig[]>>('/task-configs')
    ])
    targetTotal.value = targets.data.data.total
    enabledTargetTotal.value = enabledTargets.data.data.total
    filingTotal.value = filings.data.data.total
    syncTotal.value = syncRuns.data.data.total
    notificationTotal.value = notifications.data.data.total
    dashboardFilings.value = filings.data.data.items
    recentFilings.value = filings.data.data.items.slice(0, 6)
    latestSync.value = syncRuns.data.data.items[0] || null
    recentNotifications.value = notifications.data.data.items
    successfulTargets.value = enabledTargets.data.data.items.filter((item) => item.last_sync_status === 'success').length
    failedTargets.value = enabledTargets.data.data.items.filter((item) => item.last_sync_status === 'failed').length
    failedTargetItems.value = enabledTargets.data.data.items.filter((item) => item.last_sync_status === 'failed').slice(0, 5)
    telegramEnabled.value = configValue(telegramConfigs.data.data, 'telegram.enabled') === 'true'
    schedulerEnabled.value = taskConfigs.data.data.some((item) => item.enabled)
  } finally {
    loading.value = false
  }
}

function configValue(configs: SystemConfig[], key: string) {
  return configs.find((item) => item.config_key === key)?.config_value || ''
}

async function refreshFilings() {
  refreshing.value = true
  try {
    const res = await apiClient.post<ApiResponse<{ new_filings: number }>>('/filings/refresh')
    ElMessage.success(`新增 ${res.data.data.new_filings} 条公告`)
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

function syncStatusType(status?: string) {
  if (status === 'success') return 'success'
  if (status === 'partial') return 'warning'
  if (status === 'failed') return 'danger'
  return 'info'
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

onMounted(load)
</script>
