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
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, CleanupPreview, SystemConfig } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const loading = ref(false)
const saving = ref(false)
const previewing = ref(false)
const cleaning = ref(false)
const cleanupPreview = ref<CleanupPreview | null>(null)

const secForm = reactive({ initial_fetch_days: 30, sync_window_days: 30, max_fetch_count: 300, fetch_full_history: false })
const systemForm = reactive({ data_retention_days: 30, storage_by_day: false })

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
      { key: 'system.storage_by_day', value: String(systemForm.storage_by_day), value_type: 'bool', category: 'system', encrypted: false }
    ])
    ElMessage.success(t('messages.configSaved'))
    cleanupPreview.value = null
    await load()
  } finally {
    saving.value = false
  }
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
