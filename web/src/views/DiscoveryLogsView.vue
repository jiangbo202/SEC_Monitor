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
          <span>行情 Provider 配置与可观测性</span>
          <el-tag effect="plain">只读取本地记录，不请求行情接口</el-tag>
        </div>
      </template>
      <el-skeleton v-if="observabilityLoading" :rows="3" animated />
      <template v-else-if="providerObservability">
        <el-descriptions :column="4" border>
          <el-descriptions-item label="行情源链路">{{ providerObservability.price_provider_chain || '未配置' }}</el-descriptions-item>
          <el-descriptions-item label="链路健康">
            <el-tag :type="providerStatusType(providerObservability.chain_health?.status)" effect="plain">{{ providerObservability.chain_health?.status || '-' }}</el-tag>
            <span v-if="providerObservability.chain_health"> · 连续失败 {{ providerObservability.chain_health.failure_streak }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="最新运行有效日">{{ formatDate(providerObservability.latest_run?.effective_date) }}</el-descriptions-item>
          <el-descriptions-item label="最新覆盖率">{{ formatPct(providerObservability.latest_run?.coverage_pct) }}</el-descriptions-item>
          <el-descriptions-item label="本次切换">
            <el-tag v-if="!providerObservability.latest_run?.provider_attempts?.length" type="info" effect="plain">历史批次无逐源记录</el-tag>
            <el-tag v-else-if="providerObservability.latest_run.fallback_used" type="warning" effect="plain">已启用备源</el-tag>
            <el-tag v-else type="success" effect="plain">主源完成</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="交易日历">
            {{ providerObservability.calendar_version }}
            <el-tag v-for="year in providerObservability.calendar_years" :key="year.year" size="small" :type="year.complete ? 'success' : 'warning'" effect="plain" class="calendar-year-tag">
              {{ year.year }} {{ year.complete ? '完整' : '未完整' }}
            </el-tag>
            <span v-if="providerObservability.calendar_years.length === 0">暂无本地日历</span>
          </el-descriptions-item>
          <el-descriptions-item label="最近来源构成" :span="4">
            {{ formatPriceSources(providerObservability.latest_price_source_counts) }}
          </el-descriptions-item>
        </el-descriptions>
        <el-alert class="provider-budget-notice" type="info" :closable="false" :title="providerObservability.budget_notice" show-icon />
        <el-table :data="providerObservability.providers" border empty-text="尚未配置行情 Provider">
          <el-table-column prop="provider" label="Provider" width="130" />
          <el-table-column label="凭据" width="110">
            <template #default="{ row }"><el-tag :type="row.configured_credential ? 'success' : 'warning'" effect="plain">{{ row.configured_credential ? '已配置' : '缺失' }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="token_count" label="Token 数" width="100" align="right">
            <template #default="{ row }">{{ row.token_count || '-' }}</template>
          </el-table-column>
          <el-table-column label="本地单次预算" width="150" align="right">
            <template #default="{ row }">{{ formatLocalBudget(row.local_request_budget, row.budget_scope) }}</template>
          </el-table-column>
          <el-table-column prop="latest_source_record_count" label="最近写入记录" width="130" align="right" />
          <el-table-column label="最近尝试" width="120">
            <template #default="{ row }"><el-tag :type="providerAttemptType(row.latest_attempt?.status)" effect="plain">{{ providerAttemptLabel(row.latest_attempt?.status) }}</el-tag></template>
          </el-table-column>
          <el-table-column label="尝试结果" width="130" align="right">
            <template #default="{ row }">{{ formatAttemptProgress(row.latest_attempt) }}</template>
          </el-table-column>
          <el-table-column label="耗时" width="100" align="right">
            <template #default="{ row }">{{ formatMilliseconds(row.latest_attempt?.elapsed_ms) }}</template>
          </el-table-column>
          <el-table-column label="降级原因" min-width="220" show-overflow-tooltip>
            <template #default="{ row }">{{ row.latest_attempt?.error_message || '-' }}</template>
          </el-table-column>
          <el-table-column label="健康状态" width="120">
            <template #default="{ row }"><el-tag :type="providerStatusType(row.health?.status)" effect="plain">{{ row.health?.status || '-' }}</el-tag></template>
          </el-table-column>
          <el-table-column label="最后交易日" width="130"><template #default="{ row }">{{ row.health?.last_trade_date || '-' }}</template></el-table-column>
          <el-table-column label="连续失败" width="100" align="right"><template #default="{ row }">{{ row.health?.failure_streak ?? '-' }}</template></el-table-column>
        </el-table>
      </template>
      <el-empty v-else description="暂无行情可观测性数据" :image-size="48" />
    </el-card>

    <el-card shadow="never" class="section-card">
      <template #header>
        <div class="card-header">
          <span>公司资料补偿队列</span>
          <el-space>
            <el-tag type="info" effect="plain">仅当前候选 · 本地失败记录</el-tag>
            <el-button type="primary" plain :loading="profileBulkRetrying" :disabled="profileRecoveryQueue.items.length === 0" @click="retryCompanyProfileQueue">一键重试</el-button>
            <el-button link type="primary" :loading="profileRecoveryLoading" @click="loadProfileRecoveryQueue">刷新</el-button>
          </el-space>
        </div>
      </template>
      <el-alert type="info" :closable="false" show-icon title="Longbridge 公司资料失败后会按退避时间自动补偿；单只重试仅请求这一家，一键重试按队列顺序受预算执行，均不会重跑 SEC 或市场价格流程。" class="profile-recovery-notice" />
      <el-empty v-if="!profileRecoveryLoading && profileRecoveryQueue.items.length === 0" description="当前候选没有待补偿的公司资料" :image-size="48" />
      <el-table v-else :data="profileRecoveryQueue.items" v-loading="profileRecoveryLoading" border>
        <el-table-column prop="ticker" label="Ticker" width="105" />
        <el-table-column prop="company_name" label="公司" min-width="180" show-overflow-tooltip />
        <el-table-column label="状态" width="115">
          <template #default="{ row }"><el-tag :type="row.retry_due ? 'warning' : 'info'" effect="plain">{{ row.retry_due ? '可自动补偿' : '等待退避' }}</el-tag></template>
        </el-table-column>
        <el-table-column prop="retry_count" label="失败次数" width="100" align="right" />
        <el-table-column label="下次自动重试" width="180"><template #default="{ row }">{{ row.retry_due ? '下次同步优先补偿' : formatDateTime(row.next_retry_at) }}</template></el-table-column>
        <el-table-column label="最近尝试" width="180"><template #default="{ row }">{{ formatDateTime(row.last_attempt_at) }}</template></el-table-column>
        <el-table-column prop="last_error" label="最近错误（已脱敏）" min-width="260" show-overflow-tooltip />
        <el-table-column label="操作" width="100" fixed="right">
          <template #default="{ row }"><el-button link type="primary" :loading="profileRetryTicker === row.ticker" @click="retryCompanyProfile(row)">立即重试</el-button></template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-card shadow="never" class="section-card">
      <template #header>
        <div class="card-header">
          <span>行情补偿队列</span>
          <el-space>
            <el-tag type="info" effect="plain">仅当前 A/B 候选 · 本地诊断</el-tag>
            <el-button link type="primary" :loading="marketRecoveryLoading" @click="loadMarketRecoveryQueue">刷新</el-button>
          </el-space>
        </div>
      </template>
      <el-alert type="info" :closable="false" show-icon title="只列出缺价、过期价或本地回退价。单只“补齐日线并重算”只请求该标的、写入本地日线，并发布只替换该标的行情与分数的可追溯市场修正批次；不会重跑 SEC 或全量市场扫描。" class="profile-recovery-notice" />
      <el-empty v-if="!marketRecoveryLoading && marketRecoveryQueue.items.length === 0" description="当前候选的行情证据正常" :image-size="48" />
      <el-table v-else :data="marketRecoveryQueue.items" v-loading="marketRecoveryLoading" border>
        <el-table-column prop="ticker" label="Ticker" width="105" />
        <el-table-column prop="grade" label="等级" width="90" />
        <el-table-column prop="issue_label" label="原因" min-width="150" />
        <el-table-column label="价格日期" width="150"><template #default="{ row }">{{ formatDate(row.price_trade_date) }}</template></el-table-column>
        <el-table-column prop="price_source" label="当前来源" width="130"><template #default="{ row }">{{ row.price_source || '-' }}</template></el-table-column>
        <el-table-column label="状态" width="130"><template #default="{ row }"><el-tag type="warning" effect="plain">{{ priceFreshnessLabel(row.price_freshness_status) }}</el-tag></template></el-table-column>
        <el-table-column label="操作" width="120" fixed="right">
          <template #default="{ row }"><el-button link type="primary" :loading="marketRetryTicker === row.ticker" @click="refreshCandidateMarketHistory(row)">补齐并重算</el-button></template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-card shadow="never" class="section-card">
      <template #header>
        <div class="card-header">
          <span>同步工作流</span>
          <el-tag effect="plain">步骤进度与运行日志</el-tag>
        </div>
      </template>
      <el-table :data="syncRows" v-loading="syncLoading" border empty-text="暂无同步工作流" row-key="id" @expand-change="loadSyncSteps">
        <el-table-column type="expand" width="48">
          <template #default="{ row }">
            <el-empty v-if="syncSteps[row.id]?.length === 0" description="尚未记录步骤" :image-size="48" />
            <el-timeline v-else class="sync-step-timeline">
              <el-timeline-item v-for="step in syncSteps[row.id] || []" :key="step.id" :type="syncStepType(step.status)" :timestamp="formatDateTime(step.started_at)">
                <div class="sync-step-title">{{ phaseLabel(step.phase) }} <el-tag size="small" :type="syncStepType(step.status)" effect="plain">{{ step.status }}</el-tag></div>
                <div>{{ step.message }}</div>
                <div v-if="step.record_count" class="sync-step-meta">记录数：{{ step.record_count }} · 耗时：{{ formatDuration(step.started_at, step.completed_at) }}</div>
              </el-timeline-item>
            </el-timeline>
          </template>
        </el-table-column>
        <el-table-column prop="status" label="状态" width="105">
          <template #default="{ row }"><el-tag :type="syncStepType(row.status)" effect="plain">{{ row.status }}</el-tag></template>
        </el-table-column>
        <el-table-column prop="kind" label="模式" width="120"><template #default="{ row }">{{ syncKindLabel(row.kind) }}</template></el-table-column>
        <el-table-column prop="phase" label="当前步骤" width="190"><template #default="{ row }">{{ phaseLabel(row.phase) }}</template></el-table-column>
        <el-table-column prop="started_at" label="开始时间" width="180"><template #default="{ row }">{{ formatDateTime(row.started_at) }}</template></el-table-column>
        <el-table-column label="耗时" width="100" align="right"><template #default="{ row }">{{ formatDuration(row.started_at, row.completed_at) }}</template></el-table-column>
        <el-table-column prop="security_batch_id" label="SEC Batch" min-width="180" show-overflow-tooltip />
        <el-table-column prop="market_batch_id" label="Market Batch" min-width="180" show-overflow-tooltip />
        <el-table-column prop="error_message" label="错误" min-width="260" show-overflow-tooltip />
      </el-table>
      <el-pagination class="pagination" layout="total, prev, pager, next" :total="syncTotal" :page-size="pageSize" v-model:current-page="syncPage" @current-change="loadSyncRuns" />
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
        <el-table-column type="expand" width="48">
          <template #default="{ row }">
            <el-empty v-if="!row.provider_attempts?.length" description="该历史运行没有逐源尝试记录" :image-size="36" />
            <el-table v-else :data="row.provider_attempts" size="small" border class="attempt-table">
              <el-table-column prop="provider" label="Provider" width="130" />
              <el-table-column label="结果" width="110"><template #default="{ row: attempt }"><el-tag :type="providerAttemptType(attempt.status)" effect="plain">{{ providerAttemptLabel(attempt.status) }}</el-tag></template></el-table-column>
              <el-table-column label="记录 / 预期" width="120" align="right"><template #default="{ row: attempt }">{{ formatAttemptProgress(attempt) }}</template></el-table-column>
              <el-table-column prop="remaining" label="待补齐" width="90" align="right" />
              <el-table-column label="耗时" width="100" align="right"><template #default="{ row: attempt }">{{ formatMilliseconds(attempt.elapsed_ms) }}</template></el-table-column>
              <el-table-column prop="source_version" label="Source Version" min-width="190" show-overflow-tooltip />
              <el-table-column prop="error_message" label="失败 / 降级原因" min-width="240" show-overflow-tooltip><template #default="{ row: attempt }">{{ attempt.error_message || '-' }}</template></el-table-column>
            </el-table>
          </template>
        </el-table-column>
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
        <el-table-column label="备源" width="100"><template #default="{ row }"><el-tag v-if="!row.provider_attempts?.length" type="info" effect="plain">历史记录</el-tag><el-tag v-else :type="row.fallback_used ? 'warning' : 'success'" effect="plain">{{ row.fallback_used ? '已使用' : '未使用' }}</el-tag></template></el-table-column>
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
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, CompanyProfileBulkRetryResult, CompanyProfileRecoveryItem, CompanyProfileRecoveryQueue, DiscoveryBatch, DiscoverySyncRun, DiscoverySyncRunPage, DiscoverySyncStep, MarketPriceRecoveryItem, MarketPriceRecoveryQueue, PageResult, ProviderAttempt, ProviderHealth, ProviderHealthPage, ProviderObservability, ProviderRun } from '@/api/types'

const pageSize = 20
const route = useRoute()
const loading = ref(false)

const healthLoading = ref(false)
const healthRows = ref<ProviderHealth[]>([])

const observabilityLoading = ref(false)
const providerObservability = ref<ProviderObservability | null>(null)

const profileRecoveryLoading = ref(false)
const profileRecoveryQueue = ref<CompanyProfileRecoveryQueue>({ items: [] })
const profileRetryTicker = ref('')
const profileBulkRetrying = ref(false)

const marketRecoveryLoading = ref(false)
const marketRecoveryQueue = ref<MarketPriceRecoveryQueue>({ batch_id: '', effective_date: '', items: [] })
const marketRetryTicker = ref('')

const batchLoading = ref(false)
const batchRows = ref<DiscoveryBatch[]>([])
const batchTotal = ref(0)
const batchPage = ref(1)
const batchFilters = reactive({ kind: '', status: '' })
const latestMarketBatch = ref<DiscoveryBatch | null>(null)

const syncLoading = ref(false)
const syncRows = ref<DiscoverySyncRun[]>([])
const syncTotal = ref(0)
const syncPage = ref(1)
const syncSteps = reactive<Record<number, DiscoverySyncStep[]>>({})

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

async function loadProviderObservability() {
  observabilityLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<ProviderObservability>>('/discovery/provider-observability')
    providerObservability.value = res.data.data
  } finally {
    observabilityLoading.value = false
  }
}

async function loadProfileRecoveryQueue() {
  profileRecoveryLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<CompanyProfileRecoveryQueue>>('/discovery/company-profiles/recovery-queue')
    profileRecoveryQueue.value = res.data.data
  } finally {
    profileRecoveryLoading.value = false
  }
}

async function retryCompanyProfile(row: CompanyProfileRecoveryItem) {
  profileRetryTicker.value = row.ticker
  try {
    await apiClient.post(`/discovery/company-profiles/${encodeURIComponent(row.ticker)}/retry`, null, { params: { cik: row.cik || undefined } })
    ElMessage.success(`${row.ticker} 公司资料已更新`)
    await loadProfileRecoveryQueue()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || `${row.ticker} 公司资料重试失败`)
    await loadProfileRecoveryQueue()
  } finally {
    profileRetryTicker.value = ''
  }
}

async function retryCompanyProfileQueue() {
  try {
    await ElMessageBox.confirm(
      `将按当前队列顺序重试 ${profileRecoveryQueue.value.items.length} 家公司；请求数受系统配置的公司资料预算限制，连续相同上游错误会自动停止。`,
      '一键重试公司资料',
      { confirmButtonText: '开始重试', cancelButtonText: '取消', type: 'warning' }
    )
  } catch {
    return
  }
  profileBulkRetrying.value = true
  try {
    const res = await apiClient.post<ApiResponse<CompanyProfileBulkRetryResult>>('/discovery/company-profiles/recovery-queue/retry')
    const result = res.data.data
    const message = result.message || `已尝试 ${result.attempted} 家，成功 ${result.fetched} 家，失败 ${result.failed} 家`
    if (result.stopped || result.failed > 0) ElMessage.warning(message)
    else ElMessage.success(message)
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '公司资料一键重试失败')
  } finally {
    profileBulkRetrying.value = false
    await loadProfileRecoveryQueue()
  }
}

async function loadMarketRecoveryQueue() {
  marketRecoveryLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<MarketPriceRecoveryQueue>>('/discovery/candidates/market-price-recovery-queue')
    marketRecoveryQueue.value = res.data.data
  } finally {
    marketRecoveryLoading.value = false
  }
}

async function refreshCandidateMarketHistory(row: MarketPriceRecoveryItem) {
  marketRetryTicker.value = row.ticker
  try {
    const res = await apiClient.post<ApiResponse<{ history: { persisted_count: number }, reprice: { batch_id: string } }>>(`/discovery/candidates/${encodeURIComponent(row.ticker)}/market-history-refresh`)
    ElMessage.success(`${row.ticker} 已补齐 ${res.data.data.history?.persisted_count || 0} 条日线并发布市场修正批次`)
    await loadMarketRecoveryQueue()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || `${row.ticker} 行情补齐失败`)
    await loadMarketRecoveryQueue()
  } finally {
    marketRetryTicker.value = ''
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

async function loadSyncRuns() {
  syncLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<DiscoverySyncRunPage>>('/discovery/sync-runs', {
		  params: { page: syncPage.value, page_size: pageSize, kind: typeof route.query.kind === 'string' ? route.query.kind : '' }
    })
    syncRows.value = res.data.data.items
    syncTotal.value = res.data.data.total
  } finally {
    syncLoading.value = false
  }
}

async function loadSyncSteps(row: DiscoverySyncRun, expandedRows: DiscoverySyncRun[]) {
  if (!expandedRows.some((item) => item.id === row.id) || syncSteps[row.id]) return
  const res = await apiClient.get<ApiResponse<DiscoverySyncStep[]>>(`/discovery/sync-runs/${row.id}/steps`)
  syncSteps[row.id] = res.data.data
}

async function loadAll() {
  loading.value = true
  try {
    await Promise.all([loadHealth(), loadProviderObservability(), loadProfileRecoveryQueue(), loadMarketRecoveryQueue(), loadLatestMarketBatch(), loadSyncRuns(), loadBatches(), loadRuns()])
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
    .map(([source, count]) => `${source === 'local-cache' ? '本地前日回退' : source}: ${count}`)
    .join(' / ')
}

function formatLocalBudget(budget: number, scope: string) {
  if (scope === 'provider_managed') return '供应商侧管理'
  if (scope === 'download') return '下载源，无固定上限'
  if (scope === 'unknown') return '-'
  return budget > 0 ? `${budget} 请求` : '未限额'
}

function providerAttemptType(status?: string) {
  if (status === 'success') return 'success'
  if (status === 'partial' || status === 'empty') return 'warning'
  if (status === 'failed') return 'danger'
  return 'info'
}

function providerAttemptLabel(status?: string) {
  const labels: Record<string, string> = { success: '完成', partial: '部分完成', empty: '无可用数据', failed: '失败' }
  return labels[status || ''] || status || '未运行'
}

function formatAttemptProgress(attempt?: ProviderAttempt | null) {
  if (!attempt) return '-'
  return `${attempt.records}/${attempt.expected}`
}

function formatMilliseconds(value?: number | null) {
  if (value === undefined || value === null) return '-'
  if (value < 1000) return `${value} ms`
  return `${(value / 1000).toFixed(1)} s`
}

function priceFreshnessLabel(status?: string) {
  const labels: Record<string, string> = { missing: '缺失', stale: '过期', future: '日期异常', previous_trading_day: '上一交易日' }
  return labels[status || ''] || status || '-'
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

function syncStepType(status?: string) {
  if (status === 'published' || status === 'completed') return 'success'
  if (status === 'failed') return 'danger'
  if (status === 'warning') return 'warning'
  return 'info'
}

function syncKindLabel(kind?: string) {
  if (kind === 'incremental') return '每日增量'
  if (kind === 'full') return '全量校准'
  if (kind === 'market') return '仅行情'
  if (kind === 'market-force') return '强制补齐收盘价'
  return kind || '-'
}

function phaseLabel(phase?: string) {
  const labels: Record<string, string> = {
    prepare: '准备与缓存清理', build_sources: '装载数据源', security_universe: 'SEC 全量宇宙',
    incremental_sec_refresh: 'SEC 增量财务', incremental_listing_discovery: '新增上市标的发现', market_prescreen: '行情与市值预筛',
    technical_history: '技术指标历史', publish_summary: '日报归档与健康检查', completed: '已完成', failed: '失败'
  }
  const checkpointLabels: Record<string, string> = {
    'security-listings': '上市标的清单',
    'security-universe': '公司身份与分类',
    'security-listing-classification': '上市标的分类结果',
    'financial-facts': '财务事实',
    'financial-metrics': '财务指标',
    'historical-shares': '历史股本',
    'insider-transactions': 'Form 4 内幕交易',
    'insider-coverage': 'Form 4 覆盖情况',
    'sec-filing-index': 'SEC 文件索引',
    'capital-risks': '融资风险',
    'security-validation': '发布前校验'
  }
  if (phase?.startsWith('checkpoint:')) {
    const checkpoint = phase.slice('checkpoint:'.length)
    return `恢复点 · ${checkpointLabels[checkpoint] || checkpoint}`
  }
  return labels[phase || ''] || phase || '-'
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

.sync-step-timeline {
  margin: 8px 0 0;
}

.sync-step-title {
  font-weight: 600;
  margin-bottom: 4px;
}

.sync-step-meta {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  margin-top: 4px;
}

.provider-budget-notice {
  margin: 12px 0;
}

.attempt-table {
  margin: 8px 12px;
  width: calc(100% - 24px);
}

.profile-recovery-notice {
  margin-bottom: 12px;
}

.calendar-year-tag {
  margin-left: 6px;
}
</style>
