<template>
  <section class="page">
    <div class="page-header">
      <h1>{{ t('pages.syncRuns.title') }}</h1>
      <div class="page-actions">
        <el-button :disabled="!selectedRunFailedDetails.length" :loading="retryingAll" type="primary" @click="retrySelectedFailures">{{ t('pages.syncRuns.retryCurrentFailures') }}</el-button>
        <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
      </div>
    </div>
    <el-form :inline="true" :model="filters" class="toolbar">
      <el-form-item :label="t('common.status')">
        <el-select v-model="filters.status" clearable style="width: 150px">
          <el-option label="Success" value="success" />
          <el-option label="Partial" value="partial" />
          <el-option label="Failed" value="failed" />
          <el-option label="Running" value="running" />
        </el-select>
      </el-form-item>
      <el-form-item><el-button :loading="loading" @click="load">{{ t('common.query') }}</el-button></el-form-item>
    </el-form>
    <el-table :data="rows" v-loading="loading" border :empty-text="t('pages.syncRuns.empty')" @expand-change="onExpandChange" @current-change="onCurrentRunChange">
      <el-table-column type="expand">
        <template #default="{ row }">
          <el-table :data="details[row.id] || []" border class="sync-detail-table">
            <el-table-column prop="ticker" label="Ticker" width="100" />
            <el-table-column prop="status" :label="t('common.status')" width="120">
              <template #default="{ row: detail }">
                <el-tag class="status-tag" :type="syncStatusType(detail.status)" effect="plain">{{ syncStatusLabel(detail.status) }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="new_filings" :label="t('common.newCount')" width="80" align="right" />
            <el-table-column prop="duration_ms" :label="t('common.duration')" width="100">
              <template #default="{ row: detail }">{{ formatDuration(detail.duration_ms) }}</template>
            </el-table-column>
            <el-table-column prop="started_at" :label="t('common.startTime')" width="170">
              <template #default="{ row: detail }">{{ formatDateTime(detail.started_at) }}</template>
            </el-table-column>
            <el-table-column prop="error_message" :label="t('common.error')" min-width="260" show-overflow-tooltip />
            <el-table-column :label="t('common.actions')" width="150">
              <template #default="{ row: detail }">
                <el-button
                  v-if="detail.status === 'failed'"
                  size="small"
                  type="primary"
                  :loading="retryingTargetId === detail.target_id"
                  @click="retryTarget(row, detail)"
                >
                  {{ t('common.retry') }}
                </el-button>
                <el-button v-else size="small" @click="$router.push(`/targets?ticker=${encodeURIComponent(detail.ticker)}`)">{{ t('common.target') }}</el-button>
                <el-dropdown v-if="detail.status === 'failed'" trigger="click" @command="(command: string) => handleDetailCommand(command, detail)">
                  <el-button size="small" :icon="MoreFilled" />
                  <template #dropdown>
                    <el-dropdown-menu>
                      <el-dropdown-item command="target">{{ t('pages.syncRuns.viewTarget') }}</el-dropdown-item>
                    </el-dropdown-menu>
                  </template>
                </el-dropdown>
              </template>
            </el-table-column>
          </el-table>
        </template>
      </el-table-column>
      <el-table-column prop="status" :label="t('common.status')" width="120">
        <template #default="{ row }">
          <el-tag class="status-tag" :type="syncStatusType(row.status)" effect="plain">{{ syncStatusLabel(row.status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="trigger" :label="t('common.source')" width="100">
        <template #default="{ row }">
          <el-tag type="info" effect="plain">{{ triggerLabel(row.trigger) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="started_at" :label="t('common.startTime')" width="170">
        <template #default="{ row }">{{ formatDateTime(row.started_at) }}</template>
      </el-table-column>
      <el-table-column prop="finished_at" :label="t('common.finishTime')" width="170">
        <template #default="{ row }">{{ formatDateTime(row.finished_at) }}</template>
      </el-table-column>
      <el-table-column prop="targets_checked" :label="t('common.target')" width="80" align="right" />
      <el-table-column prop="new_filings" :label="t('common.newCount')" width="80" align="right" />
      <el-table-column prop="failed_targets" :label="t('status.failed')" width="80" align="right" />
      <el-table-column prop="error_message" :label="t('common.error')" min-width="220" />
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { MoreFilled } from '@element-plus/icons-vue'
import { apiClient } from '@/api/client'
import type { ApiResponse, PageResult, SyncRun, SyncRunDetail } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const loading = ref(false)
const rows = ref<SyncRun[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const filters = reactive({ status: '' })
const details = ref<Record<number, SyncRunDetail[]>>({})
const retryingTargetId = ref<number | null>(null)
const retryingAll = ref(false)
const currentRun = ref<SyncRun | null>(null)

const selectedRunFailedDetails = computed(() => {
  if (!currentRun.value) return []
  return (details.value[currentRun.value.id] || []).filter((item) => item.status === 'failed')
})

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<SyncRun>>>('/sync-runs', { params: { ...filters, page: page.value, page_size: pageSize } })
    rows.value = res.data.data.items
    total.value = res.data.data.total
  } finally {
    loading.value = false
  }
}

async function onExpandChange(row: SyncRun) {
  currentRun.value = row
  if (details.value[row.id]) return
  const res = await apiClient.get<ApiResponse<SyncRunDetail[]>>(`/sync-runs/${row.id}/details`)
  details.value = { ...details.value, [row.id]: res.data.data }
}

async function onCurrentRunChange(row?: SyncRun) {
  if (!row) return
  currentRun.value = row
  if (!details.value[row.id]) {
    await onExpandChange(row)
  }
}

function formatDuration(value: number) {
  if (!value) return '-'
  if (value < 1000) return `${value} ms`
  return `${(value / 1000).toFixed(1)} s`
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
  return '-'
}

function triggerLabel(trigger?: string) {
  if (trigger === 'manual') return t('pages.syncRuns.triggers.manual')
  if (trigger === 'scheduler') return t('pages.syncRuns.triggers.scheduler')
  if (trigger === 'target') return t('pages.syncRuns.triggers.target')
  return trigger || '-'
}

function handleDetailCommand(command: string, detail: SyncRunDetail) {
  if (command === 'target') {
    window.location.href = `/targets?ticker=${encodeURIComponent(detail.ticker)}`
  }
}

async function retryTarget(run: SyncRun, detail: SyncRunDetail) {
  retryingTargetId.value = detail.target_id
  try {
    const res = await apiClient.post<ApiResponse<{ new_filings: number }>>(`/watch-targets/${detail.target_id}/sync`)
    ElMessage.success(t('messages.retryDone', { ticker: detail.ticker, count: res.data.data.new_filings }))
    const nextDetails = { ...details.value }
    delete nextDetails[run.id]
    details.value = nextDetails
    await onExpandChange(run)
    await load()
  } finally {
    retryingTargetId.value = null
  }
}

async function retrySelectedFailures() {
  if (!currentRun.value || selectedRunFailedDetails.value.length === 0) return
  retryingAll.value = true
  try {
    let totalNew = 0
    for (const detail of selectedRunFailedDetails.value) {
      const res = await apiClient.post<ApiResponse<{ new_filings: number }>>(`/watch-targets/${detail.target_id}/sync`)
      totalNew += res.data.data.new_filings
    }
    ElMessage.success(t('messages.retryAllDone', { targets: selectedRunFailedDetails.value.length, count: totalNew }))
    const run = currentRun.value
    const nextDetails = { ...details.value }
    delete nextDetails[run.id]
    details.value = nextDetails
    await onExpandChange(run)
    await load()
  } finally {
    retryingAll.value = false
  }
}

onMounted(load)
</script>
