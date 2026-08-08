<template>
  <section class="page dashboard-page">
    <div class="page-header">
      <div>
        <h1>{{ t('pages.dashboard.title') }}</h1>
        <p class="page-subtitle">{{ t('pages.dashboard.subtitle') }}</p>
      </div>
      <div class="dashboard-actions">
        <el-button :loading="refreshing" type="primary" @click="refreshFilings">{{ t('pages.dashboard.refreshFilings') }}</el-button>
        <el-button :loading="refreshingIpo" @click="refreshIpoFilings">{{ t('pages.dashboard.refreshIpoRadar') }}</el-button>
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
    <el-alert
      v-if="dashboardLoadWarnings.length"
      class="dashboard-load-warning"
      type="warning"
      :closable="false"
      show-icon
      :title="t('pages.dashboard.partialDataTitle')"
      :description="t('pages.dashboard.partialDataDescription', { sections: dashboardLoadWarnings.join('、') })"
    >
      <template #default><el-button link type="primary" @click="load">{{ t('common.refresh') }}</el-button></template>
    </el-alert>

    <el-card shadow="never" class="dashboard-panel dashboard-operations-panel">
      <template #header>
        <div class="panel-header">
          <span>{{ t('pages.dashboard.operationalTodo') }}</span>
          <div class="panel-header-actions">
            <el-tag :type="operationalStatusType(operational?.status)" effect="plain">{{ operationalStatusLabel(operational?.status) }}</el-tag>
            <el-link type="primary" @click="router.push('/system-health')">{{ t('pages.dashboard.viewOperationalHealth') }}</el-link>
          </div>
        </div>
      </template>
      <template v-if="operational">
        <div class="operational-brief">
          <div>
            <span class="operational-brief-kicker">{{ operationalStatusLabel(operational.status) }}</span>
            <strong>{{ operationalIssueSummary }}</strong>
          </div>
        </div>
        <div v-if="operationalMetrics.length" class="operational-metric-grid">
          <div v-for="metric in operationalMetrics" :key="metric.key" class="operational-metric" :class="`is-${metric.tone}`">
            <span>{{ metric.label }}</span>
            <strong>{{ metric.value }}</strong>
          </div>
        </div>
        <div v-if="operational.issues.length" class="operational-issue-list">
          <div v-for="issue in operational.issues.slice(0, 4)" :key="issue.key" class="operational-issue-row">
            <div class="operational-issue-content">
              <el-tag :type="issue.severity === 'critical' ? 'danger' : 'warning'" size="small" effect="plain">{{ operationalIssueSeverityLabel(issue.severity) }}</el-tag>
              <div>
                <strong>{{ issue.title }}</strong>
                <span>{{ issue.detail }}</span>
              </div>
            </div>
            <el-button v-if="issue.action" link type="primary" @click="openOperationalAction(issue.action)">{{ t('pages.dashboard.handleOperationalIssue') }}</el-button>
          </div>
          <el-link v-if="operational.issues.length > 4" class="operational-more-link" type="primary" @click="router.push('/system-health')">
            {{ t('pages.dashboard.moreOperationalIssues', { count: operational.issues.length - 4 }) }}
          </el-link>
        </div>
        <el-empty v-else :description="t('pages.dashboard.noOperationalIssues')" :image-size="54" />
      </template>
      <el-skeleton v-else :rows="3" animated />
    </el-card>

    <div class="section-label">{{ t('pages.dashboard.targetMonitor') }}</div>
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

    <div class="section-label">{{ t('pages.dashboard.ipoRadar') }}</div>
    <div class="kpi-grid ipo-kpi-grid">
      <el-card v-for="item in ipoMetrics" :key="item.label" shadow="never" class="kpi-card">
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
            <span>{{ t('pages.dashboard.ipoRadar') }}</span>
            <div class="panel-header-actions">
              <el-tag v-if="latestIpoSync" :type="syncStatusType(latestIpoSync.status)" effect="plain">{{ triggerLabel(latestIpoSync.trigger) }}</el-tag>
              <el-link type="primary" @click="$router.push('/ipo-radar')">{{ t('common.viewAll') }}</el-link>
            </div>
          </div>
        </template>
        <div class="ipo-summary-row">
          <div>
            <span>{{ t('pages.dashboard.ipoTotal') }}</span>
            <strong>{{ ipoFilingTotal }}</strong>
          </div>
          <div>
            <span>{{ t('pages.dashboard.ipoLastChecked') }}</span>
            <strong>{{ latestIpoSync ? formatDateTime(latestIpoSync.started_at) : '-' }}</strong>
          </div>
          <div>
            <span>{{ t('pages.dashboard.ipoLastNew') }}</span>
            <strong>{{ latestIpoSync?.new_filings ?? 0 }}</strong>
          </div>
        </div>
        <el-table :data="recentIpoFilings" v-loading="loading" border>
          <el-table-column prop="filing_type" :label="t('common.type')" width="100">
            <template #default="{ row }"><el-tag type="warning" effect="plain">{{ row.filing_type }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="company_name" :label="t('common.company')" min-width="220" show-overflow-tooltip />
          <el-table-column prop="cik" label="CIK" width="130" />
          <el-table-column prop="filing_date" :label="t('common.filingDate')" width="130">
            <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
          </el-table-column>
          <el-table-column prop="accepted_at" :label="t('pages.dashboard.secAcceptedAt')" width="170">
            <template #default="{ row }">{{ formatDateTime(row.accepted_at) }}</template>
          </el-table-column>
          <el-table-column prop="title" :label="t('common.title')" min-width="260">
            <template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">{{ row.title || row.company_name }}</el-link></template>
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
        <div v-if="latestFilingSync" class="status-block">
          <el-tag :type="syncStatusType(latestFilingSync.status)" effect="plain">{{ syncStatusLabel(latestFilingSync.status) }}</el-tag>
          <strong>{{ t('pages.dashboard.newFilings', { count: latestFilingSync.new_filings }) }}</strong>
          <span>{{ t('pages.dashboard.syncSummary', { targets: latestFilingSync.targets_checked, failed: latestFilingSync.failed_targets }) }}</span>
          <span>{{ t('pages.dashboard.startedAt', { time: formatDateTime(latestFilingSync.started_at) }) }}</span>
          <span>{{ t('pages.dashboard.finishedAt', { time: formatDateTime(latestFilingSync.finished_at) }) }}</span>
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
          <el-table-column prop="source" :label="t('pages.notificationLogs.source')" width="120">
            <template #default="{ row }">{{ notificationSourceLabel(row.source) }}</template>
          </el-table-column>
          <el-table-column prop="item_count" :label="t('pages.notificationLogs.totalCount')" width="90" align="right" />
          <el-table-column prop="sent_count" :label="t('pages.notificationLogs.sentCount')" width="90" align="right" />
          <el-table-column prop="suppressed_count" :label="t('pages.notificationLogs.suppressedCount')" width="90" align="right" />
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
import { useRouter } from 'vue-router'
import { Aim, Bell, DataAnalysis, Document, TrendCharts } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, Filing, IPOCompany, IPOFiling, IPORadarHealth, IPORadarRefreshResult, NotificationBatch, OperationalReport, PageResult, SyncRun, SystemConfig, TaskConfig, WatchTarget } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const router = useRouter()
const loading = ref(false)
const refreshing = ref(false)
const refreshingIpo = ref(false)
const targetTotal = ref(0)
const enabledTargetTotal = ref(0)
const filingTotal = ref(0)
const notificationTotal = ref(0)
const syncTotal = ref(0)
const ipoFilingTotal = ref(0)
const ipoCompanies = ref<IPOCompany[]>([])
const recentFilings = ref<Filing[]>([])
const recentIpoFilings = ref<IPOFiling[]>([])
const dashboardFilings = ref<Filing[]>([])
const recentNotifications = ref<NotificationBatch[]>([])
const ipoHealth = ref<IPORadarHealth | null>(null)
const operational = ref<OperationalReport | null>(null)
const dashboardLoadWarnings = ref<string[]>([])

function notificationSourceLabel(value: string) {
  if (value === 'ipo') return t('pages.notificationLogs.sources.ipo')
  if (value === 'ipo_offering') return t('pages.notificationLogs.sources.ipoOffering')
  return t('pages.notificationLogs.sources.filing')
}
const latestFilingSync = ref<SyncRun | null>(null)
const latestIpoSync = ref<SyncRun | null>(null)
const successfulTargets = ref(0)
const failedTargets = ref(0)
const failedTargetItems = ref<WatchTarget[]>([])
const telegramEnabled = ref(false)
const schedulerEnabled = ref(false)
const onboardingVisible = ref(false)
const onboardingActiveStep = computed(() => {
  if (targetTotal.value === 0) return 1
  if (!telegramEnabled.value) return 2
  if (!latestFilingSync.value) return 3
  return 4
})

const metrics = computed(() => [
  { label: t('nav.targets'), value: targetTotal.value, hint: t('pages.dashboard.enabledTargets') + ` ${enabledTargetTotal.value}`, icon: Aim },
  { label: t('nav.filings'), value: filingTotal.value, hint: t('common.filings'), icon: Document },
  { label: t('nav.syncRuns'), value: syncTotal.value, hint: latestFilingSync.value ? syncStatusLabel(latestFilingSync.value.status) : t('pages.dashboard.noSyncRuns'), icon: DataAnalysis },
  { label: t('nav.notificationLogs'), value: notificationTotal.value, hint: 'Telegram', icon: Bell }
])

const ipoStatusCounts = computed(() => {
  const counts = new Map<string, number>()
  for (const company of ipoCompanies.value) {
    counts.set(company.status, (counts.get(company.status) || 0) + 1)
  }
  return counts
})

const ipoInProgressTotal = computed(() => {
  const activeStatuses = new Set(['new', 'updating', 'effective', 'stale'])
  return ipoCompanies.value.filter((company) => activeStatuses.has(company.status)).length
})

const ipoMetrics = computed(() => [
  { label: t('pages.dashboard.ipoInProgress'), value: ipoInProgressTotal.value, hint: t('pages.dashboard.ipoInProgressHint'), icon: TrendCharts },
  { label: t('pages.ipoRadar.statuses.new'), value: ipoStatusCounts.value.get('new') || 0, hint: t('pages.dashboard.ipoNewHint'), icon: Aim },
  { label: t('pages.ipoRadar.statuses.updating'), value: ipoStatusCounts.value.get('updating') || 0, hint: t('pages.dashboard.ipoUpdatingHint'), icon: DataAnalysis },
  { label: t('pages.ipoRadar.statuses.effective'), value: ipoStatusCounts.value.get('effective') || 0, hint: t('pages.dashboard.ipoEffectiveHint'), icon: TrendCharts },
  { label: t('pages.dashboard.ipoTotal'), value: ipoFilingTotal.value, hint: t('pages.dashboard.ipoStoredHint'), icon: Document },
  { label: t('pages.dashboard.ipoLastNew'), value: latestIpoSync.value?.new_filings ?? 0, hint: latestIpoSync.value ? formatDateTime(latestIpoSync.value.started_at) : t('pages.dashboard.noIpoRuns'), icon: Bell }
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
  if ((ipoHealth.value?.failed_notification_batches || 0) > 0 || (ipoHealth.value?.dead_letter_batches || 0) > 0) {
    alerts.push({
      title: t('pages.dashboard.ipoNotificationFailedAlertTitle', { failed: ipoHealth.value?.failed_notification_batches || 0, dead: ipoHealth.value?.dead_letter_batches || 0 }),
      description: t('pages.dashboard.ipoNotificationFailedAlertDescription'),
      type: 'error'
    })
  }
  if (!latestFilingSync.value) {
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

const operationalIssueSummary = computed(() => {
  const count = operational.value?.issues.length || 0
  return count > 0
    ? t('pages.dashboard.operationalIssueSummary', { count })
    : t('pages.dashboard.noOperationalIssues')
})

const operationalMetrics = computed(() => {
  if (!operational.value) return []
  const report = operational.value
  return [
    { key: 'retryable', label: t('pages.dashboard.retryableTargets'), value: report.retryable_targets, tone: 'danger' },
    { key: 'profiles', label: t('pages.dashboard.profileRetryDue'), value: report.company_profile_retry_due, tone: 'warning' },
    { key: 'market', label: t('pages.dashboard.marketRecovery'), value: report.market_price_recovery, tone: 'warning' },
    { key: 'providers', label: t('pages.dashboard.providerWarnings'), value: report.provider_warnings, tone: 'warning' },
  ].filter((item) => item.value > 0)
})

const latestSyncAgeHours = computed(() => {
  if (!latestFilingSync.value?.started_at) return 0
  const started = new Date(latestFilingSync.value.started_at)
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
  const success = recentNotifications.value.filter((item) => item.status === 'sent' || item.status === 'suppressed').length
  return Math.round((success / recentNotifications.value.length) * 100)
})

const notificationRateType = computed(() => {
  if (notificationSuccessRate.value >= 90) return 'success'
  if (notificationSuccessRate.value >= 70) return 'warning'
  return 'danger'
})

async function load() {
  loading.value = true
  dashboardLoadWarnings.value = []
  // The dashboard's operational card is intentionally non-blocking: a local
  // diagnostics failure must not hide targets, SEC filings, or IPO activity.
  void loadOperationalReport()
  try {
    const [targets, enabledTargets, filings, ipoFilings, ipoCompanyRes, ipoHealthRes, syncRuns, notifications, telegramConfigs, taskConfigs, uiConfigs] = await Promise.all([
      safeDashboardRequest(t('nav.targets'), apiClient.get<ApiResponse<PageResult<WatchTarget>>>('/watch-targets', { params: { page: 1, page_size: 10 } })),
      safeDashboardRequest(t('pages.dashboard.targetHealth'), apiClient.get<ApiResponse<PageResult<WatchTarget>>>('/watch-targets', { params: { status: 'enabled', page: 1, page_size: 200 } })),
      safeDashboardRequest(t('nav.filings'), apiClient.get<ApiResponse<PageResult<Filing>>>('/filings', { params: { page: 1, page_size: 100, sort_by: 'pulled_at', sort_order: 'desc' } })),
      safeDashboardRequest(t('pages.dashboard.ipoRadar'), apiClient.get<ApiResponse<PageResult<IPOFiling>>>('/ipo-filings', { params: { page: 1, page_size: 6 } })),
      safeDashboardRequest(t('pages.dashboard.ipoRadar'), apiClient.get<ApiResponse<PageResult<IPOCompany>>>('/ipo-companies', { params: { page: 1, page_size: 500 } })),
      safeDashboardRequest(t('pages.dashboard.ipoRadar'), apiClient.get<ApiResponse<IPORadarHealth>>('/ipo-health')),
      safeDashboardRequest(t('pages.dashboard.syncStatus'), apiClient.get<ApiResponse<PageResult<SyncRun>>>('/sync-runs', { params: { page: 1, page_size: 20 } })),
      safeDashboardRequest(t('pages.dashboard.recentNotifications'), apiClient.get<ApiResponse<PageResult<NotificationBatch>>>('/notification-batches', { params: { page: 1, page_size: 5 } })),
      safeDashboardRequest('Telegram', apiClient.get<ApiResponse<SystemConfig[]>>('/telegram/config')),
      safeDashboardRequest(t('pages.scheduler.title'), apiClient.get<ApiResponse<TaskConfig[]>>('/task-configs')),
      safeDashboardRequest(t('pages.configs.title'), apiClient.get<ApiResponse<SystemConfig[]>>('/system-configs', { params: { category: 'ui' } }))
    ])
    if (targets) targetTotal.value = targets.total
    if (enabledTargets) {
      enabledTargetTotal.value = enabledTargets.total
      successfulTargets.value = enabledTargets.items.filter((item) => item.last_sync_status === 'success').length
      failedTargets.value = enabledTargets.items.filter((item) => item.last_sync_status === 'failed').length
      failedTargetItems.value = enabledTargets.items.filter((item) => item.last_sync_status === 'failed').slice(0, 5)
    }
    if (filings) {
      filingTotal.value = filings.total
      dashboardFilings.value = filings.items
      recentFilings.value = filings.items.slice(0, 6)
    }
    if (ipoFilings) {
      ipoFilingTotal.value = ipoFilings.total
      recentIpoFilings.value = ipoFilings.items
    }
    if (ipoCompanyRes) ipoCompanies.value = ipoCompanyRes.items
    if (ipoHealthRes) ipoHealth.value = ipoHealthRes
    if (syncRuns) {
      syncTotal.value = syncRuns.total
      latestFilingSync.value = syncRuns.items.find((item) => ['manual', 'scheduler', 'target'].includes(item.trigger)) || null
      latestIpoSync.value = syncRuns.items.find((item) => item.trigger === 'ipo_manual' || item.trigger === 'ipo_scheduler') || null
    }
    if (notifications) {
      notificationTotal.value = notifications.total
      recentNotifications.value = notifications.items
    }
    if (telegramConfigs) telegramEnabled.value = configValue(telegramConfigs, 'telegram.enabled') === 'true'
    if (taskConfigs) schedulerEnabled.value = taskConfigs.some((item) => item.enabled)
    if (uiConfigs) onboardingVisible.value = configValue(uiConfigs, 'ui.onboarding_completed') !== 'true'
  } finally {
    loading.value = false
  }
}

async function safeDashboardRequest<T>(section: string, request: Promise<{ data: ApiResponse<T> }>): Promise<T | null> {
  try {
    return (await request).data.data
  } catch {
    if (!dashboardLoadWarnings.value.includes(section)) dashboardLoadWarnings.value.push(section)
    return null
  }
}

async function loadOperationalReport() {
  try {
    const res = await apiClient.get<ApiResponse<OperationalReport>>('/operational-health')
    operational.value = res.data.data
  } catch {
    // Keep the most recently rendered report. Operational diagnostics are
    // supplemental and should never break the primary dashboard refresh.
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
  } catch (error: any) {
    ElMessage.error(error?.response?.data?.message || error?.message || t('messages.taskTriggerFailed'))
  } finally {
    refreshing.value = false
  }
}

async function refreshIpoFilings() {
  refreshingIpo.value = true
  try {
    const res = await apiClient.post<ApiResponse<IPORadarRefreshResult>>('/ipo-filings/refresh', null, { timeout: 120000 })
    ElMessage.success(t('messages.ipoRefreshDone', { count: res.data.data.new_filings, notified: res.data.data.notified }))
    await load()
  } catch (error: any) {
    ElMessage.error(error?.response?.data?.message || error?.message || t('messages.taskTriggerFailed'))
  } finally {
    refreshingIpo.value = false
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

function syncStatusLabel(status?: string) {
  if (status === 'success') return t('status.success')
  if (status === 'partial') return t('status.partial')
  if (status === 'failed') return t('status.failed')
  if (status === 'running') return t('status.running')
  return status || '-'
}

function triggerLabel(trigger?: string) {
  if (trigger === 'ipo_manual') return t('pages.syncRuns.triggers.ipoManual')
  if (trigger === 'ipo_scheduler') return t('pages.syncRuns.triggers.ipoScheduler')
  if (trigger === 'manual') return t('pages.syncRuns.triggers.manual')
  if (trigger === 'scheduler') return t('pages.syncRuns.triggers.scheduler')
  if (trigger === 'target') return t('pages.syncRuns.triggers.target')
  return trigger || '-'
}

function notificationStatusType(status?: string) {
	if (status === 'sent') return 'success'
	if (status === 'failed') return 'danger'
	if (status === 'suppressed') return 'warning'
	return 'info'
}

function notificationStatusLabel(status?: string) {
	if (status === 'sent' || status === 'failed' || status === 'suppressed') return t(`pages.notificationLogs.statuses.${status}`)
	return status || '-'
}

function operationalStatusType(status?: string) {
  if (status === 'ok') return 'success'
  if (status === 'critical') return 'danger'
  return 'warning'
}

function operationalStatusLabel(status?: string) {
  if (status === 'ok') return t('pages.dashboard.operationalHealthy')
  if (status === 'critical') return t('pages.dashboard.operationalCritical')
  return t('pages.dashboard.operationalWarning')
}

function operationalIssueSeverityLabel(severity?: string) {
  return severity === 'critical' ? t('pages.dashboard.operationalCritical') : t('pages.dashboard.operationalWarning')
}

function openOperationalAction(action: string) {
  const routes: Record<string, string> = {
    scheduler: '/scheduler',
    'sync-runs': '/sync-runs',
    'discovery-logs': '/discovery-logs',
    'system-health': '/system-health',
  }
  router.push(routes[action] || '/system-health')
}

onMounted(load)
</script>

<style scoped>
.dashboard-operations-panel :deep(.el-card__body) {
  padding-top: 14px;
}

.operational-brief {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 14px;
}

.operational-brief > div {
  display: flex;
  align-items: baseline;
  gap: 9px;
  min-width: 0;
}

.operational-brief-kicker {
  flex: none;
  color: #64748b;
  font-size: 13px;
}

.operational-brief strong {
  overflow: hidden;
  color: #1f2937;
  font-size: 16px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.operational-metric-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 10px;
  margin-bottom: 14px;
}

.operational-metric {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 12px;
  border: 1px solid #e8edf5;
  border-radius: 8px;
  background: #fafcff;
}

.operational-metric span {
  color: #64748b;
  font-size: 12px;
}

.operational-metric strong {
  color: #334155;
  font-size: 20px;
  line-height: 1;
}

.operational-metric.is-danger strong { color: #dc2626; }
.operational-metric.is-warning strong { color: #d97706; }

.operational-issue-list {
  border-top: 1px solid #eef2f7;
}

.operational-issue-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  min-height: 56px;
  border-bottom: 1px solid #eef2f7;
}

.operational-issue-content {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.operational-issue-content > div {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.operational-issue-content strong {
  color: #1f2937;
  font-size: 14px;
}

.operational-issue-content span {
  overflow: hidden;
  color: #64748b;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.operational-more-link {
  margin-top: 10px;
  font-size: 13px;
}

@media (max-width: 720px) {
  .operational-brief,
  .operational-brief > div {
    align-items: flex-start;
    flex-direction: column;
    gap: 4px;
  }

  .operational-issue-row { align-items: flex-start; padding: 10px 0; }
  .operational-issue-content { align-items: flex-start; }
  .operational-issue-content span { white-space: normal; }
}
</style>
