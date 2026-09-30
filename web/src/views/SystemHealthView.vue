<template>
  <section class="page">
    <div class="page-header">
      <div>
        <h1>{{ t('pages.systemHealth.title') }}</h1>
        <p class="page-subtitle">{{ t('pages.systemHealth.subtitle') }}</p>
      </div>
      <el-button :loading="loading" type="primary" @click="load(true)">{{ t('pages.systemHealth.refresh') }}</el-button>
    </div>

    <div class="kpi-grid">
      <el-card shadow="never" class="kpi-card">
        <div class="metric">
          <span>{{ t('common.status') }}</span>
          <strong>{{ summary ? (summary.status === 'ok' ? t('pages.systemHealth.statusOk') : t('pages.systemHealth.statusWarning')) : '读取中' }}</strong>
          <span v-if="operational?.tasks.some(task => task.enabled && task.last_status === 'partial')">有任务部分完成，请查看下方运行健康</span>
        </div>
      </el-card>
      <el-card shadow="never" class="kpi-card">
        <div class="metric">
          <span>{{ t('pages.systemHealth.targets') }}</span>
          <strong>{{ summary ? `${summary.enabled_targets} / ${summary.target_total}` : '读取中' }}</strong>
        </div>
      </el-card>
      <el-card shadow="never" class="kpi-card">
        <div class="metric">
          <span>{{ t('pages.systemHealth.filings') }}</span>
          <strong>{{ summary?.filing_total ?? '读取中' }}</strong>
        </div>
      </el-card>
      <el-card shadow="never" class="kpi-card">
        <div class="metric">
          <span>{{ t('pages.systemHealth.notificationFailures') }}</span>
          <strong>{{ summary?.notification_failures ?? '读取中' }}</strong>
			<span v-if="health && !health.telegram_enabled">Telegram 未启用；零失败不等于已验证投递</span>
			<span v-for="(channel, name) in operational?.notification_delivery || {}" :key="name">{{ name }} · 14 日投递批次 {{ channel.sent_batches }} / {{ channel.attempted_batches }} · {{ channel.failed_batches }} 失败</span>
        </div>
      </el-card>
    </div>

    <div class="dashboard-grid">
      <el-card shadow="never" class="dashboard-panel">
        <template #header>当前需要处理 · {{ consolidatedIssues.length }} 项</template>
        <div v-if="consolidatedIssues.length" class="health-alert-grid">
          <el-alert
            v-for="item in consolidatedIssues"
            :key="item.key"
            :title="item.title"
            :description="item.detail"
            :type="healthAlertType(item.level)"
            :closable="false"
            show-icon
          >
            <template #default>
              <el-space wrap :size="6">
                <el-tag v-if="item.category" size="small" type="info" effect="plain">影响：{{ issueImpact(item.category) }}</el-tag>
                <el-tag v-if="item.category" size="small" :type="item.manual ? 'warning' : 'success'" effect="plain">{{ item.manual ? '需要人工确认' : '自动任务将继续重试' }}</el-tag>
                <el-button v-if="item.action" type="primary" link @click="openSourceAction(item.action)">{{ t('pages.systemHealth.viewAction') }}</el-button>
              </el-space>
            </template>
          </el-alert>
        </div>
        <el-empty v-else description="系统与运行任务当前均无待处理项" />
      </el-card>

      <el-card shadow="never" class="dashboard-panel">
        <template #header>{{ t('pages.systemHealth.database') }}</template>
        <el-descriptions :column="1" border>
          <el-descriptions-item :label="t('common.type')">{{ health?.database_type || (loading ? '读取中' : '-') }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.systemHealth.databaseSize')">{{ health ? formatBytes(health.database_size_bytes) : (loading ? '读取中' : '-') }}</el-descriptions-item>
          <el-descriptions-item label="路径">{{ health?.database_path || (loading ? '读取中' : '-') }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.systemHealth.storageUsage')">
            {{ health?.storage ? `${health.storage.used_pct}% · ${formatBytes(health.storage.used_bytes)} / ${formatBytes(health.storage.total_bytes)}` : (loading ? '读取中' : '-') }}
          </el-descriptions-item>
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
        <el-skeleton v-else-if="loading" :rows="2" animated />
        <el-empty v-else :description="t('pages.dashboard.noSyncRuns')" />
      </el-card>

      <el-card shadow="never" class="dashboard-panel">
        <template #header>{{ t('pages.systemHealth.sqliteBackup') }}</template>
        <div v-if="health?.backup?.latest_completed" class="status-block">
          <el-tag type="success" effect="plain">{{ t('pages.systemHealth.backupReady') }}</el-tag>
          <span>{{ formatDateTime(health.backup.latest_completed) }}</span>
          <span>{{ t('pages.systemHealth.backupPairs', { count: health.backup.complete_pairs }) }}</span>
			<span>备份占用 {{ formatBytes(health.backup.total_bytes) }}（最新一组 {{ formatBytes(health.backup.latest_pair_bytes) }}）</span>
			<span v-for="(bytes, name) in health.backup.database_bytes || {}" :key="name">{{ name }} 数据库 {{ formatBytes(bytes) }}</span>
			<span>本地及副本合计 {{ formatBytes(health.backup.combined_backup_bytes || health.backup.total_bytes) }} · 每目录容量预算 {{ health.backup.capacity_budget_bytes ? formatBytes(health.backup.capacity_budget_bytes) : '未启用' }}</span>
			<span v-if="health.backup.replica?.enabled">副本故障域：{{ health.backup.replica.failure_domain === 'same_filesystem' ? '同一文件系统，不具备异盘容灾' : health.backup.replica.failure_domain === 'different_filesystem' ? '不同文件系统，仍需验证异地恢复' : '尚未确认' }}</span>
          <span v-if="health.backup.incomplete_pairs">{{ t('pages.systemHealth.backupIncompletePairs', { count: health.backup.incomplete_pairs }) }}</span>
          <span><el-tag :type="health.backup.replica?.status === 'ready' ? 'info' : 'warning'" effect="plain">备份副本 {{ health.backup.replica?.enabled ? (health.backup.replica.status === 'ready' ? '文件齐全' : '需处理') : '未配置' }}</el-tag></span>
          <span v-if="health.backup.replica?.latest_completed">最近副本 {{ formatDateTime(health.backup.replica.latest_completed) }} · {{ health.backup.replica.complete_pairs }} 组（同盘目录不等于异地容灾）</span>
          <span v-if="health.recovery_drill?.id">{{ t('pages.systemHealth.lastRecoveryDrill', { status: health.recovery_drill.status, time: formatDateTime(health.recovery_drill.started_at) }) }}</span>
          <el-tooltip v-if="health.recovery_drill?.id" :content="health.recovery_drill.local_reason || '最近一次本地隔离恢复演练'">
            <el-tag :type="health.recovery_drill.local_status === 'ready' ? 'success' : 'warning'">本地恢复 {{ recoveryStatusLabel(health.recovery_drill.local_status) }}</el-tag>
          </el-tooltip>
          <el-tooltip v-if="health.recovery_drill?.id" :content="health.recovery_drill.replica_reason || '最近一次副本隔离恢复演练'">
            <el-tag :type="health.recovery_drill.replica_status === 'ready' ? 'success' : 'warning'">副本恢复 {{ recoveryStatusLabel(health.recovery_drill.replica_status) }}</el-tag>
          </el-tooltip>
          <el-button size="small" :loading="verifyingBackup" @click="verifyLatestBackup">{{ t('pages.systemHealth.recoveryCheck') }}</el-button>
			<el-button size="small" type="warning" :loading="compacting" @click="compactDatabases">{{ t('pages.systemHealth.compactDatabases') }}</el-button>
			<span v-if="latestCompaction?.id">{{ t('pages.systemHealth.lastCompaction', { status: latestCompaction.status, time: formatDateTime(latestCompaction.started_at), size: formatBytes(compactionReclaimedBytes(latestCompaction)) }) }}</span>
        </div>
		<el-skeleton v-else-if="loading" :rows="3" animated />
		<div v-else>
			<el-empty :description="t('pages.systemHealth.noBackup')" />
			<el-button size="small" type="warning" :loading="compacting" @click="compactDatabases">{{ t('pages.systemHealth.compactDatabases') }}</el-button>
		</div>
      </el-card>

      <el-card shadow="never" class="dashboard-panel panel-wide">
        <template #header>{{ t('pages.systemHealth.dataSources') }}</template>
        <el-table :data="health?.data_sources || []" size="small" border :empty-text="t('pages.systemHealth.noDataSources')">
          <el-table-column prop="source" :label="t('pages.systemHealth.source')" min-width="145" />
          <el-table-column prop="status" :label="t('common.status')" width="105">
            <template #default="{ row }"><el-tag :type="sourceStatusType(row.status)" effect="plain">{{ sourceStatusLabel(row.status) }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="coverage_pct" :label="t('pages.systemHealth.coverage')" width="100" align="right">
            <template #default="{ row }">{{ formatPct(row.coverage_pct) }}</template>
          </el-table-column>
          <el-table-column prop="failure_streak" :label="t('pages.systemHealth.failureStreak')" width="105" align="right" />
          <el-table-column prop="last_checked_at" :label="t('pages.systemHealth.lastChecked')" width="180">
            <template #default="{ row }">{{ formatDateTime(row.last_checked_at) }}</template>
          </el-table-column>
          <el-table-column prop="detail" :label="t('pages.systemHealth.detail')" min-width="260" show-overflow-tooltip />
          <el-table-column prop="error_message" :label="t('common.error')" min-width="260" show-overflow-tooltip />
          <el-table-column :label="t('common.actions')" width="105" fixed="right">
            <template #default="{ row }">
              <el-button v-if="row.recommended_action" type="primary" link @click="openSourceAction(row.recommended_action)">{{ t('pages.systemHealth.viewAction') }}</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-card>

      <el-card shadow="never" class="dashboard-panel panel-wide">
        <template #header><div class="panel-heading-action"><span>价格周期运行健康</span><el-button link type="primary" @click="router.push({name:'price-action-cycle',query:{tab:'rules'}})">查看规则</el-button></div></template>
        <el-descriptions :column="4" border size="small">
          <el-descriptions-item label="阶段覆盖"><el-tag :type="cycleHealth ? cycleCoverageTagType(cycleHealth) : 'info'" effect="plain">{{ cycleHealth ? `${cycleHealth.ready_count}/${cycleHealth.scope_count} · ${cycleHealth.coverage_pct.toFixed(1)}%` : (loading ? '读取中' : '-') }}</el-tag></el-descriptions-item>
          <el-descriptions-item label="正式版本">{{ cycleHealth?.active_rule_version || (loading ? '读取中' : '-') }}</el-descriptions-item>
          <el-descriptions-item label="IWM 历史">{{ cycleHealth ? `${cycleRuntimeStatusLabel(cycleHealth.iwm_status)} · ${cycleHealth.iwm_sample_days} 日` : (loading ? '读取中' : '-') }}</el-descriptions-item>
          <el-descriptions-item label="效果状态">{{ cycleHealth ? cycleValidationStatusLabel(cycleHealth.effectiveness_status) : (loading ? '读取中' : '尚无记录') }}</el-descriptions-item>
          <el-descriptions-item label="OHLC 缺失">{{ cycleHealth?.ohlc_missing_count ?? (loading ? '读取中' : '-') }}</el-descriptions-item>
          <el-descriptions-item label="复权阻断">{{ cycleHealth?.adjustment_blocked_count ?? (loading ? '读取中' : '-') }}</el-descriptions-item>
          <el-descriptions-item label="最近回放">{{ cycleHealth ? `${formatDateTime(cycleHealth.last_replay_at)} · ${cycleRuntimeStatusLabel(cycleHealth.last_replay_status)}` : (loading ? '读取中' : '-') }}</el-descriptions-item>
          <el-descriptions-item label="结果推进">{{ cycleHealth ? `${cycleHealth.effectiveness_latest_date || '-'} / IWM ${cycleHealth.iwm_latest_trade_date || '-'}` : (loading ? '读取中' : '-') }}</el-descriptions-item>
          <el-descriptions-item label="当日结论">{{ cycleHealth ? `${cycleHealth.scope_current_count}/${cycleHealth.scope_count}` : (loading ? '读取中' : '-') }}</el-descriptions-item>
          <el-descriptions-item label="结论滞后"><el-tag :type="cycleHealth ? (cycleHealth.scope_stale_count ? 'warning' : 'success') : 'info'" effect="plain">{{ cycleHealth?.scope_stale_count ?? (loading ? '读取中' : '-') }}</el-tag></el-descriptions-item>
        </el-descriptions>
      </el-card>

      <el-card shadow="never" class="dashboard-panel panel-wide">
        <template #header>
          <div class="panel-heading-action">
            <span>{{ t('pages.systemHealth.operationalReport') }}</span>
            <el-button size="small" :loading="notifyingOperational" @click="notifyOperational">{{ t('pages.systemHealth.sendOperationalReport') }}</el-button>
          </div>
        </template>
        <div v-if="operational">
          <el-descriptions :column="4" border size="small" class="operational-metrics">
            <el-descriptions-item :label="t('common.status')"><el-tag :type="sourceStatusType(operational.status)" effect="plain">{{ sourceStatusLabel(operational.status) }}</el-tag></el-descriptions-item>
            <el-descriptions-item :label="t('pages.systemHealth.retryQueue')">{{ operational.retryable_targets }}</el-descriptions-item>
            <el-descriptions-item :label="t('pages.systemHealth.deferredTargets')">{{ operational.deferred_targets }}</el-descriptions-item>
            <el-descriptions-item :label="t('pages.systemHealth.profileRetryDue')">{{ operational.company_profile_retry_due }}</el-descriptions-item>
            <el-descriptions-item :label="t('pages.systemHealth.marketRecovery')">{{ operational.market_price_recovery }}</el-descriptions-item>
            <el-descriptions-item label="本地行情可用">{{ operational.market_price_local_current || 0 }}</el-descriptions-item>
            <el-descriptions-item label="周期当日 / 滞后">{{ operational.price_action_current || 0 }} / {{ operational.price_action_stale || 0 }}</el-descriptions-item>
            <el-descriptions-item :label="t('pages.systemHealth.lowCoverageProviders')">{{ operational.low_coverage_providers }}</el-descriptions-item>
			<el-descriptions-item :label="t('pages.systemHealth.slowSECTargets')">{{ operational.slow_sec_targets }}</el-descriptions-item>
			<el-descriptions-item :label="t('pages.systemHealth.slowDiscoverySteps')">{{ operational.slow_discovery_steps }}</el-descriptions-item>
            <el-descriptions-item :label="t('pages.systemHealth.providerWarnings')">{{ operational.provider_warnings }}</el-descriptions-item>
			<el-descriptions-item label="技术历史待补">{{ operational.technical_history_pending }}</el-descriptions-item>
			<el-descriptions-item label="技术历史到期重试">{{ operational.technical_history_retry_due }}</el-descriptions-item>
			<el-descriptions-item label="等待自然样本">{{ operational.technical_history_waiting || 0 }}</el-descriptions-item>
			<el-descriptions-item label="技术历史连续失败">{{ operational.technical_history_deferred }}</el-descriptions-item>
			<el-descriptions-item label="失败通知批次">{{ operational.failed_notification_batches }}</el-descriptions-item>
			<el-descriptions-item label="通知死信批次">{{ operational.dead_letter_batches }}</el-descriptions-item>
          </el-descriptions>
          <p class="operational-summary">{{ operational.summary }}</p>
          <div v-if="operational.issues.length" class="health-alert-grid">
            <el-alert v-for="issue in operational.issues" :key="issue.key" :title="issue.title" :description="safeOperationalDetail(issue.detail)" :type="healthAlertType(issue.severity)" :closable="false" show-icon>
              <template #default>
				<el-tag size="small" type="info" effect="plain">影响：{{ issueImpact(issue.category) }}</el-tag>
				<el-tag size="small" :type="issueNeedsManualAction(issue.category) ? 'warning' : 'success'" effect="plain">{{ issueNeedsManualAction(issue.category) ? '需要人工确认' : '自动任务将继续重试' }}</el-tag>
                <el-button v-if="issue.action" type="primary" link @click="openSourceAction(issue.action)">{{ t('pages.systemHealth.viewAction') }}</el-button>
              </template>
            </el-alert>
          </div>
          <el-empty v-else :description="t('pages.systemHealth.noOperationalIssues')" />
        </div>
        <el-skeleton v-else :rows="3" animated />
      </el-card>

      <el-card shadow="never" class="dashboard-panel panel-wide">
        <template #header>
          <div class="panel-heading-action">
            <span>{{ t('pages.systemHealth.taskExecutionPlan') }}</span>
            <el-tag type="info" effect="plain">{{ t('pages.systemHealth.schedulerTimezone', { timezone: schedulerTimezone }) }}</el-tag>
          </div>
        </template>
        <el-table :data="operational?.tasks || []" size="small" border :empty-text="t('pages.systemHealth.noScheduledTasks')">
          <el-table-column :label="t('common.task')" min-width="205" show-overflow-tooltip><template #default="{ row }">{{ taskBusinessLabel(row.task_name) }}</template></el-table-column>
          <el-table-column :label="t('common.status')" width="115">
            <template #default="{ row }">
              <el-tag :type="taskExecutionTagType(row)" effect="plain">{{ taskExecutionLabel(row) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="t('pages.systemHealth.lastExecution')" width="185">
            <template #default="{ row }">{{ formatDateTime(row.last_run_at) }}</template>
          </el-table-column>
			<el-table-column label="14 日运行窗口" min-width="230"><template #default="{ row }">成功 {{ row.window_counts?.success || 0 }} · 覆盖降级 {{ row.window_counts?.degraded || 0 }} · 部分 {{ row.window_counts?.partial || 0 }} · 失败 {{ row.window_counts?.failed || 0 }}</template></el-table-column>
          <el-table-column :label="t('pages.systemHealth.nextExecution')" width="185">
            <template #default="{ row }">
              <span v-if="row.enabled && row.next_run_at">{{ formatScheduledDateTime(row.next_run_at) }}</span>
              <el-tag v-else type="info" effect="plain">{{ t('pages.systemHealth.notScheduled') }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="t('pages.systemHealth.consecutiveFailures')" width="112" align="center">
            <template #default="{ row }"><el-tag :type="row.consecutive_failures >= 3 ? 'danger' : row.consecutive_failures > 0 ? 'warning' : 'info'" effect="plain">{{ row.consecutive_failures || 0 }}</el-tag></template>
          </el-table-column>
          <el-table-column :label="t('common.actions')" width="92" fixed="right">
            <template #default><el-button type="primary" link @click="router.push({ name: 'scheduler' })">{{ t('pages.systemHealth.viewAction') }}</el-button></template>
          </el-table-column>
        </el-table>
      </el-card>

      <el-card shadow="never" class="dashboard-panel panel-wide">
        <template #header>{{ t('pages.systemHealth.secUserAgent') }}</template>
        <code>{{ health?.sec_user_agent || '-' }}</code>
      </el-card>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, OperationalAlertResult, OperationalReport, OperationalTaskStatus, SQLiteCompactionResult, SQLiteCompactionRun, SQLiteRecoveryReadiness, SystemConfig, SystemHealth } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
type CycleHealth = { scope_count:number; ready_count:number; coverage_pct:number; ohlc_missing_count:number; adjustment_blocked_count:number; iwm_status:string; iwm_sample_days:number; iwm_latest_trade_date:string; active_rule_version:string; last_replay_status:string; last_replay_at?:string; effectiveness_status:string; effectiveness_latest_date:string; scope_current_count:number; scope_stale_count:number; scope_missing_count:number }
type HealthSummary = { generated_at:string; status:string; target_total:number; enabled_targets:number; filing_total:number; notification_failures:number; issues:Array<{key:string; title:string; detail:string; severity:string; category:string; action?:string}> }
const router = useRouter()
const loading = ref(false)
const verifyingBackup = ref(false)
const compacting = ref(false)
const health = ref<SystemHealth | null>(null)
const summary = ref<HealthSummary | null>(null)
const operational = ref<OperationalReport | null>(null)
const notifyingOperational = ref(false)
const latestCompaction = ref<SQLiteCompactionRun | null>(null)
const cycleHealth = ref<CycleHealth | null>(null)
const schedulerTimezone = ref('UTC')
const consolidatedIssues = computed(() => {
  const systemIssues = (health.value?.issues || []).map((item, index) => ({ key: `system-${index}`, title: item.message, detail: '', level: item.level, category: '', action: '', manual: false }))
  const sourceIssues = operational.value?.issues || summary.value?.issues || []
  const operationalIssues = sourceIssues.map(issue => ({ key: issue.key, title: issue.title, detail: safeOperationalDetail(issue.detail), level: issue.severity, category: issue.category, action: issue.action || '', manual: issueNeedsManualAction(issue.category) }))
  return [...systemIssues, ...operationalIssues]
})

async function load(force = false) {
  loading.value = true
  try {
    try {
      const summaryRes = await apiClient.get<ApiResponse<HealthSummary>>('/system-health/summary')
      summary.value = summaryRes.data.data
    } catch {
      // Keep loading the independent detail cards when the fast summary fails.
    }
    const [healthRes, operationalRes, compactionRes, schedulerConfigsRes, cycleHealthRes] = await Promise.allSettled([
      apiClient.get<ApiResponse<SystemHealth>>('/system-health'),
      apiClient.get<ApiResponse<OperationalReport>>('/operational-health'),
      apiClient.get<ApiResponse<SQLiteCompactionRun>>('/system/databases/latest-compaction'),
      apiClient.get<ApiResponse<SystemConfig[]>>('/system-configs?category=scheduler'),
      apiClient.get<ApiResponse<CycleHealth>>('/price-action-cycle/health', { params: force ? { refresh: 1 } : undefined })
    ])
    if (healthRes.status === 'fulfilled') health.value = healthRes.value.data.data
    if (operationalRes.status === 'fulfilled') operational.value = operationalRes.value.data.data
    if (compactionRes.status === 'fulfilled') latestCompaction.value = compactionRes.value.data.data?.id ? compactionRes.value.data.data : null
    if (schedulerConfigsRes.status === 'fulfilled') schedulerTimezone.value = schedulerConfigsRes.value.data.data.find((item) => item.config_key === 'scheduler.timezone')?.config_value || 'UTC'
    if (cycleHealthRes.status === 'fulfilled') cycleHealth.value = cycleHealthRes.value.data.data
  } finally {
    loading.value = false
  }
}

async function compactDatabases() {
	await ElMessageBox.confirm(t('pages.systemHealth.compactionWarning'), t('pages.systemHealth.compactDatabases'), { type: 'warning', confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel') })
	compacting.value = true
	try {
		const res = await apiClient.post<ApiResponse<SQLiteCompactionResult>>('/system/databases/compact', null, { timeout: 1_250_000 })
		const result = res.data.data
		ElMessage.success(t('pages.systemHealth.compactionCompleted', { size: formatBytes(result.reclaimed_bytes) }))
		await load()
	} catch (error: any) {
		ElMessage.error(error?.response?.data?.message || error?.message || t('common.error'))
	} finally {
		compacting.value = false
	}
}

function compactionReclaimedBytes(run: SQLiteCompactionRun) {
	return Math.max(0, run.main_before_bytes - run.main_after_bytes) + Math.max(0, run.discovery_before_bytes - run.discovery_after_bytes)
}

async function notifyOperational() {
  notifyingOperational.value = true
  try {
    const res = await apiClient.post<ApiResponse<OperationalAlertResult>>('/operational-health/notify')
    const result = res.data.data
    ElMessage.success(result.sent ? t('pages.systemHealth.operationalSent') : t('pages.systemHealth.operationalSuppressed'))
    operational.value = result.report
  } catch (error: any) {
    ElMessage.error(error?.response?.data?.message || error?.message || t('common.error'))
  } finally {
    notifyingOperational.value = false
  }
}

async function verifyLatestBackup() {
	verifyingBackup.value = true
	try {
		const res = await apiClient.post<ApiResponse<SQLiteRecoveryReadiness>>('/system/backups/recovery-check', null, { timeout: 1_250_000 })
		const result = res.data.data
    await load()
		if (result.status !== 'ready') {
			ElMessage.warning(result.reason || t('pages.systemHealth.backupRecoveryUnavailable'))
			return
		}
		ElMessage.success(t('pages.systemHealth.backupVerifySuccess'))
  } catch (error: any) {
    ElMessage.error(error?.response?.data?.message || error?.message || t('common.error'))
  } finally {
    verifyingBackup.value = false
  }
}

function recoveryStatusLabel(status?: string) {
  return ({ ready: '通过', failed: '未通过', unavailable: '不可用', disabled: '未配置' } as Record<string, string>)[status || ''] || '尚未验证'
}

function cycleRuntimeStatusLabel(status?: string) {
  return ({ ready: '就绪', success: '成功', degraded: '降级可用', running: '运行中', failed: '失败', missing: '缺失', warning: '需关注', ok: '正常' } as Record<string, string>)[status || ''] || '尚无记录'
}

function cycleCoverageTagType(value: CycleHealth | null) {
  return value && value.coverage_pct >= 90 ? 'success' : 'warning'
}

function cycleValidationStatusLabel(status?: string) {
  return ({ validated: '已验证', validating: '观察中', unverified: '未验证' } as Record<string, string>)[status || ''] || '尚无记录'
}

function formatBytes(value: number) {
  if (!value) return '0 B'
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  if (value < 1024 * 1024 * 1024) return `${(value / 1024 / 1024).toFixed(1)} MB`
  return `${(value / 1024 / 1024 / 1024).toFixed(1)} GB`
}

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

function formatScheduledDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  try {
    return new Intl.DateTimeFormat(undefined, {
      timeZone: schedulerTimezone.value,
      year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
    }).format(date)
  } catch {
    return date.toLocaleString()
  }
}

function taskExecutionTagType(task: OperationalTaskStatus) {
  if (task.running) return 'primary'
  if (!task.enabled) return 'info'
  if (task.last_status === 'failed') return 'danger'
  if (task.last_status === 'partial' || task.last_status === 'interrupted') return 'warning'
  if (task.last_status === 'success') return 'success'
  return 'info'
}

function taskExecutionLabel(task: OperationalTaskStatus) {
  if (task.running) return t('pages.scheduler.status.running')
  if (!task.enabled) return t('pages.systemHealth.disabled')
  return t(`pages.scheduler.status.${task.last_status || 'idle'}`)
}

function healthAlertType(level: string) {
  if (level === 'critical') return 'error'
  if (level === 'warning') return 'warning'
  return 'info'
}

function sourceStatusType(status: string) {
  if (status === 'ok') return 'success'
  if (status === 'critical') return 'danger'
  if (status === 'warning') return 'warning'
  return 'info'
}

function sourceStatusLabel(status: string) {
  return t(`pages.systemHealth.sourceStatuses.${status || 'unknown'}`)
}

function issueImpact(category: string) {
  const labels: Record<string, string> = { data: '研究数据可用性', provider: '外部数据更新', notification: '消息送达', scheduler: '自动运行', task: '自动运行', backup: '数据恢复', discovery: '候选研究完整性', technical_history: '技术指标完整性' }
  return labels[category] || '运行稳定性'
}

function issueNeedsManualAction(category: string) {
  return ['backup', 'notification', 'data'].includes(category)
}

function safeOperationalDetail(value?: string) {
  return (value || '')
    .replace(/ipo_listing_reconcile_sync/g, 'IPO 上市状态核对')
    .replace(/longbridge_watch_target_valuation_sync/g, '监控标的估值更新')
    .replace(/operational_health_notification_sync/g, '运行健康通知')
    .replace(/technical_history_backfill/g, '技术历史补齐')
    .replace(/price_action_cycle_sync/g, '价格周期更新')
    .replace(/\btrace(?:[_ -]?id)?\s*[:=]\s*[a-z0-9-]+/gi, '诊断编号已隐藏')
    .replace(/\brequest(?:[_ -]?id)?\s*[:=]\s*[a-z0-9-]+/gi, '请求编号已隐藏')
}

function taskBusinessLabel(value: string) {
  const labels: Record<string, string> = {
    candidate_notification_sync: '候选通知',
    institutional_holdings_sync: '机构持仓更新',
    ipo_lifecycle_reconcile_sync: 'IPO 生命周期核对',
    ipo_listing_reconcile_sync: 'IPO 上市状态核对',
    ipo_offering_reconcile_sync: 'IPO 发行进度核对',
    ipo_radar_sync: 'IPO 新申报扫描',
    longbridge_candidate_option_research_sync: '候选期权研究更新',
    longbridge_candidate_research_sync: '候选市场研究更新',
    longbridge_candidate_valuation_sync: '候选估值研究更新',
    longbridge_watch_target_option_research_sync: '监控标的期权研究更新',
    longbridge_watch_target_research_sync: '监控标的市场研究更新',
    longbridge_watch_target_valuation_sync: '监控标的估值更新',
    macro_calendar_sync: '宏观日历更新',
    market_trend_sync: '市场趋势更新',
    notification_retry_sync: '失败通知重试',
    operation_history_cleanup: '运行历史清理',
    operational_health_notification_sync: '运行健康通知',
    price_action_cycle_replay: '价格周期回放',
    sec_filing_sync: 'SEC 公告增量同步',
    small_cap_discovery_full_sync: '小盘候选全量扫描',
    small_cap_discovery_sync: '小盘候选增量扫描',
    sqlite_backup: 'SQLite 数据备份',
    sqlite_recovery_drill: 'SQLite 恢复演练',
    trade_setup_notification_sync: '交易计划通知',
    us_futures_sync: '美股期货更新',
    watch_target_earnings_sync: '监控标的财报日历更新',
    watch_target_market_sync: '监控标的行情更新',
    technical_history_backfill: '技术历史补齐',
    price_action_cycle_sync: '价格周期更新',
  }
  return labels[value] || value.replace(/_/g, ' ')
}

function formatPct(value?: number | null) {
  return typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(1)}%` : '-'
}

function openSourceAction(action: string) {
  const routes: Record<string, string> = {
    scheduler: 'scheduler',
    configs: 'configs',
    'discovery-logs': 'discovery-logs',
    discovery_logs: 'discovery-logs',
    'sync-runs': 'sync-runs',
    'notification-logs': 'notification-logs',
    'macro-calendar': 'macro-calendar',
    'price-action-cycle': 'price-action-cycle',
    'system-health': 'system-health'
  }
  const name = routes[action]
  if (name) router.push({ name })
}

onMounted(load)
</script>
