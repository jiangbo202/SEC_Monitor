<template>
  <el-dialog
    :model-value="visible"
    title="调整小盘股范围"
    width="980px"
    destroy-on-close
    @update:model-value="emit('update:visible', $event)"
    @open="loadPolicy"
  >
    <el-alert
      title="这里只调整市值范围。预览和重算均使用本地已有快照，不会重新下载 SEC 文件或请求行情。"
      type="info"
      :closable="false"
      show-icon
      class="policy-alert"
    />

    <div class="policy-heading">
      <div>
        <strong>当前生效版本</strong>
        <el-tag v-if="activePolicy" type="success" effect="plain">v{{ activePolicy.version }}</el-tag>
        <el-tag v-else type="warning" effect="plain">默认策略</el-tag>
        <span v-if="activePolicy?.activated_at">{{ formatDateTime(activePolicy.activated_at) }} 激活</span>
      </div>
      <el-button :loading="loading" @click="loadPolicy">刷新</el-button>
    </div>

    <el-form label-position="top" class="policy-form">
      <div class="policy-form-grid">
        <el-form-item label="最低市值（百万美元）" :error="validationErrors.minimum">
          <el-input-number v-model="form.marketCapMinMillion" :min="1" :max="100000" :step="5" controls-position="right" />
          <span class="form-help">{{ formatUSD(form.marketCapMinMillion * million) }}</span>
        </el-form-item>
        <el-form-item label="A 型强信号市值上限（百万美元，不含）" :error="validationErrors.aMaximum">
          <el-input-number v-model="form.aMarketCapMaxMillion" :min="1" :max="100000" :step="25" controls-position="right" />
          <span class="form-help">{{ formatUSD(form.aMarketCapMaxMillion * million) }}</span>
        </el-form-item>
        <el-form-item label="候选池 / B 型成长观察上限（百万美元，不含）" :error="validationErrors.bMaximum">
          <el-input-number v-model="form.bMarketCapMaxMillion" :min="1" :max="100000" :step="50" controls-position="right" />
          <span class="form-help">{{ formatUSD(form.bMarketCapMaxMillion * million) }}</span>
        </el-form-item>
      </div>
      <el-form-item label="版本名称" :error="form.name.trim() ? '' : '请输入版本名称'">
        <el-input v-model="form.name" maxlength="80" show-word-limit placeholder="例如：市值 30M–1B" />
      </el-form-item>
      <el-form-item label="调整说明">
        <el-input v-model="form.note" type="textarea" :rows="2" maxlength="300" show-word-limit placeholder="记录本次调整原因，便于以后回滚和审计" />
      </el-form-item>
    </el-form>

    <div class="policy-actions">
      <el-button type="primary" plain :loading="previewing" :disabled="!formValid" @click="previewPolicy">预览影响</el-button>
      <el-button type="primary" :loading="activating" :disabled="!previewCurrent || !preview?.can_activate || !form.name.trim()" @click="activatePolicy">激活并本地重算</el-button>
      <span v-if="preview && !previewCurrent" class="stale-preview">市值范围已修改，请重新预览。</span>
    </div>

    <el-card v-if="preview" shadow="never" class="preview-card">
      <template #header>
        <div class="preview-heading">
          <strong>影响预览</strong>
          <span>{{ preview.data_as_of ? `数据有效日 ${preview.data_as_of}` : '当前尚无可重算批次' }}</span>
        </div>
      </template>
      <el-alert
        v-if="preview.warnings?.length"
        :title="previewWarnings"
        type="warning"
        :closable="false"
        show-icon
        class="preview-warning"
      />
      <div class="preview-counts">
        <div v-for="item in previewCountRows" :key="item.label" class="preview-count">
          <span>{{ item.label }}</span>
          <strong>{{ item.before.toLocaleString() }} → {{ item.after.toLocaleString() }}</strong>
          <small :class="deltaClass(item.after - item.before)">{{ formatDelta(item.after - item.before) }}</small>
        </div>
      </div>
      <div class="change-heading">
        <strong>变化样例</strong>
        <span>共 {{ preview.changed_count.toLocaleString() }} 只发生变化<span v-if="preview.changes_truncated">，下表仅显示部分</span></span>
      </div>
      <el-table v-if="preview.changes?.length" :data="preview.changes" size="small" max-height="260">
        <el-table-column prop="ticker" label="Ticker" width="110" />
        <el-table-column label="市值" width="150" align="right">
          <template #default="{ row }">{{ formatUSD(row.market_cap_usd) }}</template>
        </el-table-column>
        <el-table-column label="调整前" width="110">
          <template #default="{ row }">{{ gradeLabel(row.before_grade) }}</template>
        </el-table-column>
        <el-table-column label="调整后" width="110">
          <template #default="{ row }"><el-tag :type="gradeTagType(row.after_grade)" effect="plain">{{ gradeLabel(row.after_grade) }}</el-tag></template>
        </el-table-column>
        <el-table-column label="变化" min-width="180">
          <template #default="{ row }">{{ changeTypeLabel(row.change_type) }}</template>
        </el-table-column>
      </el-table>
      <el-empty v-else description="当前本地数据下没有候选发生变化" :image-size="64" />
    </el-card>

    <el-card shadow="never" class="history-card">
      <template #header>
        <div class="preview-heading">
          <strong>版本历史</strong>
          <span>回滚会复制旧口径并生成一个新的生效版本。</span>
        </div>
      </template>
      <el-table v-loading="loading" :data="history" size="small" max-height="300" empty-text="暂无历史版本">
        <el-table-column label="版本" width="90">
          <template #default="{ row }"><el-tag :type="row.id === activePolicy?.id ? 'success' : 'info'" effect="plain">v{{ row.version }}</el-tag></template>
        </el-table-column>
        <el-table-column label="市值范围" min-width="230">
          <template #default="{ row }">{{ policyScope(row) }}</template>
        </el-table-column>
        <el-table-column prop="name" label="名称" min-width="150" show-overflow-tooltip />
        <el-table-column prop="note" label="说明" min-width="170" show-overflow-tooltip />
        <el-table-column label="激活时间" width="170">
          <template #default="{ row }">{{ formatDateTime(row.activated_at || row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="90" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" :disabled="row.id === activePolicy?.id" :loading="rollbackID === row.id" @click="rollbackPolicy(row)">回滚</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient } from '@/api/client'
import type {
  ApiResponse,
  CandidateSelectionCriteria,
  PageResult,
  SmallCapPolicyActivationResult,
  SmallCapPolicyEditableCriteria,
  SmallCapPolicyPreviewResult,
  SmallCapPolicyState,
  SmallCapPolicyVersion,
} from '@/api/types'

const million = 1_000_000
const props = defineProps<{ visible: boolean; criteria: CandidateSelectionCriteria | null }>()
const emit = defineEmits<{ 'update:visible': [value: boolean]; updated: [result: SmallCapPolicyActivationResult] }>()

const loading = ref(false)
const previewing = ref(false)
const activating = ref(false)
const rollbackID = ref<number | null>(null)
const activePolicy = ref<SmallCapPolicyVersion | null>(null)
const history = ref<SmallCapPolicyVersion[]>([])
const preview = ref<SmallCapPolicyPreviewResult | null>(null)
const previewKey = ref('')
const form = reactive({ marketCapMinMillion: 30, aMarketCapMaxMillion: 500, bMarketCapMaxMillion: 1000, name: '', note: '' })

const criteriaPayload = computed<SmallCapPolicyEditableCriteria>(() => ({
  market_cap_min_usd: Math.round(form.marketCapMinMillion * million),
  a_market_cap_max_exclusive_usd: Math.round(form.aMarketCapMaxMillion * million),
  b_market_cap_max_exclusive_usd: Math.round(form.bMarketCapMaxMillion * million),
}))
const currentFormKey = computed(() => JSON.stringify(criteriaPayload.value))
const previewCurrent = computed(() => Boolean(preview.value && previewKey.value === currentFormKey.value))
const validationErrors = computed(() => {
  const result = { minimum: '', aMaximum: '', bMaximum: '' }
  if (!Number.isFinite(form.marketCapMinMillion) || form.marketCapMinMillion <= 0) result.minimum = '最低市值必须大于 0'
  if (form.aMarketCapMaxMillion <= form.marketCapMinMillion) result.aMaximum = 'A 型上限必须大于最低市值'
  if (form.bMarketCapMaxMillion <= form.aMarketCapMaxMillion) result.bMaximum = '候选池上限必须大于 A 型上限'
  if (form.bMarketCapMaxMillion > 100000) result.bMaximum = '候选池上限不能超过 1000 亿美元'
  return result
})
const formValid = computed(() => Object.values(validationErrors.value).every((value) => !value))
const previewWarnings = computed(() => (preview.value?.warnings || []).map(warningLabel).join('；'))
const previewCountRows = computed(() => {
  const before = preview.value?.before
  const after = preview.value?.after
  if (!before || !after) return []
  return [
    { label: '市值范围内', before: before.in_market_cap_scope, after: after.in_market_cap_scope },
    { label: 'A 型强信号', before: before.grade_a, after: after.grade_a },
    { label: 'B 型成长观察', before: before.grade_b, after: after.grade_b },
    { label: '排除池', before: before.excluded, after: after.excluded },
  ]
})

watch(() => props.visible, (value) => {
  if (value) applyCriteria(activePolicy.value?.criteria || props.criteria)
})

function applyCriteria(criteria?: CandidateSelectionCriteria | null) {
  if (!criteria) return
  form.marketCapMinMillion = criteria.market_cap_min_usd / million
  form.aMarketCapMaxMillion = criteria.a_market_cap_max_exclusive_usd / million
  form.bMarketCapMaxMillion = criteria.b_market_cap_max_exclusive_usd / million
  form.name = `市值 ${formatCompactUSD(criteria.market_cap_min_usd)}–${formatCompactUSD(criteria.b_market_cap_max_exclusive_usd)}`
  form.note = ''
  preview.value = null
  previewKey.value = ''
}

async function loadPolicy() {
  loading.value = true
  try {
    const response = await apiClient.get<ApiResponse<SmallCapPolicyState>>('/discovery/candidates/policy')
    activePolicy.value = response.data.data.active || null
    history.value = response.data.data.history || []
    if (!Array.isArray(response.data.data.history)) {
      const versions = await apiClient.get<ApiResponse<PageResult<SmallCapPolicyVersion>>>('/discovery/candidates/policy/versions', { params: { page: 1, page_size: 20 } })
      history.value = versions.data.data.items || []
    }
    applyCriteria(activePolicy.value?.criteria || props.criteria)
  } catch (error: any) {
    ElMessage.error(apiErrorMessage(error, '加载小盘股策略失败'))
  } finally {
    loading.value = false
  }
}

function requestBody() {
  return {
    expected_active_version_id: activePolicy.value?.id || undefined,
    name: form.name.trim(),
    note: form.note.trim(),
    criteria: criteriaPayload.value,
  }
}

async function previewPolicy() {
  if (!formValid.value) return
  previewing.value = true
  const requestedKey = currentFormKey.value
  try {
    const response = await apiClient.post<ApiResponse<SmallCapPolicyPreviewResult>>('/discovery/candidates/policy/preview', requestBody(), { timeout: 60_000 })
    preview.value = response.data.data
    previewKey.value = requestedKey
    if ((response.data.data.warnings || []).some(item => item === 'needs_bootstrap')) {
      ElMessage.warning('当前尚无已发布候选批次；可以先激活策略，首次完整同步会按新范围执行。')
    }
  } catch (error: any) {
    preview.value = null
    previewKey.value = ''
    ElMessage.error(apiErrorMessage(error, '预览策略影响失败'))
  } finally {
    previewing.value = false
  }
}

async function activatePolicy() {
  if (!previewCurrent.value || !preview.value?.can_activate) return
  try {
    await ElMessageBox.confirm(
      `将激活“${form.name || '未命名策略'}”，并只使用本地快照重新计算候选。SEC 与行情数据不会重新下载。`,
      '确认激活策略',
      { type: 'warning', confirmButtonText: '激活并重算', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  activating.value = true
  try {
    const response = await apiClient.post<ApiResponse<SmallCapPolicyActivationResult>>('/discovery/candidates/policy/activate', requestBody(), { timeout: 120_000 })
    const result = response.data.data
    showActivationMessage(result)
    emit('updated', result)
    await loadPolicy()
  } catch (error: any) {
    ElMessage.error(apiErrorMessage(error, '激活小盘股策略失败'))
  } finally {
    activating.value = false
  }
}

async function rollbackPolicy(version: SmallCapPolicyVersion) {
  try {
    await ElMessageBox.confirm(
      `将复制 v${version.version} 的市值口径并生成一个新的生效版本，不会修改原历史记录。`,
      '确认回滚策略',
      { type: 'warning', confirmButtonText: '回滚并重算', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  rollbackID.value = version.id
  try {
    const response = await apiClient.post<ApiResponse<SmallCapPolicyActivationResult>>(
      `/discovery/candidates/policy/versions/${version.id}/rollback`,
      { expected_active_version_id: activePolicy.value?.id || undefined, note: `回滚至 v${version.version}` },
      { timeout: 120_000 },
    )
    showActivationMessage(response.data.data)
    emit('updated', response.data.data)
    await loadPolicy()
  } catch (error: any) {
    ElMessage.error(apiErrorMessage(error, '回滚小盘股策略失败'))
  } finally {
    rollbackID.value = null
  }
}

function showActivationMessage(result: SmallCapPolicyActivationResult) {
  if (result.status === 'needs_bootstrap') {
    ElMessage.warning('策略已激活；当前没有候选批次，首次完整同步会按新范围执行。')
  } else if (result.status === 'unchanged') {
    ElMessage.info('策略内容没有变化，继续使用当前版本。')
  } else {
    const scored = result.rescore?.scored_count
    ElMessage.success(scored == null ? '策略已激活并完成本地重算' : `策略已激活，本地重算 ${scored.toLocaleString()} 只证券`)
  }
}

function apiErrorMessage(error: any, fallback: string) {
  if (error?.response?.status === 409) return '策略已经被其他操作更新，请刷新后重新预览。'
  return error?.response?.data?.message || fallback
}

function policyScope(version: SmallCapPolicyVersion) {
  const criteria = version.criteria
  if (!criteria) return '-'
  return `${formatCompactUSD(criteria.market_cap_min_usd)} – <${formatCompactUSD(criteria.b_market_cap_max_exclusive_usd)}（A <${formatCompactUSD(criteria.a_market_cap_max_exclusive_usd)}）`
}

function warningLabel(value: string) {
  if (value === 'needs_bootstrap') return '当前没有已发布候选批次；激活后将在首次完整同步时应用。'
  return value
}

function gradeLabel(value?: string) {
  if (value === 'A') return 'A 型强信号'
  if (value === 'B') return 'B 型成长观察'
  if (!value || value === 'excluded') return '排除池'
  return value
}

function gradeTagType(value?: string) {
  if (value === 'A') return 'success'
  if (value === 'B') return 'warning'
  return 'info'
}

function changeTypeLabel(value: string) {
  const labels: Record<string, string> = {
    entered_scope: '进入市值范围', exited_scope: '退出市值范围',
    selection_entered: '新入选', selection_exited: '退出候选',
    a_to_b: 'A 降为 B', b_to_a: 'B 升为 A', grade_changed: '候选等级变化',
  }
  return labels[value] || value
}

function formatDelta(value: number) {
  if (!value) return '无变化'
  return value > 0 ? `+${value.toLocaleString()}` : value.toLocaleString()
}

function deltaClass(value: number) {
  if (value > 0) return 'delta-positive'
  if (value < 0) return 'delta-negative'
  return ''
}

function formatUSD(value: number) {
  if (!Number.isFinite(value)) return '-'
  return new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', maximumFractionDigits: 0 }).format(value)
}

function formatCompactUSD(value: number) {
  if (value >= 1_000_000_000) return `$${Number((value / 1_000_000_000).toFixed(2))}B`
  return `$${Number((value / million).toFixed(2))}M`
}

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}
</script>

<style scoped>
.policy-alert,
.preview-card,
.history-card {
  margin-bottom: 16px;
}

.policy-heading,
.preview-heading,
.change-heading,
.policy-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.policy-heading {
  margin: 16px 0;
}

.policy-heading > div,
.preview-heading,
.change-heading {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.policy-heading strong,
.preview-heading strong,
.change-heading strong {
  color: var(--el-text-color-primary);
  font-size: 14px;
}

.policy-form-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

.policy-form :deep(.el-input-number) {
  width: 100%;
}

.form-help {
  display: block;
  color: var(--el-text-color-secondary);
  font-size: 12px;
  margin-top: 4px;
}

.policy-actions {
  justify-content: flex-start;
  margin-bottom: 16px;
}

.stale-preview,
.preview-heading span,
.change-heading span {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.preview-warning {
  margin-bottom: 12px;
}

.preview-counts {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
  margin-bottom: 16px;
}

.preview-count {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 6px;
}

.preview-count span,
.preview-count small {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.preview-count strong {
  font-size: 18px;
}

.preview-count .delta-positive {
  color: var(--el-color-success);
}

.preview-count .delta-negative {
  color: var(--el-color-danger);
}

.change-heading {
  margin-bottom: 8px;
}

@media (max-width: 860px) {
  .policy-form-grid,
  .preview-counts {
    grid-template-columns: 1fr;
  }
}
</style>
