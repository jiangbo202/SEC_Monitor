<template>
  <section class="page">
    <div class="page-header">
      <div>
        <h1>{{ t('pages.systemHealth.title') }}</h1>
        <p class="page-subtitle">{{ t('pages.systemHealth.subtitle') }}</p>
      </div>
      <el-button :loading="loading" type="primary" @click="load">{{ t('pages.systemHealth.refresh') }}</el-button>
    </div>

    <div class="kpi-grid">
      <el-card shadow="never" class="kpi-card">
        <div class="metric">
          <span>{{ t('common.status') }}</span>
          <strong>{{ health?.status === 'ok' ? t('pages.systemHealth.statusOk') : t('pages.systemHealth.statusWarning') }}</strong>
        </div>
      </el-card>
      <el-card shadow="never" class="kpi-card">
        <div class="metric">
          <span>{{ t('pages.systemHealth.targets') }}</span>
          <strong>{{ health?.enabled_targets || 0 }} / {{ health?.target_total || 0 }}</strong>
        </div>
      </el-card>
      <el-card shadow="never" class="kpi-card">
        <div class="metric">
          <span>{{ t('pages.systemHealth.filings') }}</span>
          <strong>{{ health?.filing_total || 0 }}</strong>
        </div>
      </el-card>
      <el-card shadow="never" class="kpi-card">
        <div class="metric">
          <span>{{ t('pages.systemHealth.notificationFailures') }}</span>
          <strong>{{ health?.notification_failures || 0 }}</strong>
        </div>
      </el-card>
    </div>

    <div class="dashboard-grid">
      <el-card shadow="never" class="dashboard-panel">
        <template #header>{{ t('pages.systemHealth.issues') }}</template>
        <div v-if="health?.issues?.length" class="health-alert-grid">
          <el-alert
            v-for="item in health.issues"
            :key="item.message"
            :title="item.message"
            :type="item.level === 'warning' ? 'warning' : 'info'"
            :closable="false"
            show-icon
          />
        </div>
        <el-empty v-else :description="t('pages.systemHealth.noIssues')" />
      </el-card>

      <el-card shadow="never" class="dashboard-panel">
        <template #header>{{ t('pages.systemHealth.database') }}</template>
        <el-descriptions :column="1" border>
          <el-descriptions-item :label="t('common.type')">{{ health?.database_type || '-' }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.systemHealth.databaseSize')">{{ formatBytes(health?.database_size_bytes || 0) }}</el-descriptions-item>
          <el-descriptions-item label="Path">{{ health?.database_path || '-' }}</el-descriptions-item>
        </el-descriptions>
      </el-card>

      <el-card shadow="never" class="dashboard-panel">
        <template #header>{{ t('pages.systemHealth.telegram') }}</template>
        <el-tag :type="health?.telegram_enabled ? 'success' : 'info'" effect="plain">
          {{ health?.telegram_enabled ? t('pages.systemHealth.enabled') : t('pages.systemHealth.disabled') }}
        </el-tag>
      </el-card>

      <el-card shadow="never" class="dashboard-panel">
        <template #header>{{ t('pages.systemHealth.latestSync') }}</template>
        <div v-if="health?.latest_sync?.id" class="status-block">
          <el-tag effect="plain">{{ health.latest_sync.status }}</el-tag>
          <span>{{ formatDateTime(health.latest_sync.started_at) }}</span>
          <span>{{ t('pages.dashboard.newFilings', { count: health.latest_sync.new_filings }) }}</span>
        </div>
        <el-empty v-else :description="t('pages.dashboard.noSyncRuns')" />
      </el-card>

      <el-card shadow="never" class="dashboard-panel panel-wide">
        <template #header>{{ t('pages.systemHealth.secUserAgent') }}</template>
        <code>{{ health?.sec_user_agent || '-' }}</code>
      </el-card>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { apiClient } from '@/api/client'
import type { ApiResponse, SystemHealth } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const loading = ref(false)
const health = ref<SystemHealth | null>(null)

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<SystemHealth>>('/system-health')
    health.value = res.data.data
  } finally {
    loading.value = false
  }
}

function formatBytes(value: number) {
  if (!value) return '0 B'
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

onMounted(load)
</script>
