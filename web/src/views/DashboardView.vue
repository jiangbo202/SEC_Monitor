<template>
  <section class="page dashboard-page">
    <div class="page-header">
      <div>
        <h1>今日决策</h1>
        <p class="page-subtitle">基于本地已同步快照整理市场环境、可执行交易计划与近期事件；不会在打开页面时请求第三方数据。</p>
      </div>
      <div class="dashboard-actions">
        <el-button :icon="Setting" @click="preferencesVisible = true">总览布局</el-button>
        <el-dropdown>
          <el-button>手动同步<el-icon class="el-icon--right"><ArrowDown /></el-icon></el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item :disabled="refreshing" @click="refreshFilings">刷新 SEC 公告</el-dropdown-item>
              <el-dropdown-item :disabled="refreshingIpo" @click="refreshIpoFilings">扫描 IPO</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <el-button type="primary" :loading="loading" :icon="Refresh" @click="load(true)">刷新总览</el-button>
      </div>
    </div>

    <el-alert
      v-for="issue in criticalIssues"
      :key="issue.key"
      class="dashboard-alert"
      type="error"
      :title="issue.title"
      :description="issue.detail"
      show-icon
      :closable="false"
    >
      <template #default>
        <div class="dashboard-alert-content">
          <span>{{ issue.detail }}</span>
          <el-button v-if="issue.action" link type="primary" @click="openOperationalAction(issue.action)">查看处理项</el-button>
        </div>
      </template>
    </el-alert>
    <el-alert v-if="summary?.warnings.length" class="dashboard-alert" type="warning" :closable="false" show-icon title="部分总览数据暂不可用">
      <template #default>{{ summary.warnings.join('；') }}</template>
    </el-alert>

    <el-card v-if="summary?.decision.readiness" shadow="never" class="decision-readiness" :class="`is-${summary.decision.readiness.status}`">
      <div class="decision-readiness-main">
        <div class="decision-readiness-title">
          <span class="status-dot" />
          <div><strong>{{ summary.decision.readiness.label }}</strong><small>截至 {{ summary.decision.readiness.as_of || '-' }} · 预期交易日 {{ summary.decision.readiness.expected_trade_date || '-' }}</small></div>
        </div>
        <div class="decision-readiness-tags">
          <el-tag :type="summary.decision.readiness.research_usable ? 'success' : 'danger'" effect="plain">研究{{ summary.decision.readiness.research_usable ? '可用' : '暂停' }}</el-tag>
          <el-tag :type="summary.decision.readiness.new_trade_plan_allowed ? 'success' : 'warning'" effect="plain">新交易计划{{ summary.decision.readiness.new_trade_plan_allowed ? '可形成' : '受限' }}</el-tag>
          <el-tag effect="plain">效果验证 {{ effectivenessStatusLabel(summary.decision.readiness.effectiveness_status) }}</el-tag>
        </div>
      </div>
      <div v-if="summary.decision.readiness.reasons.length" class="decision-readiness-reasons">
        <div v-for="reason in summary.decision.readiness.reasons" :key="reason.key" class="decision-readiness-reason">
          <el-tag size="small" :type="readinessReasonType(reason.severity)" effect="plain">{{ reason.severity === 'critical' ? '阻断' : reason.severity === 'warning' ? '限制' : '提示' }}</el-tag>
          <span><b>{{ reason.title }}</b> {{ reason.detail }}</span>
          <el-button v-if="reason.action" link type="primary" @click="openOperationalAction(reason.action)">处理</el-button>
        </div>
      </div>
    </el-card>

    <el-tabs v-model="activeView" class="dashboard-tabs">
      <el-tab-pane label="决策" name="decision">
        <div v-if="isVisible('market')" class="dashboard-grid decision-grid">
          <el-card shadow="never" class="dashboard-panel panel-wide">
            <template #header>
              <div class="panel-header">
                <span>市场环境</span>
                <div class="panel-header-actions">
                  <el-tooltip :content="summary?.decision.market.freshness.detail || '读取本地市场快照状态'">
                    <el-tag :type="marketFreshnessType(summary?.decision.market.freshness.status)" effect="plain">{{ marketFreshnessLabel(summary?.decision.market.freshness.status) }}</el-tag>
                  </el-tooltip>
                  <el-link type="primary" @click="router.push('/market-trend')">查看大盘趋势</el-link>
                </div>
              </div>
            </template>
            <div class="market-strip">
              <div v-for="item in summary?.decision.market.market || []" :key="item.symbol" class="market-item">
                <span>{{ item.label }}</span><strong>{{ formatNumber(item.close) }}</strong><em :class="changeClass(item.change_1d_pct)">{{ formatChange(item.change_1d_pct) }}</em>
              </div>
              <div v-if="summary?.decision.market.temperature" class="market-item temperature-item">
                <span>市场温度</span><strong>{{ summary.decision.market.temperature.temperature }}</strong><em>{{ summary.decision.market.temperature.description || 'Longbridge' }}</em>
              </div>
            </div>
			<el-empty v-if="!loading && !(summary?.decision.market.market || []).length" :image-size="48" description="市场快照未就绪；当前研究结论可能处于降级状态"><el-button type="primary" link @click="router.push('/market-trend')">查看数据状态并刷新</el-button></el-empty>
            <div class="market-subsection">
              <span class="subsection-label">板块强弱</span>
              <el-tag v-for="item in summary?.decision.market.sectors || []" :key="item.symbol" :type="changeTagType(item.change_1d_pct)" effect="plain">{{ item.label }} {{ formatChange(item.change_1d_pct) }}</el-tag>
            </div>
            <div class="market-subsection">
              <span class="subsection-label">美股期货</span>
              <el-tag v-for="item in (summary?.decision.market.futures || []).slice(0, 4)" :key="item.symbol" :type="changeTagType(item.change_1d_pct)" effect="plain">{{ item.label }} {{ formatChange(item.change_1d_pct) }}</el-tag>
              <el-link type="primary" @click="router.push('/us-futures')">全部期货</el-link>
            </div>
          </el-card>
        </div>

        <div v-if="isVisible('actions')" class="dashboard-grid decision-grid">
          <el-card shadow="never" class="dashboard-panel panel-wide availability-panel">
            <template #header>
              <div class="panel-header">
                <span>候选可用性</span>
                <div class="panel-header-actions">
                  <el-tag type="success" effect="plain">可行动 {{ summary?.decision.availability?.eligible || 0 }}</el-tag>
                  <el-tag type="warning" effect="plain">仅研究 {{ summary?.decision.availability?.research_only || 0 }}</el-tag>
                  <el-tag type="danger" effect="plain">阻断 {{ summary?.decision.availability?.blocked || 0 }}</el-tag>
                  <el-link type="primary" @click="router.push('/discovery-candidates')">全部候选</el-link>
                </div>
              </div>
            </template>
            <div v-if="availabilityUsable.length" class="availability-section">
              <div class="availability-heading"><strong>当前可用标的</strong><span>行情与关键研究证据均满足形成新计划的门槛</span></div>
              <div class="availability-list">
                <button v-for="item in availabilityUsable" :key="item.ticker" class="availability-row is-usable" type="button" @click="openCandidate(item.ticker)">
                  <span class="availability-ticker">{{ item.ticker }} <small>{{ item.grade }} {{ item.score }}</small></span>
                  <span>{{ item.primary_reason }}</span>
                  <span class="availability-meta">{{ freshnessLabel(item.price_freshness) }} · {{ item.price_trade_date || '-' }}</span>
                  <span class="availability-next">{{ item.next_action }}</span>
                </button>
              </div>
            </div>
            <div v-else class="availability-empty"><strong>当前没有可形成新交易计划的标的</strong><span>这不等于“没有研究价值”；下方列出最优先需要解除的门控。</span></div>
            <div v-if="availabilityExcluded.length" class="availability-section excluded-section">
              <div class="availability-heading"><strong>优先排除项</strong><span>每个标的仅展示当前最主要原因</span></div>
              <div class="availability-list">
                <button v-for="item in availabilityExcluded" :key="item.ticker" class="availability-row" type="button" @click="openCandidate(item.ticker)">
                  <span class="availability-ticker">{{ item.ticker }} <el-tag size="small" :type="readinessType(item.readiness)" effect="plain">{{ readinessLabel(item.readiness) }}</el-tag></span>
                  <span>{{ item.primary_reason }}</span>
                  <span class="availability-meta">{{ freshnessLabel(item.price_freshness) }} · {{ item.price_trade_date || '-' }}</span>
                  <span class="availability-next">下一步：{{ item.next_action }}</span>
                </button>
              </div>
            </div>
          </el-card>

          <el-card shadow="never" class="dashboard-panel panel-wide">
            <template #header>
              <div class="panel-header">
                <span>今日行动队列</span>
                <div class="panel-header-actions">
                  <el-tag v-if="summary?.decision.review_due.overdue" type="danger" effect="plain">{{ summary.decision.review_due.overdue }} 项复核逾期</el-tag>
                  <el-tag v-if="summary?.decision.review_due.due_today" type="warning" effect="plain">{{ summary.decision.review_due.due_today }} 项今日复核</el-tag>
                  <el-link type="primary" @click="router.push('/strategy-pool')">查看策略观察池</el-link>
                </div>
              </div>
            </template>
            <el-table v-if="summary?.decision.actions.length" class="action-table" :data="summary?.decision.actions || []">
              <el-table-column label="优先级" width="90"><template #default="{ row }"><el-tag :type="priorityType(row.priority)" effect="dark">{{ priorityLabel(row.priority) }}</el-tag></template></el-table-column>
              <el-table-column prop="ticker" label="标的" width="110" fixed>
                <template #default="{ row }"><el-link type="primary" @click="openCandidate(row.ticker)">{{ row.ticker }}</el-link></template>
              </el-table-column>
              <el-table-column label="状态" width="126"><template #default="{ row }"><div class="stacked-tags"><el-tag :type="tradeStatusType(row.status)" effect="plain">{{ tradeStatusLabel(row.status) }}</el-tag><el-tag :type="readinessType(row.tradability)" effect="plain">{{ readinessLabel(row.tradability) }}</el-tag></div></template></el-table-column>
              <el-table-column label="触发与动作" min-width="350"><template #default="{ row }"><div class="action-reason"><strong>{{ row.reason || '-' }}</strong><span>{{ row.next_action }}</span></div></template></el-table-column>
              <el-table-column label="证据新鲜度" width="130"><template #default="{ row }">截至 {{ row.evidence_as_of || '-' }}</template></el-table-column>
              <el-table-column label="风险预算" width="175"><template #default="{ row }"><div class="action-risk"><span>{{ row.risk_pct ? `风险 ${formatNumber(row.risk_pct)}%` : '风险待设定' }}</span><small>{{ row.stop_loss_usd ? `止损 ${formatNumber(row.stop_loss_usd)} USD` : '止损待复核' }}</small></div></template></el-table-column>
              <el-table-column prop="due_label" label="截止" width="110" />
              <el-table-column label="动作" width="84" fixed="right"><template #default="{ row }"><el-link type="primary" @click="openWorkspace(row.ticker)">处理</el-link></template></el-table-column>
            </el-table>
            <div v-if="summary?.decision.actions.length" class="action-cards">
              <article v-for="row in summary.decision.actions" :key="row.ticker" class="action-card">
                <div class="action-card-head"><el-tag :type="priorityType(row.priority)" effect="dark">{{ priorityLabel(row.priority) }}</el-tag><el-link type="primary" @click="openCandidate(row.ticker)">{{ row.ticker }}</el-link><el-tag :type="tradeStatusType(row.status)" effect="plain">{{ tradeStatusLabel(row.status) }}</el-tag></div>
                <strong>{{ row.reason || '-' }}</strong><p>{{ row.next_action }}</p>
                <div class="action-card-meta"><span>状态 {{ readinessLabel(row.tradability) }}</span><span>证据 {{ row.evidence_as_of || '-' }}</span><span>{{ row.risk_pct ? `风险 ${formatNumber(row.risk_pct)}%` : '风险待设定' }}</span><span>截止 {{ row.due_label }}</span></div>
                <el-button type="primary" plain @click="openWorkspace(row.ticker)">进入标的工作台处理</el-button>
              </article>
            </div>
            <div v-if="!loading && actionEmptyState" class="decision-empty" :class="`is-${actionEmptyState.kind}`">
              <div><strong>{{ actionEmptyState.title }}</strong><p>{{ actionEmptyState.detail }}</p></div>
              <el-button type="primary" plain @click="router.push(actionEmptyState.route)">{{ actionEmptyState.action }}</el-button>
            </div>
          </el-card>
        </div>

        <div v-if="isVisible('calendar')" class="dashboard-grid decision-grid">
          <el-card shadow="never" class="dashboard-panel panel-wide">
            <template #header><div class="panel-header"><span>近期事件日历（14 天）</span><el-link type="primary" @click="router.push('/macro-calendar')">宏观日历</el-link></div></template>
            <el-table :data="summary?.decision.calendar || []" empty-text="未来 14 天暂无已同步事件">
              <el-table-column label="类型" width="120" fixed><template #default="{ row }"><el-tag :type="calendarTagType(row.kind)" effect="plain">{{ calendarLabel(row) }}</el-tag></template></el-table-column>
              <el-table-column prop="ticker" label="标的" width="100" />
              <el-table-column prop="title" label="事件" min-width="300" show-overflow-tooltip />
              <el-table-column label="时间" width="180"><template #default="{ row }">{{ formatDateTime(row.at) }} {{ row.session || '' }}</template></el-table-column>
              <el-table-column label="操作" width="100"><template #default="{ row }"><el-link type="primary" @click="row.link && router.push(row.link)">查看</el-link></template></el-table-column>
            </el-table>
          </el-card>
        </div>
      </el-tab-pane>

      <el-tab-pane label="监控" name="monitoring">
        <div v-if="isVisible('monitoring')" class="dashboard-grid">
          <el-card shadow="never" class="dashboard-panel metric-card"><span>监控标的</span><strong>{{ summary?.monitoring.enabled_targets || 0 }}</strong><small>共 {{ summary?.monitoring.watch_targets || 0 }} 个标的</small></el-card>
          <el-card shadow="never" class="dashboard-panel metric-card"><span>近期财报预告</span><strong>{{ summary?.monitoring.upcoming_earnings || 0 }}</strong><small>{{ earningsCoverageLabel }}</small></el-card>
          <el-card shadow="never" class="dashboard-panel metric-card"><span>进行中 IPO</span><strong>{{ summary?.monitoring.ipo.in_progress || 0 }}</strong><small>关注 {{ summary?.monitoring.ipo.followed_total || 0 }} 家</small></el-card>
        </div>
        <div v-if="isVisible('monitoring')" class="dashboard-grid monitoring-grid">
          <el-card shadow="never" class="dashboard-panel panel-wide">
            <template #header><div class="panel-header"><span>监控标的重要公告</span><el-link type="primary" @click="router.push('/event-radar')">重大事件</el-link></div></template>
            <el-table :data="summary?.monitoring.recent_filings || []" empty-text="暂无重要公告">
              <el-table-column prop="ticker" label="标的" width="110" fixed />
              <el-table-column prop="company_name" label="公司 / 基金" min-width="180" show-overflow-tooltip />
              <el-table-column prop="filing_type" label="类型" width="105"><template #default="{ row }"><el-tag effect="plain">{{ row.filing_type }}</el-tag></template></el-table-column>
              <el-table-column prop="title" label="公告" min-width="280" show-overflow-tooltip />
              <el-table-column label="时间" width="175"><template #default="{ row }">{{ formatDateTime(row.filed_at) }}</template></el-table-column>
            </el-table>
          </el-card>
          <el-card shadow="never" class="dashboard-panel panel-wide">
            <template #header><div class="panel-header"><span>关注 IPO 进展</span><el-link type="primary" @click="router.push('/ipo-radar')">IPO 监控</el-link></div></template>
            <el-table :data="summary?.monitoring.ipo.followed || []" empty-text="还没有关注 IPO 公司">
              <el-table-column prop="company_name" label="公司" min-width="240" fixed show-overflow-tooltip />
              <el-table-column prop="status" label="进度" width="125"><template #default="{ row }"><el-tag effect="plain">{{ row.status }}</el-tag></template></el-table-column>
              <el-table-column prop="final_ticker" label="Ticker" width="115" />
              <el-table-column prop="latest_filing_type" label="最新文件" width="120" />
              <el-table-column label="最近更新" width="175"><template #default="{ row }">{{ formatDateTime(row.latest_accepted_at || row.latest_filing_date) }}</template></el-table-column>
            </el-table>
          </el-card>
        </div>
      </el-tab-pane>

      <el-tab-pane label="运行健康" name="operations">
        <div v-if="isVisible('operations')" class="dashboard-grid">
          <el-card shadow="never" class="dashboard-panel metric-card"><span>运行状态</span><strong>{{ operationalStatusLabel(summary?.operations.status) }}</strong><small>{{ summary?.operations.issues.length || 0 }} 项运行待办</small></el-card>
          <el-card shadow="never" class="dashboard-panel metric-card"><span>通知失败</span><strong>{{ summary?.operations.failed_notification_batches || 0 }}</strong><small>待重试或人工处理</small></el-card>
          <el-card shadow="never" class="dashboard-panel metric-card"><span>死信</span><strong>{{ summary?.operations.dead_letter_batches || 0 }}</strong><small>需要检查 Telegram 配置或错误</small></el-card>
        </div>
        <div v-if="isVisible('operations')" class="dashboard-grid monitoring-grid">
          <el-card shadow="never" class="dashboard-panel panel-wide">
            <template #header><div class="panel-header"><span>运行待办</span><el-link type="primary" @click="router.push('/system-health')">系统健康页</el-link></div></template>
            <el-table class="operations-table" :data="summary?.operations.issues || []" empty-text="当前没有运行待办">
              <el-table-column prop="severity" label="级别" width="105"><template #default="{ row }"><el-tag :type="issueTagType(row.severity)" effect="plain">{{ issueSeverityLabel(row.severity) }}</el-tag></template></el-table-column>
              <el-table-column prop="title" label="项目" min-width="200" />
              <el-table-column label="影响与处理" min-width="440"><template #default="{ row }"><div class="operation-detail"><span>{{ safeOperationalDetail(row.detail) }}</span><small>影响：{{ issueImpact(row.category) }} · {{ issueHandlingLabel(row.category) }}</small></div></template></el-table-column>
              <el-table-column label="操作" width="110"><template #default="{ row }"><el-link v-if="row.action" type="primary" @click="openOperationalAction(row.action)">处理</el-link></template></el-table-column>
            </el-table>
            <div class="operations-cards">
              <article v-for="row in summary?.operations.issues || []" :key="row.key" class="operation-card">
                <div><el-tag :type="issueTagType(row.severity)" effect="plain">{{ issueSeverityLabel(row.severity) }}</el-tag><strong>{{ row.title }}</strong></div>
                <p>{{ safeOperationalDetail(row.detail) }}</p>
                <small>影响：{{ issueImpact(row.category) }} · {{ issueHandlingLabel(row.category) }}</small>
                <el-button v-if="row.action" type="primary" plain @click="openOperationalAction(row.action)">查看处理项</el-button>
              </article>
            </div>
          </el-card>
          <el-card shadow="never" class="dashboard-panel panel-wide">
            <template #header><div class="panel-header"><span>定时任务摘要</span><el-link type="primary" @click="router.push('/scheduler')">调度任务</el-link></div></template>
            <el-table class="operations-table" :data="summary?.operations.tasks || []" empty-text="暂无任务记录">
              <el-table-column label="任务" min-width="260" fixed><template #default="{ row }">{{ taskBusinessLabel(row.task_name) }}</template></el-table-column>
              <el-table-column label="已启用" width="100"><template #default="{ row }"><el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '开启' : '关闭' }}</el-tag></template></el-table-column>
              <el-table-column prop="last_status" label="最近状态" width="120"><template #default="{ row }"><el-tag :type="taskStatusType(row.last_status)" effect="plain">{{ taskStatusLabel(row.last_status) }}</el-tag></template></el-table-column>
              <el-table-column label="上次运行" width="175"><template #default="{ row }">{{ formatDateTime(row.last_run_at) }}</template></el-table-column>
              <el-table-column label="下次运行" width="175"><template #default="{ row }">{{ formatDateTime(row.next_run_at) }}</template></el-table-column>
              <el-table-column prop="consecutive_failures" label="连续失败" width="110" />
            </el-table>
            <div class="operations-cards">
              <article v-for="row in summary?.operations.tasks || []" :key="row.task_name" class="operation-card">
                <div><strong>{{ taskBusinessLabel(row.task_name) }}</strong><el-tag :type="taskStatusType(row.last_status)" effect="plain">{{ taskStatusLabel(row.last_status) }}</el-tag></div>
                <small>上次 {{ formatDateTime(row.last_run_at) }} · 下次 {{ formatDateTime(row.next_run_at) }}</small>
                <span v-if="row.consecutive_failures">连续失败 {{ row.consecutive_failures }} 次</span>
              </article>
            </div>
          </el-card>
        </div>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="preferencesVisible" title="总览布局" width="520px">
      <p class="page-subtitle">隐藏模块只影响当前本地用户的总览显示，不影响同步、通知或数据保留。</p>
      <el-checkbox-group v-model="visibleModules" class="dashboard-module-selector">
        <el-checkbox v-for="module in moduleOptions" :key="module.key" :value="module.key">{{ module.label }}</el-checkbox>
      </el-checkbox-group>
      <template #footer>
        <el-button @click="restoreDefaultModules">还原默认</el-button>
        <el-button type="primary" :loading="savingPreferences" @click="savePreferences">保存布局</el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ArrowDown, Refresh, Setting } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiClient } from '@/api/client'
import type { ApiResponse } from '@/api/types'

interface MarketSeries { symbol: string; label: string; close: number; change_1d_pct?: number | null }
interface CandidateAction { ticker: string; company_name?: string; status: string; priority: string; tradability: string; entry_trigger?: string; reason?: string; next_action: string; due_label: string; evidence_as_of?: string; close_usd?: number; stop_loss_usd?: number; risk_pct?: number; take_profit_low_usd?: number; take_profit_high_usd?: number; score?: number; grade?: string; since: string }
interface CandidateAvailabilityItem { ticker: string; company_name?: string; grade?: string; score?: number; readiness: string; primary_reason: string; next_action: string; price_freshness: string; price_trade_date?: string; close_usd?: number; review_priority?: number }
interface CandidateAvailability { total: number; eligible: number; research_only: number; blocked: number; usable: CandidateAvailabilityItem[]; excluded: CandidateAvailabilityItem[] }
interface CalendarItem { kind: string; scope: string; ticker?: string; title: string; at?: string | null; session?: string; link?: string }
interface FilingItem { id: number; ticker: string; company_name: string; filing_type: string; title: string; filed_at: string }
interface IPOCompany { company_name: string; status: string; final_ticker?: string; latest_filing_type?: string; latest_accepted_at?: string; latest_filing_date?: string }
interface OperationalIssue { key: string; category: string; severity: string; title: string; detail: string; action?: string }
interface OperationalTask { task_name: string; enabled: boolean; last_status: string; last_run_at?: string; next_run_at?: string; consecutive_failures: number }
interface DecisionReadinessReason { key: string; severity: string; title: string; detail: string; action?: string }
interface DecisionReadiness { status: 'ready' | 'research_only' | 'blocked' | string; label: string; research_usable: boolean; new_trade_plan_allowed: boolean; as_of?: string; expected_trade_date?: string; effectiveness_status: string; effectiveness_version?: string; reasons: DecisionReadinessReason[] }
interface DashboardSummary {
  generated_at: string
  warnings: string[]
  preferences: { hidden_modules: string[] }
  decision: { market: { market: MarketSeries[]; sectors: MarketSeries[]; futures: MarketSeries[]; temperature?: { temperature: number; description?: string }; freshness: { status: string; detail: string } }; readiness: DecisionReadiness; availability?: CandidateAvailability; actions: CandidateAction[]; calendar: CalendarItem[]; review_due: { overdue: number; due_today: number; upcoming: number } }
  monitoring: { watch_targets: number; enabled_targets: number; upcoming_earnings: number; earnings_coverage_status: string; earnings_covered_targets: number; earnings_unavailable: number; earnings_last_fetched_at?: string; recent_filings: FilingItem[]; ipo: { in_progress: number; followed_total: number; followed: IPOCompany[] } }
  operations: { status: string; critical_issues: OperationalIssue[]; issues: OperationalIssue[]; tasks: OperationalTask[]; failed_notification_batches: number; dead_letter_batches: number }
}

const router = useRouter()
const loading = ref(false)
const refreshing = ref(false)
const refreshingIpo = ref(false)
const savingPreferences = ref(false)
const preferencesVisible = ref(false)
const activeView = ref('decision')
const summary = ref<DashboardSummary | null>(null)
const defaultModules = ['market', 'actions', 'calendar', 'monitoring', 'operations']
const visibleModules = ref<string[]>([...defaultModules])
const moduleOptions = [
  { key: 'market', label: '市场环境' }, { key: 'actions', label: '候选行动' }, { key: 'calendar', label: '近期事件日历' },
  { key: 'monitoring', label: '监控概览' }, { key: 'operations', label: '运行健康' }
]

const criticalIssues = computed(() => summary.value?.operations.critical_issues || [])
const availabilityUsable = computed(() => summary.value?.decision.availability?.usable || [])
const availabilityExcluded = computed(() => summary.value?.decision.availability?.excluded || [])
const actionEmptyState = computed(() => {
  if ((summary.value?.decision.actions || []).length) return null
  const availability = summary.value?.decision.availability
  if (!availability || availability.total === 0) return { kind: 'data', title: '数据不足，暂时无法判断机会', detail: '当前没有可用候选批次或候选数据尚未完成同步。', action: '查看数据状态', route: '/discovery-logs' }
  if (availability.eligible === 0 && availability.excluded.length > 0) return { kind: 'gated', title: '候选均被门控排除', detail: `当前 ${availability.research_only} 只仅供研究、${availability.blocked} 只被阻断；这不是“没有机会”，应先解除上方列出的行情或证据门控。`, action: '查看全部排除项', route: '/discovery-candidates' }
  return { kind: 'none', title: '当前没有触发行动的机会', detail: `已有 ${availability.eligible} 只标的满足研究门槛，但尚未触发入场候选、离场预警或趋势失效。`, action: '打开策略观察池', route: '/strategy-pool' }
})
const earningsCoverageLabel = computed(() => {
  const monitoring = summary.value?.monitoring
  if (!monitoring) return '财报日历尚未载入'
  const coverage = `${monitoring.earnings_covered_targets || 0}/${monitoring.enabled_targets || 0}`
  if (monitoring.earnings_coverage_status === 'complete') return `日历覆盖 ${coverage}；0 表示未来暂无已知事件`
  if (monitoring.earnings_coverage_status === 'partial') return `日历仅部分覆盖 ${coverage}`
  if (monitoring.earnings_coverage_status === 'unavailable') return `财报来源不可用 ${monitoring.earnings_unavailable || 0} 项`
  return `日历尚未同步 · 覆盖 ${coverage}`
})
const isVisible = (key: string) => visibleModules.value.includes(key)

onMounted(load)

async function load(force = false) {
  loading.value = true
  try {
    const response = await apiClient.get<ApiResponse<DashboardSummary>>('/dashboard/summary', { params: force ? { refresh: 1 } : undefined })
    summary.value = response.data.data
    const hidden = new Set(response.data.data.preferences?.hidden_modules || [])
    visibleModules.value = defaultModules.filter((key) => !hidden.has(key))
  } catch (error: any) {
    ElMessage.error(error?.response?.data?.message || error?.message || '总览读取失败')
  } finally { loading.value = false }
}

async function savePreferences() {
  savingPreferences.value = true
  try {
    const hidden = defaultModules.filter((key) => !visibleModules.value.includes(key))
    await apiClient.put('/dashboard/preferences', { hidden_modules: hidden })
    if (summary.value) summary.value.preferences.hidden_modules = hidden
    preferencesVisible.value = false
    ElMessage.success('总览布局已保存')
  } catch (error: any) {
    ElMessage.error(error?.response?.data?.message || error?.message || '保存布局失败')
  } finally { savingPreferences.value = false }
}

function restoreDefaultModules() { visibleModules.value = [...defaultModules] }
async function refreshFilings() {
  refreshing.value = true
  try { const res = await apiClient.post<ApiResponse<{ new_filings: number }>>('/filings/refresh'); ElMessage.success(`已同步 ${res.data.data.new_filings} 条 SEC 公告`); await load(true) }
  catch (error: any) { ElMessage.error(error?.response?.data?.message || error?.message || 'SEC 同步失败') }
  finally { refreshing.value = false }
}
async function refreshIpoFilings() {
  refreshingIpo.value = true
  try { const res = await apiClient.post<ApiResponse<{ new_filings: number }>>('/ipo-filings/refresh', null, { timeout: 120000 }); ElMessage.success(`IPO 扫描完成，新增 ${res.data.data.new_filings} 条文件`); await load(true) }
  catch (error: any) { ElMessage.error(error?.response?.data?.message || error?.message || 'IPO 扫描失败') }
  finally { refreshingIpo.value = false }
}
function openCandidate(ticker: string) { router.push({ path: '/discovery-candidates', query: { ticker } }) }
function openWorkspace(ticker: string) { router.push({ path: '/ticker-workspace', query: { ticker } }) }
function openOperationalAction(action: string) { const routes: Record<string, string> = { notification_logs: '/notification-logs', scheduler: '/scheduler', system_health: '/system-health', refresh: '/ipo-radar', targets: '/targets', 'market-trend': '/market-trend', 'discovery-logs': '/discovery-logs', 'discovery-candidates': '/discovery-candidates' }; router.push(routes[action] || '/system-health') }
function formatNumber(value?: number | null) { return value == null ? '-' : new Intl.NumberFormat('en-US', { maximumFractionDigits: 2 }).format(value) }
function formatChange(value?: number | null) { return value == null ? '-' : `${value >= 0 ? '+' : ''}${value.toFixed(2)}%` }
function formatDateTime(value?: string | null) { if (!value) return '-'; const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString() }
function changeClass(value?: number | null) { return value == null ? '' : value >= 0 ? 'positive' : 'negative' }
function changeTagType(value?: number | null) { return value == null ? 'info' : value >= 0 ? 'success' : 'danger' }
function calendarTagType(kind: string) { return kind === 'ipo' ? 'warning' : kind === 'macro' ? 'danger' : 'success' }
function calendarLabel(row: CalendarItem) { if (row.kind === 'ipo') return 'IPO 进展'; if (row.kind === 'macro') return '宏观事件'; return row.scope === 'candidate' ? '候选财报' : '监控财报' }
function tradeStatusLabel(status: string) { return ({ entry_candidate: '入场候选', exit_warning: '离场预警', invalidated: '趋势失效' } as Record<string, string>)[status] || status }
function tradeStatusType(status: string) { return status === 'entry_candidate' ? 'success' : status === 'exit_warning' ? 'warning' : 'danger' }
function priorityLabel(value: string) { return ({ critical: '紧急', high: '高', medium: '中', low: '低' } as Record<string, string>)[value] || '中' }
function priorityType(value: string) { return value === 'critical' ? 'danger' : value === 'high' ? 'warning' : value === 'medium' ? 'primary' : 'info' }
function readinessLabel(value?: string) { return ({ ready: '可行动', research_only: '仅研究', blocked: '阻断' } as Record<string, string>)[value || ''] || '待核验' }
function readinessType(value?: string) { return value === 'ready' ? 'success' : value === 'blocked' ? 'danger' : 'warning' }
function freshnessLabel(value?: string) { return ({ current: '当日行情', previous_trading_day: '前一交易日', stale: '行情过期', missing: '缺少行情', future: '日期异常' } as Record<string, string>)[value || ''] || '新鲜度未知' }
function issueTagType(severity: string) { return severity === 'critical' || severity === 'danger' ? 'danger' : severity === 'warning' ? 'warning' : 'info' }
function issueSeverityLabel(value?: string) { return value === 'critical' || value === 'danger' ? '需立即处理' : value === 'warning' ? '需关注' : '提示' }
function operationalStatusLabel(value?: string) { return value === 'critical' ? '需立即处理' : value === 'warning' ? '需要关注' : value === 'ok' ? '运行正常' : '尚未评估' }
function issueImpact(category?: string) { return ({ data: '研究数据可用性', provider: '外部数据更新', notification: '消息送达', scheduler: '自动运行', task: '自动运行', backup: '数据恢复', discovery: '候选研究完整性', technical_history: '技术指标完整性' } as Record<string, string>)[category || ''] || '运行稳定性' }
function issueHandlingLabel(category?: string) { return ['backup', 'notification', 'data'].includes(category || '') ? '需要人工确认' : '自动任务将继续重试' }
function safeOperationalDetail(value?: string) { return (value || '').replace(/ipo_listing_reconcile_sync/g, 'IPO 上市状态核对').replace(/longbridge_watch_target_valuation_sync/g, '监控标的估值更新').replace(/operational_health_notification_sync/g, '运行健康通知').replace(/technical_history_backfill/g, '技术历史补齐').replace(/price_action_cycle_sync/g, '价格周期更新').replace(/\btrace(?:[_ -]?id)?\s*[:=]\s*[a-z0-9-]+/gi, '诊断编号已隐藏').replace(/\brequest(?:[_ -]?id)?\s*[:=]\s*[a-z0-9-]+/gi, '请求编号已隐藏') }
function taskBusinessLabel(value: string) { return ({ ipo_listing_reconcile_sync: 'IPO 上市状态核对', longbridge_watch_target_valuation_sync: '监控标的估值更新', operational_health_notification_sync: '运行健康通知', sqlite_backup: 'SQLite 数据备份', discovery_candidate_sync: '小盘候选扫描', technical_history_backfill: '技术历史补齐', price_action_cycle_sync: '价格周期更新' } as Record<string, string>)[value] || value.replace(/_/g, ' ') }
function marketFreshnessType(value?: string) { return value === 'fresh' ? 'success' : value === 'stale' ? 'warning' : value === 'expired' || value === 'unavailable' ? 'danger' : 'info' }
function marketFreshnessLabel(value?: string) { return ({ fresh: '数据新鲜', stale: '数据偏旧', expired: '数据过期', unavailable: '暂无快照' } as Record<string, string>)[value || ''] || '状态未知' }
function taskStatusType(status: string) { return status === 'success' ? 'success' : status === 'failed' ? 'danger' : status === 'partial' ? 'warning' : 'info' }
function taskStatusLabel(status?: string) { return ({ success: '成功', failed: '失败', partial: '部分完成', running: '运行中', interrupted: '已中断', idle: '等待运行', degraded: '降级完成' } as Record<string, string>)[status || ''] || '尚无记录' }
function readinessReasonType(severity: string) { return severity === 'critical' || severity === 'danger' ? 'danger' : severity === 'warning' ? 'warning' : 'info' }
function effectivenessStatusLabel(status?: string) { return ({ validated: '已验证', validating: '验证中', unverified: '未验证', unavailable: '不可读' } as Record<string, string>)[status || ''] || '未知' }
</script>

<style scoped>
.dashboard-actions { display: flex; gap: 10px; align-items: center; }
.dashboard-alert { margin-bottom: 12px; }
.dashboard-alert-content { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.decision-readiness { margin-bottom: 12px; border-left: 4px solid var(--el-color-success); }
.decision-readiness.is-research_only { border-left-color: var(--el-color-warning); }
.decision-readiness.is-blocked { border-left-color: var(--el-color-danger); }
.decision-readiness-main { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
.decision-readiness-title { display: flex; align-items: center; gap: 9px; min-width: 0; }
.decision-readiness-title .status-dot { width: 9px; height: 9px; border-radius: 50%; background: var(--el-color-success); flex: 0 0 auto; }
.decision-readiness.is-research_only .status-dot { background: var(--el-color-warning); }
.decision-readiness.is-blocked .status-dot { background: var(--el-color-danger); }
.decision-readiness-title div { display: grid; gap: 3px; }
.decision-readiness-title strong { font-size: 16px; }
.decision-readiness-title small { color: var(--el-text-color-secondary); }
.decision-readiness-tags { display: flex; gap: 6px; flex-wrap: wrap; justify-content: flex-end; }
.decision-readiness-reasons { display: grid; gap: 6px; margin-top: 10px; padding-top: 9px; border-top: 1px solid var(--el-border-color-lighter); }
.decision-readiness-reason { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 8px; color: var(--el-text-color-regular); font-size: 12px; }
.decision-readiness-reason b { margin-right: 5px; color: var(--el-text-color-primary); }
.dashboard-tabs :deep(.el-tabs__header) { margin-bottom: 12px; }
.dashboard-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; margin-bottom: 12px; }
.decision-grid, .monitoring-grid { grid-template-columns: 1fr; }
.dashboard-panel { border-color: var(--el-border-color-lighter); }
.panel-wide { min-width: 0; }
.panel-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; font-weight: 600; }
.panel-header-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.metric-card { min-height: 92px; display: flex; flex-direction: column; justify-content: center; gap: 5px; }
.metric-card span, .metric-card small { color: var(--el-text-color-secondary); }
.metric-card strong { max-width: 100%; font-size: 24px; line-height: 1.15; color: var(--el-text-color-primary); overflow-wrap: anywhere; }
.market-strip { display: grid; grid-template-columns: repeat(auto-fit, minmax(145px, 1fr)); gap: 12px; }
.market-item { border: 1px solid var(--el-border-color-lighter); border-radius: 6px; padding: 9px 10px; display: flex; flex-direction: column; gap: 3px; }
.market-item span, .market-item em { font-size: 13px; color: var(--el-text-color-secondary); font-style: normal; }
.market-item strong { font-size: 18px; }
.market-item .positive { color: var(--el-color-success); }.market-item .negative { color: var(--el-color-danger); }
.temperature-item { background: var(--el-fill-color-light); }
.market-subsection { margin-top: 10px; display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
.subsection-label { color: var(--el-text-color-secondary); min-width: 70px; }
.availability-panel :deep(.el-card__body) { padding-top: 12px; }
.availability-section { display: grid; gap: 8px; }
.excluded-section { margin-top: 14px; padding-top: 12px; border-top: 1px solid var(--el-border-color-lighter); }
.availability-heading { display: flex; align-items: baseline; gap: 10px; }
.availability-heading span, .availability-empty span { color: var(--el-text-color-secondary); font-size: 12px; }
.availability-list { display: grid; gap: 6px; }
.availability-row { appearance: none; width: 100%; border: 1px solid var(--el-border-color-lighter); border-radius: 6px; background: var(--el-bg-color); color: var(--el-text-color-regular); padding: 9px 10px; display: grid; grid-template-columns: 150px minmax(150px, 1fr) 175px minmax(220px, 1.2fr); gap: 10px; align-items: center; text-align: left; cursor: pointer; }
.availability-row:hover { border-color: var(--el-color-primary-light-5); background: var(--el-color-primary-light-9); }
.availability-row.is-usable { border-left: 3px solid var(--el-color-success); }
.availability-ticker { font-weight: 700; color: var(--el-text-color-primary); display: flex; align-items: center; gap: 6px; }
.availability-ticker small, .availability-meta { font-weight: 400; color: var(--el-text-color-secondary); font-size: 12px; }
.availability-next { color: var(--el-text-color-secondary); }
.availability-empty { display: flex; flex-direction: column; gap: 4px; padding: 10px 12px; border-left: 3px solid var(--el-color-warning); background: var(--el-color-warning-light-9); }
.stacked-tags, .action-risk, .action-reason { display: flex; flex-direction: column; align-items: flex-start; gap: 4px; }
.action-reason span, .action-risk small { color: var(--el-text-color-secondary); font-size: 12px; }
.action-cards { display: none; }
.action-card { border: 1px solid var(--el-border-color-lighter); border-radius: 8px; padding: 12px; }
.action-card-head, .action-card-meta { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.action-card > strong { display: block; margin-top: 10px; }
.action-card p { color: var(--el-text-color-secondary); margin: 5px 0 10px; }
.action-card-meta { color: var(--el-text-color-secondary); font-size: 12px; margin-bottom: 10px; }
.decision-empty { border: 1px solid var(--el-border-color-lighter); border-left: 4px solid var(--el-color-info); border-radius: 6px; padding: 15px; display: flex; justify-content: space-between; align-items: center; gap: 18px; }
.decision-empty.is-gated { border-left-color: var(--el-color-warning); background: var(--el-color-warning-light-9); }
.decision-empty.is-data { border-left-color: var(--el-color-danger); background: var(--el-color-danger-light-9); }
.decision-empty p { margin: 5px 0 0; color: var(--el-text-color-secondary); }
.dashboard-module-selector { display: flex; flex-direction: column; gap: 10px; padding: 8px 0; }
.operation-detail { display: grid; gap: 4px; }
.operation-detail small { color: var(--el-text-color-secondary); }
.operations-cards { display: none; }
.operation-card { display: grid; gap: 8px; padding: 12px; border: 1px solid var(--el-border-color-lighter); border-radius: 8px; }
.operation-card > div { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.operation-card p { margin: 0; color: var(--el-text-color-regular); overflow-wrap: anywhere; }
.operation-card small { color: var(--el-text-color-secondary); }
.operation-card .el-button { justify-self: start; }
@media (max-width: 900px) {
  .dashboard-grid { grid-template-columns: 1fr; }
  .page-header, .dashboard-actions, .decision-readiness-main { align-items: flex-start; flex-wrap: wrap; }
  .dashboard-alert-content, .decision-empty { align-items: flex-start; flex-direction: column; }
  .decision-readiness-tags { justify-content: flex-start; }
  .decision-readiness-reason { grid-template-columns: auto minmax(0, 1fr); }
  .decision-readiness-reason .el-button { grid-column: 2; justify-self: start; }
  .availability-row { grid-template-columns: 1fr; gap: 5px; }
  .availability-heading { align-items: flex-start; flex-direction: column; gap: 3px; }
  .action-table { display: none; }
  .action-cards { display: grid; gap: 10px; }
  .operations-table { display: none; }
  .operations-cards { display: grid; gap: 10px; }
  .metric-card { min-height: 78px; }
  .metric-card strong { font-size: 21px; }
}
</style>
