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
          <el-input v-model="form.ticker" placeholder="TSLA" @input="invalidateFundIdentity" @blur="lookupTicker">
            <template #append>
              <el-button :loading="lookingUp" @click="lookupTicker">{{ t('pages.targets.lookup') }}</el-button>
            </template>
          </el-input>
        </el-form-item>
        <el-form-item :label="t('common.companyName')"><el-input v-model="form.company_name" /></el-form-item>
        <el-form-item label="CIK"><el-input v-model="form.cik" @input="markManualFundIdentity" /></el-form-item>
        <el-form-item :label="t('common.type')">
          <el-select v-model="form.target_type" @change="handleTargetTypeChange">
            <el-option label="Stock" value="stock" />
            <el-option label="ETF" value="etf" />
          </el-select>
        </el-form-item>
        <template v-if="form.target_type === 'etf'">
          <el-form-item v-if="fundCandidates.length" :label="t('pages.targets.fundCandidate')">
            <el-select v-model="selectedFundCandidateKey" :placeholder="t('pages.targets.fundCandidatePlaceholder')" @change="selectFundCandidate">
              <el-option v-for="candidate in fundCandidates" :key="fundCandidateKey(candidate)" :label="fundCandidateLabel(candidate)" :value="fundCandidateKey(candidate)" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('pages.targets.fundSeriesId')">
            <el-input v-model="form.fund_series_id" placeholder="S000102337" @input="markManualFundIdentity" />
          </el-form-item>
          <el-form-item :label="t('pages.targets.fundClassId')">
            <el-input v-model="form.fund_class_id" placeholder="C000272806" @input="markManualFundIdentity" />
          </el-form-item>
          <el-form-item :label="t('pages.targets.identitySource')">
            <el-input v-model="form.identity_source" readonly :placeholder="t('pages.targets.identitySourceManual')" />
          </el-form-item>
          <el-form-item label-width="0">
            <el-alert
              :title="formHasExactFundIdentity ? t('pages.targets.fundIdentityExact') : t('pages.targets.fundIdentityUnresolved')"
              :description="fundIdentityFormDescription"
              :type="formHasExactFundIdentity ? 'success' : 'warning'"
              :closable="false"
              show-icon
            />
          </el-form-item>
        </template>
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
        <el-button type="primary" :loading="saving" :disabled="saveDisabled" @click="save">{{ t('common.save') }}</el-button>
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
          <el-alert
            v-if="detailTarget.target_type === 'etf'"
            :title="hasStoredExactFundIdentity(detailTarget) ? t('pages.targets.fundIdentityExact') : t('pages.targets.fundIdentityLegacy')"
            :description="hasStoredExactFundIdentity(detailTarget) ? t('pages.targets.fundIdentityExactDetail') : t('pages.targets.fundIdentityLegacyDetail')"
            :type="hasStoredExactFundIdentity(detailTarget) ? 'success' : 'warning'"
            :closable="false"
            show-icon
          />
          <el-descriptions :column="2" border>
            <el-descriptions-item :label="t('common.company')">{{ detailTarget.company_name }}</el-descriptions-item>
            <el-descriptions-item label="CIK">{{ detailTarget.cik || '-' }}</el-descriptions-item>
            <el-descriptions-item :label="t('common.type')">{{ detailTarget.target_type }}</el-descriptions-item>
            <template v-if="detailTarget.target_type === 'etf'">
              <el-descriptions-item :label="t('pages.targets.fundSeriesId')">{{ detailTarget.fund_series_id || '-' }}</el-descriptions-item>
              <el-descriptions-item :label="t('pages.targets.fundClassId')">{{ detailTarget.fund_class_id || '-' }}</el-descriptions-item>
              <el-descriptions-item :label="t('pages.targets.identitySource')">{{ detailTarget.identity_source || '-' }}</el-descriptions-item>
            </template>
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

        <div v-if="detailTarget.target_type === 'stock'" class="target-detail-section">
          <ProfitHistoryChart :history="detailProfitHistory" />
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
		  <el-table-column prop="warning_message" :label="t('pages.syncRuns.warning')" min-width="180" show-overflow-tooltip />
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
import axios from 'axios'
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { MoreFilled } from '@element-plus/icons-vue'
import { apiClient } from '@/api/client'
import ProfitHistoryChart from '@/components/ProfitHistoryChart.vue'
import type { ApiResponse, Filing, FundIdentity, PageResult, ProfitHistory, SyncRunDetail, SystemConfig, TickerLookup, WatchTarget } from '@/api/types'
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
const detailProfitHistory = ref<ProfitHistory | null>(null)
const systemConfigs = ref<SystemConfig[]>([])
const editingId = ref<number | null>(null)
const filters = reactive({ ticker: '', status: '', group: '' })
const form = reactive({
  ticker: '', company_name: '', cik: '', target_type: 'stock', fund_series_id: '', fund_class_id: '', identity_source: '', group: '', status: 'enabled'
})
const fundCandidates = ref<FundIdentity[]>([])
const selectedFundCandidateKey = ref('')
const resolvedFundIdentity = ref<FundIdentity | null>(null)

const formHasExactFundIdentity = computed(() => matchesResolvedFundIdentity(form))
const saveDisabled = computed(() => form.target_type === 'etf' && !formHasExactFundIdentity.value)
const fundIdentityFormDescription = computed(() => {
  if (formHasExactFundIdentity.value) {
    return t('pages.targets.fundIdentityExactForm', { source: resolvedFundIdentity.value?.source || t('pages.targets.identitySourceManual') })
  }
  if (resolvedFundIdentity.value) return t('pages.targets.fundIdentityModified')
  if (fundCandidates.value.length > 0) return t('pages.targets.fundCandidateRequired')
  return t('pages.targets.fundIdentityUnresolvedDetail')
})

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
  Object.assign(form, { ticker: '', company_name: '', cik: '', target_type: 'stock', fund_series_id: '', fund_class_id: '', identity_source: '', group: '', status: 'enabled' })
  clearFundResolution()
  dialogVisible.value = true
}

async function lookupTicker() {
  const ticker = form.ticker.trim().toUpperCase()
  if (!ticker) return
  form.ticker = ticker
  lookingUp.value = true
  try {
    const res = await apiClient.get<ApiResponse<TickerLookup>>(`/sec/tickers/${encodeURIComponent(ticker)}`, {
      params: { target_type: form.target_type }
    })
    const lookup = res.data.data
    form.company_name = lookup.company_name
    form.cik = lookup.cik
    form.target_type = lookup.target_type || form.target_type || 'stock'
    clearFundResolution()
    Object.assign(form, { fund_series_id: '', fund_class_id: '', identity_source: '' })
    if (lookup.fund_identity) {
      applyFundIdentity(lookup.fund_identity)
      ElMessage.success(t('pages.targets.fundIdentityExact'))
    } else if (lookup.fund_candidates?.length) {
      fundCandidates.value = lookup.fund_candidates
      ElMessage.warning(t('pages.targets.fundCandidateRequired'))
    } else if (form.target_type === 'etf') {
      ElMessage.warning(t('pages.targets.fundIdentityUnresolved'))
    } else {
      ElMessage.success(t('messages.lookupDone'))
    }
  } catch (error) {
    ElMessage.warning(t('messages.lookupFailed'))
  } finally {
    lookingUp.value = false
  }
}

function openEdit(row: WatchTarget) {
  editingId.value = row.id
  Object.assign(form, {
    ...row,
    fund_series_id: row.fund_series_id || '',
    fund_class_id: row.fund_class_id || '',
    identity_source: row.identity_source || ''
  })
  clearFundCandidates()
  resolvedFundIdentity.value = resolvedIdentityFromStoredTarget(row)
  dialogVisible.value = true
}

function hasCompleteFundIdentity(target: Pick<WatchTarget, 'target_type' | 'cik' | 'fund_series_id' | 'fund_class_id'> | typeof form) {
  return target.target_type === 'etf' && Boolean(target.cik?.trim()) && Boolean(target.fund_series_id?.trim()) && Boolean(target.fund_class_id?.trim())
}

function hasStoredExactFundIdentity(target: WatchTarget) {
  return hasCompleteFundIdentity(target)
}

// Pure conversion helper: a complete stored ETF tuple is the verified baseline
// for non-identity edits. It returns null for legacy Trust-level records.
function resolvedIdentityFromStoredTarget(target: WatchTarget): FundIdentity | null {
  if (!hasCompleteFundIdentity(target)) return null
  return {
    ticker: target.ticker,
    cik: target.cik,
    series_id: target.fund_series_id || '',
    class_id: target.fund_class_id || '',
    fund_name: target.company_name || target.identity_note || target.ticker,
    source: target.identity_source || 'stored_watch_target'
  }
}

function matchesResolvedFundIdentity(target: typeof form) {
  const identity = resolvedFundIdentity.value
  return Boolean(identity && hasCompleteFundIdentity(target) &&
    target.cik.trim() === identity.cik.trim() &&
    target.fund_series_id.trim() === identity.series_id.trim() &&
    target.fund_class_id.trim() === identity.class_id.trim())
}

function clearFundResolution() {
  clearFundCandidates()
  resolvedFundIdentity.value = null
}

function clearFundCandidates() {
  fundCandidates.value = []
  selectedFundCandidateKey.value = ''
}

function invalidateFundIdentity() {
  Object.assign(form, { fund_series_id: '', fund_class_id: '', identity_source: '' })
  clearFundResolution()
}

function handleTargetTypeChange(targetType: string) {
  if (targetType !== 'etf') {
    Object.assign(form, { fund_series_id: '', fund_class_id: '', identity_source: '' })
    clearFundResolution()
  }
}

function fundCandidateKey(candidate: FundIdentity) {
  return `${candidate.cik}:${candidate.series_id}:${candidate.class_id}`
}

function fundCandidateLabel(candidate: FundIdentity) {
  const name = candidate.fund_name || candidate.ticker
  return `${name} · ${candidate.cik} · ${candidate.series_id} · ${candidate.class_id}`
}

function selectFundCandidate(key: string) {
  const candidate = fundCandidates.value.find((item) => fundCandidateKey(item) === key)
  if (!candidate) return
  applyFundIdentity(candidate)
}

function applyFundIdentity(identity: FundIdentity) {
  resolvedFundIdentity.value = { ...identity }
  Object.assign(form, {
    cik: identity.cik,
    company_name: identity.fund_name || identity.ticker,
    fund_series_id: identity.series_id,
    fund_class_id: identity.class_id,
    identity_source: identity.source
  })
}

function markManualFundIdentity() {
  // A manual CIK, Series ID, or Class ID edit requires a fresh lookup or
  // candidate selection before this ETF can be saved again.
  clearFundResolution()
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
  } catch (error) {
    ElMessage.error(saveErrorMessage(error))
    return
  } finally {
    saving.value = false
  }
  dialogVisible.value = false
  ElMessage.success(t('messages.saved'))
  await load()
  if (createdTarget) {
    await offerImmediateSync(createdTarget)
  }
}

function saveErrorMessage(error: unknown) {
  if (axios.isAxiosError(error)) {
    const message = error.response?.data?.message
    if (typeof message === 'string' && message.trim()) return message
  }
  return t('messages.saveFailed')
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
	detailProfitHistory.value = null
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
		if (row.target_type === 'stock') {
			try {
				const history = await apiClient.get<ApiResponse<ProfitHistory>>(`/discovery/profit-history/${encodeURIComponent(row.ticker)}`)
				detailProfitHistory.value = history.data.data
			} catch {
				detailProfitHistory.value = null
			}
		}
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
