<template>
  <section class="page">
    <div class="page-header">
      <h1>监控标的</h1>
      <el-button type="primary" @click="openCreate">新增标的</el-button>
    </div>
    <el-form :inline="true" :model="filters" class="toolbar">
      <el-form-item label="Ticker"><el-input v-model="filters.ticker" clearable /></el-form-item>
      <el-form-item label="状态">
        <el-select v-model="filters.status" clearable style="width: 140px">
          <el-option label="已启用" value="enabled" />
          <el-option label="已停用" value="disabled" />
        </el-select>
      </el-form-item>
      <el-form-item><el-button :loading="loading" @click="load">查询</el-button></el-form-item>
    </el-form>
    <el-table :data="rows" v-loading="loading" border empty-text="暂无标的，点击右上角新增">
      <el-table-column prop="ticker" label="Ticker" width="105">
        <template #default="{ row }">
          <el-link type="primary" @click="openDetail(row)">{{ row.ticker }}</el-link>
        </template>
      </el-table-column>
      <el-table-column prop="company_name" label="公司名称" min-width="220" show-overflow-tooltip />
      <el-table-column prop="cik" label="CIK" width="120" />
      <el-table-column prop="target_type" label="类型" width="90">
        <template #default="{ row }">
          <el-tag :type="row.target_type === 'etf' ? 'warning' : 'info'" effect="plain">{{ row.target_type === 'etf' ? 'ETF' : 'Stock' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="status" label="启用" width="90">
        <template #default="{ row }">
          <el-switch
            :model-value="row.status === 'enabled'"
            inline-prompt
            active-text="开"
            inactive-text="关"
            @change="(value: boolean) => setTargetEnabled(row, value)"
          />
        </template>
      </el-table-column>
      <el-table-column prop="last_sync_status" label="同步" width="120">
        <template #default="{ row }">
          <el-tag class="status-tag" :type="syncStatusType(row.last_sync_status)" effect="plain">{{ syncStatusLabel(row.last_sync_status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="last_sync_at" label="上次同步" width="170">
        <template #default="{ row }">{{ formatDateTime(row.last_sync_at) }}</template>
      </el-table-column>
      <el-table-column prop="last_new_filings" label="新增" width="80" align="right" />
      <el-table-column prop="last_sync_error" label="同步错误" min-width="180" show-overflow-tooltip />
      <el-table-column prop="updated_at" label="更新" width="170">
        <template #default="{ row }">{{ formatDateTime(row.updated_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="150" fixed="right">
        <template #default="{ row }">
          <el-button size="small" type="primary" :loading="syncingId === row.id" @click="syncTarget(row)">同步</el-button>
          <el-dropdown trigger="click" @command="(command: string) => handleTargetCommand(command, row)">
            <el-button size="small" :icon="MoreFilled" />
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="detail">查看详情</el-dropdown-item>
                <el-dropdown-item command="edit">编辑</el-dropdown-item>
                <el-dropdown-item command="delete" divided>删除</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />

    <el-dialog v-model="dialogVisible" :title="editingId ? '编辑标的' : '新增标的'" width="520px">
      <el-form :model="form" label-width="110px">
        <el-form-item label="Ticker">
          <el-input v-model="form.ticker" placeholder="TSLA" @blur="lookupTicker">
            <template #append>
              <el-button :loading="lookingUp" @click="lookupTicker">带出信息</el-button>
            </template>
          </el-input>
        </el-form-item>
        <el-form-item label="公司名称"><el-input v-model="form.company_name" /></el-form-item>
        <el-form-item label="CIK"><el-input v-model="form.cik" /></el-form-item>
        <el-form-item label="类型">
          <el-select v-model="form.target_type">
            <el-option label="Stock" value="stock" />
            <el-option label="ETF" value="etf" />
          </el-select>
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="form.status">
            <el-option label="已启用" value="enabled" />
            <el-option label="已停用" value="disabled" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="detailVisible" :title="detailTarget ? `${detailTarget.ticker} 详情` : '标的详情'" size="720px">
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
            <el-descriptions-item label="公司">{{ detailTarget.company_name }}</el-descriptions-item>
            <el-descriptions-item label="CIK">{{ detailTarget.cik || '-' }}</el-descriptions-item>
            <el-descriptions-item label="类型">{{ detailTarget.target_type }}</el-descriptions-item>
            <el-descriptions-item label="状态">
              <el-tag :type="detailTarget.status === 'enabled' ? 'success' : 'info'" effect="plain">{{ targetStatusLabel(detailTarget.status) }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="同步状态">
              <el-tag :type="syncStatusType(detailTarget.last_sync_status)" effect="plain">{{ detailTarget.last_sync_status || '-' }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="上次同步">{{ formatDateTime(detailTarget.last_sync_at) }}</el-descriptions-item>
            <el-descriptions-item label="最近新增">{{ detailTarget.last_new_filings || 0 }}</el-descriptions-item>
            <el-descriptions-item label="同步错误">{{ detailTarget.last_sync_error || '-' }}</el-descriptions-item>
            <el-descriptions-item label="拉取策略">{{ policySummary }}</el-descriptions-item>
          </el-descriptions>
          <div class="target-detail-actions">
            <el-button type="primary" :loading="syncingId === detailTarget.id" @click="syncTarget(detailTarget)">同步该标的</el-button>
            <el-button @click="openEdit(detailTarget)">编辑</el-button>
          </div>
        </div>

        <div class="panel-header target-detail-section-title">
          <span>最近同步</span>
          <el-link type="primary" @click="$router.push('/sync-runs')">历史</el-link>
        </div>
        <el-table :data="detailSyncDetails" v-loading="detailLoading" border empty-text="暂无同步记录">
          <el-table-column prop="status" label="状态" width="130">
            <template #default="{ row }">
              <el-tag class="status-tag" :type="syncStatusType(row.status)" effect="plain">{{ row.status }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="new_filings" label="新增" width="80" />
          <el-table-column prop="duration_ms" label="耗时" width="100">
            <template #default="{ row }">{{ formatDuration(row.duration_ms) }}</template>
          </el-table-column>
          <el-table-column prop="started_at" label="开始时间" width="180">
            <template #default="{ row }">{{ formatDateTime(row.started_at) }}</template>
          </el-table-column>
          <el-table-column prop="error_message" label="错误" min-width="180" show-overflow-tooltip />
        </el-table>

        <div class="panel-header target-detail-section-title">
          <span>最近公告</span>
          <el-link type="primary" @click="$router.push(`/filings?ticker=${encodeURIComponent(detailTarget.ticker)}`)">查看全部</el-link>
        </div>
        <el-table :data="detailFilings" v-loading="detailLoading" border empty-text="暂无公告，尝试同步该标的">
          <el-table-column prop="filing_type" label="类型" width="90" />
          <el-table-column prop="filing_date" label="Filing Date" width="130">
            <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
          </el-table-column>
          <el-table-column prop="pulled_at" label="同步时间" width="170">
            <template #default="{ row }">{{ formatDateTime(row.pulled_at) }}</template>
          </el-table-column>
          <el-table-column prop="title" label="标题" min-width="200" show-overflow-tooltip />
          <el-table-column label="链接" width="80">
            <template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">打开</el-link></template>
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
const filters = reactive({ ticker: '', status: '' })
const form = reactive({ ticker: '', company_name: '', cik: '', target_type: 'stock', status: 'enabled' })

const policySummary = computed(() => {
  const days = configValue('sec.initial_fetch_days', '30')
  const syncWindow = configValue('sec.sync_window_days', '30')
  const max = configValue('sec.max_fetch_count', '300')
  const full = configValue('sec.fetch_full_history', 'false') === 'true'
  const syncText = syncWindow === '0' ? '每次不限制时间' : `每次最近 ${syncWindow} 天`
  return `${syncText}，首次 ${full ? '完整历史' : `最近 ${days} 天`}，最多 ${max === '0' ? '不限制' : `${max} 条`}`
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
  Object.assign(form, { ticker: '', company_name: '', cik: '', target_type: 'stock', status: 'enabled' })
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
    ElMessage.success('已带出公司名称和 CIK')
  } catch (error) {
    ElMessage.warning('未能自动带出信息，请检查 Ticker 或手动填写')
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
    ElMessage.success('已保存')
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
    ElMessage.success(`同步完成，新增 ${res.data.data.new_filings} 条`)
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
    await ElMessageBox.confirm(`是否现在同步 ${target.ticker} 的 SEC 公告？`, '新增标的已保存', {
      confirmButtonText: '立即同步',
      cancelButtonText: '稍后',
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
  await ElMessageBox.confirm(`删除 ${row.ticker}?`, '确认删除', { type: 'warning' })
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
  if (status === 'success') return '成功'
  if (status === 'failed') return '失败'
  if (status === 'running') return '运行中'
  return '-'
}

function targetStatusLabel(status?: string) {
  if (status === 'enabled') return '已启用'
  if (status === 'disabled') return '已停用'
  return status || '-'
}

function formatDuration(value: number) {
  if (!value) return '-'
  if (value < 1000) return `${value} ms`
  return `${(value / 1000).toFixed(1)} s`
}

function syncIssueTitle(target: WatchTarget) {
  return `${target.ticker} 最近同步失败`
}

function syncIssueSuggestion(target: WatchTarget) {
  const message = target.last_sync_error || ''
  if (message.toLowerCase().includes('cik')) return '建议检查 Ticker 是否正确，或手动补充 CIK 后重试。'
  if (message.toLowerCase().includes('timeout') || message.includes('deadline')) return '看起来像 SEC 请求超时，可以稍后重试或降低最大拉取条数。'
  if (message.toLowerCase().includes('telegram')) return '公告可能已入库，但通知失败；请检查 Telegram 配置。'
  return message || '可先重试该标的；如果继续失败，请查看同步历史中的错误明细。'
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
