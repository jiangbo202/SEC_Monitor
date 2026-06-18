<template>
  <section class="page dashboard-page">
    <div class="page-header">
      <div>
        <h1>{{ t('pages.dashboard.title') }}</h1>
        <p class="page-subtitle">{{ t('pages.dashboard.subtitle') }}</p>
      </div>
      <div class="dashboard-actions">
        <el-button :loading="refreshing" type="primary" @click="refreshFilings">{{ t('pages.dashboard.refreshFilings') }}</el-button>
        <el-button :loading="loading" @click="load">{{ t('common.refreshPanel') }}</el-button>
      </div>
    </div>

    <el-dialog v-model="onboardingVisible" :title="t('pages.onboarding.title')" width="720px">
      <p class="page-subtitle">{{ t('pages.onboarding.description') }}</p>
      <el-steps direction="vertical" :active="onboardingActiveStep" class="onboarding-steps">
        <el-step :title="t('pages.onboarding.userAgent')" :description="t('pages.onboarding.userAgentHint')" />
        <el-step :title="t('pages.onboarding.target')" :description="t('pages.onboarding.targetHint')" />
        <el-step :title="t('pages.onboarding.telegram')" :description="t('pages.onboarding.telegramHint')" />
        <el-step :title="t('pages.onboarding.sync')" :description="t('pages.onboarding.syncHint')" />
      </el-steps>
      <template #footer>
        <el-button @click="completeOnboarding">{{ t('pages.onboarding.skip') }}</el-button>
        <el-button @click="$router.push('/configs')">{{ t('pages.onboarding.goConfigs') }}</el-button>
        <el-button @click="$router.push('/targets')">{{ t('pages.onboarding.addTarget') }}</el-button>
        <el-button @click="$router.push('/telegram')">{{ t('pages.onboarding.goTelegram') }}</el-button>
        <el-button type="primary" :loading="refreshing" @click="refreshFilings">{{ t('pages.onboarding.refreshFilings') }}</el-button>
        <el-button type="success" @click="completeOnboarding">{{ t('pages.onboarding.finish') }}</el-button>
      </template>
    </el-dialog>

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
            <span>{{ t('pages.dashboard.latestFilings') }}</span>
            <el-link type="primary" @click="$router.push('/filings')">{{ t('common.viewAll') }}</el-link>
          </div>
        </template>
        <el-table :data="recentFilings" v-loading="loading" border>
          <el-table-column prop="filing_type" :label="t('common.type')" width="100">
            <template #default="{ row }"><el-tag effect="plain">{{ row.filing_type }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="ticker" label="Ticker" width="90" />
          <el-table-column prop="company_name" :label="t('common.company')" min-width="160" show-overflow-tooltip />
          <el-table-column prop="filing_date" :label="t('common.filingDate')" width="130">
            <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
          </el-table-column>
          <el-table-column prop="pulled_at" :label="t('common.syncTime')" width="180">
            <template #default="{ row }">{{ formatDateTime(row.pulled_at) }}</template>
          </el-table-column>
          <el-table-column :label="t('common.link')" width="80">
            <template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">{{ t('common.open') }}</el-link></template>
          </el-table-column>
        </el-table>
      </el-card>

      <el-card shadow="never" class="dashboard-panel">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.dashboard.syncStatus') }}</span>
            <el-link type="primary" @click="$router.push('/sync-runs')">{{ t('common.history') }}</el-link>
          </div>
        </template>
        <div v-if="latestSync" class="status-block">
          <el-tag :type="syncStatusType(latestSync.status)" effect="plain">{{ latestSync.status }}</el-tag>
          <strong>{{ t('pages.dashboard.newFilings', { count: latestSync.new_filings }) }}</strong>
          <span>{{ t('pages.dashboard.syncSummary', { targets: latestSync.targets_checked, failed: latestSync.failed_targets }) }}</span>
          <span>{{ t('pages.dashboard.startedAt', { time: formatDateTime(latestSync.started_at) }) }}</span>
          <span>{{ t('pages.dashboard.finishedAt', { time: formatDateTime(latestSync.finished_at) }) }}</span>
        </div>
        <el-empty v-else :description="t('pages.dashboard.noSyncRuns')" />
      </el-card>

      <el-card shadow="never" class="dashboard-panel">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.dashboard.targetHealth') }}</span>
            <el-link type="primary" @click="$router.push('/targets')">{{ t('common.manage') }}</el-link>
          </div>
        </template>
        <div class="target-health">
          <div class="health-row"><span>{{ t('pages.dashboard.enabledTargets') }}</span><strong>{{ enabledTargetTotal }}</strong></div>
          <div class="health-row"><span>{{ t('pages.dashboard.syncSuccess') }}</span><strong>{{ successfulTargets }}</strong></div>
          <div class="health-row danger"><span>{{ t('pages.dashboard.syncFailed') }}</span><strong>{{ failedTargets }}</strong></div>
        </div>
      </el-card>

      <el-card shadow="never" class="dashboard-panel">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.dashboard.activeTargets') }}</span>
            <el-link type="primary" @click="$router.push('/filings')">{{ t('common.filings') }}</el-link>
          </div>
        </template>
        <div v-if="activeTargets.length" class="rank-list">
          <div v-for="item in activeTargets" :key="item.ticker" class="rank-row">
            <div>
              <strong>{{ item.ticker }}</strong>
              <span>{{ item.latestType }}</span>
            </div>
            <el-tag effect="plain">{{ t('pages.dashboard.countSuffix', { count: item.count }) }}</el-tag>
          </div>
        </div>
        <el-empty v-else :description="t('pages.dashboard.noActiveTargets')" />
      </el-card>

      <el-card shadow="never" class="dashboard-panel">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.dashboard.failedTargets') }}</span>
            <el-link type="primary" @click="$router.push('/targets?status=enabled')">{{ t('common.process') }}</el-link>
          </div>
        </template>
        <div v-if="failedTargetItems.length" class="issue-list">
          <div v-for="item in failedTargetItems" :key="item.id" class="issue-row">
            <div>
              <strong>{{ item.ticker }}</strong>
              <span>{{ item.last_sync_error || t('pages.dashboard.noSyncErrorDetail') }}</span>
            </div>
            <el-button size="small" @click="$router.push(`/targets?ticker=${encodeURIComponent(item.ticker)}`)">{{ t('common.view') }}</el-button>
          </div>
        </div>
        <el-empty v-else :description="t('pages.dashboard.noFailedTargets')" />
      </el-card>

      <el-card shadow="never" class="dashboard-panel panel-wide">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.dashboard.recentNotifications') }}</span>
            <div class="panel-header-actions">
              <el-tag :type="notificationRateType" effect="plain">{{ t('pages.dashboard.notificationRate', { rate: notificationSuccessRate }) }}</el-tag>
              <el-link type="primary" @click="$router.push('/notification-logs')">{{ t('nav.notificationLogs') }}</el-link>
            </div>
          </div>
        </template>
        <el-table :data="recentNotifications" v-loading="loading" border>
          <el-table-column prop="created_at" :label="t('common.time')" width="180">
            <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
          </el-table-column>
          <el-table-column prop="filing_id" label="Filing ID" min-width="180" show-overflow-tooltip />
          <el-table-column prop="status" :label="t('common.status')" width="110">
            <template #default="{ row }">
              <el-tag class="status-tag" :type="notificationStatusType(row.status)" effect="plain">{{ notificationStatusLabel(row.status) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="error_message" :label="t('common.error')" min-width="180" show-overflow-tooltip />
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
import { useI18n } from '@/i18n'

const { t } = useI18n()
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
const onboardingVisible = ref(false)
const onboardingActiveStep = computed(() => {
  if (targetTotal.value === 0) return 1
  if (!telegramEnabled.value) return 2
  if (!latestSync.value) return 3
  return 4
})

const metrics = computed(() => [
  { label: t('nav.targets'), value: targetTotal.value, hint: t('pages.dashboard.enabledTargets') + ` ${enabledTargetTotal.value}`, icon: Aim },
  { label: t('nav.filings'), value: filingTotal.value, hint: t('common.filings'), icon: Document },
  { label: t('nav.syncRuns'), value: syncTotal.value, hint: latestSync.value ? latestSync.value.status : t('pages.dashboard.noSyncRuns'), icon: DataAnalysis },
  { label: t('nav.notificationLogs'), value: notificationTotal.value, hint: 'Telegram', icon: Bell }
])

const healthAlerts = computed(() => {
  const alerts: Array<{ title: string, description: string, type: 'success' | 'warning' | 'error' | 'info' }> = []
  if (failedTargets.value > 0) {
    alerts.push({
      title: t('pages.dashboard.failedTargetsAlertTitle', { count: failedTargets.value }),
      description: t('pages.dashboard.failedTargetsAlertDescription'),
      type: 'error'
    })
  }
  if (!latestSync.value) {
    alerts.push({ title: t('pages.dashboard.noSyncAlertTitle'), description: t('pages.dashboard.noSyncAlertDescription'), type: 'warning' })
  } else if (latestSyncAgeHours.value >= 6) {
    alerts.push({
      title: t('pages.dashboard.staleSyncAlertTitle', { hours: latestSyncAgeHours.value }),
      description: t('pages.dashboard.staleSyncAlertDescription'),
      type: 'warning'
    })
  }
  if (!schedulerEnabled.value) {
    alerts.push({ title: t('pages.dashboard.schedulerDisabledTitle'), description: t('pages.dashboard.schedulerDisabledDescription'), type: 'warning' })
  }
  if (!telegramEnabled.value) {
    alerts.push({ title: t('pages.dashboard.telegramDisabledTitle'), description: t('pages.dashboard.telegramDisabledDescription'), type: 'info' })
  }
  if (alerts.length === 0) {
    alerts.push({ title: t('pages.dashboard.healthyTitle'), description: t('pages.dashboard.healthyDescription'), type: 'success' })
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
    const [targets, enabledTargets, filings, syncRuns, notifications, telegramConfigs, taskConfigs, uiConfigs] = await Promise.all([
      apiClient.get<ApiResponse<PageResult<WatchTarget>>>('/watch-targets', { params: { page: 1, page_size: 10 } }),
      apiClient.get<ApiResponse<PageResult<WatchTarget>>>('/watch-targets', { params: { status: 'enabled', page: 1, page_size: 200 } }),
      apiClient.get<ApiResponse<PageResult<Filing>>>('/filings', { params: { page: 1, page_size: 100, sort_by: 'pulled_at', sort_order: 'desc' } }),
      apiClient.get<ApiResponse<PageResult<SyncRun>>>('/sync-runs', { params: { page: 1, page_size: 1 } }),
      apiClient.get<ApiResponse<PageResult<NotificationLog>>>('/notification-logs', { params: { page: 1, page_size: 5 } }),
      apiClient.get<ApiResponse<SystemConfig[]>>('/telegram/config'),
      apiClient.get<ApiResponse<TaskConfig[]>>('/task-configs'),
      apiClient.get<ApiResponse<SystemConfig[]>>('/system-configs', { params: { category: 'ui' } })
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
    onboardingVisible.value = configValue(uiConfigs.data.data, 'ui.onboarding_completed') !== 'true'
  } finally {
    loading.value = false
  }
}

async function completeOnboarding() {
  await apiClient.put('/system-configs', [
    { key: 'ui.onboarding_completed', value: 'true', value_type: 'bool', category: 'ui', encrypted: false }
  ])
  onboardingVisible.value = false
  ElMessage.success(t('messages.onboardingDone'))
}

function configValue(configs: SystemConfig[], key: string) {
  return configs.find((item) => item.config_key === key)?.config_value || ''
}

async function refreshFilings() {
  refreshing.value = true
  try {
    const res = await apiClient.post<ApiResponse<{ new_filings: number }>>('/filings/refresh')
    ElMessage.success(t('messages.newFilingsAdded', { count: res.data.data.new_filings }))
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
  if (status === 'success') return t('status.success')
  if (status === 'failed') return t('status.failed')
  return status || '-'
}

onMounted(load)
</script>
