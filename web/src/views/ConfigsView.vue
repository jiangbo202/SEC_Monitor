<template>
  <section class="page">
    <div class="page-header">
      <h1>{{ t('pages.configs.title') }}</h1>
      <div>
        <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ t('pages.configs.save') }}</el-button>
      </div>
    </div>

    <div class="config-grid">
      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.interfaceSettings') }}</span>
            <el-tag effect="plain">{{ localeLabel(uiForm.default_locale) }}</el-tag>
          </div>
        </template>
        <el-form :model="uiForm" label-width="150px">
          <el-form-item :label="t('pages.configs.defaultLanguage')">
            <el-select v-model="uiForm.default_locale" style="width: 180px">
              <el-option label="中文" value="zh-CN" />
              <el-option label="English" value="en-US" />
            </el-select>
          </el-form-item>
        </el-form>
        <el-alert :title="t('pages.configs.defaultLanguageHint')" type="info" :closable="false" show-icon />
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.notificationRules') }}</span>
          </div>
        </template>
        <el-form :model="notificationForm" label-width="150px">
          <el-form-item :label="t('pages.configs.importantOnly')">
            <el-switch v-model="notificationForm.important_only" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.notifyFilingTypes')">
            <el-input v-model="notificationForm.filing_types" :placeholder="t('pages.configs.notifyFilingTypesPlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.notifyKeywords')">
            <el-input v-model="notificationForm.keywords" :placeholder="t('pages.configs.notifyKeywordsPlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.quietHoursEnabled')">
            <el-switch v-model="notificationForm.quiet_hours_enabled" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.quietHoursStart')">
            <el-time-picker v-model="notificationForm.quiet_hours_start" format="HH:mm" value-format="HH:mm" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.quietHoursEnd')">
            <el-time-picker v-model="notificationForm.quiet_hours_end" format="HH:mm" value-format="HH:mm" />
          </el-form-item>
        </el-form>
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.candidateNotification') }}</span>
            <el-tag effect="plain">{{ candidateNotificationSummary }}</el-tag>
          </div>
        </template>
        <el-form :model="candidateNotificationForm" label-width="150px">
          <el-form-item :label="t('pages.configs.candidateNotificationEnabled')">
            <el-switch v-model="candidateNotificationForm.enabled" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.candidateNotifyA')">
            <el-switch v-model="candidateNotificationForm.notify_a" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.candidateNotifyB')">
            <el-switch v-model="candidateNotificationForm.notify_b" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.candidateSendTime')">
            <el-time-picker v-model="candidateNotificationForm.send_time" format="HH:mm" value-format="HH:mm" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.candidateMaxPerGrade')">
            <el-input-number v-model="candidateNotificationForm.max_per_grade" :min="1" :max="20" />
          </el-form-item>
        </el-form>
        <el-alert :title="t('pages.configs.candidateNotificationHint')" type="info" :closable="false" show-icon />
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.schedulerSettings') }}</span>
            <el-tag effect="plain">{{ schedulerForm.timezone }}</el-tag>
          </div>
        </template>
        <el-form :model="schedulerForm" label-width="150px">
          <el-form-item :label="t('pages.configs.schedulerTimezone')">
            <el-select
              v-model="schedulerForm.timezone"
              filterable
              allow-create
              default-first-option
              style="width: 240px"
            >
              <el-option v-for="item in timezoneOptions" :key="item" :label="item" :value="item" />
            </el-select>
          </el-form-item>
        </el-form>
        <el-alert :title="t('pages.configs.schedulerTimezoneHint')" type="info" :closable="false" show-icon />
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.discoveryDatasource') }}</span>
            <el-tag effect="plain">{{ discoveryDatasourceSummary }}</el-tag>
          </div>
        </template>
        <el-form :model="discoveryForm" label-width="150px">
          <el-form-item :label="t('pages.configs.discoveryPriceProvider')">
            <el-select v-model="discoveryForm.price_provider" style="width: 220px">
              <el-option :label="t('pages.configs.discoveryProviderAuto')" value="" />
              <el-option label="Tiingo" value="tiingo" />
              <el-option label="Tiingo → Twelve Data → Yahoo" value="tiingo,twelvedata,yahoo" />
              <el-option label="Tiingo → Yahoo" value="tiingo,yahoo" />
              <el-option label="Twelve Data" value="twelvedata" />
              <el-option label="Stooq → Tiingo → Yahoo" value="stooq,tiingo,yahoo" />
              <el-option label="Yahoo" value="yahoo" />
              <el-option label="Stooq" value="stooq" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('pages.configs.stooqUrls')">
            <el-input
              v-model="discoveryForm.stooq_urls"
              type="textarea"
              :rows="2"
              :placeholder="t('pages.configs.stooqUrlsPlaceholder')"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.tiingoApiToken')">
            <el-input
              v-model="discoveryForm.tiingo_api_token"
              show-password
              :placeholder="t('pages.configs.tiingoApiTokenPlaceholder')"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.tiingoApiTokens')">
            <el-input
              v-model="discoveryForm.tiingo_api_tokens"
              show-password
              :placeholder="t('pages.configs.tiingoApiTokensPlaceholder')"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.tiingoRequestBudget')">
            <el-input-number
              v-model="discoveryForm.tiingo_request_budget"
              :min="0"
              :step="5"
              controls-position="right"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.tiingoBaseUrl')">
            <el-input v-model="discoveryForm.tiingo_base_url" placeholder="https://api.tiingo.com" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.twelveDataApiKey')">
            <el-input
              v-model="discoveryForm.twelve_data_api_key"
              show-password
              :placeholder="t('pages.configs.twelveDataApiKeyPlaceholder')"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.twelveDataRequestBudget')">
            <el-input-number
              v-model="discoveryForm.twelve_data_request_budget"
              :min="0"
              :step="50"
              controls-position="right"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.twelveDataRequestIntervalMs')">
            <el-input-number
              v-model="discoveryForm.twelve_data_request_interval_ms"
              :min="1000"
              :step="500"
              controls-position="right"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.twelveDataBaseUrl')">
            <el-input v-model="discoveryForm.twelve_data_base_url" placeholder="https://api.twelvedata.com" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.yahooRequestBudget')">
            <el-input-number
              v-model="discoveryForm.yahoo_request_budget"
              :min="0"
              :step="5"
              controls-position="right"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.yahooBaseUrl')">
            <el-input v-model="discoveryForm.yahoo_base_url" placeholder="https://query1.finance.yahoo.com" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.minPublishCoveragePct')">
            <el-input-number
              v-model="discoveryForm.min_publish_coverage_pct"
              :min="0"
              :max="100"
              :step="5"
              controls-position="right"
            />
          </el-form-item>
        </el-form>
        <el-alert :title="t('pages.configs.discoveryDatasourceHint')" type="info" :closable="false" show-icon />
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.ipoRadar') }}</span>
          </div>
        </template>
        <el-form :model="ipoForm" label-width="150px">
          <el-form-item :label="t('pages.configs.ipoEnabled')">
            <el-switch v-model="ipoForm.enabled" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.ipoFormTypes')">
            <el-input v-model="ipoForm.form_types" :placeholder="t('pages.configs.ipoFormTypesPlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.ipoLookbackDays')">
            <el-input-number v-model="ipoForm.lookback_days" :min="1" :max="365" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.ipoMaxResults')">
            <el-input-number v-model="ipoForm.max_results" :min="1" :max="100" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.ipoNotifyEnabled')">
            <el-switch v-model="ipoForm.notify_enabled" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.ipoNotifyFormTypes')">
            <el-input v-model="ipoForm.notify_form_types" :placeholder="t('pages.configs.ipoNotifyFormTypesPlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.ipoKeywords')">
            <el-input v-model="ipoForm.keywords" :placeholder="t('pages.configs.ipoKeywordsPlaceholder')" />
          </el-form-item>
        </el-form>
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.secPolicy') }}</span>
            <el-tag effect="plain">{{ secPolicySummary }}</el-tag>
          </div>
        </template>
        <el-form :model="secForm" label-width="150px">
          <el-form-item :label="t('pages.configs.syncWindowDays')">
            <el-input-number v-model="secForm.sync_window_days" :min="0" :max="3650" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.initialFetchDays')">
            <el-input-number v-model="secForm.initial_fetch_days" :min="1" :max="3650" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.maxFetchCount')">
            <el-input-number v-model="secForm.max_fetch_count" :min="0" :max="5000" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.fetchFullHistory')">
            <el-switch v-model="secForm.fetch_full_history" />
          </el-form-item>
        </el-form>
        <div class="config-risk-list">
          <el-alert
            v-for="item in secRiskHints"
            :key="item.title"
            :title="item.title"
            :description="item.description"
            :type="item.type"
            :closable="false"
            show-icon
          />
        </div>
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.retentionCleanup') }}</span>
            <el-tag effect="plain">{{ retentionPolicySummary }}</el-tag>
          </div>
        </template>
        <el-form :model="systemForm" label-width="150px">
          <el-form-item :label="t('pages.configs.retentionDays')">
            <el-input-number v-model="systemForm.data_retention_days" :min="1" :max="3650" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.storageByDay')">
            <el-switch v-model="systemForm.storage_by_day" />
          </el-form-item>
        </el-form>
        <div class="config-risk-list">
          <el-alert
            v-for="item in systemRiskHints"
            :key="item.title"
            :title="item.title"
            :description="item.description"
            :type="item.type"
            :closable="false"
            show-icon
          />
        </div>
        <div class="cleanup-actions">
          <el-button :loading="previewing" @click="loadCleanupPreview">{{ t('pages.configs.cleanupPreview') }}</el-button>
          <el-button type="danger" :disabled="!cleanupPreview || cleanupPreview.delete_count === 0" :loading="cleaning" @click="cleanup">{{ t('pages.configs.cleanupExecute') }}</el-button>
        </div>
        <el-descriptions v-if="cleanupPreview" class="cleanup-preview" :column="1" border>
          <el-descriptions-item :label="t('pages.configs.retentionDays')">{{ cleanupPreview.retention_days }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.configs.cleanupCutoff')">{{ formatDateTime(cleanupPreview.cutoff) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.configs.expectedDelete')">{{ cleanupPreview.delete_count }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.configs.oldestSync')">{{ formatDateTime(cleanupPreview.oldest_pulled_at) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.configs.newestSync')">{{ formatDateTime(cleanupPreview.newest_pulled_at) }}</el-descriptions-item>
        </el-descriptions>
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.exportBackup') }}</span>
          </div>
        </template>
        <div class="export-actions">
          <el-button @click="download('/api/exports/filings.csv')">{{ t('pages.configs.exportFilings') }}</el-button>
          <el-button @click="download('/api/exports/ipo-companies.csv')">{{ t('pages.configs.exportIPOCompanies') }}</el-button>
          <el-button @click="download('/api/exports/ipo-filings.csv')">{{ t('pages.configs.exportIPOFilings') }}</el-button>
          <el-button @click="download('/api/exports/watch-targets.csv')">{{ t('pages.configs.exportTargets') }}</el-button>
          <el-button @click="download('/api/exports/configs.json')">{{ t('pages.configs.exportConfigs') }}</el-button>
          <el-button type="primary" @click="download('/api/exports/backup.json')">{{ t('pages.configs.exportAll') }}</el-button>
        </div>
      </el-card>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, CleanupPreview, SystemConfig } from '@/api/types'
import { type Locale, useI18n } from '@/i18n'

const { store, t } = useI18n()
const loading = ref(false)
const saving = ref(false)
const previewing = ref(false)
const cleaning = ref(false)
const cleanupPreview = ref<CleanupPreview | null>(null)

const secForm = reactive({ initial_fetch_days: 30, sync_window_days: 30, max_fetch_count: 300, fetch_full_history: false })
const systemForm = reactive({ data_retention_days: 30, storage_by_day: false })
const uiForm = reactive<{ default_locale: Locale }>({ default_locale: 'zh-CN' })
const notificationForm = reactive({
  important_only: false,
  filing_types: '',
  keywords: '',
  quiet_hours_enabled: false,
  quiet_hours_start: '22:00',
  quiet_hours_end: '08:00'
})
const candidateNotificationForm = reactive({
  enabled: false,
  notify_a: false,
  notify_b: false,
  send_time: '09:30',
  max_per_grade: 5
})
const schedulerForm = reactive({ timezone: 'UTC' })
const timezoneOptions = ['Asia/Shanghai', 'UTC', 'America/New_York', 'America/Los_Angeles']
const discoveryForm = reactive({
  price_provider: '',
  stooq_urls: '',
  tiingo_api_token: '',
  tiingo_api_tokens: '',
  tiingo_request_budget: 45,
  tiingo_base_url: 'https://api.tiingo.com',
  twelve_data_api_key: '',
  twelve_data_request_budget: 700,
  twelve_data_request_interval_ms: 8000,
  twelve_data_base_url: 'https://api.twelvedata.com',
  yahoo_request_budget: 45,
  yahoo_base_url: 'https://query1.finance.yahoo.com',
  min_publish_coverage_pct: 20
})
const ipoForm = reactive({
  enabled: true,
  form_types: 'S-1,S-1/A,F-1,F-1/A,S-1MEF',
  lookback_days: 7,
  max_results: 100,
  notify_enabled: true,
  notify_form_types: '',
  keywords: ''
})

const secRiskHints = computed(() => {
  const hints: Array<{ title: string, description: string, type: 'warning' | 'info' }> = []
  if (secForm.fetch_full_history) {
    hints.push({ title: t('pages.configs.fullHistoryTitle'), description: t('pages.configs.fullHistoryDescription'), type: 'warning' })
  }
  if (secForm.max_fetch_count === 0) {
    hints.push({ title: t('pages.configs.unlimitedMaxTitle'), description: t('pages.configs.unlimitedMaxDescription'), type: 'warning' })
  } else if (secForm.max_fetch_count >= 1000) {
    hints.push({ title: t('pages.configs.highMaxTitle'), description: t('pages.configs.highMaxDescription'), type: 'info' })
  }
  if (secForm.sync_window_days === 0) {
    hints.push({ title: t('pages.configs.unlimitedWindowTitle'), description: t('pages.configs.unlimitedWindowDescription'), type: 'warning' })
  } else if (secForm.sync_window_days > 365) {
    hints.push({ title: t('pages.configs.longWindowTitle'), description: t('pages.configs.longWindowDescription'), type: 'info' })
  }
  if (secForm.initial_fetch_days > 365) {
    hints.push({ title: t('pages.configs.longInitialTitle'), description: t('pages.configs.longInitialDescription'), type: 'info' })
  }
  return hints
})

const secPolicySummary = computed(() => {
  const syncWindowText = secForm.sync_window_days === 0 ? t('pages.configs.summarySyncUnlimited') : t('pages.configs.summarySyncDays', { days: secForm.sync_window_days })
  const initialWindowText = secForm.fetch_full_history ? t('pages.configs.summaryInitialFull') : t('pages.configs.summaryInitialDays', { days: secForm.initial_fetch_days })
  const maxText = secForm.max_fetch_count === 0 ? t('pages.configs.summaryMaxUnlimited') : t('pages.configs.summaryMaxCount', { count: secForm.max_fetch_count })
  return t('pages.configs.summarySecPolicy', { syncWindow: syncWindowText, initialWindow: initialWindowText, max: maxText })
})

const retentionPolicySummary = computed(() => {
  const storage = systemForm.storage_by_day ? t('pages.configs.summaryStorageByDay') : t('pages.configs.summaryContinuousDb')
  return t('pages.configs.summaryRetention', { days: systemForm.data_retention_days, storage })
})

const candidateNotificationSummary = computed(() => {
  if (!candidateNotificationForm.enabled) return t('status.disabled')
  const grades = [
    candidateNotificationForm.notify_a ? 'A' : '',
    candidateNotificationForm.notify_b ? 'B' : ''
  ].filter(Boolean).join('/')
  return t('pages.configs.candidateNotificationSummary', {
    grades: grades || '-',
    time: candidateNotificationForm.send_time,
    count: candidateNotificationForm.max_per_grade
  })
})

const discoveryDatasourceSummary = computed(() => {
  if (discoveryForm.price_provider === 'tiingo') return 'Tiingo'
  if (discoveryForm.price_provider === 'tiingo,twelvedata,yahoo') return 'Tiingo → Twelve Data → Yahoo'
  if (discoveryForm.price_provider === 'tiingo,yahoo') return 'Tiingo → Yahoo'
  if (discoveryForm.price_provider === 'twelvedata') return 'Twelve Data'
  if (discoveryForm.price_provider === 'stooq,tiingo,yahoo') return 'Stooq → Tiingo → Yahoo'
  if (discoveryForm.price_provider === 'yahoo') return 'Yahoo'
  if (discoveryForm.price_provider === 'stooq') return 'Stooq'
  return t('pages.configs.discoveryProviderAuto')
})

const systemRiskHints = computed(() => {
  const hints: Array<{ title: string, description: string, type: 'warning' | 'info' }> = []
  if (systemForm.data_retention_days < 14) {
    hints.push({ title: t('pages.configs.shortRetentionTitle'), description: t('pages.configs.shortRetentionDescription'), type: 'warning' })
  }
  if (systemForm.storage_by_day) {
    hints.push({ title: t('pages.configs.byDayTitle'), description: t('pages.configs.byDayDescription'), type: 'info' })
  }
  return hints
})

function configValue(configs: SystemConfig[], key: string, fallback: string) {
  return configs.find((item) => item.config_key === key)?.config_value || fallback
}

function localeValue(value: string): Locale {
  return value === 'en-US' ? 'en-US' : 'zh-CN'
}

function localeLabel(value: Locale) {
  return value === 'en-US' ? 'English' : '中文'
}

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<SystemConfig[]>>('/system-configs')
    const configs = res.data.data
    secForm.initial_fetch_days = Number(configValue(configs, 'sec.initial_fetch_days', '30'))
    secForm.sync_window_days = Number(configValue(configs, 'sec.sync_window_days', '30'))
    secForm.max_fetch_count = Number(configValue(configs, 'sec.max_fetch_count', '300'))
    secForm.fetch_full_history = configValue(configs, 'sec.fetch_full_history', 'false') === 'true'
    systemForm.data_retention_days = Number(configValue(configs, 'system.data_retention_days', '30'))
    systemForm.storage_by_day = configValue(configs, 'system.storage_by_day', 'false') === 'true'
    uiForm.default_locale = localeValue(configValue(configs, 'ui.default_locale', 'zh-CN'))
    notificationForm.important_only = configValue(configs, 'notification.important_only', 'false') === 'true'
    notificationForm.filing_types = configValue(configs, 'notification.filing_types', '')
    notificationForm.keywords = configValue(configs, 'notification.keywords', '')
    notificationForm.quiet_hours_enabled = configValue(configs, 'notification.quiet_hours_enabled', 'false') === 'true'
    notificationForm.quiet_hours_start = configValue(configs, 'notification.quiet_hours_start', '22:00')
    notificationForm.quiet_hours_end = configValue(configs, 'notification.quiet_hours_end', '08:00')
    candidateNotificationForm.enabled = configValue(configs, 'candidate_notification.enabled', 'false') === 'true'
    candidateNotificationForm.notify_a = configValue(configs, 'candidate_notification.notify_a', 'false') === 'true'
    candidateNotificationForm.notify_b = configValue(configs, 'candidate_notification.notify_b', 'false') === 'true'
    candidateNotificationForm.send_time = configValue(configs, 'candidate_notification.send_time', '09:30')
    candidateNotificationForm.max_per_grade = Number(configValue(configs, 'candidate_notification.max_per_grade', '5'))
    schedulerForm.timezone = configValue(configs, 'scheduler.timezone', 'UTC')
    discoveryForm.price_provider = configValue(configs, 'discovery.price_provider', '')
    discoveryForm.stooq_urls = configValue(configs, 'discovery.stooq_urls', '')
    discoveryForm.tiingo_api_token = configValue(configs, 'discovery.tiingo_api_token', '')
    discoveryForm.tiingo_api_tokens = configValue(configs, 'discovery.tiingo_api_tokens', '')
    discoveryForm.tiingo_request_budget = Number(configValue(configs, 'discovery.tiingo_request_budget', '45'))
    discoveryForm.tiingo_base_url = configValue(configs, 'discovery.tiingo_base_url', 'https://api.tiingo.com')
    discoveryForm.twelve_data_api_key = configValue(configs, 'discovery.twelve_data_api_key', '')
    discoveryForm.twelve_data_request_budget = Number(configValue(configs, 'discovery.twelve_data_request_budget', '700'))
    discoveryForm.twelve_data_request_interval_ms = Number(configValue(configs, 'discovery.twelve_data_request_interval_ms', '8000'))
    discoveryForm.twelve_data_base_url = configValue(configs, 'discovery.twelve_data_base_url', 'https://api.twelvedata.com')
    discoveryForm.yahoo_request_budget = Number(configValue(configs, 'discovery.yahoo_request_budget', '45'))
    discoveryForm.yahoo_base_url = configValue(configs, 'discovery.yahoo_base_url', 'https://query1.finance.yahoo.com')
    discoveryForm.min_publish_coverage_pct = Number(configValue(configs, 'discovery.min_publish_coverage_pct', '20'))
    ipoForm.enabled = configValue(configs, 'ipo.enabled', 'true') === 'true'
    ipoForm.form_types = configValue(configs, 'ipo.form_types', 'S-1,S-1/A,F-1,F-1/A,S-1MEF')
    ipoForm.lookback_days = Number(configValue(configs, 'ipo.lookback_days', '7'))
    ipoForm.max_results = Number(configValue(configs, 'ipo.max_results', '100'))
    ipoForm.notify_enabled = configValue(configs, 'ipo.notify_enabled', 'true') === 'true'
    ipoForm.notify_form_types = configValue(configs, 'ipo.notify_form_types', '')
    ipoForm.keywords = configValue(configs, 'ipo.keywords', '')
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    await apiClient.put('/system-configs', [
      { key: 'sec.initial_fetch_days', value: String(secForm.initial_fetch_days), value_type: 'int', category: 'sec', encrypted: false },
      { key: 'sec.sync_window_days', value: String(secForm.sync_window_days), value_type: 'int', category: 'sec', encrypted: false },
      { key: 'sec.max_fetch_count', value: String(secForm.max_fetch_count), value_type: 'int', category: 'sec', encrypted: false },
      { key: 'sec.fetch_full_history', value: String(secForm.fetch_full_history), value_type: 'bool', category: 'sec', encrypted: false },
      { key: 'system.data_retention_days', value: String(systemForm.data_retention_days), value_type: 'int', category: 'system', encrypted: false },
      { key: 'system.storage_by_day', value: String(systemForm.storage_by_day), value_type: 'bool', category: 'system', encrypted: false },
      { key: 'ui.default_locale', value: uiForm.default_locale, value_type: 'string', category: 'ui', encrypted: false },
      { key: 'notification.important_only', value: String(notificationForm.important_only), value_type: 'bool', category: 'notification', encrypted: false },
      { key: 'notification.filing_types', value: notificationForm.filing_types, value_type: 'string', category: 'notification', encrypted: false },
      { key: 'notification.keywords', value: notificationForm.keywords, value_type: 'string', category: 'notification', encrypted: false },
      { key: 'notification.quiet_hours_enabled', value: String(notificationForm.quiet_hours_enabled), value_type: 'bool', category: 'notification', encrypted: false },
      { key: 'notification.quiet_hours_start', value: notificationForm.quiet_hours_start, value_type: 'string', category: 'notification', encrypted: false },
      { key: 'notification.quiet_hours_end', value: notificationForm.quiet_hours_end, value_type: 'string', category: 'notification', encrypted: false },
      { key: 'candidate_notification.enabled', value: String(candidateNotificationForm.enabled), value_type: 'bool', category: 'candidate_notification', encrypted: false },
      { key: 'candidate_notification.notify_a', value: String(candidateNotificationForm.notify_a), value_type: 'bool', category: 'candidate_notification', encrypted: false },
      { key: 'candidate_notification.notify_b', value: String(candidateNotificationForm.notify_b), value_type: 'bool', category: 'candidate_notification', encrypted: false },
      { key: 'candidate_notification.send_time', value: candidateNotificationForm.send_time, value_type: 'string', category: 'candidate_notification', encrypted: false },
      { key: 'candidate_notification.max_per_grade', value: String(candidateNotificationForm.max_per_grade), value_type: 'int', category: 'candidate_notification', encrypted: false },
      { key: 'scheduler.timezone', value: schedulerForm.timezone, value_type: 'string', category: 'scheduler', encrypted: false },
      { key: 'discovery.price_provider', value: discoveryForm.price_provider, value_type: 'string', category: 'discovery', encrypted: false },
      { key: 'discovery.stooq_urls', value: discoveryForm.stooq_urls, value_type: 'string', category: 'discovery', encrypted: false },
      { key: 'discovery.tiingo_api_token', value: discoveryForm.tiingo_api_token, value_type: 'string', category: 'discovery', encrypted: true },
      { key: 'discovery.tiingo_api_tokens', value: discoveryForm.tiingo_api_tokens, value_type: 'string', category: 'discovery', encrypted: true },
      { key: 'discovery.tiingo_request_budget', value: String(discoveryForm.tiingo_request_budget), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.tiingo_base_url', value: discoveryForm.tiingo_base_url, value_type: 'string', category: 'discovery', encrypted: false },
      { key: 'discovery.twelve_data_api_key', value: discoveryForm.twelve_data_api_key, value_type: 'string', category: 'discovery', encrypted: true },
      { key: 'discovery.twelve_data_request_budget', value: String(discoveryForm.twelve_data_request_budget), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.twelve_data_request_interval_ms', value: String(discoveryForm.twelve_data_request_interval_ms), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.twelve_data_base_url', value: discoveryForm.twelve_data_base_url, value_type: 'string', category: 'discovery', encrypted: false },
      { key: 'discovery.yahoo_request_budget', value: String(discoveryForm.yahoo_request_budget), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.yahoo_base_url', value: discoveryForm.yahoo_base_url, value_type: 'string', category: 'discovery', encrypted: false },
      { key: 'discovery.min_publish_coverage_pct', value: String(discoveryForm.min_publish_coverage_pct), value_type: 'float', category: 'discovery', encrypted: false },
      { key: 'ipo.enabled', value: String(ipoForm.enabled), value_type: 'bool', category: 'ipo', encrypted: false },
      { key: 'ipo.form_types', value: ipoForm.form_types, value_type: 'string', category: 'ipo', encrypted: false },
      { key: 'ipo.lookback_days', value: String(ipoForm.lookback_days), value_type: 'int', category: 'ipo', encrypted: false },
      { key: 'ipo.max_results', value: String(ipoForm.max_results), value_type: 'int', category: 'ipo', encrypted: false },
      { key: 'ipo.notify_enabled', value: String(ipoForm.notify_enabled), value_type: 'bool', category: 'ipo', encrypted: false },
      { key: 'ipo.notify_form_types', value: ipoForm.notify_form_types, value_type: 'string', category: 'ipo', encrypted: false },
      { key: 'ipo.keywords', value: ipoForm.keywords, value_type: 'string', category: 'ipo', encrypted: false }
    ])
    store.applyDefaultLocale(uiForm.default_locale)
    ElMessage.success(t('messages.configSaved'))
    cleanupPreview.value = null
    await load()
  } finally {
    saving.value = false
  }
}

function download(url: string) {
  window.location.href = url
}

async function loadCleanupPreview() {
  previewing.value = true
  try {
    await save()
    const res = await apiClient.get<ApiResponse<CleanupPreview>>('/filings/cleanup-preview')
    cleanupPreview.value = res.data.data
  } finally {
    previewing.value = false
  }
}

async function cleanup() {
  if (!cleanupPreview.value) return
  await ElMessageBox.confirm(t('messages.confirmCleanup', { count: cleanupPreview.value.delete_count }), t('messages.cleanupTitle'), { type: 'warning' })
  cleaning.value = true
  try {
    const res = await apiClient.post<ApiResponse<{ deleted: number }>>('/filings/cleanup')
    ElMessage.success(t('messages.deletedFilings', { count: res.data.data.deleted }))
    await loadCleanupPreview()
  } finally {
    cleaning.value = false
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
