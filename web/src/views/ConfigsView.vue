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
            <span>SEC 拉取策略</span>
            <el-tag effect="plain">{{ secPolicySummary }}</el-tag>
          </div>
        </template>
        <el-form :model="secForm" label-width="150px">
          <el-form-item label="每次同步窗口">
            <el-input-number v-model="secForm.sync_window_days" :min="0" :max="3650" />
          </el-form-item>
          <el-form-item label="首次拉取天数">
            <el-input-number v-model="secForm.initial_fetch_days" :min="1" :max="3650" />
          </el-form-item>
          <el-form-item label="最大拉取条数">
            <el-input-number v-model="secForm.max_fetch_count" :min="0" :max="5000" />
          </el-form-item>
          <el-form-item label="完整历史归档">
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
            <span>数据保留与清理</span>
            <el-tag effect="plain">{{ retentionPolicySummary }}</el-tag>
          </div>
        </template>
        <el-form :model="systemForm" label-width="150px">
          <el-form-item label="保留天数">
            <el-input-number v-model="systemForm.data_retention_days" :min="1" :max="3650" />
          </el-form-item>
          <el-form-item label="按天分目录存储">
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
          <el-button :loading="previewing" @click="loadCleanupPreview">清理预览</el-button>
          <el-button type="danger" :disabled="!cleanupPreview || cleanupPreview.delete_count === 0" :loading="cleaning" @click="cleanup">执行清理</el-button>
        </div>
        <el-descriptions v-if="cleanupPreview" class="cleanup-preview" :column="1" border>
          <el-descriptions-item label="保留天数">{{ cleanupPreview.retention_days }}</el-descriptions-item>
          <el-descriptions-item label="清理截止">{{ formatDateTime(cleanupPreview.cutoff) }}</el-descriptions-item>
          <el-descriptions-item label="预计删除">{{ cleanupPreview.delete_count }}</el-descriptions-item>
          <el-descriptions-item label="最早同步">{{ formatDateTime(cleanupPreview.oldest_pulled_at) }}</el-descriptions-item>
          <el-descriptions-item label="最晚同步">{{ formatDateTime(cleanupPreview.newest_pulled_at) }}</el-descriptions-item>
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
    hints.push({ title: '完整历史归档已开启', description: '首次同步可能拉取大量历史公告，建议配合最大拉取条数使用。', type: 'warning' })
  }
  if (secForm.max_fetch_count === 0) {
    hints.push({ title: '最大拉取条数未限制', description: 'SEC 返回较多历史数据时，同步耗时和本地数据量会明显增加。', type: 'warning' })
  } else if (secForm.max_fetch_count >= 1000) {
    hints.push({ title: '最大拉取条数较高', description: '新增热门标的时可能一次入库大量公告，建议确认这是预期行为。', type: 'info' })
  }
  if (secForm.sync_window_days === 0) {
    hints.push({ title: '每次同步窗口未限制', description: '已有标的后续同步也可能继续处理较早公告。', type: 'warning' })
  } else if (secForm.sync_window_days > 365) {
    hints.push({ title: '每次同步窗口较长', description: '周期任务会在较长时间范围内检查所有启用标的，耗时可能增加。', type: 'info' })
  }
  if (secForm.initial_fetch_days > 365) {
    hints.push({ title: '首次拉取窗口较长', description: '新标的首次同步会覆盖超过一年的公告数据。', type: 'info' })
  }
  return hints
})

const secPolicySummary = computed(() => {
  const syncWindowText = secForm.sync_window_days === 0 ? '每次不限制时间' : `每次最近 ${secForm.sync_window_days} 天`
  const initialWindowText = secForm.fetch_full_history ? '首次完整历史' : `首次最近 ${secForm.initial_fetch_days} 天`
  const maxText = secForm.max_fetch_count === 0 ? '不限制条数' : `最多 ${secForm.max_fetch_count} 条`
  return `${syncWindowText}，${initialWindowText}，${maxText}`
})

const retentionPolicySummary = computed(() => {
  const storage = systemForm.storage_by_day ? '按天分目录' : '连续数据库'
  return `公告保留 ${systemForm.data_retention_days} 天，${storage}`
})

const systemRiskHints = computed(() => {
  const hints: Array<{ title: string, description: string, type: 'warning' | 'info' }> = []
  if (systemForm.data_retention_days < 14) {
    hints.push({ title: '保留天数较短', description: '清理后较早公告将无法在本地继续检索。', type: 'warning' })
  }
  if (systemForm.storage_by_day) {
    hints.push({ title: '已启用按天分目录', description: '适合测试和归档隔离；长期运行时请确认备份和清理策略。', type: 'info' })
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
    ElMessage.success('配置已保存')
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
  await ElMessageBox.confirm(`确认删除 ${cleanupPreview.value.delete_count} 条过期公告？`, '执行数据清理', { type: 'warning' })
  cleaning.value = true
  try {
    const res = await apiClient.post<ApiResponse<{ deleted: number }>>('/filings/cleanup')
    ElMessage.success(`已删除 ${res.data.data.deleted} 条公告`)
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
