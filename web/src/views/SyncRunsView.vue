<template>
  <section class="page">
    <div class="page-header">
      <div>
        <h1>任务执行历史</h1>
        <p>查看除“小盘发现”外的全部调度与手动任务记录；小盘发现保留在“发现日志”的独立工作流中。</p>
      </div>
      <el-button :loading="loading" @click="load">刷新</el-button>
    </div>

    <el-form :inline="true" class="toolbar">
      <el-form-item label="任务">
        <el-select v-model="filters.task_name" clearable filterable placeholder="全部任务" style="width: 260px">
          <el-option v-for="task in loggableTasks" :key="task.task_name" :label="taskLabel(task.task_name)" :value="task.task_name">
            <span>{{ taskLabel(task.task_name) }}</span>
            <span class="option-code">{{ task.task_name }}</span>
          </el-option>
        </el-select>
      </el-form-item>
      <el-form-item label="状态">
        <el-select v-model="filters.status" clearable placeholder="全部状态" style="width: 135px">
          <el-option label="成功" value="success" />
          <el-option label="部分完成" value="partial" />
          <el-option label="已跳过" value="skipped" />
          <el-option label="失败" value="failed" />
          <el-option label="已中断" value="interrupted" />
          <el-option label="运行中" value="running" />
        </el-select>
      </el-form-item>
      <el-form-item label="触发方式">
        <el-select v-model="filters.trigger" clearable placeholder="全部方式" style="width: 135px">
          <el-option label="定时调度" value="scheduled" />
          <el-option label="手动执行" value="manual" />
        </el-select>
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="loading" @click="query">查询</el-button>
        <el-button @click="reset">重置</el-button>
      </el-form-item>
    </el-form>

    <el-table :data="rows" v-loading="loading" border :empty-text="'暂无任务执行记录'">
      <el-table-column label="任务" min-width="245" show-overflow-tooltip>
        <template #default="{ row }">
          <div>{{ taskLabel(row.task_name) }}</div>
          <div class="task-code">{{ row.task_name }}</div>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="115">
        <template #default="{ row }"><el-tag :type="statusType(row.status)" effect="plain">{{ statusLabel(row.status) }}</el-tag></template>
      </el-table-column>
      <el-table-column label="触发方式" width="115">
        <template #default="{ row }"><el-tag type="info" effect="plain">{{ triggerLabel(row.trigger) }}</el-tag></template>
      </el-table-column>
      <el-table-column label="开始时间" width="175">
        <template #default="{ row }">{{ formatDateTime(row.started_at) }}</template>
      </el-table-column>
      <el-table-column label="结束时间" width="175">
        <template #default="{ row }">{{ formatDateTime(row.finished_at) }}</template>
      </el-table-column>
      <el-table-column label="耗时" width="100" align="right">
        <template #default="{ row }">{{ formatDuration(row.duration_ms, row.status) }}</template>
      </el-table-column>
      <el-table-column prop="summary" label="执行摘要" min-width="230" show-overflow-tooltip />
      <el-table-column prop="error_message" label="错误详情" min-width="280" show-overflow-tooltip />
    </el-table>
    <el-pagination class="pagination" layout="total, sizes, prev, pager, next" :total="total" v-model:page-size="pageSize" v-model:current-page="page" @size-change="query" @current-change="load" />

    <el-divider />
    <el-card shadow="never">
      <template #header>
        <div class="legacy-header">
          <div>
            <strong>SEC / IPO 标的同步明细</strong>
            <span>保留原有标的级结果、失败原因与重试操作。</span>
          </div>
          <el-button :loading="legacyLoading" @click="loadLegacyRuns">刷新</el-button>
        </div>
      </template>
      <el-form :inline="true" class="toolbar compact-toolbar">
        <el-form-item label="状态">
          <el-select v-model="legacyFilters.status" clearable placeholder="全部状态" style="width: 150px">
            <el-option label="成功" value="success" /><el-option label="部分完成" value="partial" />
            <el-option label="失败" value="failed" /><el-option label="暂缓" value="deferred" /><el-option label="运行中" value="running" />
          </el-select>
        </el-form-item>
        <el-form-item><el-button type="primary" :loading="legacyLoading" @click="queryLegacyRuns">查询</el-button></el-form-item>
      </el-form>
      <el-table :data="legacyRows" v-loading="legacyLoading" border @expand-change="onLegacyExpand" @current-change="onLegacyCurrentChange">
        <el-table-column type="expand">
          <template #default="{ row: run }">
            <el-table :data="legacyDetails[run.id] || []" border>
              <el-table-column prop="ticker" label="Ticker" width="100" />
              <el-table-column label="状态" width="110"><template #default="{ row: detail }"><el-tag :type="statusType(detail.status)" effect="plain">{{ statusLabel(detail.status) }}</el-tag></template></el-table-column>
              <el-table-column prop="new_filings" label="新增" width="80" align="right" />
              <el-table-column prop="failure_kind" label="失败类别" width="130"><template #default="{ row: detail }">{{ detail.failure_kind || '-' }}</template></el-table-column>
              <el-table-column prop="attempt_count" label="尝试" width="70" align="right" />
              <el-table-column label="下次重试" width="170"><template #default="{ row: detail }">{{ formatDateTime(detail.next_retry_at) }}</template></el-table-column>
              <el-table-column label="耗时" width="90"><template #default="{ row: detail }">{{ formatDuration(detail.duration_ms, detail.status) }}</template></el-table-column>
              <el-table-column prop="error_message" label="错误" min-width="220" show-overflow-tooltip />
              <el-table-column label="操作" width="100" fixed="right"><template #default="{ row: detail }"><el-button v-if="detail.status === 'failed' || detail.status === 'deferred'" link type="primary" :loading="retryingTargetID === detail.target_id" @click="retryTarget(run, detail)">重试</el-button></template></el-table-column>
            </el-table>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="105"><template #default="{ row }"><el-tag :type="statusType(row.status)" effect="plain">{{ statusLabel(row.status) }}</el-tag></template></el-table-column>
        <el-table-column label="来源" width="115"><template #default="{ row }"><el-tag type="info" effect="plain">{{ syncTriggerLabel(row.trigger) }}</el-tag></template></el-table-column>
        <el-table-column label="开始时间" width="175"><template #default="{ row }">{{ formatDateTime(row.started_at) }}</template></el-table-column>
        <el-table-column label="结束时间" width="175"><template #default="{ row }">{{ formatDateTime(row.finished_at) }}</template></el-table-column>
        <el-table-column prop="targets_checked" label="标的数" width="85" align="right" />
        <el-table-column prop="new_filings" label="新增" width="80" align="right" />
        <el-table-column prop="failed_targets" label="失败" width="80" align="right" />
        <el-table-column prop="warning_message" label="警告" min-width="180" show-overflow-tooltip />
        <el-table-column prop="error_message" label="错误" min-width="220" show-overflow-tooltip />
      </el-table>
      <el-pagination class="pagination" layout="total, prev, pager, next" :total="legacyTotal" :page-size="pageSize" v-model:current-page="legacyPage" @current-change="loadLegacyRuns" />
    </el-card>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, PageResult, SyncRun, SyncRunDetail, TaskConfig, TaskExecution } from '@/api/types'

const route = useRoute()
const loading = ref(false)
const rows = ref<TaskExecution[]>([])
const tasks = ref<TaskConfig[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const filters = reactive({ task_name: '', status: '', trigger: '' })
const standaloneDiscoveryTasks = new Set(['small_cap_discovery_sync', 'small_cap_discovery_full_sync'])
const legacyLoading = ref(false)
const legacyRows = ref<SyncRun[]>([])
const legacyTotal = ref(0)
const legacyPage = ref(1)
const legacyFilters = reactive({ status: '' })
const legacyDetails = ref<Record<number, SyncRunDetail[]>>({})
const retryingTargetID = ref<number | null>(null)

const loggableTasks = computed(() => tasks.value.filter((task) => !standaloneDiscoveryTasks.has(task.task_name)))

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<TaskExecution>>>('/task-executions', {
      params: { ...filters, page: page.value, page_size: pageSize.value }
    })
    rows.value = res.data.data.items
    total.value = res.data.data.total
  } finally {
    loading.value = false
  }
}

async function query() {
  page.value = 1
  await load()
}

async function reset() {
  filters.task_name = ''
  filters.status = ''
  filters.trigger = ''
  await query()
}

async function loadLegacyRuns() {
  legacyLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<SyncRun>>>('/sync-runs', { params: { ...legacyFilters, page: legacyPage.value, page_size: pageSize.value } })
    legacyRows.value = res.data.data.items
    legacyTotal.value = res.data.data.total
  } finally {
    legacyLoading.value = false
  }
}

async function queryLegacyRuns() {
  legacyPage.value = 1
  await loadLegacyRuns()
}

async function onLegacyExpand(row: SyncRun) {
  if (legacyDetails.value[row.id]) return
  const res = await apiClient.get<ApiResponse<SyncRunDetail[]>>(`/sync-runs/${row.id}/details`)
  legacyDetails.value = { ...legacyDetails.value, [row.id]: res.data.data }
}

async function onLegacyCurrentChange(row?: SyncRun) {
  if (row) await onLegacyExpand(row)
}

function syncTriggerLabel(trigger: string) {
  const labels: Record<string, string> = { manual: '手动', scheduler: '调度', target: '单标的', ipo_manual: 'IPO 手动', ipo_scheduler: 'IPO 调度' }
  return labels[trigger] || trigger || '-'
}

async function retryTarget(run: SyncRun, detail: SyncRunDetail) {
  retryingTargetID.value = detail.target_id
  try {
    const res = await apiClient.post<ApiResponse<{ new_filings: number }>>(`/watch-targets/${detail.target_id}/sync`)
    ElMessage.success(`已重试 ${detail.ticker}，新增 ${res.data.data.new_filings} 条公告`)
    const nextDetails = { ...legacyDetails.value }
    delete nextDetails[run.id]
    legacyDetails.value = nextDetails
    await onLegacyExpand(run)
    await loadLegacyRuns()
  } finally {
    retryingTargetID.value = null
  }
}

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function formatDuration(value: number, status: string) {
  if (status === 'running') return '进行中'
  if (!value) return '-'
  if (value < 1000) return `${value} ms`
  if (value < 60_000) return `${(value / 1000).toFixed(1)} 秒`
  return `${Math.floor(value / 60_000)} 分 ${Math.round((value % 60_000) / 1000)} 秒`
}

function statusType(status: string) {
  if (status === 'success') return 'success'
  if (status === 'partial' || status === 'interrupted') return 'warning'
  if (status === 'failed') return 'danger'
  if (status === 'running') return 'primary'
  return 'info'
}

function statusLabel(status: string) {
  const labels: Record<string, string> = { success: '成功', partial: '部分完成', skipped: '已跳过', failed: '失败', interrupted: '已中断', running: '运行中' }
  return labels[status] || status || '-'
}

function triggerLabel(trigger: string) {
  return trigger === 'scheduled' ? '定时调度' : trigger === 'manual' ? '手动执行' : trigger || '-'
}

function taskLabel(value: string) {
  const labels: Record<string, string> = {
    watch_target_market_sync: '监控标的每日行情同步', watch_target_earnings_sync: '监控标的财报预告同步',
    sec_filing_sync: 'SEC 公告同步', ipo_radar_sync: 'IPO 新申报扫描', ipo_lifecycle_reconcile_sync: 'IPO 生命周期补查', ipo_offering_reconcile_sync: 'IPO 发行条款重解析', ipo_listing_reconcile_sync: 'IPO 上市状态核验', macro_calendar_sync: '宏观日历同步',
    market_trend_sync: '大盘趋势日线同步', us_futures_sync: '美股期货日线同步',
    longbridge_candidate_research_sync: 'Longbridge P1 候选市场研究', longbridge_candidate_valuation_sync: 'Longbridge P2 候选估值研究',
    longbridge_watch_target_valuation_sync: 'Longbridge 监控标的估值研究', longbridge_watch_target_research_sync: 'Longbridge 监控标的机构持仓研究',
    candidate_notification_sync: '候选通知同步', trade_setup_notification_sync: '交易计划通知同步', notification_retry_sync: '通知重试',
    sqlite_backup: 'SQLite 备份', operation_history_cleanup: '运行历史清理', operational_health_notification_sync: '运行健康告警', institutional_holdings_sync: '机构持仓同步'
  }
  return labels[value] || value
}

onMounted(async () => {
  const requestedTask = typeof route.query.task_name === 'string' ? route.query.task_name : ''
  filters.task_name = standaloneDiscoveryTasks.has(requestedTask) ? '' : requestedTask
  const tasksRes = await apiClient.get<ApiResponse<TaskConfig[]>>('/task-configs')
  tasks.value = tasksRes.data.data
	await Promise.all([load(), loadLegacyRuns()])
})
</script>

<style scoped>
.task-code, .option-code { color: var(--el-text-color-secondary); font-size: 12px; }
.option-code { margin-left: 10px; }
.legacy-header { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.legacy-header span { margin-left: 12px; color: var(--el-text-color-secondary); font-size: 13px; }
.compact-toolbar { margin-bottom: 12px; }
</style>
