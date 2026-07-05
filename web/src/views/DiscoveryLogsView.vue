<template>
  <section class="page">
    <div class="page-header">
      <div>
        <h1>小盘发现日志</h1>
        <p>查看小盘候选发现任务的批次历史、行情 Provider 运行记录和当前健康状态。</p>
      </div>
      <el-button :loading="loading" @click="loadAll">刷新</el-button>
    </div>

    <el-card shadow="never" class="section-card">
      <template #header>
        <div class="card-header">
          <span>最近一次 Market Sync 摘要</span>
          <el-tag effect="plain">本次跑了多少</el-tag>
        </div>
      </template>
      <el-empty v-if="!latestMarketBatch" description="暂无 Market Sync 批次" />
      <el-descriptions v-else :column="4" border>
        <el-descriptions-item label="状态">
          <el-tag :type="batchStatusType(latestMarketBatch.status)" effect="plain">{{ latestMarketBatch.status }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="有效日期">{{ latestMarketBatch.effective_date || '-' }}</el-descriptions-item>
        <el-descriptions-item label="耗时">{{ formatDuration(latestMarketBatch.started_at, latestMarketBatch.completed_at) }}</el-descriptions-item>
        <el-descriptions-item label="候选数">{{ latestMarketBatch.candidate_count ?? 0 }}</el-descriptions-item>
        <el-descriptions-item label="补价进度">
          {{ formatProviderProgress(latestMarketBatch) }}
        </el-descriptions-item>
        <el-descriptions-item label="覆盖率">
          {{ formatPct(latestMarketBatch.provider_summary?.coverage_pct) }}
        </el-descriptions-item>
        <el-descriptions-item label="价格来源" :span="2">
          <span>{{ formatPriceSources(latestMarketBatch.provider_summary?.price_source_counts) }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="Batch ID" :span="4">
          <el-text truncated>{{ latestMarketBatch.batch_id }}</el-text>
        </el-descriptions-item>
      </el-descriptions>
    </el-card>

    <el-card shadow="never" class="section-card">
      <template #header>
        <div class="card-header">
          <span>Provider Health</span>
          <el-tag effect="plain">当前状态</el-tag>
        </div>
      </template>
      <el-table :data="healthRows" v-loading="healthLoading" border empty-text="暂无 Provider Health">
        <el-table-column prop="provider" label="Provider" width="120" />
        <el-table-column prop="status" label="状态" width="120">
          <template #default="{ row }">
            <el-tag :type="providerStatusType(row.status)" effect="plain">{{ row.status || '-' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="last_trade_date" label="最后交易日" width="130" />
        <el-table-column prop="qualified_trading_days" label="合格交易日" width="110" align="right" />
        <el-table-column prop="failure_streak" label="连续失败" width="100" align="right" />
        <el-table-column prop="gold_evidence_ready" label="Gold Ready" width="110">
          <template #default="{ row }">
            <el-tag :type="row.gold_evidence_ready ? 'success' : 'info'" effect="plain">{{ row.gold_evidence_ready ? 'yes' : 'no' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="updated_at" label="更新时间" width="180">
          <template #default="{ row }">{{ formatDateTime(row.updated_at) }}</template>
        </el-table-column>
        <el-table-column prop="gold_sha256" label="Gold SHA" min-width="220" show-overflow-tooltip />
      </el-table>
    </el-card>

    <el-card shadow="never" class="section-card">
      <template #header>
        <div class="card-header">
          <span>Discovery Batches</span>
          <el-form :inline="true" :model="batchFilters" class="inline-filters">
            <el-form-item label="Kind">
              <el-select v-model="batchFilters.kind" clearable style="width: 180px">
                <el-option label="security-universe" value="security-universe" />
                <el-option label="market-prescreen" value="market-prescreen" />
              </el-select>
            </el-form-item>
            <el-form-item label="Status">
              <el-select v-model="batchFilters.status" clearable style="width: 140px">
                <el-option label="published" value="published" />
                <el-option label="failed" value="failed" />
                <el-option label="draft" value="draft" />
                <el-option label="partial" value="partial" />
              </el-select>
            </el-form-item>
            <el-form-item><el-button :loading="batchLoading" @click="queryBatches">查询</el-button></el-form-item>
          </el-form>
        </div>
      </template>
      <el-table :data="batchRows" v-loading="batchLoading" border empty-text="暂无发现批次" @row-click="selectBatch">
        <el-table-column prop="status" label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="batchStatusType(row.status)" effect="plain">{{ row.status }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="kind" label="Kind" width="160" />
        <el-table-column prop="effective_date" label="有效日期" width="110" />
        <el-table-column prop="record_count" label="记录数" width="90" align="right" />
        <el-table-column prop="candidate_count" label="候选数" width="90" align="right" />
        <el-table-column label="补价" width="130" align="right">
          <template #default="{ row }">{{ formatProviderProgress(row) }}</template>
        </el-table-column>
        <el-table-column label="覆盖率" width="90" align="right">
          <template #default="{ row }">{{ formatPct(row.provider_summary?.coverage_pct) }}</template>
        </el-table-column>
        <el-table-column label="价格来源" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">{{ formatPriceSources(row.provider_summary?.price_source_counts) }}</template>
        </el-table-column>
        <el-table-column label="耗时" width="100" align="right">
          <template #default="{ row }">{{ formatDuration(row.started_at, row.completed_at) }}</template>
        </el-table-column>
        <el-table-column prop="started_at" label="开始时间" width="180">
          <template #default="{ row }">{{ formatDateTime(row.started_at) }}</template>
        </el-table-column>
        <el-table-column prop="completed_at" label="结束时间" width="180">
          <template #default="{ row }">{{ formatDateTime(row.completed_at) }}</template>
        </el-table-column>
        <el-table-column prop="batch_id" label="Batch ID" min-width="220" show-overflow-tooltip />
        <el-table-column prop="price_source_version" label="价格版本" min-width="180" show-overflow-tooltip />
        <el-table-column prop="error_message" label="错误" min-width="260" show-overflow-tooltip />
      </el-table>
      <el-pagination class="pagination" layout="total, prev, pager, next" :total="batchTotal" :page-size="pageSize" v-model:current-page="batchPage" @current-change="loadBatches" />
    </el-card>

    <el-card shadow="never" class="section-card">
      <template #header>
        <div class="card-header">
          <span>Provider Runs</span>
          <el-form :inline="true" :model="runFilters" class="inline-filters">
            <el-form-item label="Provider">
              <el-input v-model="runFilters.provider" clearable placeholder="tiingo" style="width: 130px" />
            </el-form-item>
            <el-form-item label="Status">
              <el-select v-model="runFilters.status" clearable style="width: 140px">
                <el-option label="validation" value="validation" />
                <el-option label="active" value="active" />
                <el-option label="degraded" value="degraded" />
                <el-option label="failed" value="failed" />
              </el-select>
            </el-form-item>
            <el-form-item label="Batch">
              <el-input v-model="runFilters.batch_id" clearable placeholder="点击批次可带入" style="width: 220px" />
            </el-form-item>
            <el-form-item><el-button :loading="runLoading" @click="queryRuns">查询</el-button></el-form-item>
          </el-form>
        </div>
      </template>
      <el-table :data="runRows" v-loading="runLoading" border empty-text="暂无 Provider Run">
        <el-table-column prop="provider" label="Provider" width="110" />
        <el-table-column prop="status" label="状态" width="120">
          <template #default="{ row }">
            <el-tag :type="providerStatusType(row.status)" effect="plain">{{ row.status }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="effective_date" label="有效日期" width="170">
          <template #default="{ row }">{{ formatDate(row.effective_date) }}</template>
        </el-table-column>
        <el-table-column prop="record_count" label="记录" width="80" align="right" />
        <el-table-column prop="expected_count" label="预期" width="80" align="right" />
        <el-table-column prop="coverage_pct" label="覆盖率" width="90" align="right">
          <template #default="{ row }">{{ formatPct(row.coverage_pct) }}</template>
        </el-table-column>
        <el-table-column prop="timely" label="及时" width="80">
          <template #default="{ row }">
            <el-tag :type="row.timely ? 'success' : 'warning'" effect="plain">{{ row.timely ? 'yes' : 'no' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="created_at" label="创建时间" width="180">
          <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column prop="batch_id" label="Batch ID" min-width="220" show-overflow-tooltip />
        <el-table-column prop="source_version" label="Source Version" min-width="220" show-overflow-tooltip />
        <el-table-column prop="error_message" label="错误" min-width="240" show-overflow-tooltip />
      </el-table>
      <el-pagination class="pagination" layout="total, prev, pager, next" :total="runTotal" :page-size="pageSize" v-model:current-page="runPage" @current-change="loadRuns" />
    </el-card>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { apiClient } from '@/api/client'
import type { ApiResponse, DiscoveryBatch, PageResult, ProviderHealth, ProviderHealthPage, ProviderRun } from '@/api/types'

const pageSize = 20
const loading = ref(false)

const healthLoading = ref(false)
const healthRows = ref<ProviderHealth[]>([])

const batchLoading = ref(false)
const batchRows = ref<DiscoveryBatch[]>([])
const batchTotal = ref(0)
const batchPage = ref(1)
const batchFilters = reactive({ kind: '', status: '' })
const latestMarketBatch = ref<DiscoveryBatch | null>(null)

const runLoading = ref(false)
const runRows = ref<ProviderRun[]>([])
const runTotal = ref(0)
const runPage = ref(1)
const runFilters = reactive({ provider: '', status: '', batch_id: '' })

async function loadHealth() {
  healthLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<ProviderHealthPage>>('/discovery/provider-health')
    healthRows.value = res.data.data.items
  } finally {
    healthLoading.value = false
  }
}

async function loadLatestMarketBatch() {
  const res = await apiClient.get<ApiResponse<PageResult<DiscoveryBatch>>>('/discovery/batches', {
    params: { kind: 'market-prescreen', page: 1, page_size: 1 }
  })
  latestMarketBatch.value = res.data.data.items[0] || null
}

async function loadBatches() {
  batchLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<DiscoveryBatch>>>('/discovery/batches', {
      params: { ...batchFilters, page: batchPage.value, page_size: pageSize }
    })
    batchRows.value = res.data.data.items
    batchTotal.value = res.data.data.total
  } finally {
    batchLoading.value = false
  }
}

async function loadRuns() {
  runLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<ProviderRun>>>('/discovery/provider-runs', {
      params: { ...runFilters, page: runPage.value, page_size: pageSize }
    })
    runRows.value = res.data.data.items
    runTotal.value = res.data.data.total
  } finally {
    runLoading.value = false
  }
}

async function loadAll() {
  loading.value = true
  try {
    await Promise.all([loadHealth(), loadLatestMarketBatch(), loadBatches(), loadRuns()])
  } finally {
    loading.value = false
  }
}

function queryBatches() {
  batchPage.value = 1
  return loadBatches()
}

function queryRuns() {
  runPage.value = 1
  return loadRuns()
}

function selectBatch(row: DiscoveryBatch) {
  runFilters.batch_id = row.batch_id
  runPage.value = 1
  loadRuns()
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
  return date.toLocaleDateString()
}

function formatPct(value?: number) {
  if (value === undefined || value === null) return '-'
  return `${value.toFixed(1)}%`
}

function formatProviderProgress(row?: DiscoveryBatch | null) {
  const summary = row?.provider_summary
  if (!summary) return '-'
  return `${summary.record_count}/${summary.expected_count}`
}

function formatPriceSources(counts?: Record<string, number> | null) {
  if (!counts || Object.keys(counts).length === 0) return '-'
  return Object.entries(counts)
    .sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]))
    .map(([source, count]) => `${source}: ${count}`)
    .join(' / ')
}

function formatDuration(startedAt?: string | null, completedAt?: string | null) {
  if (!startedAt || !completedAt) return '-'
  const started = new Date(startedAt)
  const completed = new Date(completedAt)
  if (Number.isNaN(started.getTime()) || Number.isNaN(completed.getTime())) return '-'
  const seconds = Math.max(0, Math.round((completed.getTime() - started.getTime()) / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  const restSeconds = seconds % 60
  if (minutes < 60) return restSeconds ? `${minutes}m ${restSeconds}s` : `${minutes}m`
  const hours = Math.floor(minutes / 60)
  const restMinutes = minutes % 60
  return restMinutes ? `${hours}h ${restMinutes}m` : `${hours}h`
}

function batchStatusType(status?: string) {
  if (status === 'published') return 'success'
  if (status === 'failed') return 'danger'
  if (status === 'partial') return 'warning'
  return 'info'
}

function providerStatusType(status?: string) {
  if (status === 'active') return 'success'
  if (status === 'degraded') return 'warning'
  if (status === 'failed') return 'danger'
  return 'info'
}

onMounted(loadAll)
</script>

<style scoped>
.section-card {
  margin-bottom: 16px;
}

.card-header {
  align-items: center;
  display: flex;
  gap: 12px;
  justify-content: space-between;
}

.inline-filters {
  margin-bottom: -18px;
}
</style>
