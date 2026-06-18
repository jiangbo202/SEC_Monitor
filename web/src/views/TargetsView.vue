<template>
  <section class="page">
    <div class="page-header">
      <h1>{{ t('pages.targets.title') }}</h1>
      <el-button type="primary" @click="openCreate">{{ t('pages.targets.add') }}</el-button>
    </div>
    <el-form :inline="true" :model="filters" class="toolbar">
      <el-form-item label="Ticker"><el-input v-model="filters.ticker" clearable /></el-form-item>
      <el-form-item :label="t('common.targetGroup')"><el-input v-model="filters.group" clearable style="width: 150px" /></el-form-item>
      <el-form-item :label="t('common.status')">
        <el-select v-model="filters.status" clearable style="width: 140px">
          <el-option :label="t('common.enabled')" value="enabled" />
          <el-option :label="t('common.disabled')" value="disabled" />
        </el-select>
      </el-form-item>
      <el-form-item><el-button :loading="loading" @click="load">{{ t('common.query') }}</el-button></el-form-item>
    </el-form>
    <el-table :data="rows" v-loading="loading" border :empty-text="t('pages.targets.empty')">
      <el-table-column prop="ticker" label="Ticker" width="105">
        <template #default="{ row }">
          <el-link type="primary" @click="openDetail(row)">{{ row.ticker }}</el-link>
        </template>
      </el-table-column>
      <el-table-column prop="company_name" :label="t('common.companyName')" min-width="220" show-overflow-tooltip />
      <el-table-column prop="cik" label="CIK" width="120" />
      <el-table-column prop="target_type" :label="t('common.type')" width="90">
        <template #default="{ row }">
          <el-tag :type="row.target_type === 'etf' ? 'warning' : 'info'" effect="plain">{{ row.target_type === 'etf' ? 'ETF' : 'Stock' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="group" :label="t('common.targetGroup')" width="120">
        <template #default="{ row }">
          <el-tag v-if="row.group" effect="plain">{{ row.group }}</el-tag>
          <span v-else>-</span>
        </template>
      </el-table-column>
      <el-table-column prop="status" :label="t('common.enabled')" width="90">
        <template #default="{ row }">
          <el-switch
            :model-value="row.status === 'enabled'"
            inline-prompt
            :active-text="t('pages.targets.enableShort')"
            :inactive-text="t('pages.targets.disableShort')"
            @change="(value: boolean) => setTargetEnabled(row, value)"
          />
        </template>
      </el-table-column>
      <el-table-column prop="last_sync_status" :label="t('common.sync')" width="120">
        <template #default="{ row }">
          <el-tag class="status-tag" :type="syncStatusType(row.last_sync_status)" effect="plain">{{ syncStatusLabel(row.last_sync_status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="last_sync_at" :label="t('pages.targets.lastSync')" width="170">
        <template #default="{ row }">{{ formatDateTime(row.last_sync_at) }}</template>
      </el-table-column>
      <el-table-column prop="last_new_filings" :label="t('common.newCount')" width="80" align="right" />
      <el-table-column prop="last_sync_error" :label="t('pages.targets.syncError')" min-width="180" show-overflow-tooltip />
      <el-table-column prop="updated_at" :label="t('common.update')" width="170">
        <template #default="{ row }">{{ formatDateTime(row.updated_at) }}</template>
      </el-table-column>
      <el-table-column :label="t('common.actions')" width="150" fixed="right">
        <template #default="{ row }">
          <el-button size="small" type="primary" :loading="syncingId === row.id" @click="syncTarget(row)">{{ t('common.sync') }}</el-button>
          <el-dropdown trigger="click" @command="(command: string) => handleTargetCommand(command, row)">
            <el-button size="small" :icon="MoreFilled" />
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="detail">{{ t('common.details') }}</el-dropdown-item>
                <el-dropdown-item command="edit">{{ t('common.edit') }}</el-dropdown-item>
                <el-dropdown-item command="delete" divided>{{ t('common.delete') }}</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />

    <el-dialog v-model="dialogVisible" :title="editingId ? t('pages.targets.edit') : t('pages.targets.add')" width="520px">
      <el-form :model="form" label-width="110px">
        <el-form-item label="Ticker">
          <el-input v-model="form.ticker" placeholder="TSLA" @blur="lookupTicker">
            <template #append>
              <el-button :loading="lookingUp" @click="lookupTicker">{{ t('pages.targets.lookup') }}</el-button>
            </template>
          </el-input>
        </el-form-item>
        <el-form-item :label="t('common.companyName')"><el-input v-model="form.company_name" /></el-form-item>
        <el-form-item label="CIK"><el-input v-model="form.cik" /></el-form-item>
        <el-form-item :label="t('common.type')">
          <el-select v-model="form.target_type">
            <el-option label="Stock" value="stock" />
            <el-option label="ETF" value="etf" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('common.targetGroup')">
          <el-input v-model="form.group" :placeholder="t('pages.targets.groupPlaceholder')" />
        </el-form-item>
        <el-form-item :label="t('common.status')">
          <el-select v-model="form.status">
            <el-option :label="t('common.enabled')" value="enabled" />
            <el-option :label="t('common.disabled')" value="disabled" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="detailVisible" :title="detailTarget ? `${detailTarget.ticker} ${t('common.details')}` : t('pages.targets.detail')" size="720px">
      <div v-if="detailTarget" class="target-detail">
        <el-alert
          v-if="detailTarget.last_sync_status === 'failed'"
          :title="syncIssueTitle(detailTarget)"
          :description="syncIssueSuggestion(detailTarget)"
          type="error"
          :closable="false"
          show-icon
        />
        <div class="target-detail-summary">
          <el-descriptions :column="2" border>
            <el-descriptions-item :label="t('common.company')">{{ detailTarget.company_name }}</el-descriptions-item>
            <el-descriptions-item label="CIK">{{ detailTarget.cik || '-' }}</el-descriptions-item>
            <el-descriptions-item :label="t('common.type')">{{ detailTarget.target_type }}</el-descriptions-item>
            <el-descriptions-item :label="t('common.targetGroup')">{{ detailTarget.group || '-' }}</el-descriptions-item>
            <el-descriptions-item :label="t('common.status')">
              <el-tag :type="detailTarget.status === 'enabled' ? 'success' : 'info'" effect="plain">{{ targetStatusLabel(detailTarget.status) }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="t('pages.targets.syncStatus')">
              <el-tag :type="syncStatusType(detailTarget.last_sync_status)" effect="plain">{{ detailTarget.last_sync_status || '-' }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="t('pages.targets.lastSync')">{{ formatDateTime(detailTarget.last_sync_at) }}</el-descriptions-item>
            <el-descriptions-item :label="t('pages.targets.recentNew')">{{ detailTarget.last_new_filings || 0 }}</el-descriptions-item>
            <el-descriptions-item :label="t('pages.targets.syncError')">{{ detailTarget.last_sync_error || '-' }}</el-descriptions-item>
            <el-descriptions-item :label="t('pages.targets.fetchPolicy')">{{ policySummary }}</el-descriptions-item>
          </el-descriptions>
          <div class="target-detail-actions">
            <el-button type="primary" :loading="syncingId === detailTarget.id" @click="syncTarget(detailTarget)">{{ t('pages.targets.syncTarget') }}</el-button>
            <el-button @click="openEdit(detailTarget)">{{ t('common.edit') }}</el-button>
          </div>
        </div>

        <div class="panel-header target-detail-section-title">
          <span>{{ t('pages.targets.recentSync') }}</span>
          <el-link type="primary" @click="$router.push('/sync-runs')">{{ t('common.history') }}</el-link>
        </div>
        <el-table :data="detailSyncDetails" v-loading="detailLoading" border :empty-text="t('pages.targets.noSyncRuns')">
          <el-table-column prop="status" :label="t('common.status')" width="130">
            <template #default="{ row }">
              <el-tag class="status-tag" :type="syncStatusType(row.status)" effect="plain">{{ row.status }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="new_filings" :label="t('common.newCount')" width="80" />
          <el-table-column prop="duration_ms" :label="t('common.duration')" width="100">
            <template #default="{ row }">{{ formatDuration(row.duration_ms) }}</template>
          </el-table-column>
          <el-table-column prop="started_at" :label="t('common.time')" width="180">
            <template #default="{ row }">{{ formatDateTime(row.started_at) }}</template>
          </el-table-column>
          <el-table-column prop="error_message" :label="t('common.error')" min-width="180" show-overflow-tooltip />
        </el-table>

        <div class="panel-header target-detail-section-title">
          <span>{{ t('pages.targets.recentFilings') }}</span>
          <el-link type="primary" @click="$router.push(`/filings?ticker=${encodeURIComponent(detailTarget.ticker)}`)">{{ t('common.viewAll') }}</el-link>
        </div>
        <el-table :data="detailFilings" v-loading="detailLoading" border :empty-text="t('pages.targets.noFilings')">
          <el-table-column prop="filing_type" :label="t('common.type')" width="90" />
          <el-table-column prop="filing_date" :label="t('common.filingDate')" width="130">
            <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
          </el-table-column>
          <el-table-column prop="pulled_at" :label="t('common.syncTime')" width="170">
            <template #default="{ row }">{{ formatDateTime(row.pulled_at) }}</template>
          </el-table-column>
          <el-table-column prop="title" :label="t('common.title')" min-width="200" show-overflow-tooltip />
          <el-table-column :label="t('common.link')" width="80">
            <template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">{{ t('common.open') }}</el-link></template>
          </el-table-column>
        </el-table>
      </div>
    </el-drawer>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { MoreFilled } from '@element-plus/icons-vue'
import { apiClient } from '@/api/client'
import type { ApiResponse, Filing, PageResult, SyncRunDetail, SystemConfig, TickerLookup, WatchTarget } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const loading = ref(false)
const saving = ref(false)
const lookingUp = ref(false)
const syncingId = ref<number | null>(null)
const route = useRoute()
const rows = ref<WatchTarget[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const dialogVisible = ref(false)
const detailVisible = ref(false)
const detailLoading = ref(false)
const detailTarget = ref<WatchTarget | null>(null)
const detailFilings = ref<Filing[]>([])
const detailSyncDetails = ref<SyncRunDetail[]>([])
const systemConfigs = ref<SystemConfig[]>([])
const editingId = ref<number | null>(null)
const filters = reactive({ ticker: '', status: '', group: '' })
const form = reactive({ ticker: '', company_name: '', cik: '', target_type: 'stock', group: '', status: 'enabled' })

const policySummary = computed(() => {
  const days = configValue('sec.initial_fetch_days', '30')
  const syncWindow = configValue('sec.sync_window_days', '30')
  const max = configValue('sec.max_fetch_count', '300')
  const full = configValue('sec.fetch_full_history', 'false') === 'true'
  const syncText = syncWindow === '0' ? t('pages.targets.policyEveryUnlimited') : t('pages.targets.policyEveryDays', { days: syncWindow })
  const initialText = full ? t('pages.targets.policyInitialFull') : t('pages.targets.policyInitialDays', { days })
  const maxText = max === '0' ? t('pages.targets.policyMaxUnlimited') : t('pages.targets.policyMaxCount', { count: max })
  return t('pages.targets.policySummary', { syncWindow: syncText, initialWindow: initialText, max: maxText })
})

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<WatchTarget>>>('/watch-targets', { params: { ...filters, page: page.value, page_size: pageSize } })
    rows.value = res.data.data.items
    total.value = res.data.data.total
  } finally {
    loading.value = false
  }
}

function configValue(key: string, fallback: string) {
  return systemConfigs.value.find((item) => item.config_key === key)?.config_value || fallback
}

function openCreate() {
  editingId.value = null
  Object.assign(form, { ticker: '', company_name: '', cik: '', target_type: 'stock', group: '', status: 'enabled' })
  dialogVisible.value = true
}

async function lookupTicker() {
  const ticker = form.ticker.trim().toUpperCase()
  if (!ticker) return
  form.ticker = ticker
  lookingUp.value = true
  try {
    const res = await apiClient.get<ApiResponse<TickerLookup>>(`/sec/tickers/${encodeURIComponent(ticker)}`)
    form.company_name = res.data.data.company_name
    form.cik = res.data.data.cik
    if (!form.target_type) {
      form.target_type = res.data.data.target_type || 'stock'
    }
    ElMessage.success(t('messages.lookupDone'))
  } catch (error) {
    ElMessage.warning(t('messages.lookupFailed'))
  } finally {
    lookingUp.value = false
  }
}

function openEdit(row: WatchTarget) {
  editingId.value = row.id
  Object.assign(form, row)
  dialogVisible.value = true
}

async function save() {
  saving.value = true
  let createdTarget: WatchTarget | null = null
  try {
    if (editingId.value) {
      await apiClient.put(`/watch-targets/${editingId.value}`, form)
    } else {
      const res = await apiClient.post<ApiResponse<WatchTarget>>('/watch-targets', form)
      createdTarget = res.data.data
    }
    dialogVisible.value = false
    ElMessage.success(t('messages.saved'))
    await load()
    if (createdTarget) {
      await offerImmediateSync(createdTarget)
    }
  } finally {
    saving.value = false
  }
}

async function setTargetEnabled(row: WatchTarget, enabled: boolean) {
  const previous = row.status
  row.status = enabled ? 'enabled' : 'disabled'
  try {
    await apiClient.patch(`/watch-targets/${row.id}/status`, { status: row.status })
    await load()
  } catch (error) {
    row.status = previous
    throw error
  }
}

async function handleTargetCommand(command: string, row: WatchTarget) {
  if (command === 'detail') {
    await openDetail(row)
    return
  }
  if (command === 'edit') {
    openEdit(row)
    return
  }
  if (command === 'delete') {
    await remove(row)
  }
}

async function syncTarget(row: WatchTarget) {
  syncingId.value = row.id
  try {
    const res = await apiClient.post<ApiResponse<{ new_filings: number, failed_targets: number }>>(`/watch-targets/${row.id}/sync`)
    ElMessage.success(t('messages.syncDone', { count: res.data.data.new_filings }))
    await load()
    if (detailVisible.value && detailTarget.value?.id === row.id) {
      const updated = rows.value.find((item) => item.id === row.id)
      if (updated) detailTarget.value = updated
      await loadTargetDetailData(row)
    }
  } finally {
    syncingId.value = null
  }
}

async function offerImmediateSync(target: WatchTarget) {
  try {
    await ElMessageBox.confirm(t('messages.offerSync', { ticker: target.ticker }), t('messages.targetSavedTitle'), {
      confirmButtonText: t('messages.syncNow'),
      cancelButtonText: t('messages.later'),
      type: 'info'
    })
  } catch (error) {
    // User chose to wait for scheduled sync.
    return
  }
  await syncTarget(target)
}

async function openDetail(row: WatchTarget) {
  detailTarget.value = row
  detailVisible.value = true
  await loadTargetDetailData(row)
}

async function loadTargetDetailData(row: WatchTarget) {
  detailLoading.value = true
  try {
    const [filings, syncDetails, configs] = await Promise.all([
      apiClient.get<ApiResponse<PageResult<Filing>>>('/filings', {
        params: { ticker: row.ticker, page: 1, page_size: 8, sort_by: 'pulled_at', sort_order: 'desc' }
      }),
      apiClient.get<ApiResponse<SyncRunDetail[]>>(`/watch-targets/${row.id}/sync-details`),
      apiClient.get<ApiResponse<SystemConfig[]>>('/system-configs')
    ])
    detailFilings.value = filings.data.data.items
    detailSyncDetails.value = syncDetails.data.data
    systemConfigs.value = configs.data.data
  } finally {
    detailLoading.value = false
  }
}

async function remove(row: WatchTarget) {
  await ElMessageBox.confirm(t('messages.confirmDeleteTarget', { ticker: row.ticker }), t('messages.confirmDeleteTitle'), { type: 'warning' })
  await apiClient.delete(`/watch-targets/${row.id}`)
  await load()
}

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

function formatDate(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toISOString().slice(0, 10)
}

function syncStatusType(status?: string) {
  if (status === 'success') return 'success'
  if (status === 'failed') return 'danger'
  return 'info'
}

function syncStatusLabel(status?: string) {
  if (status === 'success') return t('status.success')
  if (status === 'failed') return t('status.failed')
  if (status === 'running') return t('status.running')
  return '-'
}

function targetStatusLabel(status?: string) {
  if (status === 'enabled') return t('status.enabled')
  if (status === 'disabled') return t('status.disabled')
  return status || '-'
}

function formatDuration(value: number) {
  if (!value) return '-'
  if (value < 1000) return `${value} ms`
  return `${(value / 1000).toFixed(1)} s`
}

function syncIssueTitle(target: WatchTarget) {
  return t('pages.targets.syncIssueTitle', { ticker: target.ticker })
}

function syncIssueSuggestion(target: WatchTarget) {
  const message = target.last_sync_error || ''
  if (message.toLowerCase().includes('cik')) return t('pages.targets.syncIssueCik')
  if (message.toLowerCase().includes('timeout') || message.includes('deadline')) return t('pages.targets.syncIssueTimeout')
  if (message.toLowerCase().includes('telegram')) return t('pages.targets.syncIssueTelegram')
  return message || t('pages.targets.syncIssueDefault')
}

onMounted(() => {
  const ticker = route.query.ticker
  if (typeof ticker === 'string') {
    filters.ticker = ticker
  }
  const status = route.query.status
  if (typeof status === 'string') {
    filters.status = status
  }
  load()
})
</script>
