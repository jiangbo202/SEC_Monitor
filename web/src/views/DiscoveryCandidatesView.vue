<template>
  <div>
    <div class="page-header candidate-page-header">
      <div class="candidate-page-title">
        <h1>小盘股候选</h1>
        <p>基于公开 SEC 文件、财务指标、内幕交易和融资风险生成的研究候选列表。</p>
      </div>
      <el-space wrap class="candidate-page-actions">
        <el-button @click="openEligibilityCheck">检查小盘资格</el-button>
        <el-button :loading="workflowLoading" type="primary" plain @click="runWorkflow">刷新候选工作流</el-button>
        <el-button :loading="loading" @click="load">刷新列表</el-button>
        <el-dropdown trigger="click" @command="handleCandidateToolCommand">
          <el-button>更多研究工具</el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="market">强制补齐收盘价</el-dropdown-item>
              <el-dropdown-item command="technical">回填技术历史</el-dropdown-item>
              <el-dropdown-item command="watch">关注列表</el-dropdown-item>
              <el-dropdown-item command="portfolio">研究组合</el-dropdown-item>
              <el-dropdown-item command="effectiveness">效果评估</el-dropdown-item>
              <el-dropdown-item command="sector">赛道分布</el-dropdown-item>
              <el-dropdown-item command="report">查看日报</el-dropdown-item>
              <el-dropdown-item command="notification">预检通知摘要</el-dropdown-item>
              <el-dropdown-item divided command="export">导出 CSV</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </el-space>
    </div>

    <el-card v-if="discoverySyncRun" shadow="never" class="discovery-sync-status-card">
      <div class="discovery-sync-status-main">
        <div class="discovery-sync-status-title">
          <strong>最近候选同步</strong>
          <el-tag :type="discoverySyncStatusTagType(discoverySyncRun.status)" effect="plain">
            {{ discoverySyncStatusLabel(discoverySyncRun.status) }}
          </el-tag>
          <span>{{ discoverySyncPhaseLabel(discoverySyncRun.phase) }}</span>
        </div>
        <div class="discovery-sync-status-meta">
          <span>开始：{{ formatDateTime(discoverySyncRun.started_at) }}</span>
          <span v-if="discoverySyncRun.completed_at">结束：{{ formatDateTime(discoverySyncRun.completed_at) }}</span>
          <span>耗时：{{ discoverySyncDuration(discoverySyncRun) }}</span>
          <span v-if="discoverySyncRun.status === 'running'">最近心跳：{{ formatDateTime(discoverySyncRun.updated_at) }}</span>
          <el-button link type="primary" @click="loadDiscoverySyncStatus">刷新状态</el-button>
        </div>
      </div>
      <el-alert
        v-if="discoverySyncRun.error_message"
        type="error"
        :closable="false"
        show-icon
        :title="`同步失败：${discoverySyncRun.error_message}`"
        class="discovery-sync-error"
      />
    </el-card>

    <el-alert
      v-if="discoveryStorage"
      :type="discoveryStorage.status === 'ok' ? 'info' : discoveryStorage.status === 'error' ? 'error' : 'warning'"
      :closable="false"
      show-icon
      class="discovery-storage-alert"
    >
      <template #title>
        <span>本地存储：研究库 {{ formatBytes(discoveryStorage.database_bytes) }} · SEC 缓存 {{ formatBytes(discoveryStorage.cache_bytes) }}（{{ discoveryStorage.cache_files.toLocaleString() }} 个文件）</span>
        <el-button link type="primary" :loading="cacheCleanupLoading" @click="cleanupDiscoveryCache">清理过期缓存</el-button>
      </template>
      <template #default>
        {{ discoveryStorage.issues.length ? discoveryStorage.issues.join('；') : 'SQLite 已启用 WAL 与写锁等待；过期缓存清理不会删除候选、评分、公告或研究记录。' }}
      </template>
    </el-alert>

    <el-alert
      v-if="health"
      :type="health.status === 'ok' ? 'success' : health.status === 'missing' ? 'warning' : 'error'"
      :closable="false"
      show-icon
      class="health-alert"
      :title="`数据健康：${healthStatusLabel(health.status)}｜候选 ${health.total_candidates}｜可行动 ${health.ready_candidates ?? 0}｜待核验 ${health.research_only_candidates ?? 0}｜已阻断 ${health.blocked_candidates ?? 0}｜财务指标不可用 ${health.missing_financials}｜内幕源 ${healthInsiderDataLabel(health.insider_data_status)}｜内幕覆盖 ${health.candidates_with_insider_coverage ?? 0}/${health.total_candidates}｜内幕记录 ${health.candidates_with_insider_records}/${health.total_candidates}｜SEC 公告 ${health.candidates_with_recent_filings}/${health.total_candidates}｜合格买入 ${health.qualified_insider_candidates}｜批次价格当日 ${health.current_price_candidates}｜前一交易日 ${health.fallback_price_candidates}｜缺/过期 ${health.missing_price_candidates + health.stale_price_candidates}｜缺市值 ${health.missing_market_cap}｜活跃风险 ${health.active_risk_events}`"
      :description="health.issues.length ? health.issues.map(formatHealthIssue).join('；') : '当前候选证据链完整度正常。'"
    />

    <el-card v-if="criteria" shadow="never" class="criteria-card">
      <div class="criteria-heading">
        <div>
          <strong>当前选股口径</strong>
          <span>按当前评分规则筛选；研究用途，不构成投资建议。</span>
        </div>
        <el-tag type="info" effect="plain">{{ criteria.scoring_version }}</el-tag>
      </div>
      <el-space wrap class="criteria-tags">
        <el-tag effect="plain">候选池：市值 {{ formatCriteriaUSD(criteria.market_cap_min_usd) }} – &lt;{{ formatCriteriaUSD(criteria.b_market_cap_max_exclusive_usd) }}</el-tag>
        <el-tooltip placement="top" :content="criteria.revenue_growth_selection"><el-tag type="success" effect="plain">A级：市值 &lt;{{ formatCriteriaUSD(criteria.a_market_cap_max_exclusive_usd) }} · 收入 &gt;{{ criteria.a_revenue_growth_min_exclusive_pct }}%</el-tag></el-tooltip>
        <el-tooltip placement="top" :content="`${criteria.a_runway_min_months} 个月以上现金 runway；${criteria.qualified_insider_requirement}；${criteria.active_capital_risk_requirement}`"><el-tag type="success" effect="plain">A级：现金 ≥{{ criteria.a_runway_min_months }}月 · {{ criteria.insider_lookback_days }}日内幕买入 · 无阻断</el-tag></el-tooltip>
        <el-tooltip placement="top" :content="criteria.revenue_growth_selection"><el-tag type="warning" effect="plain">B级：市值 &lt;{{ formatCriteriaUSD(criteria.b_market_cap_max_exclusive_usd) }} · 收入 &gt;{{ criteria.b_revenue_growth_min_exclusive_pct }}% · 赛道 ≥{{ criteria.b_min_sector_score }}/10 · 无B级阻断</el-tag></el-tooltip>
      </el-space>
      <div class="criteria-note">收入增长：{{ criteria.revenue_growth_selection }}。风险阻断：{{ criteria.active_capital_risk_requirement }}。</div>
      <div class="criteria-state-legend">
        <span>状态说明：</span>
        <el-tooltip content="关键财务、市场和证据条件已满足，可进入人工研究或通知流程。"><el-tag type="success" effect="plain">可行动</el-tag></el-tooltip>
        <el-tooltip content="评分可供研究，但财务时效、内幕覆盖或身份等证据仍需人工确认；不会默认通知。"><el-tag type="warning" effect="plain">待核验</el-tag></el-tooltip>
        <el-tooltip content="存在融资、反向拆股、持续经营等规则定义的阻断风险，不进入 A/B 推荐。"><el-tag type="danger" effect="plain">已阻断</el-tag></el-tooltip>
      </div>
    </el-card>

    <el-card v-if="reviewQueue?.items.length" shadow="never" class="review-queue-card">
      <div class="review-queue-heading">
        <div>
          <strong>研究复查待办</strong>
          <span>按系统时区 {{ reviewQueue.as_of }} 汇总已关注标的；复查日期由研究卡手动设定，不会触发交易或通知。</span>
        </div>
        <el-space wrap>
          <el-tag v-if="reviewQueue.overdue_count" type="danger" effect="plain">逾期 {{ reviewQueue.overdue_count }}</el-tag>
          <el-tag v-if="reviewQueue.due_today_count" type="warning" effect="plain">今天 {{ reviewQueue.due_today_count }}</el-tag>
          <el-tag v-if="reviewQueue.upcoming_count" type="info" effect="plain">未来 7 天 {{ reviewQueue.upcoming_count }}</el-tag>
          <el-button link type="primary" @click="loadReviewQueue">刷新待办</el-button>
        </el-space>
      </div>
      <el-table :data="reviewQueue.items" size="small" max-height="260" class="review-queue-table">
        <el-table-column prop="ticker" label="Ticker" width="100" />
        <el-table-column prop="company_name" label="公司" min-width="170" show-overflow-tooltip />
        <el-table-column label="复查时间" width="150">
          <template #default="{ row }">
            <el-tag :type="reviewQueueStateTagType(row.review_state)" effect="plain">{{ reviewQueueStateLabel(row.review_state, row.days_until_review) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="next_review_at" label="日期" width="120">
          <template #default="{ row }">{{ formatDate(row.next_review_at) }}</template>
        </el-table-column>
        <el-table-column prop="research_status" label="研究状态" width="110">
          <template #default="{ row }"><el-tag :type="researchStatusTagType(row.research_status)" effect="plain">{{ researchStatusLabel(row.research_status) }}</el-tag></template>
        </el-table-column>
        <el-table-column label="当前候选" width="115">
          <template #default="{ row }">
            <span v-if="row.latest_score">{{ row.latest_score.grade }}级 · {{ row.latest_score.total_score }}分</span>
            <el-tag v-else type="info" effect="plain">已退出候选</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="catalyst" label="催化剂/复查依据" min-width="220" show-overflow-tooltip />
        <el-table-column label="操作" width="130" fixed="right">
          <template #default="{ row }">
            <el-button v-if="row.latest_score" link type="primary" @click="openDetail(row.latest_score)">详情</el-button>
            <el-button link type="primary" @click="editCandidateWatch(row)">研究</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <div v-if="overview" class="overview-grid">
      <el-card shadow="never" class="overview-card">
        <span class="overview-label">当前候选</span>
        <strong>{{ overview.total }}</strong>
        <small>A {{ overview.grade_counts?.A || 0 }} / B {{ overview.grade_counts?.B || 0 }}</small>
      </el-card>
      <el-card shadow="never" class="overview-card">
        <span class="overview-label">可行动</span>
        <strong>{{ health?.ready_candidates ?? 0 }}</strong>
        <small>关键证据完整，可进入默认通知</small>
      </el-card>
      <el-card shadow="never" class="overview-card">
        <span class="overview-label">待核验</span>
        <strong>{{ health?.research_only_candidates ?? 0 }}</strong>
        <small>基本面可研究，关键证据仍待补齐</small>
      </el-card>
      <el-card shadow="never" class="overview-card">
        <span class="overview-label">行情待补偿</span>
        <strong>{{ (health?.stale_price_candidates || 0) + (health?.missing_price_candidates || 0) }}</strong>
        <small>过期 {{ health?.stale_price_candidates || 0 }} / 缺失 {{ health?.missing_price_candidates || 0 }}</small>
      </el-card>
      <el-card shadow="never" class="overview-card">
        <span class="overview-label">变化</span>
        <strong>{{ overview.change_counts?.new || 0 }}</strong>
        <small>改善 {{ overview.change_counts?.improved || 0 }} / 退出 {{ overview.change_counts?.exited || 0 }}</small>
      </el-card>
      <el-card shadow="never" class="overview-card overview-wide">
        <span class="overview-label">主要赛道</span>
        <strong>{{ topSectorLabel }}</strong>
        <small>{{ topSectorCount }} 只</small>
      </el-card>
    </div>

    <el-card shadow="never" class="filter-card">
      <div class="quick-filter-row">
        <span class="quick-filter-label">快捷筛选</span>
        <el-tooltip content="默认显示可行动与待核验候选；已阻断标的需手动查看。" placement="top">
          <el-button :type="quickFilterActive('default_candidates') ? 'primary' : 'default'" plain @click="showDefaultCandidates">
            默认候选
          </el-button>
        </el-tooltip>
        <el-button :type="quickFilterActive('actionable') ? 'primary' : 'default'" plain @click="setReadinessFilter('ready')">
          可行动
        </el-button>
        <el-button :type="quickFilterActive('verification') ? 'primary' : 'default'" plain @click="setReadinessFilter('research_only')">
          待核验
        </el-button>
        <el-button :type="quickFilterActive('blocked') ? 'primary' : 'default'" plain @click="setReadinessFilter('blocked')">
          已阻断
        </el-button>
        <el-button :type="quickFilterActive('improved') ? 'primary' : 'default'" plain @click="toggleQuickFilter('improved')">
          改善
        </el-button>
        <el-button :type="quickFilterActive('upcoming_earnings') ? 'primary' : 'default'" plain @click="toggleQuickFilter('upcoming_earnings')">
          即将财报 <el-badge :value="upcomingEarningsCount" :hidden="upcomingEarningsCount === 0" class="quick-filter-badge" />
        </el-button>
        <el-button :type="quickFilterActive('followed') ? 'primary' : 'default'" plain @click="toggleQuickFilter('followed')">
          已关注
        </el-button>
        <el-button :type="quickFilterActive('exclude_low_liquidity') ? 'primary' : 'default'" plain @click="toggleQuickFilter('exclude_low_liquidity')">
          排除低流动性
        </el-button>
        <el-tooltip content="只显示价格过期、缺失、日期异常或尚未能校验的候选；前一交易日回退价仍视为可用。" placement="top">
          <el-button :type="quickFilterActive('price_attention') ? 'primary' : 'default'" plain @click="toggleQuickFilter('price_attention')">
            行情待补偿
          </el-button>
        </el-tooltip>
        <span class="table-view-label">列表字段</span>
        <el-radio-group v-model="candidateTableView" size="small" aria-label="候选列表字段显示方式" @change="load">
          <el-radio-button value="compact">紧凑</el-radio-button>
          <el-radio-button value="full">完整</el-radio-button>
        </el-radio-group>
        <span class="current-list-count">当前显示 {{ total }} / {{ overview?.total || total }} 只（{{ candidateScopeLabel }}）</span>
        <span v-if="supplementalLoading" class="candidate-supplemental-loading">正在更新健康与运行状态…</span>
      </div>
      <el-form :inline="true" :model="filters">
        <el-form-item label="等级">
          <el-select v-model="filters.grade" clearable style="width: 120px">
            <el-option label="A级" value="A" />
            <el-option label="B级" value="B" />
            <el-option label="排除池" value="excluded" />
          </el-select>
        </el-form-item>
        <el-form-item label="Ticker">
          <el-input v-model="filters.ticker" clearable placeholder="ACME" style="width: 140px" @keyup.enter="search" />
        </el-form-item>
        <el-form-item label="赛道分类">
          <el-select v-model="filters.sector_category" clearable filterable style="width: 180px">
            <el-option v-for="category in sectorCategoryOptions" :key="category" :label="category" :value="category" />
          </el-select>
        </el-form-item>
        <el-form-item label="证据状态">
          <el-select v-model="filters.research_readiness" clearable style="width: 140px">
            <el-option label="可行动" value="ready" />
            <el-option label="待核验" value="research_only" />
            <el-option label="已阻断" value="blocked" />
          </el-select>
        </el-form-item>
        <el-form-item label="技术信号">
          <el-select v-model="filters.technical_signal" clearable style="width: 170px">
            <el-option label="上穿 20 日均线" value="cross_above_ma20" />
            <el-option label="突破 20 日最高收盘价" value="breakout_20d_high" />
            <el-option label="放量突破" value="volume_backed_breakout" />
          </el-select>
        </el-form-item>
        <template v-if="advancedFiltersVisible">
          <el-form-item label="A级合格">
            <el-select v-model="filters.eligible_a" clearable style="width: 120px">
              <el-option label="是" value="true" />
              <el-option label="否" value="false" />
            </el-select>
          </el-form-item>
          <el-form-item label="B级合格">
            <el-select v-model="filters.eligible_b" clearable style="width: 120px">
              <el-option label="是" value="true" />
              <el-option label="否" value="false" />
            </el-select>
          </el-form-item>
          <el-form-item label="EV/Sales ≤">
            <el-input-number v-model="filters.max_ev_sales" :min="0" :precision="1" controls-position="right" style="width: 145px" />
          </el-form-item>
          <el-form-item label="净现金/市值 ≥">
            <el-input-number v-model="filters.min_net_cash_to_market_cap_pct" :min="0" :max="100" :precision="0" controls-position="right" style="width: 145px" />
            <span class="filter-suffix">%</span>
          </el-form-item>
          <el-form-item label="价格新鲜度">
            <el-select v-model="filters.price_freshness" clearable style="width: 170px">
              <el-option label="最近已收盘交易日" value="current" />
              <el-option label="前一交易日回退" value="previous_trading_day" />
              <el-option label="需要补偿" value="attention" />
            </el-select>
          </el-form-item>
        </template>
        <el-form-item>
          <el-button type="primary" @click="search">查询</el-button>
          <el-button @click="reset">重置</el-button>
          <el-button link type="primary" @click="advancedFiltersVisible = !advancedFiltersVisible">{{ advancedFiltersVisible ? '收起高级筛选' : '高级筛选' }}</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-table :data="rows" v-loading="loading" border empty-text="暂无候选" :default-sort="{ prop: 'total_score', order: 'descending' }" :size="candidateTableView === 'compact' ? 'small' : 'default'" :class="{ 'candidate-table-compact': candidateTableView === 'compact' }" @sort-change="onSortChange">
      <el-table-column prop="grade" label="等级" width="90" align="center">
        <template #default="{ row }">
          <el-tag :type="gradeTagType(row.grade)" effect="dark">{{ gradeLabel(row.grade) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="证据状态" width="96" align="center">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <div v-for="reason in readinessTooltipLines(row)" :key="reason">{{ reason }}</div>
              </div>
            </template>
            <el-tag :type="readinessTagType(row.research_readiness?.status)" effect="plain">{{ readinessLabel(row.research_readiness?.status) }}</el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="ticker" label="Ticker" width="128" sortable="custom">
        <template #default="{ row }">
          <el-space :size="4">
            <span>{{ row.ticker }}</span>
            <el-tag v-if="row.followed" size="small" type="warning" effect="plain">关注</el-tag>
          </el-space>
        </template>
      </el-table-column>
      <el-table-column prop="total_score" label="总分" width="90" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="score-tooltip">
                <div class="score-tooltip-title">总分（0–100）= 六项基本面评分之和</div>
                <div class="score-reason"><span>收入增长（满分 30）</span><strong>{{ row.revenue_growth_score }}</strong></div>
                <div class="score-reason"><span>现金储备（满分 20）</span><strong>{{ row.cash_runway_score }}</strong></div>
                <div class="score-reason"><span>内幕增持（满分 20）</span><strong>{{ row.insider_score }}</strong></div>
                <div class="score-reason"><span>毛利率（满分 10）</span><strong>{{ row.gross_margin_score }}</strong></div>
                <div class="score-reason"><span>股本稀释（满分 10）</span><strong>{{ row.dilution_risk_score }}</strong></div>
                <div class="score-reason"><span>赛道空间（满分 10）</span><strong>{{ row.sector_score }}</strong></div>
                <div class="score-tooltip-total"><span>合计</span><strong>{{ row.total_score }} / 100</strong></div>
                <div class="score-tooltip-note">等级：{{ gradeLabel(row.grade) }}；评分有效日：{{ formatDate(row.score_effective_date) }}</div>
                <div v-if="row.business_model?.revenue_score_cap_reason" class="score-tooltip-note">业务模型校准：{{ row.business_model.revenue_score_cap_reason }}</div>
              </div>
            </template>
            <span class="metric-help">{{ row.total_score }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="review_priority_score" label="短线复核" width="110" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="score-tooltip">
                <div class="score-tooltip-title">短线复核优先级（0–100）= 质量基础 + 近期变化/交易条件 − 风险</div>
                <div class="score-reason"><span>质量调整分 × 60%</span><strong>{{ reviewPriorityBaseScore(row) }}</strong></div>
                <div class="score-tooltip-note">原始质量调整分：{{ row.quality_adjusted_score ?? row.total_score }}；变化状态：{{ changeStatusLabel(row.change_status) }}</div>
                <div v-if="row.review_priority_reasons?.length" class="score-tooltip-reasons">
                  <div v-for="reason in row.review_priority_reasons" :key="`${reason.label}-${reason.points}`" class="score-reason">
                    <span>{{ reason.label }}</span>
                    <strong>{{ formatReviewPriorityPoints(reason.points) }}</strong>
                  </div>
                </div>
                <div v-else class="score-tooltip-note">暂无可计算的近期变化或交易条件。</div>
                <div class="score-tooltip-note">成交量：{{ formatVolume(row.price_volume) }}；市值：{{ formatUSD(row.market_cap_usd) }}；市场质量：{{ row.market_quality?.status === 'risk' ? '需复核' : '正常' }}</div>
                <div class="score-tooltip-total"><span>合计</span><strong>{{ row.review_priority_score ?? 0 }} / 100</strong></div>
                <div class="score-tooltip-note">用于决定优先复核顺序，不会改变基本面总分或默认排序。</div>
              </div>
            </template>
            <span class="metric-help">{{ row.review_priority_score ?? '-' }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" prop="quality_adjusted_score" label="调整分" width="90" align="right">
        <template #default="{ row }">
          <el-tooltip v-if="row.quality_adjusted_score !== row.total_score" content="已按低基数、极端增长、低流动性或融资风险进行上限保护" placement="top">
            <span class="metric-help">{{ row.quality_adjusted_score ?? row.total_score }}</span>
          </el-tooltip>
          <span v-else>{{ row.quality_adjusted_score ?? row.total_score }}</span>
        </template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" prop="quality_tier" label="质量" width="110">
        <template #default="{ row }">
          <el-tag :type="qualityTierTagType(row.quality_tier)" effect="plain">{{ qualityTierLabel(row.quality_tier) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" prop="change_status" label="变化" width="100">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark" :disabled="!(row.change_reasons || []).length">
            <template #content>
              <div class="metric-tooltip">
                <div v-for="reason in row.change_reasons || []" :key="`${reason.field}-${reason.current}`">
                  {{ reason.label }}：{{ reason.previous || '-' }} → {{ reason.current || '-' }}
                </div>
              </div>
            </template>
            <el-tag :type="changeStatusTagType(row.change_status)" effect="plain">{{ changeStatusLabel(row.change_status) }}</el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="market_cap_usd" label="市值" width="130" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip :content="`用于评分的市值快照；评分有效日：${formatDate(row.score_effective_date)}`" placement="top">
            <span class="metric-help">{{ formatUSD(row.market_cap_usd) }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="price_close_usd" label="价格" width="100" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip :content="priceEvidenceTooltip(row)" placement="top">
            <span class="metric-help">{{ formatPrice(row.price_close_usd, row.price_currency) }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="price_volume" label="成交量" width="120" align="right" sortable="custom">
        <template #default="{ row }">{{ formatVolume(row.price_volume) }}</template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" label="市场质量" width="120" align="right">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <div>平均成交额：{{ formatUSD(row.market_quality?.average_dollar_volume_usd) }}</div>
                <div>波动率：{{ formatPct(row.market_quality?.volatility_pct) }}</div>
                <div>20日动量：{{ formatPerformance(row.market_quality?.momentum_pct) }}</div>
                <div>最大回撤：{{ formatPerformance(row.market_quality?.max_drawdown_pct) }}</div>
              </div>
            </template>
            <el-tag :type="row.market_quality?.status === 'risk' ? 'warning' : 'success'" effect="plain">{{ row.market_quality?.status === 'risk' ? '需复核' : '正常' }}</el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" label="可交易性" width="120">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content><div class="metric-tooltip"><div v-for="reason in investabilityTooltipLines(row.investability)" :key="reason">{{ reason }}</div></div></template>
            <el-tag :type="investabilityTagType(row.investability?.status)" effect="plain">{{ investabilityLabel(row.investability?.status) }}</el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" label="股本趋势" width="120">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content><div class="metric-tooltip"><div v-for="line in dilutionTooltipLines(row.dilution_trend)" :key="line">{{ line }}</div></div></template>
            <el-tag :type="dilutionTagType(row.dilution_trend?.status)" effect="plain">{{ dilutionLabel(row.dilution_trend?.status) }}</el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="技术信号" min-width="170">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <template v-if="row.technical?.status === 'ready'">
                  <div>收盘价：{{ formatPrice(row.technical.close_usd, 'USD') }}</div>
                  <div>MA20：{{ formatPrice(row.technical.ma20_usd, 'USD') }}（{{ formatPerformance(row.technical.distance_to_ma20_pct) }}）</div>
                  <div>MA200：{{ row.technical.ma200_available ? formatPrice(row.technical.ma200_usd, 'USD') : '历史不足' }}</div>
                  <div>前 20 日最高收盘价：{{ formatPrice(row.technical.prior_20d_high_usd, 'USD') }}（{{ formatPerformance(row.technical.distance_to_20d_high_pct) }}）</div>
                  <div>量比：{{ formatRatio(row.technical.volume_ratio_20) }}（相对 20 日均量）</div>
                  <div>相对 IWM（20 日）：{{ relativeStrengthSummary(row.technical) }}</div>
                  <div>研究事件锚定价：{{ anchoredVWAPSummary(row.technical) }}</div>
                </template>
                <div v-else>{{ technicalStatusDescription(row.technical) }}</div>
              </div>
            </template>
            <el-space wrap>
              <el-tag v-for="signal in row.technical?.signals || []" :key="signal.kind" type="success" effect="plain">{{ signal.label }}</el-tag>
              <el-tag v-if="!(row.technical?.signals || []).length" :type="row.technical?.status === 'ready' ? 'info' : 'warning'" effect="plain">
                {{ row.technical?.status === 'ready' ? '暂无突破' : technicalStatusLabel(row.technical) }}
              </el-tag>
            </el-space>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="交易计划" min-width="130">
        <template #default="{ row }">
          <el-tooltip :content="tradeSetupSummary(row.technical)" placement="top">
            <el-tag :type="tradeSetupTagType(row.technical?.trade_setup?.status)" effect="plain">
              {{ tradeSetupLabel(row.technical?.trade_setup?.status) }}
            </el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="price_trade_date" label="价格日期" width="110" sortable="custom">
        <template #default="{ row }">
          <el-tooltip :content="priceFreshnessTooltip(row)" placement="top">
            <el-tag :type="priceFreshnessTagType(row.price_freshness_status)" effect="plain">{{ formatDate(row.price_trade_date) }}</el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="sector_category" label="赛道分类" min-width="150">
        <template #default="{ row }">
          <el-space wrap>
            <span>{{ row.sector_category || '-' }}</span>
            <el-tag v-if="Number.isFinite(row.sector_rating_score)" size="small" :type="sectorTagType(row.sector_rating_score)" effect="plain">
              {{ row.sector_rating_score }}/10
            </el-tag>
          </el-space>
        </template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" label="业务模型" min-width="150">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <div>{{ businessModelLabel(row.business_model?.model) }}</div>
                <div v-if="row.business_model?.revenue_score_cap_reason">{{ row.business_model.revenue_score_cap_reason }}</div>
                <div v-if="row.business_model?.reason">依据：{{ row.business_model.reason }}</div>
              </div>
            </template>
            <el-tag v-if="row.sector_category === '生物医药'" :type="row.business_model?.requires_review ? 'warning' : 'success'" effect="plain">
              {{ businessModelLabel(row.business_model?.model) }}
            </el-tag>
            <span v-else>-</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" prop="ev_sales" label="估值" width="120" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <div>市值：{{ formatUSD(row.valuation?.market_cap_usd) }}</div>
                <div>净现金：{{ formatUSD(netCash(row.valuation)) }}</div>
                <div>EV：{{ formatUSD(row.valuation?.enterprise_value_usd) }}</div>
                <div>TTM 收入：{{ formatUSD(row.valuation?.ttm_revenue_usd) }}</div>
                <div>EV/Sales：{{ formatMultiple(row.valuation?.ev_sales) }}｜P/S：{{ formatMultiple(row.valuation?.price_to_sales) }}</div>
                <div v-if="row.valuation?.reasons?.length">{{ valuationReasonText(row.valuation?.reasons) }}</div>
              </div>
            </template>
            <span class="metric-help">{{ formatMultiple(row.valuation?.ev_sales ?? row.valuation?.price_to_sales) }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="quarterly_revenue_yoy_pct" label="季度同比" width="110" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <div v-for="line in revenueGrowthCalculationTooltipLines(row, 'quarterly_yoy')" :key="line">{{ line }}</div>
              </div>
            </template>
            <span class="metric-help">{{ formatPct(revenueGrowthQuarterly(row)) }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" prop="quarterly_revenue_qoq_pct" label="季度环比" width="110" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <div v-for="line in revenueGrowthCalculationTooltipLines(row, 'quarterly_qoq')" :key="line">{{ line }}</div>
              </div>
            </template>
            <span class="metric-help">{{ formatPct(revenueGrowthQuarterlyQoQ(row)) }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" prop="annual_revenue_yoy_pct" label="年度同比" width="110" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <div v-for="line in revenueGrowthCalculationTooltipLines(row, 'annual_yoy')" :key="line">{{ line }}</div>
              </div>
            </template>
            <span class="metric-help">{{ formatPct(revenueGrowthAnnual(row)) }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" prop="annual_revenue_qoq_pct" label="年度环比" width="110" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <div v-for="line in revenueGrowthCalculationTooltipLines(row, 'annual_qoq')" :key="line">{{ line }}</div>
              </div>
            </template>
            <span class="metric-help">{{ formatPct(revenueGrowthAnnualQoQ(row)) }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="cash_runway_months" label="现金 runway" width="120" align="right" sortable="custom">
        <template #default="{ row }"><el-tooltip :content="cashRunwayTooltip(row.cash_runway_months)" placement="top"><span class="metric-help">{{ formatMonths(row.cash_runway_months) }}</span></el-tooltip></template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" label="表现" width="140" align="right">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <div>基准：{{ row.performance?.base_date || '-' }} / {{ formatPrice(row.performance?.base_close, 'USD') }}</div>
                <div>1日：{{ formatPerformance(row.performance?.return_1d) }} {{ row.performance?.date_1d || '' }}</div>
                <div>5日：{{ formatPerformance(row.performance?.return_5d) }} {{ row.performance?.date_5d || '' }}</div>
                <div>20日：{{ formatPerformance(row.performance?.return_20d) }} {{ row.performance?.date_20d || '' }}</div>
              </div>
            </template>
            <span class="metric-help">{{ formatPerformance(row.performance?.return_5d ?? row.performance?.return_1d) }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" label="核心信号" min-width="220">
        <template #default="{ row }">
          <el-space wrap>
            <el-tag v-if="row.recent_qualified_insider" type="success" effect="plain">内部人买入</el-tag>
            <el-tooltip v-if="row.active_blocks_a" placement="top" effect="dark">
              <template #content>
                <div class="metric-tooltip">
                  <div v-for="line in capitalRiskTooltipLines(row, 'A')" :key="line">{{ line }}</div>
                </div>
              </template>
              <el-tag type="danger" effect="plain" class="risk-tag">阻断A</el-tag>
            </el-tooltip>
            <el-tooltip v-if="row.active_blocks_b" placement="top" effect="dark">
              <template #content>
                <div class="metric-tooltip">
                  <div v-for="line in capitalRiskTooltipLines(row, 'B')" :key="line">{{ line }}</div>
                </div>
              </template>
              <el-tag type="danger" effect="plain" class="risk-tag">阻断B</el-tag>
            </el-tooltip>
            <el-tag v-if="!row.active_blocks_a && !row.active_blocks_b" type="info" effect="plain">无阻断风险</el-tag>
            <el-tag v-for="tag in displayQualityTags(row.quality_tags)" :key="tag" :type="qualityTagType(tag)" effect="plain">
              {{ qualityTagLabel(tag) }}
            </el-tag>
          </el-space>
        </template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" label="分项" min-width="260">
        <template #default="{ row }">
          增长 {{ row.revenue_growth_score }} / 现金 {{ row.cash_runway_score }} / 内幕 {{ row.insider_score }} / 稀释 {{ row.dilution_risk_score }}
        </template>
      </el-table-column>
      <el-table-column v-if="candidateTableView === 'full'" prop="reason_code" label="原因" min-width="140" />
      <el-table-column label="操作" width="180" fixed="right">
        <template #default="{ row }">
          <el-space>
            <el-button link type="primary" :loading="detailLoadingTicker === row.ticker" @click="openDetail(row)">详情</el-button>
            <el-tag v-if="row.followed" size="small" type="warning" effect="plain">已关注</el-tag>
            <el-button v-else link type="primary" :loading="watchingTicker === row.ticker" @click="addToCandidateWatches(row)">关注</el-button>
          </el-space>
        </template>
      </el-table-column>
    </el-table>

    <div class="pagination-row">
      <el-pagination
        background
        layout="prev, pager, next, total"
        :page-size="pageSize"
        :current-page="page"
        :total="total"
        @current-change="onPageChange"
      />
    </div>

    <el-dialog v-model="summaryVisible" title="小盘候选通知 dry-run 预检" width="760px">
      <el-alert
        :type="notificationPreview?.enabled ? 'success' : 'warning'"
        :closable="false"
        show-icon
        :title="notificationPreviewStatus"
        class="summary-alert"
      />
      <div v-if="summary" class="summary-dialog">
        <el-descriptions :column="3" border size="small">
          <el-descriptions-item label="批次">{{ summary.batch_id || '-' }}</el-descriptions-item>
          <el-descriptions-item label="A级候选">{{ summary.total_a }}</el-descriptions-item>
          <el-descriptions-item label="B级候选">{{ summary.total_b }}</el-descriptions-item>
          <el-descriptions-item v-if="notificationPreview" label="通知等级">
            A: {{ notificationPreview.settings.notify_a ? '开' : '关' }} / B: {{ notificationPreview.settings.notify_b ? '开' : '关' }}
          </el-descriptions-item>
          <el-descriptions-item v-if="notificationPreview" label="发送时间">{{ notificationPreview.settings.send_time }}</el-descriptions-item>
          <el-descriptions-item v-if="notificationPreview" label="每级最多">{{ notificationPreview.settings.max_per_grade }}</el-descriptions-item>
          <el-descriptions-item v-if="notificationPreview" label="仅行动候选">{{ notificationPreview.settings.actionable_only ? '是' : '否' }}</el-descriptions-item>
          <el-descriptions-item v-if="notificationPreview" label="通知最低综合排序">{{ notificationPreview.settings.min_review_priority_score || '-' }}</el-descriptions-item>
        </el-descriptions>
        <el-input
          :model-value="summary.message"
          type="textarea"
          :autosize="{ minRows: 8, maxRows: 16 }"
          readonly
          class="summary-message"
        />
        <el-tabs>
          <el-tab-pane :label="`A级候选 (${summary.items_a.length})`">
            <el-table :data="summary.items_a" border size="small" empty-text="暂无A级候选">
              <el-table-column prop="ticker" label="Ticker" width="100" />
              <el-table-column prop="total_score" label="总分" width="80" align="right" />
              <el-table-column prop="market_cap_usd" label="市值" width="120" align="right">
                <template #default="{ row }">{{ formatUSD(row.market_cap_usd) }}</template>
              </el-table-column>
              <el-table-column prop="revenue_growth_pct" label="收入增长" width="110" align="right">
                <template #default="{ row }">{{ formatPct(row.revenue_growth_pct) }}</template>
              </el-table-column>
              <el-table-column prop="cash_runway_months" label="现金 runway" width="120" align="right">
                <template #default="{ row }">{{ formatMonths(row.cash_runway_months) }}</template>
              </el-table-column>
              <el-table-column label="信号" min-width="160">
                <template #default="{ row }">
                  <el-tag v-if="row.recent_qualified_insider" type="success" effect="plain">内部人买入</el-tag>
                  <el-tag v-else type="info" effect="plain">无内部人买入</el-tag>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>
          <el-tab-pane :label="`B级候选 (${summary.items_b.length})`">
            <el-table :data="summary.items_b" border size="small" empty-text="暂无B级候选">
              <el-table-column prop="ticker" label="Ticker" width="100" />
              <el-table-column prop="total_score" label="总分" width="80" align="right" />
              <el-table-column prop="market_cap_usd" label="市值" width="120" align="right">
                <template #default="{ row }">{{ formatUSD(row.market_cap_usd) }}</template>
              </el-table-column>
              <el-table-column prop="revenue_growth_pct" label="收入增长" width="110" align="right">
                <template #default="{ row }">{{ formatPct(row.revenue_growth_pct) }}</template>
              </el-table-column>
              <el-table-column prop="cash_runway_months" label="现金 runway" width="120" align="right">
                <template #default="{ row }">{{ formatMonths(row.cash_runway_months) }}</template>
              </el-table-column>
              <el-table-column label="风险" min-width="160">
                <template #default="{ row }">
                  <el-tag v-if="row.active_blocks_a || row.active_blocks_b" type="danger" effect="plain">有阻断风险</el-tag>
                  <el-tag v-else type="info" effect="plain">无阻断风险</el-tag>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>
        </el-tabs>
      </div>
      <template #footer>
        <el-checkbox v-model="forceCandidateNotification" class="force-resend-checkbox">
          强制重发（会重复推送）
        </el-checkbox>
        <el-button
          type="primary"
          :loading="sendingNotification"
          :disabled="!notificationPreview || !notificationPreview.enabled || !!notificationPreview.suppressed_reason"
          @click="sendNotification"
        >
          确认发送 Telegram
        </el-button>
        <el-button @click="summaryVisible = false">关闭</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="reportVisible" title="小盘候选日报" width="820px">
      <div v-if="report" class="summary-dialog">
        <el-descriptions :column="3" border size="small">
          <el-descriptions-item label="日期">{{ report.date }}</el-descriptions-item>
          <el-descriptions-item label="批次">{{ report.batch.batch_id || '-' }}</el-descriptions-item>
          <el-descriptions-item label="状态">{{ report.batch.status || '-' }}</el-descriptions-item>
          <el-descriptions-item label="A级">{{ report.summary.total_a }}</el-descriptions-item>
          <el-descriptions-item label="B级">{{ report.summary.total_b }}</el-descriptions-item>
          <el-descriptions-item label="健康">{{ healthStatusLabel(report.health.status) }}</el-descriptions-item>
        </el-descriptions>
        <el-input
          :model-value="report.summary.message"
          type="textarea"
          :autosize="{ minRows: 8, maxRows: 16 }"
          readonly
          class="summary-message"
        />
      </div>
      <template #footer>
        <el-button @click="reportVisible = false">关闭</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="eligibilityCheckVisible" title="检查小盘资格" width="860px" destroy-on-close>
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="检查只读取当前已发布的本地 SEC、财务、Form 4 与行情快照，不会请求外部接口。"
        description="结果按当前候选规则逐项判断，并保存到本地历史；市值、财务和内幕数据日期会随结果一并显示。"
        class="eligibility-check-alert"
      />
      <el-tabs v-model="eligibilityCheckTab">
        <el-tab-pane label="即时检查" name="check">
          <el-form inline class="eligibility-check-form" @submit.prevent="runEligibilityCheck">
            <el-form-item label="Ticker">
              <el-input v-model="eligibilityCheckTicker" placeholder="例如 SLDP" maxlength="16" clearable @keyup.enter="runEligibilityCheck" />
            </el-form-item>
            <el-button type="primary" :loading="eligibilityCheckLoading" @click="runEligibilityCheck">开始检查</el-button>
          </el-form>
          <template v-if="eligibilityCheckResult">
            <el-alert
              :type="eligibilityResultAlertType(eligibilityCheckResult)"
              :closable="false"
              show-icon
              :title="eligibilityCheckResult.summary"
              :description="eligibilityResultMeta(eligibilityCheckResult)"
            />
            <el-alert
              v-if="eligibilityCheckResult.comparison"
              type="warning"
              :closable="false"
              show-icon
              class="eligibility-comparison-alert"
              :title="eligibilityComparisonTitle(eligibilityCheckResult)"
              :description="eligibilityComparisonDescription(eligibilityCheckResult)"
            />
            <el-descriptions :column="3" border size="small" class="eligibility-check-meta">
              <el-descriptions-item label="Ticker">{{ eligibilityCheckResult.ticker }}</el-descriptions-item>
              <el-descriptions-item label="公司">{{ eligibilityCheckResult.company_name || '-' }}</el-descriptions-item>
              <el-descriptions-item label="结论"><el-tag :type="eligibilityGradeTagType(eligibilityCheckResult.grade)" effect="plain">{{ eligibilityGradeLabel(eligibilityCheckResult) }}</el-tag></el-descriptions-item>
            </el-descriptions>
            <el-table :data="eligibilityCheckResult.conditions" border size="small" class="eligibility-check-table">
              <el-table-column prop="applies_to" label="适用范围" width="90" />
              <el-table-column prop="label" label="条件" width="145" />
              <el-table-column prop="requirement" label="规则要求" min-width="210" show-overflow-tooltip />
              <el-table-column prop="actual" label="当前数据" min-width="170" show-overflow-tooltip />
              <el-table-column label="相比上次" width="135" show-overflow-tooltip>
                <template #default="{ row }">{{ eligibilityPreviousActual(eligibilityCheckResult, row) }}</template>
              </el-table-column>
              <el-table-column label="结果" width="108">
                <template #default="{ row }">
                  <el-tooltip v-if="row.detail" :content="row.detail" placement="top">
                    <el-tag :type="eligibilityStatusTagType(row.status)" effect="plain">{{ eligibilityStatusLabel(row.status) }}</el-tag>
                  </el-tooltip>
                  <el-tag v-else :type="eligibilityStatusTagType(row.status)" effect="plain">{{ eligibilityStatusLabel(row.status) }}</el-tag>
                </template>
              </el-table-column>
            </el-table>
          </template>
          <el-empty v-else :image-size="72" description="输入 Ticker 后开始逐项检查" />
        </el-tab-pane>
        <el-tab-pane label="检查历史" name="history">
          <div class="eligibility-history-toolbar">
            <el-input v-model="eligibilityHistoryTicker" placeholder="按 Ticker 筛选" clearable @keyup.enter="loadEligibilityHistory" />
            <el-button :loading="eligibilityHistoryLoading" @click="loadEligibilityHistory">查询</el-button>
          </div>
          <el-table :data="eligibilityHistory" border size="small" max-height="390" empty-text="暂无检查历史" @row-click="openEligibilityHistoryItem">
            <el-table-column prop="created_at" label="检查时间" width="170"><template #default="{ row }">{{ formatDateTime(row.created_at) }}</template></el-table-column>
            <el-table-column prop="ticker" label="Ticker" width="100" />
            <el-table-column prop="company_name" label="公司" min-width="170" show-overflow-tooltip />
            <el-table-column label="数据日期" width="135"><template #default="{ row }">行情 {{ row.result.market_as_of || '-' }}<br>SEC {{ row.result.security_as_of || '-' }}</template></el-table-column>
            <el-table-column label="候选池" width="95"><template #default="{ row }"><el-tag :type="row.in_small_cap_pool ? 'success' : 'info'" effect="plain">{{ row.in_small_cap_pool ? '✅ 在池内' : '❌ 不在池' }}</el-tag></template></el-table-column>
            <el-table-column label="结论" width="105"><template #default="{ row }"><el-tag :type="eligibilityGradeTagType(row.grade)" effect="plain">{{ eligibilityHistoryGradeLabel(row) }}</el-tag></template></el-table-column>
            <el-table-column label="操作" width="75" fixed="right"><template #default="{ row }"><el-button link type="primary" @click.stop="openEligibilityHistoryItem(row)">查看</el-button></template></el-table-column>
          </el-table>
          <el-pagination
            v-if="eligibilityHistoryTotal > 0"
            v-model:current-page="eligibilityHistoryPage"
            :page-size="10"
            layout="total, prev, pager, next"
            :total="eligibilityHistoryTotal"
            class="eligibility-history-pagination"
            @current-change="loadEligibilityHistory"
          />
        </el-tab-pane>
      </el-tabs>
      <template #footer>
        <el-button @click="eligibilityCheckVisible = false">关闭</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="detailVisible" title="候选证据链" size="720px">
      <div v-if="candidateDetail" class="candidate-detail">
        <el-card shadow="never">
          <template #header>研究摘要</template>
          <div class="detail-summary-grid">
            <div>
              <div class="detail-summary-title">值得关注</div>
              <el-space wrap>
                <el-tag v-for="signal in candidatePositiveSignals(candidateDetail)" :key="signal" type="success" effect="plain">{{ signal }}</el-tag>
              </el-space>
            </div>
            <div>
              <div class="detail-summary-title">需要谨慎</div>
              <el-space wrap>
                <el-tag v-for="risk in candidateRiskSignals(candidateDetail)" :key="risk" :type="risk === '暂无明显风险' ? 'info' : 'warning'" effect="plain">{{ risk }}</el-tag>
              </el-space>
            </div>
          </div>
        </el-card>
        <el-card shadow="never">
          <template #header>
            <div class="card-header-actions">
              <span>建议下一步（研究工作流）</span>
              <el-tag :type="researchNextStepTagType(candidateDetail.research_next_step?.priority)" effect="plain">
                {{ researchNextStepPriorityLabel(candidateDetail.research_next_step?.priority) }}
              </el-tag>
            </div>
          </template>
          <el-alert
            :type="researchNextStepAlertType(candidateDetail.research_next_step?.priority)"
            :closable="false"
            show-icon
            :title="candidateDetail.research_next_step?.action || '阅读近期 SEC 文件并设定研究论点'"
            :description="candidateDetail.research_next_step?.rationale || '先确认催化剂、可证伪判断与失效条件；本提示仅用于研究流程，不构成投资建议。'"
          />
          <div v-if="candidateDetail.research_next_step?.reasons?.length" class="research-next-step-reasons">
            <el-tag v-for="reason in candidateDetail.research_next_step.reasons" :key="reason" size="small" type="info" effect="plain">{{ readinessReasonLabel(reason) }}</el-tag>
          </div>
        </el-card>
        <el-descriptions :column="2" border>
          <el-descriptions-item label="Ticker">{{ candidateDetail.score.ticker }}</el-descriptions-item>
          <el-descriptions-item label="公司">{{ candidateDetail.security.company_name }}</el-descriptions-item>
          <el-descriptions-item label="等级">{{ gradeLabel(candidateDetail.score.grade) }}</el-descriptions-item>
          <el-descriptions-item label="总分">{{ candidateDetail.score.total_score }}</el-descriptions-item>
          <el-descriptions-item label="市值">{{ formatUSD(candidateDetail.score.market_cap_usd) }}</el-descriptions-item>
          <el-descriptions-item label="SIC">{{ candidateDetail.security.sic || '-' }}</el-descriptions-item>
        </el-descriptions>

        <el-card shadow="never" class="candidate-ai-card">
          <template #header><div class="card-header-actions"><span>AI 研判（手动）</span><el-space><el-select v-model="candidateAIProvider" placeholder="选择模型" size="small" style="width:210px"><el-option v-for="provider in aiProviders" :key="provider.id" :label="`${provider.name} · ${provider.model}`" :value="provider.id" /></el-select><el-select v-model="candidateAIPromptTemplate" placeholder="选择模板" size="small" style="width:180px"><el-option v-for="template in aiPromptTemplates" :key="template.id" :label="template.name" :value="template.id" /></el-select><el-button type="primary" size="small" :disabled="!candidateAIProvider || !candidateAIPromptTemplate" :loading="candidateAIGenerating" @click="generateCandidateAI">生成研判</el-button></el-space></div></template>
          <el-alert v-if="!aiProviders.length" type="info" :closable="false" title="尚未配置可用 AI 模型；请在系统配置 → AI 分析中添加供应商。" />
          <template v-else-if="candidateAIAnalyses.length"><el-select v-model="candidateAIAnalysisID" size="small" style="width:100%;margin-bottom:12px"><el-option v-for="item in candidateAIAnalyses" :key="item.id" :label="`${item.provider_name} · ${item.model} · ${item.template_name || '历史模板'} · ${formatDateTime(item.requested_at)}`" :value="item.id" /></el-select><el-alert v-if="activeCandidateAIAnalysis?.status === 'failed'" type="error" :closable="false" :title="activeCandidateAIAnalysis.error_message || 'AI 调用失败'" /><template v-else><AIRequestPrompt :system-prompt="activeCandidateAIAnalysis?.system_prompt" :user-prompt="activeCandidateAIAnalysis?.user_prompt" /><div class="ai-analysis-content"><MarkdownContent :content="activeCandidateAIAnalysis?.content" /></div></template></template>
          <el-empty v-else-if="aiProviders.length" description="尚无 AI 研判记录；仅在手动点击后生成。" :image-size="44" />
          <el-alert v-show="activeCandidateAIAnalysis?.status === 'queued' || activeCandidateAIAnalysis?.status === 'running'" type="warning" :closable="false" title="AI 研判正在后台处理，页面会自动刷新结果。" />
        </el-card>

        <el-card v-if="candidateDetail.company_profile" shadow="never">
          <template #header>
            <div class="card-header-actions">
              <span>公司概览（SEC + Longbridge）</span>
              <el-button size="small" :loading="companyProfileRefreshing" @click="refreshCandidateCompanyProfile">刷新公司资料</el-button>
            </div>
          </template>
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="公司">{{ candidateDetail.company_profile.company_name || candidateDetail.security.company_name || '-' }}</el-descriptions-item>
            <el-descriptions-item label="交易所">{{ candidateDetail.company_profile.exchange || '-' }}</el-descriptions-item>
            <el-descriptions-item label="CIK">{{ candidateDetail.company_profile.cik || '-' }}</el-descriptions-item>
            <el-descriptions-item label="注册州/地区">{{ candidateDetail.company_profile.state_of_incorporation || '-' }}</el-descriptions-item>
            <el-descriptions-item label="SEC 行业（SIC）" :span="2">
              {{ candidateDetail.company_profile.sic_description || (candidateDetail.company_profile.sic ? `SIC ${candidateDetail.company_profile.sic}` : '-') }}
            </el-descriptions-item>
            <el-descriptions-item label="业务概览" :span="2">{{ candidateDetail.company_profile.business_summary }}</el-descriptions-item>
            <el-descriptions-item v-if="candidateDetail.company_profile.website" label="官网"><a :href="companyProfileWebsiteURL(candidateDetail.company_profile.website)" target="_blank" rel="noopener">{{ candidateDetail.company_profile.website }}</a></el-descriptions-item>
            <el-descriptions-item v-if="candidateDetail.company_profile.founded" label="成立时间">{{ candidateDetail.company_profile.founded }}</el-descriptions-item>
            <el-descriptions-item v-if="candidateDetail.company_profile.listing_date" label="上市时间">{{ candidateDetail.company_profile.listing_date }}</el-descriptions-item>
            <el-descriptions-item v-if="candidateDetail.company_profile.market" label="上市市场">{{ candidateDetail.company_profile.market }}</el-descriptions-item>
            <el-descriptions-item v-if="candidateDetail.company_profile.employees" label="员工数">{{ candidateDetail.company_profile.employees }}</el-descriptions-item>
            <el-descriptions-item v-if="candidateDetail.company_profile.manager" label="管理者">{{ candidateDetail.company_profile.manager }}</el-descriptions-item>
            <el-descriptions-item v-if="candidateDetail.company_profile.year_end" label="财年截止日">{{ candidateDetail.company_profile.year_end }}</el-descriptions-item>
            <el-descriptions-item v-if="candidateDetail.company_profile.address" label="公司地址" :span="2">{{ candidateDetail.company_profile.address }}</el-descriptions-item>
            <el-descriptions-item label="来源" :span="2">
              {{ candidateDetail.company_profile.summary_source }}<span v-if="candidateDetail.company_profile.profile_fetched_at"> · Longbridge 更新于 {{ formatDateTime(candidateDetail.company_profile.profile_fetched_at) }}</span><span v-else-if="candidateDetail.company_profile.metadata_as_of"> · SEC 同步于 {{ formatDateTime(candidateDetail.company_profile.metadata_as_of) }}</span>
            </el-descriptions-item>
          </el-descriptions>
        </el-card>

        <el-card shadow="never">
          <template #header>市场一致目标价与合理价值情景</template>
          <el-alert type="warning" :closable="false" show-icon title="不提供 Longbridge “公允价值”结论：市场一致目标价与本地历史估值情景必须分开阅读，均不构成投资建议。" />
          <template v-if="candidateDetail.fair_value?.status === 'available'">
            <el-descriptions :column="3" border size="small" style="margin-top: 12px">
              <el-descriptions-item label="参考收盘价">{{ formatFairValuePrice(candidateDetail.fair_value.reference_price, candidateDetail.fair_value.currency) }}<span v-if="candidateDetail.fair_value.reference_price_date"> · {{ candidateDetail.fair_value.reference_price_date }}</span></el-descriptions-item>
              <el-descriptions-item label="市场一致目标价（平均）">{{ formatFairValuePrice(candidateDetail.fair_value.market_consensus_target, candidateDetail.fair_value.currency) }}</el-descriptions-item>
              <el-descriptions-item label="目标价相对空间">{{ formatForecastNumber(candidateDetail.fair_value.market_consensus_upside_pct, '%') }}</el-descriptions-item>
              <el-descriptions-item label="市场目标价区间" :span="2">{{ formatFairValuePrice(candidateDetail.fair_value.market_consensus_low, candidateDetail.fair_value.currency) }} - {{ formatFairValuePrice(candidateDetail.fair_value.market_consensus_high, candidateDetail.fair_value.currency) }}</el-descriptions-item>
              <el-descriptions-item label="机构覆盖数">{{ candidateDetail.fair_value.analyst_count || '-' }}</el-descriptions-item>
              <el-descriptions-item v-if="candidateDetail.fair_value.local_historical_scenario" label="本地历史倍数情景（低 / 中 / 高）" :span="3">
                {{ formatFairValuePrice(candidateDetail.fair_value.local_historical_scenario.low, candidateDetail.fair_value.currency) }} / {{ formatFairValuePrice(candidateDetail.fair_value.local_historical_scenario.mid, candidateDetail.fair_value.currency) }} / {{ formatFairValuePrice(candidateDetail.fair_value.local_historical_scenario.high, candidateDetail.fair_value.currency) }}（{{ candidateDetail.fair_value.local_historical_scenario.metrics }} 个可用指标等权）
              </el-descriptions-item>
              <el-descriptions-item label="本地参考价来源" :span="3">{{ candidateDetail.fair_value.reference_price_source || '-' }}</el-descriptions-item>
            </el-descriptions>
            <div v-if="candidateDetail.fair_value.metric_scenarios.length" class="analyst-rating-provenance-title">本地计算输入与过程</div>
            <el-table v-if="candidateDetail.fair_value.metric_scenarios.length" :data="candidateDetail.fair_value.metric_scenarios" size="small" border>
              <el-table-column prop="metric" label="指标" width="75" />
              <el-table-column label="当前倍数" width="100" align="right"><template #default="{ row }">{{ formatForecastNumber(row.current_multiple) }}</template></el-table-column>
              <el-table-column label="历史低 / 中 / 高" min-width="180" align="right"><template #default="{ row }">{{ formatForecastNumber(row.historical_low) }} / {{ formatForecastNumber(row.historical_mid) }} / {{ formatForecastNumber(row.historical_high) }}</template></el-table-column>
              <el-table-column label="推导价格低 / 中 / 高" min-width="210" align="right"><template #default="{ row }">{{ formatFairValuePrice(row.price_low, candidateDetail.fair_value.currency) }} / {{ formatFairValuePrice(row.price_mid, candidateDetail.fair_value.currency) }} / {{ formatFairValuePrice(row.price_high, candidateDetail.fair_value.currency) }}</template></el-table-column>
            </el-table>
            <el-alert type="info" :closable="false" style="margin-top: 12px" :title="candidateDetail.fair_value.methodology" :description="candidateDetail.fair_value.message" />
          </template>
          <el-alert v-else type="info" :closable="false" show-icon style="margin-top:12px" :title="candidateDetail.fair_value?.message || '尚缺机构目标价或可用估值倍数，无法计算本地历史估值情景。'" />
        </el-card>

        <el-card shadow="never">
          <template #header><div class="card-header-actions"><span>估值历史与同业比较（Longbridge）</span><el-button size="small" :loading="valuationResearchRefreshing" @click="refreshCandidateValuationResearch">刷新估值研究</el-button></div></template>
          <el-alert type="info" :closable="false" show-icon class="business-model-alert" title="小盘与亏损公司可能缺少 PE、同业或历史覆盖；此数据仅用于研究比较，绝不作为候选硬筛选。" />
          <template v-if="candidateDetail.valuation_research?.latest">
            <el-table :data="valuationMetricRows(candidateDetail.valuation_research.latest)" size="small" border style="margin-top: 12px">
              <el-table-column prop="metric" label="指标" width="80" />
              <el-table-column label="当前" width="110" align="right"><template #default="{ row }">{{ formatForecastNumber(row.current) }}</template></el-table-column>
              <el-table-column label="历史低 / 中 / 高" min-width="210" align="right"><template #default="{ row }">{{ formatForecastNumber(row.low) }} / {{ formatForecastNumber(row.median) }} / {{ formatForecastNumber(row.high) }}</template></el-table-column>
              <el-table-column label="行业分位" width="150"><template #default="{ row }">{{ valuationPercentileText(row.percentile) }}</template></el-table-column>
              <el-table-column label="历史点数" width="100" align="right"><template #default="{ row }">{{ row.history.length }}</template></el-table-column>
            </el-table>
            <el-alert v-if="candidateDetail.valuation_research.latest.change_summary" type="warning" :closable="false" style="margin-top: 12px" :title="`估值快照变化：${candidateDetail.valuation_research.latest.change_summary}`" />
            <div class="analyst-rating-provenance-title">同业比较（Longbridge 返回范围）</div>
            <el-table :data="candidateDetail.valuation_research.latest.peers" size="small" border empty-text="Longbridge 暂无可比同业覆盖">
              <el-table-column prop="symbol" label="代码" width="110" />
              <el-table-column prop="name" label="公司" min-width="200" show-overflow-tooltip />
              <el-table-column label="PE" width="95" align="right"><template #default="{ row }">{{ formatForecastNumber(row.pe) }}</template></el-table-column>
              <el-table-column label="PB" width="95" align="right"><template #default="{ row }">{{ formatForecastNumber(row.pb) }}</template></el-table-column>
              <el-table-column label="PS" width="95" align="right"><template #default="{ row }">{{ formatForecastNumber(row.ps) }}</template></el-table-column>
            </el-table>
          </template>
          <el-alert v-else type="info" :closable="false" show-icon style="margin-top:12px" :title="candidateDetail.valuation_research?.message || '尚未同步 Longbridge 估值研究'" />
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-header-actions">
              <span>市场预期、异动与机构持仓（Longbridge）</span>
              <el-button size="small" :loading="marketResearchRefreshing" @click="refreshCandidateMarketResearch">刷新 P1 市场研究</el-button>
            </div>
          </template>
          <el-alert type="info" :closable="false" show-icon title="这些是独立研究补充，不计入基本面总分。市场异动只会在 72 小时内增加短线复核优先级。" class="business-model-alert" />
          <template v-if="candidateDetail.market_research?.eps_forecast?.latest">
            <el-descriptions :column="3" border size="small" style="margin-top: 12px">
              <el-descriptions-item label="EPS 预期中位数">{{ formatForecastNumber(candidateDetail.market_research.eps_forecast.latest.median) }}</el-descriptions-item>
              <el-descriptions-item label="EPS 预期区间">{{ formatForecastRange(candidateDetail.market_research.eps_forecast.latest) }}</el-descriptions-item>
              <el-descriptions-item label="上修 / 下修机构">{{ candidateDetail.market_research.eps_forecast.latest.institution_up }} / {{ candidateDetail.market_research.eps_forecast.latest.institution_down }}（共 {{ candidateDetail.market_research.eps_forecast.latest.institution_total }}）</el-descriptions-item>
              <el-descriptions-item label="快照时间">{{ formatDateTime(candidateDetail.market_research.eps_forecast.latest.fetched_at) }}</el-descriptions-item>
              <el-descriptions-item label="预期变化" :span="2">{{ candidateDetail.market_research.eps_forecast.latest.change_summary || '与上一快照无有效变化，或尚无可比较历史。' }}</el-descriptions-item>
            </el-descriptions>
          </template>
          <el-alert v-else type="info" :closable="false" show-icon style="margin-top: 12px" :title="candidateDetail.market_research?.eps_forecast?.message || '尚未同步 EPS 市场预期'" />

          <div class="analyst-rating-provenance-title">近期市场异动（最多 20 条）</div>
          <el-table :data="candidateDetail.market_research?.anomalies || []" size="small" border empty-text="暂无 Longbridge 异动记录">
            <el-table-column prop="alert_name" label="异动类型" min-width="150" />
            <el-table-column label="内容" min-width="180"><template #default="{ row }">{{ anomalyValues(row.values_json) }}</template></el-table-column>
            <el-table-column label="方向" width="90"><template #default="{ row }"><el-tag :type="row.emotion === 1 ? 'success' : row.emotion === 2 ? 'danger' : 'info'" effect="plain">{{ anomalyEmotionLabel(row.emotion) }}</el-tag></template></el-table-column>
            <el-table-column label="发生时间" width="170"><template #default="{ row }">{{ formatDateTime(row.alert_time) }}</template></el-table-column>
          </el-table>

          <div class="analyst-rating-provenance-title">机构股东增减持</div>
          <el-table :data="candidateDetail.market_research?.institutional_holders || []" size="small" border empty-text="暂无 Longbridge 机构股东数据">
            <el-table-column prop="holder_name" label="机构" min-width="200" show-overflow-tooltip />
            <el-table-column prop="institution_type" label="类型" min-width="115" />
            <el-table-column label="持股比例" width="110" align="right"><template #default="{ row }">{{ formatForecastNumber(row.percent_of_shares, '%') }}</template></el-table-column>
            <el-table-column label="持股变化" width="120" align="right"><template #default="{ row }">{{ formatForecastNumber(row.shares_changed) }}</template></el-table-column>
            <el-table-column prop="report_date" label="报告日" width="115" />
          </el-table>

          <div class="analyst-rating-provenance-title">基金 / ETF 持仓</div>
          <el-table :data="candidateDetail.market_research?.fund_holders || []" size="small" border empty-text="暂无 Longbridge 基金持仓数据">
            <el-table-column prop="fund_name" label="基金" min-width="220" show-overflow-tooltip />
            <el-table-column prop="fund_symbol" label="代码" width="110" />
            <el-table-column label="持仓权重" width="110" align="right"><template #default="{ row }">{{ formatForecastNumber(row.position_ratio, '%') }}</template></el-table-column>
            <el-table-column prop="report_date" label="报告日" width="115" />
          </el-table>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-header-actions">
              <span>机构与分析师共识（Longbridge）</span>
              <el-button size="small" :loading="analystRatingRefreshing" @click="refreshCandidateAnalystRating">刷新分析师评级</el-button>
            </div>
          </template>
          <template v-if="candidateDetail.analyst_rating?.latest?.status === 'available'">
            <el-descriptions :column="3" border size="small">
              <el-descriptions-item label="共识评级"><el-tag :type="analystRecommendationTagType(candidateDetail.analyst_rating.latest.recommendation)" effect="plain">{{ analystRecommendationLabel(candidateDetail.analyst_rating.latest.recommendation) }}</el-tag></el-descriptions-item>
              <el-descriptions-item label="覆盖数">{{ candidateDetail.analyst_rating.latest.analyst_count }}</el-descriptions-item>
              <el-descriptions-item label="数据源">{{ candidateDetail.analyst_rating.latest.provider }}</el-descriptions-item>
              <el-descriptions-item label="市场一致目标价（平均）">{{ formatAnalystPrice(candidateDetail.analyst_rating.latest.target_average_micros, candidateDetail.analyst_rating.latest.currency) }}</el-descriptions-item>
              <el-descriptions-item label="目标价区间">{{ formatAnalystPrice(candidateDetail.analyst_rating.latest.target_low_micros, candidateDetail.analyst_rating.latest.currency) }} - {{ formatAnalystPrice(candidateDetail.analyst_rating.latest.target_high_micros, candidateDetail.analyst_rating.latest.currency) }}</el-descriptions-item>
              <el-descriptions-item label="参考收盘价">{{ formatAnalystPrice(candidateDetail.analyst_rating.latest.reference_price_micros, candidateDetail.analyst_rating.latest.currency) }}</el-descriptions-item>
              <el-descriptions-item label="评级分布" :span="3">强烈买入 {{ candidateDetail.analyst_rating.latest.strong_buy_count }} · 买入 {{ candidateDetail.analyst_rating.latest.buy_count }} · 持有 {{ candidateDetail.analyst_rating.latest.hold_count }} · 跑输 {{ candidateDetail.analyst_rating.latest.underperform_count }} · 卖出 {{ candidateDetail.analyst_rating.latest.sell_count }}</el-descriptions-item>
              <el-descriptions-item label="提供方更新时间" :span="3">{{ analystProviderTimeText(candidateDetail.analyst_rating.latest) }}</el-descriptions-item>
            </el-descriptions>
            <div class="analyst-rating-provenance-title">结果溯源明细</div>
            <el-table :data="analystRatingProvenanceRows(candidateDetail.analyst_rating.latest)" size="small" border class="analyst-rating-provenance-table">
              <el-table-column prop="result" label="分析结果" min-width="125" />
              <el-table-column prop="value" label="当前值" min-width="170" show-overflow-tooltip />
              <el-table-column prop="source" label="数据来源 / 原始聚合字段" min-width="255" show-overflow-tooltip />
              <el-table-column prop="providerUpdatedAt" label="提供方时间" width="150" show-overflow-tooltip />
              <el-table-column prop="fetchedAt" label="本地同步时间" width="170" />
              <el-table-column prop="note" label="说明" min-width="200" show-overflow-tooltip />
            </el-table>
            <div v-if="candidateDetail.analyst_rating.history?.length > 1" class="analyst-rating-provenance-title">快照变更历史（仅在聚合值变化时新增）</div>
            <el-table v-if="candidateDetail.analyst_rating.history?.length > 1" :data="candidateDetail.analyst_rating.history.slice(0, 12)" size="small" border class="score-history-table" style="margin-top: 12px">
              <el-table-column label="同步时间" width="170"><template #default="{ row }">{{ formatDateTime(row.fetched_at) }}</template></el-table-column>
              <el-table-column label="评级" width="110"><template #default="{ row }">{{ analystRecommendationLabel(row.recommendation) }}</template></el-table-column>
              <el-table-column prop="analyst_count" label="覆盖数" width="85" align="right" />
              <el-table-column label="平均目标价" width="130" align="right"><template #default="{ row }">{{ formatAnalystPrice(row.target_average_micros, row.currency) }}</template></el-table-column>
              <el-table-column prop="change_summary" label="有效变化" min-width="180" show-overflow-tooltip />
            </el-table>
          </template>
          <el-alert v-else type="info" :closable="false" show-icon :title="candidateDetail.analyst_rating?.message || '尚未同步分析师共识'" description="小盘股可能没有公开分析师覆盖；这不是 SEC、财务或行情数据缺失。可手动刷新当前标的，不会重跑候选工作流。" />
        </el-card>

        <el-card shadow="never">
          <template #header>可交易性闸门（研究用）</template>
          <el-alert :type="investabilityAlertType(candidateDetail.investability?.status)" :closable="false" show-icon :title="investabilityDetailTitle(candidateDetail.investability)" :description="investabilityDetailDescription(candidateDetail.investability)" />
        </el-card>

        <el-card shadow="never">
          <template #header>股本稀释趋势（SEC）</template>
          <el-alert :type="dilutionAlertType(candidateDetail.dilution_trend?.status)" :closable="false" show-icon :title="dilutionDetailTitle(candidateDetail.dilution_trend)" :description="dilutionTooltipLines(candidateDetail.dilution_trend).slice(1).join('；')" />
        </el-card>

        <el-card shadow="never">
          <template #header>评分拆解</template>
          <el-descriptions :column="3" border size="small">
            <el-descriptions-item label="收入增长">{{ candidateDetail.score.revenue_growth_score }}</el-descriptions-item>
            <el-descriptions-item label="现金储备">{{ candidateDetail.score.cash_runway_score }}</el-descriptions-item>
            <el-descriptions-item label="内幕增持">{{ candidateDetail.score.insider_score }}</el-descriptions-item>
            <el-descriptions-item label="毛利率">{{ candidateDetail.score.gross_margin_score }}</el-descriptions-item>
            <el-descriptions-item label="稀释风险">{{ candidateDetail.score.dilution_risk_score }}</el-descriptions-item>
            <el-descriptions-item label="赛道空间">{{ candidateDetail.score.sector_score }}</el-descriptions-item>
          </el-descriptions>
        </el-card>

        <el-card shadow="never">
          <template #header>评分历史与入选事件</template>
          <el-alert type="info" :closable="false" show-icon class="business-model-alert" title="仅比较已发布候选批次；分数变化不等于基本面变化，请结合下方的变化原因与证据溯源复核。" />
          <el-table :data="candidateDetail.score_history || []" size="small" border class="score-history-table" empty-text="仅有当前评分批次，暂无可比历史">
            <el-table-column prop="effective_date" label="有效日" width="115" />
            <el-table-column prop="grade" label="等级" width="75"><template #default="{ row }"><el-tag :type="gradeTagType(row.grade)" effect="plain">{{ gradeLabel(row.grade) }}</el-tag></template></el-table-column>
            <el-table-column prop="total_score" label="总分" width="75" align="right" />
            <el-table-column label="较前批" width="90" align="right"><template #default="{ row }">{{ scoreHistoryDelta(row.score_delta) }}</template></el-table-column>
            <el-table-column label="状态" width="90"><template #default="{ row }"><el-tag :type="changeStatusTagType(row.change_status)" effect="plain">{{ changeStatusLabel(row.change_status) }}</el-tag></template></el-table-column>
            <el-table-column label="核心变化" min-width="230" show-overflow-tooltip><template #default="{ row }">{{ scoreHistoryReasonSummary(row.change_reasons) }}</template></el-table-column>
            <el-table-column prop="scoring_version" label="评分版本" width="150" show-overflow-tooltip />
          </el-table>
          <div v-if="candidateDetail.signal_events?.length" class="signal-event-history">
            <div class="signal-event-heading">不可变入选事件</div>
            <el-timeline>
              <el-timeline-item v-for="event in candidateDetail.signal_events" :key="event.id" type="success" :timestamp="formatDate(event.signal_date)">
                {{ candidateSignalEventLabel(event.event_type) }} · {{ gradeLabel(event.grade) }} · {{ event.total_score }} 分
                <span v-if="event.baseline_trade_date">（锚定价 {{ formatPrice(event.baseline_close_micros / 1_000_000, 'USD') }}，{{ formatDate(event.baseline_trade_date) }}，{{ priceSourceLabel(event.price_source) }}）</span>
              </el-timeline-item>
            </el-timeline>
          </div>
        </el-card>

        <el-card shadow="never">
          <template #header>赛道解释</template>
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="分类">{{ candidateDetail.sector.category }}</el-descriptions-item>
            <el-descriptions-item label="标签">{{ candidateDetail.sector.label }}</el-descriptions-item>
            <el-descriptions-item label="SIC">{{ candidateDetail.sector.sic || '-' }}</el-descriptions-item>
            <el-descriptions-item label="赛道分">{{ candidateDetail.sector.score }}/10</el-descriptions-item>
            <el-descriptions-item label="说明" :span="2">{{ candidateDetail.sector.rationale }}</el-descriptions-item>
          </el-descriptions>
        </el-card>

        <el-card v-if="candidateDetail.sector.category === '生物医药'" shadow="never">
          <template #header>
            <div class="detail-card-header-action">
              <span>生物医药业务模型</span>
              <el-button link type="primary" @click="openBusinessModelEditor">确认/更新</el-button>
            </div>
          </template>
          <el-alert
            v-if="candidateDetail.business_model.requires_review"
            type="warning"
            :closable="false"
            show-icon
            title="业务模型尚需人工确认；该标的不进入“可行动”候选。"
            class="business-model-alert"
          />
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="模型">{{ businessModelLabel(candidateDetail.business_model.model) }}</el-descriptions-item>
            <el-descriptions-item label="可重复收入">{{ candidateDetail.business_model.revenue_repeatable_confirmed ? '已确认' : '未确认' }}</el-descriptions-item>
            <el-descriptions-item label="收入分上限">{{ candidateDetail.business_model.revenue_score_cap }}/30</el-descriptions-item>
            <el-descriptions-item label="复查日期">{{ formatDate(candidateDetail.business_model.review_due_at) }}</el-descriptions-item>
            <el-descriptions-item label="依据" :span="2">{{ candidateDetail.business_model.reason || candidateDetail.business_model.revenue_score_cap_reason || '-' }}</el-descriptions-item>
            <el-descriptions-item v-if="candidateDetail.business_model.source_url" label="来源" :span="2"><el-link :href="candidateDetail.business_model.source_url" target="_blank" type="primary">打开来源</el-link></el-descriptions-item>
          </el-descriptions>
        </el-card>

        <el-card shadow="never">
          <template #header>数据质量</template>
          <el-space wrap>
            <el-tag v-for="(value, key) in candidateDetail.data_quality" :key="key" :type="value === 'valid' ? 'success' : 'warning'" effect="plain">
              {{ key }}: {{ value }}
            </el-tag>
          </el-space>
        </el-card>

        <el-card shadow="never">
          <template #header>证据溯源（当前候选批次）</template>
          <el-alert type="info" :closable="false" show-icon class="business-model-alert" title="此处仅展示生成当前候选所使用的本地快照，不会在打开详情时请求 SEC 或行情接口。" />
          <el-descriptions :column="2" border size="small" class="lineage-batch-meta">
            <el-descriptions-item label="评分批次"><el-text truncated>{{ candidateDetail.data_lineage?.score_batch_id || candidateDetail.batch_id || '-' }}</el-text></el-descriptions-item>
            <el-descriptions-item label="证据批次"><el-text truncated>{{ candidateDetail.data_lineage?.evidence_batch_id || '-' }}</el-text></el-descriptions-item>
            <el-descriptions-item label="批次有效日">{{ candidateDetail.data_lineage?.batch_effective_date || '-' }}</el-descriptions-item>
          </el-descriptions>
          <el-table :data="candidateDetail.data_lineage?.items || []" size="small" border class="lineage-table" empty-text="暂无证据溯源记录">
            <el-table-column prop="label" label="证据" width="120" />
            <el-table-column prop="source" label="来源" min-width="190" show-overflow-tooltip />
            <el-table-column prop="as_of" label="截至" width="120"><template #default="{ row }">{{ row.as_of || '-' }}</template></el-table-column>
            <el-table-column label="状态" width="90"><template #default="{ row }"><el-tag :type="lineageStatusTagType(row.status)" effect="plain">{{ lineageStatusLabel(row.status) }}</el-tag></template></el-table-column>
            <el-table-column prop="detail" label="说明" min-width="240" show-overflow-tooltip />
          </el-table>
        </el-card>

        <el-card shadow="never">
          <template #header>财务证据</template>
          <el-descriptions v-if="candidateDetail.financial" :column="2" border size="small">
            <el-descriptions-item label="季度收入 YoY">{{ formatPct(candidateDetail.financial.quarterly_revenue_yoy_pct) }}</el-descriptions-item>
            <el-descriptions-item label="季度收入 QoQ">{{ formatPct(candidateDetail.financial.quarterly_revenue_qoq_pct) }}</el-descriptions-item>
            <el-descriptions-item label="年度收入 YoY">{{ formatPct(candidateDetail.financial.annual_revenue_yoy_pct) }}</el-descriptions-item>
            <el-descriptions-item label="年度收入环比">{{ formatPct(candidateDetail.financial.annual_revenue_qoq_pct) }}</el-descriptions-item>
            <el-descriptions-item label="现金 Runway">{{ formatMonths(candidateDetail.financial.cash_runway_months) }}</el-descriptions-item>
            <el-descriptions-item label="毛利率">{{ candidateDetail.financial.gross_margin_available ? formatPct(candidateDetail.financial.gross_margin_pct) : '-' }}</el-descriptions-item>
            <el-descriptions-item label="质量标记">{{ financialQualityFlagsLabel(candidateDetail.financial.quality_flags_json) }}</el-descriptions-item>
          </el-descriptions>
          <el-empty v-else description="暂无财务证据" />
        </el-card>

        <el-card shadow="never">
          <template #header>估值快照（SEC + 本地价格）</template>
          <el-alert v-if="candidateDetail.valuation.status !== 'ready'" type="info" :closable="false" show-icon :title="valuationReasonText(candidateDetail.valuation.reasons) || '部分估值证据不足，相关倍数已显示为 N/A。'" class="business-model-alert" />
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="市值">{{ formatUSD(candidateDetail.valuation.market_cap_usd) }}</el-descriptions-item>
            <el-descriptions-item label="企业价值 EV">{{ formatUSD(candidateDetail.valuation.enterprise_value_usd) }}</el-descriptions-item>
            <el-descriptions-item label="现金及短投">{{ formatUSD(candidateDetail.valuation.cash_usd) }}</el-descriptions-item>
            <el-descriptions-item label="总债务">{{ formatUSD(candidateDetail.valuation.total_debt_usd) }}</el-descriptions-item>
            <el-descriptions-item label="TTM 收入">{{ formatUSD(candidateDetail.valuation.ttm_revenue_usd) }}</el-descriptions-item>
            <el-descriptions-item label="TTM 毛利">{{ formatUSD(candidateDetail.valuation.ttm_gross_profit_usd) }}</el-descriptions-item>
            <el-descriptions-item label="EV/Sales">{{ formatMultiple(candidateDetail.valuation.ev_sales) }}</el-descriptions-item>
            <el-descriptions-item label="EV/Gross Profit">{{ formatMultiple(candidateDetail.valuation.ev_gross_profit) }}</el-descriptions-item>
            <el-descriptions-item label="P/S">{{ formatMultiple(candidateDetail.valuation.price_to_sales) }}</el-descriptions-item>
            <el-descriptions-item label="净现金/市值">{{ formatPct(fractionToPct(candidateDetail.valuation.net_cash_to_market_cap)) }}</el-descriptions-item>
            <el-descriptions-item label="价格日期">{{ formatDate(candidateDetail.valuation.price_trade_date) }}</el-descriptions-item>
            <el-descriptions-item label="财务期末">{{ formatDate(candidateDetail.valuation.financial_period_end) }}</el-descriptions-item>
          </el-descriptions>
        </el-card>

        <el-card shadow="never">
          <ProfitHistoryChart :history="candidateDetail.profit_history" />
        </el-card>

        <el-card shadow="never">
          <template #header>技术分析（独立研究信号，不计入基本面总分）</template>
          <el-alert
            v-if="candidateDetail.technical.status !== 'ready'"
            type="warning"
            :closable="false"
            show-icon
            :title="technicalStatusDescription(candidateDetail.technical)"
          />
          <template v-else>
            <div class="technical-signal-row">
              <el-space wrap>
						<el-tag :type="tradeSetupTagType(candidateDetail.technical.trade_setup.status)" effect="plain">{{ tradeSetupLabel(candidateDetail.technical.trade_setup.status) }}</el-tag>
                <el-tag v-for="signal in candidateDetail.technical.signals" :key="signal.kind" type="success" effect="plain">{{ signal.label }}</el-tag>
                <el-tag v-if="!candidateDetail.technical.signals.length" type="info" effect="plain">暂无突破信号</el-tag>
              </el-space>
            </div>
            <el-descriptions :column="2" border size="small">
              <el-descriptions-item label="价格日期">{{ formatDate(candidateDetail.technical.trade_date) }}</el-descriptions-item>
              <el-descriptions-item label="有效样本">{{ candidateDetail.technical.sample_days }}/{{ candidateDetail.technical.required_sample_days }} 个交易日</el-descriptions-item>
              <el-descriptions-item label="收盘价">{{ formatPrice(candidateDetail.technical.close_usd, 'USD') }}</el-descriptions-item>
              <el-descriptions-item label="20 日均线">{{ formatPrice(candidateDetail.technical.ma20_usd, 'USD') }}（{{ formatPerformance(candidateDetail.technical.distance_to_ma20_pct) }}）</el-descriptions-item>
              <el-descriptions-item label="50 日均线">{{ candidateDetail.technical.ma50_usd > 0 ? formatPrice(candidateDetail.technical.ma50_usd, 'USD') : '历史不足' }}</el-descriptions-item>
              <el-descriptions-item label="200 日均线">{{ candidateDetail.technical.ma200_available ? formatPrice(candidateDetail.technical.ma200_usd, 'USD') : '历史不足（需至少 200 个有效交易日）' }}</el-descriptions-item>
              <el-descriptions-item label="前 20 日最高收盘价">{{ formatPrice(candidateDetail.technical.prior_20d_high_usd, 'USD') }}（{{ formatPerformance(candidateDetail.technical.distance_to_20d_high_pct) }}）</el-descriptions-item>
              <el-descriptions-item label="量比">{{ formatRatio(candidateDetail.technical.volume_ratio_20) }}（20 日均量 {{ formatVolume(candidateDetail.technical.average_volume_20) }}）</el-descriptions-item>
              <el-descriptions-item label="相对 IWM（20 日）">{{ relativeStrengthSummary(candidateDetail.technical) }}</el-descriptions-item>
              <el-descriptions-item label="相对 IWM（60 日）">{{ relativeStrengthSummary(candidateDetail.technical, 60) }}</el-descriptions-item>
              <el-descriptions-item label="研究事件锚定价（日线近似）">{{ anchoredVWAPSummary(candidateDetail.technical) }}</el-descriptions-item>
              <el-descriptions-item label="锚定事件">{{ anchoredVWAPEventSummary(candidateDetail.technical) }}</el-descriptions-item>
						<el-descriptions-item label="交易计划状态">{{ tradeSetupLabel(candidateDetail.technical.trade_setup.status) }}</el-descriptions-item>
						<el-descriptions-item label="当前状态开始于">{{ formatDateTime(candidateDetail.technical.trade_setup.status_since) }}</el-descriptions-item>
						<el-descriptions-item label="入场触发">{{ candidateDetail.technical.trade_setup.entry_trigger || '等待触发条件' }}</el-descriptions-item>
						<el-descriptions-item label="计划止损">{{ formatTradeStop(candidateDetail.technical) }}</el-descriptions-item>
						<el-descriptions-item label="20% - 30% 目标区">{{ formatTradeTarget(candidateDetail.technical) }}</el-descriptions-item>
						<el-descriptions-item label="离场规则" :span="2">{{ candidateDetail.technical.trade_setup.exit_reason || '收盘跌破 MA20 时减仓；跌破 MA50 时趋势失效' }}</el-descriptions-item>
						<el-descriptions-item label="计划依据" :span="2">{{ candidateDetail.technical.trade_setup.reasons.join('；') || '-' }}</el-descriptions-item>
            </el-descriptions>
            <div class="trade-setup-history">
              <div class="technical-history-title">交易计划状态历史（仅记录状态变化）</div>
              <el-timeline v-if="candidateDetail.trade_setup_history?.length" class="trade-setup-timeline">
                <el-timeline-item v-for="event in candidateDetail.trade_setup_history" :key="event.id" :timestamp="formatDateTime(event.started_at)" :type="tradeSetupTagType(event.status)">
                  <strong>{{ tradeSetupLabel(event.status) }}</strong>
                  <span v-if="event.previous_status">（由 {{ tradeSetupLabel(event.previous_status) }} 变更）</span>
                  <div class="trade-setup-event-detail">收盘 {{ formatPrice(event.close_usd, 'USD') }} · 止损 {{ formatPrice(event.stop_loss_usd, 'USD') }} · {{ event.entry_trigger || event.exit_reason || '等待触发条件' }}</div>
                  <div v-if="event.reasons?.length" class="trade-setup-event-detail">{{ event.reasons.join('；') }}</div>
                </el-timeline-item>
              </el-timeline>
              <el-empty v-else :image-size="44" description="尚未记录状态变化；下次日线同步后会建立当前状态基线。" />
            </div>
          </template>
          <div class="technical-history-heading">
            <div class="technical-history-title">
              本地日线历史（当前显示 {{ technicalHistoryRows.length }} / {{ candidateDetail.technical_history?.length || 0 }} 个交易日；“历史回填”为手动补齐的历史数据）
            </div>
            <div class="technical-history-controls">
              <el-radio-group v-model="technicalHistoryRange" size="small" aria-label="技术历史时间范围">
                <el-radio-button value="1w">近1周</el-radio-button>
                <el-radio-button value="1m">近1月</el-radio-button>
                <el-radio-button value="3m">近3月</el-radio-button>
                <el-radio-button value="6m">近半年</el-radio-button>
                <el-radio-button value="1y">近1年</el-radio-button>
                <el-radio-button value="all">全部</el-radio-button>
              </el-radio-group>
              <el-radio-group v-model="technicalHistoryView" size="small" aria-label="技术历史展示方式">
                <el-radio-button value="chart">图表</el-radio-button>
                <el-radio-button value="table">列表</el-radio-button>
              </el-radio-group>
            </div>
          </div>
          <el-alert
            v-if="technicalHistoryHasLaterRows"
            type="info"
            :closable="false"
            show-icon
            class="technical-history-asof-alert"
            :title="`当前发布批次的价格与技术信号均截至 ${formatDate(candidateDetail.technical.trade_date)}；图表中之后的本地日线仅供后续观察，不参与当前批次评分或技术信号。`"
          />
          <template v-if="technicalHistoryView === 'chart'">
            <div v-if="technicalHistoryChart.points.length" class="technical-chart" role="img" :aria-label="`${candidateDetail.score.ticker} 本地日线价格和成交量图表`">
              <div class="technical-chart-legend">
                <span><i class="technical-chart-line-key" />收盘价</span>
                <span><i class="technical-chart-ma20-key" />20 日均线</span>
                <span><i class="technical-chart-ma50-key" />50 日均线</span>
                <span><i class="technical-chart-ma200-key" />200 日均线</span>
                <span><i class="technical-chart-bar-key" />每日累计成交量</span>
                <span>价格范围 {{ formatPrice(technicalHistoryChart.minClose, 'USD') }} – {{ formatPrice(technicalHistoryChart.maxClose, 'USD') }}</span>
              </div>
              <svg class="technical-chart-svg" viewBox="0 0 720 270" preserveAspectRatio="none">
                <line v-for="y in [24, 76, 128, 180]" :key="`grid-${y}`" x1="0" :y1="y" x2="720" :y2="y" class="technical-chart-grid" />
                <rect
                  v-for="point in technicalHistoryChart.points"
                  :key="`volume-${point.tradeDate}`"
                  :x="point.x - technicalHistoryChart.barWidth / 2"
                  :y="point.volumeY"
                  :width="technicalHistoryChart.barWidth"
                  :height="240 - point.volumeY"
                  class="technical-chart-volume"
                >
                  <title>{{ `${point.tradeDate}｜每日累计成交量 ${formatVolume(point.volume)}` }}</title>
                </rect>
                <polyline :points="technicalHistoryChart.pricePolyline" fill="none" class="technical-chart-price" />
                <polyline v-if="technicalHistoryChart.ma20Polyline" :points="technicalHistoryChart.ma20Polyline" fill="none" class="technical-chart-ma20" />
                <polyline v-if="technicalHistoryChart.ma50Polyline" :points="technicalHistoryChart.ma50Polyline" fill="none" class="technical-chart-ma50" />
                <polyline v-if="technicalHistoryChart.ma200Polyline" :points="technicalHistoryChart.ma200Polyline" fill="none" class="technical-chart-ma200" />
                <circle
                  v-for="point in technicalHistoryChart.points"
                  :key="`price-${point.tradeDate}`"
                  :cx="point.x"
                  :cy="point.priceY"
                  r="3"
                  class="technical-chart-point"
                >
                  <title>{{ `${point.tradeDate}｜收盘 ${formatPrice(point.close, 'USD')}｜MA20 ${point.ma20 == null ? '历史不足' : formatPrice(point.ma20, 'USD')}｜MA50 ${point.ma50 == null ? '历史不足' : formatPrice(point.ma50, 'USD')}｜MA200 ${point.ma200 == null ? '历史不足' : formatPrice(point.ma200, 'USD')}｜每日累计成交量 ${formatVolume(point.volume)}｜${point.backfilled ? '历史回填' : '日常同步'} / ${point.source || '-'}` }}</title>
                </circle>
                <text x="0" y="264" class="technical-chart-axis-label">{{ technicalHistoryChart.startDate }}</text>
                <text x="360" y="264" text-anchor="middle" class="technical-chart-axis-label">{{ technicalHistoryChart.middleDate }}</text>
                <text x="720" y="264" text-anchor="end" class="technical-chart-axis-label">{{ technicalHistoryChart.endDate }}</text>
              </svg>
              <div class="technical-chart-note">MA20/MA50/MA200 分别需累计满 20/50/200 个有效交易日后开始绘制；悬浮数据点可查看该日价格、均线、成交量、来源及是否为历史回填。</div>
            </div>
            <el-empty v-else :image-size="72" description="暂无可绘制的本地日线数据" />
          </template>
          <el-table v-else :data="technicalHistoryRows" size="small" border max-height="360" empty-text="该时间范围暂无本地日线数据">
            <el-table-column prop="trade_date" label="日期" width="120">
              <template #default="{ row }">{{ formatDate(row.trade_date) }}</template>
            </el-table-column>
            <el-table-column label="收盘价" width="120" align="right">
              <template #default="{ row }">{{ formatPrice(row.close_usd, 'USD') }}</template>
            </el-table-column>
              <el-table-column label="每日累计成交量" width="140" align="right">
              <template #default="{ row }">{{ formatVolume(row.volume) }}</template>
            </el-table-column>
            <el-table-column label="数据来源" min-width="180">
              <template #default="{ row }">
                <el-space>
                  <el-tag :type="row.backfilled ? 'warning' : 'info'" effect="plain">{{ row.backfilled ? '历史回填' : '日常同步' }}</el-tag>
                  <span class="history-source">{{ row.source || '-' }}</span>
                </el-space>
              </template>
            </el-table-column>
          </el-table>
        </el-card>

        <el-card shadow="never">
          <template #header>近期 SEC 公告</template>
          <el-table :data="candidateDetail.recent_filings || []" size="small" border empty-text="暂无近期公告">
            <el-table-column prop="filing_date" label="日期" width="120">
              <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
            </el-table-column>
            <el-table-column prop="filing_type" label="类型" width="90" />
            <el-table-column prop="title" label="标题" min-width="220" show-overflow-tooltip />
            <el-table-column label="链接" width="80">
              <template #default="{ row }">
                <el-link v-if="row.filing_url" :href="row.filing_url" target="_blank" type="primary">打开</el-link>
                <span v-else>-</span>
              </template>
            </el-table-column>
          </el-table>
        </el-card>

        <el-card shadow="never">
          <template #header>内幕交易</template>
          <el-alert
            v-if="candidateDetail.insider_coverage"
            :type="insiderCoverageAlertType(candidateDetail.insider_coverage.status)"
            :closable="false"
            show-icon
            class="business-model-alert"
            :title="insiderCoverageTitle(candidateDetail.insider_coverage)"
            :description="insiderCoverageDescription(candidateDetail.insider_coverage)"
          />
          <el-alert v-else type="info" :closable="false" show-icon class="business-model-alert" title="本批次未保留 Form 4 覆盖明细" description="请完成一次新的小盘股安全宇宙同步后，再将“无内幕交易”视为覆盖结论。" />
          <el-table :data="candidateDetail.insiders" size="small" border empty-text="暂无内幕交易">
            <el-table-column prop="transaction_date" label="日期" width="120"><template #default="{ row }">{{ formatDate(row.transaction_date) }}</template></el-table-column>
            <el-table-column prop="owner_name" label="人员" min-width="120" />
            <el-table-column prop="role" label="角色" width="90" />
            <el-table-column prop="transaction_code" label="代码" width="70" />
            <el-table-column prop="qualified" label="合格" width="70"><template #default="{ row }"><el-tag :type="row.qualified ? 'success' : 'info'" effect="plain">{{ row.qualified ? '是' : '否' }}</el-tag></template></el-table-column>
          </el-table>
        </el-card>

        <el-card shadow="never">
          <template #header>融资/稀释风险</template>
          <el-alert
            v-if="candidateDetail.capital_risk_summary?.total_events"
            :type="candidateDetail.capital_risk_summary?.active_events ? 'warning' : 'info'"
            :closable="false"
            show-icon
            class="capital-risk-summary"
            :title="capitalRiskSummaryTitle(candidateDetail)"
            :description="capitalRiskSummaryDescription(candidateDetail)"
          />
          <el-table :data="candidateDetail.capital_risks" size="small" border empty-text="暂无当前或近 180 日融资风险">
            <el-table-column prop="effective_at" label="日期" width="120"><template #default="{ row }">{{ formatDate(row.effective_at) }}</template></el-table-column>
            <el-table-column prop="kind" label="类型" width="140" />
            <el-table-column prop="severity" label="严重度" width="90" />
            <el-table-column prop="reason" label="原因" min-width="220" show-overflow-tooltip />
            <el-table-column label="阻断" width="120"><template #default="{ row }">A: {{ row.blocks_a ? '是' : '否' }} / B: {{ row.blocks_b ? '是' : '否' }}</template></el-table-column>
          </el-table>
        </el-card>

        <el-card shadow="never">
          <template #header>预期差研究卡（用户论点，不是系统事实）</template>
          <el-alert type="info" :closable="false" show-icon title="SEC 公告、财务和融资证据在上方独立展示；以下内容均由用户维护，需通过后续公告或数据验证。" class="business-model-alert" />
          <template v-if="candidateDetail.research">
            <el-descriptions :column="1" border size="small">
              <el-descriptions-item label="市场当前担忧">{{ candidateDetail.research.market_concern || '-' }}</el-descriptions-item>
              <el-descriptions-item label="可证伪判断">{{ candidateDetail.research.falsifiable_judgment || candidateDetail.research.thesis || '-' }}</el-descriptions-item>
              <el-descriptions-item label="下个催化剂">{{ candidateDetail.research.catalyst || '-' }}{{ candidateDetail.research.catalyst_date ? `（${formatDate(candidateDetail.research.catalyst_date)}）` : '' }}</el-descriptions-item>
              <el-descriptions-item label="催化剂来源"><el-link v-if="candidateDetail.research.catalyst_source" :href="candidateDetail.research.catalyst_source" target="_blank" type="primary">打开来源</el-link><span v-else>-</span></el-descriptions-item>
              <el-descriptions-item label="失效条件">{{ candidateDetail.research.invalidation || '-' }}</el-descriptions-item>
            </el-descriptions>
            <el-collapse v-if="candidateDetail.research_versions?.length" class="research-version-history">
              <el-collapse-item :title="`备忘录版本历史（${candidateDetail.research_versions.length} 条，仅保留最近 20 条）`" name="versions">
                <el-table :data="candidateDetail.research_versions" size="small" border max-height="260">
                  <el-table-column prop="version" label="版本" width="72" />
                  <el-table-column prop="created_at" label="保存时间" width="130"><template #default="{ row }">{{ formatDate(row.created_at) }}</template></el-table-column>
                  <el-table-column prop="author" label="作者" width="110" />
                  <el-table-column prop="falsifiable_judgment" label="可证伪判断" min-width="220" show-overflow-tooltip />
                  <el-table-column prop="invalidation" label="失效条件" min-width="180" show-overflow-tooltip />
                </el-table>
              </el-collapse-item>
            </el-collapse>
          </template>
          <el-empty v-else description="尚未建立研究卡；请先加入关注列表后填写。" :image-size="64" />
        </el-card>

        <el-card shadow="never">
          <template #header>原始证据字段</template>
          <el-table :data="candidateDetail.evidence" size="small" border>
            <el-table-column prop="field" label="字段" width="170" />
            <el-table-column prop="value" label="值" width="140" />
            <el-table-column prop="source" label="来源" min-width="180" />
          </el-table>
        </el-card>
      </div>
    </el-drawer>

    <el-dialog v-model="businessModelEditorVisible" title="确认生物医药业务模型" width="620px">
      <el-alert type="info" :closable="false" show-icon title="确认记录将保留历史；新记录会替代当前生效分类。该分类会在下一次小盘工作流评分时生效。" class="summary-alert" />
      <el-form label-width="130px" class="business-model-form">
        <el-form-item label="Ticker"><el-input :model-value="businessModelEditor.ticker" disabled /></el-form-item>
        <el-form-item label="业务模型">
          <el-select v-model="businessModelEditor.business_model" style="width: 100%">
            <el-option label="已商业化" value="commercial" />
            <el-option label="临床前/临床期" value="clinical_pre_revenue" />
            <el-option label="授权/里程碑混合" value="mixed_or_licensing" />
            <el-option label="暂不确定" value="unknown" />
          </el-select>
        </el-form-item>
        <el-form-item label="可重复收入"><el-switch v-model="businessModelEditor.revenue_repeatable_confirmed" active-text="已确认" inactive-text="未确认" /></el-form-item>
        <el-form-item label="确认依据"><el-input v-model="businessModelEditor.reason" type="textarea" :rows="3" placeholder="例如：10-K 产品收入、授权条款或临床阶段说明" /></el-form-item>
        <el-form-item label="来源链接"><el-input v-model="businessModelEditor.source_url" placeholder="SEC filing 或公开资料链接" /></el-form-item>
        <el-form-item label="确认人"><el-input v-model="businessModelEditor.operator" /></el-form-item>
        <el-form-item label="下次复查"><el-date-picker v-model="businessModelEditor.review_due_at" type="date" value-format="YYYY-MM-DD" clearable /></el-form-item>
      </el-form>
      <template #footer><el-button @click="businessModelEditorVisible = false">取消</el-button><el-button type="primary" :loading="businessModelSaving" @click="saveBusinessModel">保存确认</el-button></template>
    </el-dialog>

    <el-dialog v-model="watchDialogVisible" title="小盘候选关注列表" width="1280px">
      <div class="watch-toolbar">
        <el-switch v-model="showArchivedWatches" active-text="显示归档" @change="loadCandidateWatches" />
      </div>
      <el-table :data="watchRows" v-loading="watchLoading" border empty-text="暂无关注候选">
        <el-table-column prop="ticker" label="Ticker" width="110" />
        <el-table-column prop="company_name" label="公司" min-width="180" show-overflow-tooltip />
        <el-table-column label="关注时间" width="150">
          <template #default="{ row }">{{ formatDateTime(row.baseline_captured_at || row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="当前质量" width="110">
          <template #default="{ row }">
            <el-tag v-if="row.latest_score" :type="qualityTierTagType(row.latest_score.quality_tier)" effect="plain">
              {{ qualityTierLabel(row.latest_score.quality_tier) }}
            </el-tag>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column label="变化" width="90">
          <template #default="{ row }">
            <el-tag v-if="row.latest_score" :type="changeStatusTagType(row.latest_score.change_status)" effect="plain">
              {{ changeStatusLabel(row.latest_score.change_status) }}
            </el-tag>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column label="投资评分" width="90" align="right">
          <template #default="{ row }">
            <el-tooltip v-if="row.baseline && row.current" placement="top" effect="dark">
              <template #content>
                <div class="metric-tooltip">
                  <div>关注时：{{ row.baseline.total_score }} 分</div>
                  <div>当前：{{ row.current.total_score }} 分</div>
                  <div>变化：{{ formatWatchScoreChange(row.metric_changes?.score_change) }}</div>
                </div>
              </template>
              <span>{{ row.current.total_score }}（{{ formatWatchScoreChange(row.metric_changes?.score_change) }}）</span>
            </el-tooltip>
            <span v-else>{{ row.latest_score?.total_score ?? '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="收盘价变化" width="150" align="right">
          <template #default="{ row }">
            <el-tooltip v-if="row.baseline && row.current" placement="top" effect="dark">
              <template #content>
                <div class="metric-tooltip">
                  <div>关注时：{{ formatPrice(row.baseline.price_close_usd, 'USD') }}（{{ formatDate(row.baseline.price_trade_date) }}）</div>
                  <div>当前：{{ formatPrice(row.current.price_close_usd, 'USD') }}（{{ formatDate(row.current.price_trade_date) }}）</div>
                  <div>来源：{{ row.current.price_source || '-' }}</div>
                </div>
              </template>
              <span :class="watchChangeClass(row.metric_changes?.price_change_pct)">
                {{ formatWatchMetricChange(row.metric_changes?.price_change_pct) }}
              </span>
            </el-tooltip>
            <span v-else class="watch-baseline-missing">本次起跟踪</span>
          </template>
        </el-table-column>
        <el-table-column label="市值变化" width="150" align="right">
          <template #default="{ row }">
            <el-tooltip v-if="row.baseline && row.current" placement="top" effect="dark">
              <template #content>
                <div class="metric-tooltip">
                  <div>关注时：{{ formatUSD(row.baseline.market_cap_usd) }}</div>
                  <div>当前：{{ formatUSD(row.current.market_cap_usd) }}</div>
                  <div>收入增长：{{ formatPct(row.baseline.revenue_growth_pct) }} → {{ formatPct(row.current.revenue_growth_pct) }}</div>
                  <div>现金 runway：{{ row.baseline.cash_runway_months.toFixed(1) }} 月 → {{ row.current.cash_runway_months.toFixed(1) }} 月</div>
                </div>
              </template>
              <span :class="watchChangeClass(row.metric_changes?.market_cap_change_pct)">
                {{ formatWatchMetricChange(row.metric_changes?.market_cap_change_pct) }}
              </span>
            </el-tooltip>
            <span v-else class="watch-baseline-missing">本次起跟踪</span>
          </template>
        </el-table-column>
        <el-table-column label="表现" width="100" align="right">
          <template #default="{ row }">{{ formatPerformance(row.latest_score?.performance?.return_5d ?? row.latest_score?.performance?.return_1d) }}</template>
        </el-table-column>
        <el-table-column prop="note" label="备注" min-width="140" show-overflow-tooltip />
        <el-table-column prop="research_status" label="跟踪状态" width="110">
          <template #default="{ row }">
            <el-tag :type="researchStatusTagType(row.research_status)" effect="plain">{{ researchStatusLabel(row.research_status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="next_review_at" label="下次复查" width="120">
          <template #default="{ row }">{{ formatDate(row.next_review_at) }}</template>
        </el-table-column>
        <el-table-column prop="status" label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.status === 'archived' ? 'info' : 'success'" effect="plain">
              {{ row.status === 'archived' ? '归档' : '关注中' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="updated_at" label="更新时间" width="170">
          <template #default="{ row }">{{ formatDate(row.updated_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="190" fixed="right">
          <template #default="{ row }">
            <el-button v-if="row.latest_score" link type="primary" @click="openDetail(row.latest_score)">详情</el-button>
            <el-button v-if="!row.baseline" link type="warning" @click="captureCandidateWatchBaseline(row)">设为当前基准</el-button>
            <el-button link type="primary" @click="editCandidateWatch(row)">研究</el-button>
            <el-button v-if="row.status === 'archived'" link type="success" @click="restoreCandidateWatch(row)">恢复</el-button>
            <el-button v-else link type="warning" @click="archiveCandidateWatch(row)">归档</el-button>
            <el-button link type="danger" @click="deleteCandidateWatch(row.id)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>

    <el-dialog v-model="watchEditorVisible" :title="`${watchEditor.ticker || '候选'} 研究记录`" width="680px">
      <el-form label-position="top">
        <el-form-item label="跟踪状态">
          <el-select v-model="watchEditor.research_status" style="width: 180px">
            <el-option label="待研究" value="inbox" />
            <el-option label="研究中" value="researching" />
            <el-option label="重点关注" value="conviction" />
            <el-option label="淘汰" value="rejected" />
          </el-select>
        </el-form-item>
        <el-form-item label="研究备注"><el-input v-model="watchEditor.note" type="textarea" :rows="2" /></el-form-item>
        <el-form-item label="研究论点"><el-input v-model="watchEditor.thesis" type="textarea" :rows="3" /></el-form-item>
        <el-form-item label="市场当前担忧"><el-input v-model="watchEditor.market_concern" type="textarea" :rows="2" placeholder="例如：市场担心现金消耗或关键产品需求" /></el-form-item>
        <el-form-item label="可证伪判断"><el-input v-model="watchEditor.falsifiable_judgment" type="textarea" :rows="3" placeholder="必须能由后续数据或公告验证/证伪" /></el-form-item>
        <el-form-item label="下个催化剂"><el-input v-model="watchEditor.catalyst" type="textarea" :rows="2" /></el-form-item>
        <el-form-item label="催化剂来源"><el-input v-model="watchEditor.catalyst_source" placeholder="SEC 链接、公告或研究备注来源" /></el-form-item>
        <el-form-item label="预计验证日期"><el-date-picker v-model="watchEditor.catalyst_date" type="date" value-format="YYYY-MM-DD" clearable /></el-form-item>
        <el-form-item label="主要风险"><el-input v-model="watchEditor.risk_notes" type="textarea" :rows="2" /></el-form-item>
        <el-form-item label="失效条件"><el-input v-model="watchEditor.invalidation" type="textarea" :rows="2" /></el-form-item>
        <el-form-item label="下次复查"><el-date-picker v-model="watchEditor.next_review_at" type="date" value-format="YYYY-MM-DD" clearable /></el-form-item>
      </el-form>
      <template #footer><el-button @click="watchEditorVisible = false">取消</el-button><el-button type="primary" :loading="watchSaving" @click="saveCandidateResearch">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="researchPortfolioVisible" title="手工研究组合（不连接券商、不生成交易指令）" width="980px">
      <el-alert type="info" :closable="false" show-icon title="仅记录研究上限、参考成本和流动性/事件风险约束；不读取真实账户，也不会触发交易。" class="summary-alert" />
      <el-alert v-if="(researchPortfolio?.total_max_weight_pct || 0) > 100" type="warning" :closable="false" show-icon :title="`研究上限合计 ${formatPct(researchPortfolio?.total_max_weight_pct)}，超过 100%；请检查集中度。`" class="summary-alert" />
      <el-descriptions :column="3" border size="small" class="research-portfolio-summary">
        <el-descriptions-item label="标的数">{{ researchPortfolio?.position_count || 0 }}</el-descriptions-item>
        <el-descriptions-item label="上限权重合计">{{ formatPct(researchPortfolio?.total_max_weight_pct) }}</el-descriptions-item>
        <el-descriptions-item label="赛道上限"><span v-for="(weight, sector) in researchPortfolio?.sector_weights || {}" :key="sector" class="portfolio-sector">{{ sector }} {{ formatPct(weight) }}</span><span v-if="!Object.keys(researchPortfolio?.sector_weights || {}).length">-</span></el-descriptions-item>
      </el-descriptions>
      <div class="research-portfolio-toolbar"><el-button type="primary" plain @click="newResearchPosition()">新增研究仓位</el-button></div>
      <el-table :data="researchPortfolio?.items || []" v-loading="researchPortfolioLoading" size="small" border empty-text="尚未设置手工研究仓位">
        <el-table-column prop="ticker" label="Ticker" width="100" />
        <el-table-column label="最大权重" width="110" align="right"><template #default="{ row }">{{ formatPct(row.max_weight_pct) }}</template></el-table-column>
        <el-table-column label="参考成本" width="125" align="right"><template #default="{ row }">{{ formatPrice(row.reference_cost_usd, 'USD') }}</template></el-table-column>
        <el-table-column label="日均成交量参与上限" width="170" align="right"><template #default="{ row }">{{ formatPct(row.max_daily_volume_participation_pct) }}</template></el-table-column>
        <el-table-column prop="event_risk_note" label="事件风险" min-width="170" show-overflow-tooltip />
        <el-table-column prop="liquidity_note" label="流动性约束" min-width="170" show-overflow-tooltip />
        <el-table-column label="操作" width="130" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="editResearchPosition(row)">编辑</el-button><el-button link type="danger" @click="deleteResearchPosition(row.id)">删除</el-button></template></el-table-column>
      </el-table>
    </el-dialog>

    <el-dialog v-model="researchPositionEditorVisible" :title="`${researchPositionEditor.ticker || '新增'}研究仓位`" width="600px" append-to-body>
      <el-form label-position="top">
        <el-form-item label="Ticker"><el-input v-model="researchPositionEditor.ticker" :disabled="researchPositionEditor.existing" placeholder="例如 CLPT" /></el-form-item>
        <el-row :gutter="16"><el-col :span="12"><el-form-item label="最大研究权重（%）"><el-input-number v-model="researchPositionEditor.max_weight_pct" :min="0" :max="100" :precision="2" style="width:100%" /></el-form-item></el-col><el-col :span="12"><el-form-item label="参考成本（USD，可空）"><el-input-number v-model="researchPositionEditor.reference_cost_usd" :min="0" :precision="2" style="width:100%" /></el-form-item></el-col></el-row>
        <el-form-item label="日均成交量参与上限（%）"><el-input-number v-model="researchPositionEditor.max_daily_volume_participation_pct" :min="0" :max="100" :precision="2" style="width:100%" /></el-form-item>
        <el-form-item label="事件风险约束"><el-input v-model="researchPositionEditor.event_risk_note" type="textarea" :rows="2" placeholder="例如：财报前不提高研究上限" /></el-form-item>
        <el-form-item label="流动性约束"><el-input v-model="researchPositionEditor.liquidity_note" type="textarea" :rows="2" placeholder="例如：避免低于日均成交量上限" /></el-form-item>
        <el-form-item label="备注"><el-input v-model="researchPositionEditor.note" type="textarea" :rows="2" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="researchPositionEditorVisible = false">取消</el-button><el-button type="primary" :loading="researchPositionSaving" @click="saveResearchPosition">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="effectivenessVisible" title="候选效果评估" width="920px">
      <el-alert :type="effectiveness?.benchmark_available ? 'info' : 'warning'" :closable="false" show-icon class="summary-alert"
        :title="effectivenessNotice" />
      <el-table :data="effectiveness?.cohorts || []" border empty-text="暂无可评估候选">
        <el-table-column prop="grade" label="Cohort" width="100"><template #default="{ row }">{{ effectivenessCohortLabel(row.grade) }}</template></el-table-column>
        <el-table-column prop="candidate_count" label="首次入选数" width="110" align="right" />
        <el-table-column v-for="horizon in [1, 5, 20, 60]" :key="horizon" :label="`${horizon}日表现`" min-width="180">
          <template #default="{ row }">
            <template v-if="effectivenessWindow(row, horizon)?.sample_count">
              {{ formatPerformance(effectivenessWindow(row, horizon)?.average_return_pct) }}｜胜率 {{ formatPct(effectivenessWindow(row, horizon)?.win_rate_pct) }}｜回撤 {{ formatPerformance(effectivenessWindow(row, horizon)?.max_drawdown_pct) }}
              <small v-if="effectivenessWindow(row, horizon)?.excess_return_pct != null">｜相对 {{ formatPerformance(effectivenessWindow(row, horizon)?.excess_return_pct) }}</small>
            </template>
            <span v-else>-</span>
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>

    <el-dialog v-model="sectorDialogVisible" title="候选赛道分布" width="720px">
      <el-table :data="sectorRows" border empty-text="暂无赛道统计">
        <el-table-column prop="name" label="赛道" min-width="180" />
        <el-table-column prop="count" label="候选数" width="100" align="right" />
        <el-table-column label="占比" min-width="180">
          <template #default="{ row }">
            <el-progress :percentage="sectorPercentage(row.count)" :stroke-width="10" />
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useRoute } from 'vue-router'
import { apiClient } from '@/api/client'
import AIRequestPrompt from '@/components/AIRequestPrompt.vue'
import MarkdownContent from '@/components/MarkdownContent.vue'
import ProfitHistoryChart from '@/components/ProfitHistoryChart.vue'
import type {
  ApiResponse,
  CandidateDetail,
  DiscoveryCacheCleanupPreview,
  CandidateEffectivenessCohort,
  CandidateEffectivenessReport,
  CandidateHealth,
  CandidateNotificationPreview,
  CandidateNotificationSendInput,
  CandidateNotificationSendResult,
  CandidateOverview,
  CandidateReport,
  CandidateResearchPortfolio,
  CandidateResearchPosition,
  CandidateReviewQueue,
  CandidateChangeReason,
  CandidateScore,
  CandidateSelectionCriteria,
  CandidateSummary,
  CandidateTechnicalHistoryRow,
  CandidateDilutionTrend,
  CandidateInvestability,
  CandidateWatch,
  DiscoveryInsiderCoverage,
  DiscoveryStorageHealth,
  DiscoverySyncRun,
  DiscoveryWorkflowResult,
  PageResult,
  SmallCapEligibilityCheckHistoryItem,
  SmallCapEligibilityCheckResult,
  TechnicalHistoryBackfillResult,
} from '@/api/types'

const rows = ref<CandidateScore[]>([])
const overview = ref<CandidateOverview | null>(null)
const loading = ref(false)
const workflowLoading = ref(false)
const forceMarketLoading = ref(false)
const technicalHistoryLoading = ref(false)
const reportLoading = ref(false)
const detailVisible = ref(false)
const detailLoadingTicker = ref('')
const watchingTicker = ref('')
const watchLoading = ref(false)
const watchDialogVisible = ref(false)
const watchEditorVisible = ref(false)
const watchSaving = ref(false)
const watchRows = ref<CandidateWatch[]>([])
const reviewQueue = ref<CandidateReviewQueue | null>(null)
const researchPortfolioVisible = ref(false)
const researchPortfolioLoading = ref(false)
const researchPortfolio = ref<CandidateResearchPortfolio | null>(null)
const researchPositionEditorVisible = ref(false)
const researchPositionSaving = ref(false)
const researchPositionEditor = reactive({ ticker: '', existing: false, max_weight_pct: 0, reference_cost_usd: undefined as number | undefined, max_daily_volume_participation_pct: 0, event_risk_note: '', liquidity_note: '', note: '' })
const watchEditor = reactive({
  ticker: '',
  note: '',
  research_status: 'inbox',
  thesis: '',
  market_concern: '',
  falsifiable_judgment: '',
  catalyst: '',
  catalyst_source: '',
  catalyst_date: '',
  risk_notes: '',
  invalidation: '',
  next_review_at: '',
})
const showArchivedWatches = ref(false)
const effectivenessLoading = ref(false)
const effectivenessVisible = ref(false)
const effectiveness = ref<CandidateEffectivenessReport | null>(null)
const effectivenessNotice = computed(() => {
  const benchmark = effectiveness.value?.benchmark_ticker || 'IWM'
  const source = effectiveness.value?.cohort_source
  const baseline = source === 'signal_events'
    ? '按入选/质量升级当日固化的候选信号与价格基线统计，不会被后续名单变化改写。'
    : '当前尚无事件化历史，暂按历史批次中的首次入选回溯；后续同步生成信号后会自动切换为不可变事件基线。'
  return effectiveness.value?.benchmark_available
    ? `收益相对 ${benchmark} 计算；${baseline}`
    : `未找到 ${benchmark} 本地价格历史；当前仅展示候选自身表现。${baseline}`
})
const sectorDialogVisible = ref(false)
const candidateDetail = ref<CandidateDetail | null>(null)
type AIProvider = { id: string; name: string; model: string }
type AIPromptTemplate = { id: string; name: string }
type AIAnalysis = { id: number; provider_name: string; model: string; template_name?: string; content: string; status: string; error_message?: string; system_prompt?: string; user_prompt?: string; requested_at: string }
const aiProviders = ref<AIProvider[]>([])
const aiPromptTemplates = ref<AIPromptTemplate[]>([])
const candidateAIProvider = ref('')
const candidateAIPromptTemplate = ref('')
const candidateAIGenerating = ref(false)
const candidateAIAnalyses = ref<AIAnalysis[]>([])
const candidateAIAnalysisID = ref<number | null>(null)
const activeCandidateAIAnalysis = computed(() => candidateAIAnalyses.value.find((item) => item.id === candidateAIAnalysisID.value) || candidateAIAnalyses.value[0])
let candidateAIPollingTimer: number | undefined
const analystRatingRefreshing = ref(false)
const marketResearchRefreshing = ref(false)
const valuationResearchRefreshing = ref(false)
const companyProfileRefreshing = ref(false)
const businessModelEditorVisible = ref(false)
const businessModelSaving = ref(false)
const businessModelEditor = reactive({ ticker: '', business_model: 'unknown', revenue_repeatable_confirmed: false, reason: '', source_url: '', operator: 'local_user', review_due_at: '' })
const candidateTableView = ref<'compact' | 'full'>('compact')
const route = useRoute()
const advancedFiltersVisible = ref(false)
const supplementalLoading = ref(false)
const technicalHistoryView = ref<'chart' | 'table'>('chart')
const technicalHistoryRange = ref<TechnicalHistoryRange>('1y')
const health = ref<CandidateHealth | null>(null)
const discoverySyncRun = ref<DiscoverySyncRun | null>(null)
const discoveryStorage = ref<DiscoveryStorageHealth | null>(null)
const cacheCleanupLoading = ref(false)
const discoverySyncNow = ref(Date.now())
let discoverySyncPoll: ReturnType<typeof window.setInterval> | undefined
let candidateSupplementalTimer: ReturnType<typeof window.setTimeout> | undefined
const criteria = ref<CandidateSelectionCriteria | null>(null)
const report = ref<CandidateReport | null>(null)
const reportVisible = ref(false)
const eligibilityCheckVisible = ref(false)
const eligibilityCheckTab = ref<'check' | 'history'>('check')
const eligibilityCheckTicker = ref('')
const eligibilityCheckLoading = ref(false)
const eligibilityCheckResult = ref<SmallCapEligibilityCheckResult | null>(null)
const eligibilityHistory = ref<SmallCapEligibilityCheckHistoryItem[]>([])
const eligibilityHistoryTicker = ref('')
const eligibilityHistoryLoading = ref(false)
const eligibilityHistoryPage = ref(1)
const eligibilityHistoryTotal = ref(0)
const summaryLoading = ref(false)
const sendingNotification = ref(false)
const summaryVisible = ref(false)
const summary = ref<CandidateSummary | null>(null)
const notificationPreview = ref<CandidateNotificationPreview | null>(null)
const notificationPreviewStatus = ref('仅 dry-run 预检，不会自动发送 Telegram。')
const forceCandidateNotification = ref(false)
const page = ref(1)
const pageSize = 20
const total = ref(0)
const filters = reactive({
  grade: '',
  ticker: '',
  eligible_a: '',
  eligible_b: '',
  sector_category: '',
  quality_tier: '',
  change_status: '',
  technical_signal: '',
  research_readiness: '',
  exclude_research_readiness: ['blocked'] as string[],
  exclude_quality_tags: [] as string[],
  max_ev_sales: undefined as number | undefined,
  min_net_cash_to_market_cap_pct: undefined as number | undefined,
  price_freshness: '',
  upcoming_earnings: false,
  followed: false,
})
const sortState = reactive({ sort_by: 'total_score', sort_order: 'desc' })
const topSectorLabel = computed(() => {
  const entries = Object.entries(overview.value?.sector_counts || {}).sort((a, b) => b[1] - a[1])
  return entries[0]?.[0] || '-'
})
const topSectorCount = computed(() => {
  const entries = Object.entries(overview.value?.sector_counts || {}).sort((a, b) => b[1] - a[1])
  return entries[0]?.[1] || 0
})
const candidateScopeLabel = computed(() => {
  if (filters.research_readiness === 'ready') return '可行动'
  if (filters.research_readiness === 'research_only') return '待核验'
  if (filters.research_readiness === 'blocked') return '已阻断'
  if (filters.exclude_research_readiness.includes('blocked')) return '可行动 + 待核验'
  return '全部状态'
})
const sectorRows = computed(() => Object.entries(overview.value?.sector_counts || {})
  .map(([name, count]) => ({ name, count }))
  .sort((a, b) => b.count - a.count))
const sectorCategoryOptions = [
  '生物医药',
  '软件与数据服务',
  '电子/半导体',
  '医疗器械',
  '通信服务',
  '医疗服务',
  '能源',
  '矿业/资源',
  '化工/生命科学材料',
  '工业制造',
  '计算机硬件',
  '消费/零售',
  '商业服务',
  '消费服务',
  '教育服务',
  '专业服务',
  '其他已分类赛道',
  '赛道数据缺失',
]
const technicalHistoryRows = computed(() => filterTechnicalHistoryRange(candidateDetail.value?.technical_history || [], technicalHistoryRange.value))
const technicalHistoryChart = computed(() => buildTechnicalHistoryChart(candidateDetail.value?.technical_history || [], technicalHistoryRange.value))
const technicalHistoryHasLaterRows = computed(() => {
  const asOfDate = candidateDetail.value?.technical?.trade_date
  if (!asOfDate) return false
  return (candidateDetail.value?.technical_history || []).some((row) => row.trade_date > asOfDate)
})

function requestParams() {
  const params: Record<string, string | number> = { page: page.value, page_size: pageSize }
  if (filters.grade) params.grade = filters.grade
  if (filters.ticker) params.ticker = filters.ticker.trim().toUpperCase()
  if (filters.eligible_a) params.eligible_a = filters.eligible_a
  if (filters.eligible_b) params.eligible_b = filters.eligible_b
  if (filters.sector_category) params.sector_category = filters.sector_category
  if (filters.quality_tier) params.quality_tier = filters.quality_tier
  if (filters.change_status) params.change_status = filters.change_status
  if (filters.technical_signal) params.technical_signal = filters.technical_signal
  if (filters.research_readiness) params.research_readiness = filters.research_readiness
  if (filters.exclude_research_readiness.length) params.exclude_research_readiness = filters.exclude_research_readiness.join(',')
  if (filters.exclude_quality_tags.length) params.exclude_quality_tag = filters.exclude_quality_tags.join(',')
  if (filters.max_ev_sales != null) params.max_ev_sales = filters.max_ev_sales
  if (filters.min_net_cash_to_market_cap_pct != null) params.min_net_cash_to_market_cap_pct = filters.min_net_cash_to_market_cap_pct
  if (filters.price_freshness === 'attention') params.price_freshness = 'stale,future,missing,unknown'
  else if (filters.price_freshness) params.price_freshness = filters.price_freshness
  if (filters.upcoming_earnings) params.upcoming_earnings = 'true'
  if (filters.followed) params.followed = 'true'
  if (sortState.sort_by) params.sort_by = sortState.sort_by
  if (sortState.sort_order) params.sort_order = sortState.sort_order
  if (candidateTableView.value === 'full') params.include_performance = 'true'
  return params
}

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<CandidateScore>>>('/discovery/candidates', { params: requestParams() })
    rows.value = res.data.data.items || []
    total.value = res.data.data.total || 0
    // Let Vue paint the primary table before the diagnostic cards start their
    // own local database work. Otherwise an overview scan can make the page
    // look busy even after the rows have arrived.
    if (candidateSupplementalTimer) window.clearTimeout(candidateSupplementalTimer)
    candidateSupplementalTimer = window.setTimeout(() => {
      void loadUpcomingEarningsCount()
      void refreshCandidateSupplementals()
    }, 120)
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载候选失败')
  } finally {
    loading.value = false
  }
}

const upcomingEarningsCount = ref(0)
async function loadUpcomingEarningsCount() {
  try {
    const response = await apiClient.get<ApiResponse<PageResult<CandidateScore>>>('/discovery/candidates', { params: { page: 1, page_size: 1, upcoming_earnings: 'true' } })
    upcomingEarningsCount.value = response.data.data.total || 0
  } catch { upcomingEarningsCount.value = 0 }
}

async function refreshCandidateSupplementals() {
  supplementalLoading.value = true
  try {
    await Promise.allSettled([loadHealth(), loadOverview(), loadReviewQueue(), loadDiscoverySyncStatus(), loadDiscoveryStorage()])
  } finally {
    supplementalLoading.value = false
  }
}

async function loadHealth() {
  const res = await apiClient.get<ApiResponse<CandidateHealth>>('/discovery/candidates/health')
  health.value = res.data.data
}

async function loadOverview() {
  const res = await apiClient.get<ApiResponse<CandidateOverview>>('/discovery/candidates/overview')
  overview.value = res.data.data
}

async function loadDiscoverySyncStatus() {
  try {
    const res = await apiClient.get<ApiResponse<DiscoverySyncRun>>('/discovery/sync-status')
    const run = res.data.data
    discoverySyncRun.value = run?.id ? run : null
    discoverySyncNow.value = Date.now()
  } catch {
    // Lifecycle status is supplemental. A transient status read failure must
    // not prevent the candidate table itself from loading.
  }
}

async function loadDiscoveryStorage() {
  try {
    const res = await apiClient.get<ApiResponse<DiscoveryStorageHealth>>('/discovery/storage-health')
    discoveryStorage.value = res.data.data
  } catch {
    // Storage diagnostics are supplemental and should not prevent research
    // candidates from loading if a local volume is temporarily unavailable.
  }
}

async function cleanupDiscoveryCache() {
  cacheCleanupLoading.value = true
  try {
    const preview = await apiClient.get<ApiResponse<DiscoveryCacheCleanupPreview>>('/discovery/storage/cache-cleanup-preview')
    const plan = preview.data.data
    if (!plan.file_count) {
      ElMessage.info('没有超过保留期限的 SEC 缓存文件')
      return
    }
    await ElMessageBox.confirm(
      `将删除 ${plan.file_count.toLocaleString()} 个超过 ${plan.retention_days} 天的缓存文件，预计释放 ${formatBytes(plan.bytes)}。不会删除候选、评分、公告或研究记录。`,
      '确认清理 SEC 缓存',
      { type: 'warning', confirmButtonText: '清理缓存', cancelButtonText: '取消' },
    )
    const result = await apiClient.post<ApiResponse<DiscoveryCacheCleanupPreview>>('/discovery/storage/cache-cleanup')
    ElMessage.success(`已清理 ${result.data.data.file_count.toLocaleString()} 个缓存文件，释放 ${formatBytes(result.data.data.bytes)}`)
    await loadDiscoveryStorage()
  } catch (err: any) {
    if (err !== 'cancel' && err?.message !== 'cancel' && err?.message !== 'close') {
      ElMessage.error(err?.response?.data?.message || '清理 SEC 缓存失败')
    }
  } finally {
    cacheCleanupLoading.value = false
  }
}

async function loadCriteria() {
  try {
    const res = await apiClient.get<ApiResponse<CandidateSelectionCriteria>>('/discovery/candidates/criteria')
    criteria.value = res.data.data
  } catch {
    criteria.value = null
  }
}

function openEligibilityCheck() {
  eligibilityCheckVisible.value = true
  eligibilityCheckTab.value = 'check'
  if (!eligibilityHistory.value.length) void loadEligibilityHistory()
}

async function runEligibilityCheck() {
  const ticker = eligibilityCheckTicker.value.trim().toUpperCase()
  if (!ticker) {
    ElMessage.warning('请输入要检查的 Ticker')
    return
  }
  eligibilityCheckLoading.value = true
  try {
    const res = await apiClient.post<ApiResponse<SmallCapEligibilityCheckResult>>('/discovery/candidates/eligibility-check', { ticker })
    eligibilityCheckResult.value = res.data.data
    eligibilityCheckTicker.value = res.data.data.ticker || ticker
    eligibilityHistoryPage.value = 1
    await loadEligibilityHistory()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '检查小盘资格失败')
  } finally {
    eligibilityCheckLoading.value = false
  }
}

async function loadEligibilityHistory() {
  eligibilityHistoryLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<SmallCapEligibilityCheckHistoryItem>>>('/discovery/candidates/eligibility-checks', {
      params: { page: eligibilityHistoryPage.value, page_size: 10, ticker: eligibilityHistoryTicker.value.trim().toUpperCase() || undefined },
    })
    eligibilityHistory.value = res.data.data.items || []
    eligibilityHistoryTotal.value = res.data.data.total || 0
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载检查历史失败')
  } finally {
    eligibilityHistoryLoading.value = false
  }
}

function openEligibilityHistoryItem(item: SmallCapEligibilityCheckHistoryItem) {
  eligibilityCheckResult.value = item.result
  eligibilityCheckTicker.value = item.ticker || item.requested_ticker
  eligibilityCheckTab.value = 'check'
}

function eligibilityStatusTagType(status: string) {
  if (status === 'pass') return 'success'
  if (status === 'fail') return 'danger'
  return 'info'
}

function eligibilityStatusLabel(status: string) {
  if (status === 'pass') return '✅ 满足'
  if (status === 'fail') return '❌ 不满足'
  return '— 待补数据'
}

function eligibilityGradeTagType(grade: string) {
  if (grade === 'A') return 'success'
  if (grade === 'B') return 'warning'
  return 'info'
}

function eligibilityGradeLabel(result: SmallCapEligibilityCheckResult) {
  if (result.grade === 'A') return '✅ A级候选'
  if (result.grade === 'B') return '✅ B级候选'
  return result.in_small_cap_pool ? '候选池内，未入选' : '不在候选池'
}

function eligibilityHistoryGradeLabel(item: SmallCapEligibilityCheckHistoryItem) {
  if (item.grade === 'A') return '✅ A级'
  if (item.grade === 'B') return '✅ B级'
  return item.in_small_cap_pool ? '池内未入选' : '未入池'
}

function eligibilityResultAlertType(result: SmallCapEligibilityCheckResult) {
  if (result.eligible_a || result.eligible_b) return 'success'
  if (result.in_small_cap_pool) return 'warning'
  return 'info'
}

function eligibilityResultMeta(result: SmallCapEligibilityCheckResult) {
  const source = [result.market_as_of ? `行情批次有效日 ${result.market_as_of}` : '', result.security_as_of ? `SEC 批次有效日 ${result.security_as_of}` : ''].filter(Boolean).join('；')
  return `${source || '当前批次日期不可用'}。检查时间：${formatDateTime(result.checked_at)}`
}

function eligibilityComparisonTitle(result: SmallCapEligibilityCheckResult) {
  const comparison = result.comparison
  if (!comparison) return ''
  return `已保留本次证据快照；相较 ${formatDateTime(comparison.previous_checked_at)}，${comparison.changes.length} 项条件发生变化。`
}

function eligibilityComparisonDescription(result: SmallCapEligibilityCheckResult) {
  const comparison = result.comparison
  if (!comparison) return ''
  const sources = [comparison.previous_market_as_of ? `上次行情 ${comparison.previous_market_as_of}` : '', comparison.previous_security_as_of ? `上次 SEC ${comparison.previous_security_as_of}` : ''].filter(Boolean).join('；')
  if (!comparison.changes.length) return `${sources || '上次批次日期不可用'}；逐项条件的实际值和结论均未变化。`
  const labels = comparison.changes.slice(0, 4).map(change => change.label).join('、')
  return `${sources || '上次批次日期不可用'}；变化项：${labels}${comparison.changes.length > 4 ? ' 等' : ''}。表格“相比上次”列可查看旧值。`
}

function eligibilityPreviousActual(result: SmallCapEligibilityCheckResult, condition: { key: string; actual: string }) {
  const previous = result.comparison?.changes.find(change => change.key === condition.key)
  if (!previous) return '无变化'
  return previous.previous_actual ? `${previous.previous_actual} →` : `首次出现 → ${condition.actual}`
}

async function runWorkflow() {
  workflowLoading.value = true
  try {
    const res = await apiClient.post<ApiResponse<DiscoveryWorkflowResult>>(
      '/discovery/candidates/refresh',
      null,
      // The full SEC universe refresh can take tens of minutes. Keep this
      // request alive instead of using the API client's normal 10-second
      // timeout, which would cancel the server-side workflow.
      { timeout: 70 * 60 * 1000 },
    )
    health.value = res.data.data.health
    summary.value = res.data.data.summary
    await load()
    if (res.data.data.status === 'published') {
      const warmup = res.data.data.technical_history_warmup
      if (warmup?.status === 'warning') {
        ElMessage.warning(`小盘候选同步已完成；技术历史自动预热未完成：${warmup.error_message || '请稍后手动回填'}`)
      } else if (warmup?.status === 'completed') {
        ElMessage.success(`小盘候选真实同步已完成；技术历史已自动预热 ${warmup.result.requested_count} 个待补齐候选`)
      } else {
        ElMessage.success('小盘候选真实同步已完成')
      }
    } else if (res.data.data.status === 'market_failed') {
      ElMessage.warning('证券与 SEC 数据已同步，行情候选阶段失败，请检查行情源配置')
    } else {
      ElMessage.info(`小盘候选同步状态：${res.data.data.status}`)
    }
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '刷新候选工作流失败')
  } finally {
    workflowLoading.value = false
  }
}

async function handleCandidateToolCommand(command: string) {
  if (command === 'market') await forceRefreshMarketPrices()
  else if (command === 'technical') await backfillTechnicalHistory()
  else if (command === 'watch') await openWatchList()
  else if (command === 'portfolio') await openResearchPortfolio()
  else if (command === 'effectiveness') await openEffectiveness()
  else if (command === 'sector') sectorDialogVisible.value = true
  else if (command === 'report') await openReport()
  else if (command === 'notification') await previewSummary()
  else if (command === 'export') exportCandidates()
}

async function forceRefreshMarketPrices() {
  try {
    await ElMessageBox.confirm(
      '仅拉取最近一个已完成美股交易日的价格与成交量，不重新下载 SEC 全量数据。该操作会请求已配置的 Longbridge、Tiingo、Twelve Data、Yahoo 行情源，并消耗相应额度。',
      '确认强制补齐收盘价',
      { type: 'warning', confirmButtonText: '开始补齐', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  forceMarketLoading.value = true
  try {
    const res = await apiClient.post<ApiResponse<DiscoveryWorkflowResult>>(
      '/discovery/candidates/market-refresh-force',
      null,
      { timeout: 70 * 60 * 1000 },
    )
    health.value = res.data.data.health
    summary.value = res.data.data.summary
    await load()
    ElMessage.success('最近收盘价补齐完成')
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '强制补齐收盘价失败，请检查行情源日志')
  } finally {
    forceMarketLoading.value = false
  }
}

async function backfillTechnicalHistory() {
  try {
    await ElMessageBox.confirm(
      '将仅对当前 A/B 小盘候选补齐近 320 个自然日的日线历史（通常约 220 个交易日），用于展示 MA20/MA50/MA200，并计算相对 IWM 的强弱。任务会遵守已配置的行情源请求预算，可能需要数分钟；不会修改基本面评分或发送通知。',
      '确认回填技术历史',
      { type: 'warning', confirmButtonText: '开始回填', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  technicalHistoryLoading.value = true
  try {
    const res = await apiClient.post<ApiResponse<TechnicalHistoryBackfillResult>>(
      '/discovery/candidates/technical-history-backfill',
      { lookback_days: 320 },
      // Twelve Data can deliberately throttle to one request every several
      // seconds. This one-time task must outlive the normal 10-second UI API
      // timeout, otherwise the browser cancels its server-side context.
      { timeout: 70 * 60 * 1000 },
    )
    const result = res.data.data
    const sources = Object.entries(result.source_record_counts || {}).map(([source, count]) => `${source} ${count}`).join(' / ') || '无'
    ElMessage.success(`技术历史回填完成：请求 ${result.requested_count}，写入 ${result.persisted_count} 条（${sources}）`)
    await load()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '技术历史回填失败，请检查行情源额度与配置')
  } finally {
    technicalHistoryLoading.value = false
  }
}

async function openReport() {
  reportLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<CandidateReport>>('/discovery/candidates/report')
    report.value = res.data.data
    reportVisible.value = true
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载候选日报失败')
  } finally {
    reportLoading.value = false
  }
}

async function openEffectiveness() {
  effectivenessLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<CandidateEffectivenessReport>>('/discovery/candidates/effectiveness')
    effectiveness.value = res.data.data
    effectivenessVisible.value = true
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载候选效果评估失败')
  } finally {
    effectivenessLoading.value = false
  }
}

function exportCandidates() {
  const query = new URLSearchParams()
  Object.entries(requestParams()).forEach(([key, value]) => query.set(key, String(value)))
  window.open(`/api/exports/candidates.csv?${query.toString()}`, '_blank', 'noopener')
}

async function openDetail(row: CandidateScore) {
  detailLoadingTicker.value = row.ticker
  technicalHistoryView.value = 'chart'
  technicalHistoryRange.value = '1y'
  try {
    const res = await apiClient.get<ApiResponse<CandidateDetail>>(`/discovery/candidates/${row.ticker}/detail`)
    candidateDetail.value = res.data.data
    detailVisible.value = true
		await loadCandidateAIAnalyses()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载候选详情失败')
  } finally {
    detailLoadingTicker.value = ''
  }
}

async function loadAIProviders() {
	try {
		const [response, templateResponse] = await Promise.all([apiClient.get('/ai/providers'), apiClient.get('/ai/prompt-templates')])
		aiProviders.value = response.data.data || []; aiPromptTemplates.value = templateResponse.data.data || []
		if (!candidateAIProvider.value && aiProviders.value.length) candidateAIProvider.value = aiProviders.value[0].id
		if (!candidateAIPromptTemplate.value && aiPromptTemplates.value.length) candidateAIPromptTemplate.value = aiPromptTemplates.value[0].id
	} catch { aiProviders.value = []; aiPromptTemplates.value = [] }
}

async function loadCandidateAIAnalyses() {
	const ticker = candidateDetail.value?.score.ticker
	if (!ticker) { candidateAIAnalyses.value = []; return }
	try {
		const response = await apiClient.get('/ai/analyses', { params: { ticker, page: 1, page_size: 50 } })
		candidateAIAnalyses.value = (response.data.data.items || []).filter((item: AIAnalysis & { scope?: string }) => item.scope === 'candidate_detail')
		candidateAIAnalysisID.value = candidateAIAnalyses.value[0]?.id || null
		if (candidateAIAnalyses.value.some((item) => item.status === 'queued' || item.status === 'running')) scheduleCandidateAIPoll()
	} catch { candidateAIAnalyses.value = [] }
}

function scheduleCandidateAIPoll() {
	if (candidateAIPollingTimer !== undefined) return
	candidateAIPollingTimer = window.setTimeout(() => { candidateAIPollingTimer = undefined; void loadCandidateAIAnalyses() }, 2000)
}

async function generateCandidateAI() {
	const detail = candidateDetail.value
	if (!detail || !candidateAIProvider.value || !candidateAIPromptTemplate.value) return
	candidateAIGenerating.value = true
	try {
		const response = await apiClient.post('/ai/analyses', { provider_id: candidateAIProvider.value, template_id: candidateAIPromptTemplate.value, scope: 'candidate_detail', ticker: detail.score.ticker, company_name: detail.security.company_name, target_type: 'stock', context: detail }, { timeout: 315000 })
		ElMessage.success('AI 研判已提交，正在后台处理')
		await loadCandidateAIAnalyses()
		candidateAIAnalysisID.value = response.data.data.id
	} catch (err: any) { ElMessage.error(err?.response?.data?.message || 'AI 研判请求超时或失败；请检查供应商配置、额度或适当提高模型超时后手动重试') } finally { candidateAIGenerating.value = false }
}

async function refreshCandidateCompanyProfile() {
  if (!candidateDetail.value) return
  companyProfileRefreshing.value = true
  try {
    const ticker = candidateDetail.value.score.ticker
    const cik = candidateDetail.value.security.cik
    await apiClient.post(`/discovery/company-profiles/${encodeURIComponent(ticker)}/refresh`, null, { params: { cik: cik || undefined } })
    const res = await apiClient.get<ApiResponse<CandidateDetail>>(`/discovery/candidates/${encodeURIComponent(ticker)}/detail`)
    candidateDetail.value = res.data.data
    ElMessage.success('已更新 Longbridge 公司资料')
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '刷新公司资料失败')
  } finally {
    companyProfileRefreshing.value = false
  }
}

async function refreshCandidateAnalystRating() {
  if (!candidateDetail.value) return
  analystRatingRefreshing.value = true
  try {
    const ticker = candidateDetail.value.score.ticker
    const cik = candidateDetail.value.security.cik
    const response = await apiClient.post<ApiResponse<{ rating: CandidateDetail['analyst_rating'] }>>(`/discovery/analyst-ratings/${encodeURIComponent(ticker)}/refresh`, null, { params: { cik: cik || undefined } })
    candidateDetail.value.analyst_rating = response.data.data.rating
    ElMessage.success('已更新 Longbridge 分析师共识')
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '刷新分析师评级失败')
  } finally {
    analystRatingRefreshing.value = false
  }
}

async function refreshCandidateMarketResearch() {
  if (!candidateDetail.value) return
  marketResearchRefreshing.value = true
  try {
    const ticker = candidateDetail.value.score.ticker
    const cik = candidateDetail.value.security.cik
    const response = await apiClient.post<ApiResponse<{ warnings?: string[] }>>(`/discovery/candidates/${encodeURIComponent(ticker)}/market-research/refresh`, null, { params: { cik: cik || undefined } })
    const detail = await apiClient.get<ApiResponse<CandidateDetail>>(`/discovery/candidates/${encodeURIComponent(ticker)}/detail`)
    candidateDetail.value = detail.data.data
    const warnings = response.data.data.warnings || []
    if (warnings.length) ElMessage.warning(`已部分更新：${warnings.join('；')}`)
    else ElMessage.success('已更新 Longbridge P1 市场研究数据')
    load()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '刷新 Longbridge P1 市场研究失败')
  } finally {
    marketResearchRefreshing.value = false
  }
}

async function refreshCandidateValuationResearch() {
  if (!candidateDetail.value) return
  valuationResearchRefreshing.value = true
  try {
    const ticker = candidateDetail.value.score.ticker
    const cik = candidateDetail.value.security.cik
    await apiClient.post(`/discovery/candidates/${encodeURIComponent(ticker)}/valuation-research/refresh`, null, { params: { cik: cik || undefined } })
    const detail = await apiClient.get<ApiResponse<CandidateDetail>>(`/discovery/candidates/${encodeURIComponent(ticker)}/detail`)
    candidateDetail.value = detail.data.data
    ElMessage.success('已更新 Longbridge 估值历史与同业比较')
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '刷新 Longbridge 估值研究失败')
  } finally {
    valuationResearchRefreshing.value = false
  }
}

function valuationMetricRows(value: NonNullable<CandidateDetail['valuation_research']['latest']>) {
  return [
    { metric: 'PE', ...value.metrics.pe, percentile: value.percentiles.pe },
    { metric: 'PB', ...value.metrics.pb, percentile: value.percentiles.pb },
    { metric: 'PS', ...value.metrics.ps, percentile: value.percentiles.ps },
  ]
}

function valuationPercentileText(value: { ranking?: number | null; rank_index: string; rank_total: string }) {
  if (value.ranking == null) return '-'
  const rank = value.rank_index && value.rank_total ? `（${value.rank_index}/${value.rank_total}）` : ''
  return `${(value.ranking * 100).toFixed(1)}%${rank}`
}

function formatForecastNumber(value?: number | null, suffix = '') {
  if (value == null || Number.isNaN(value)) return '-'
  return `${value.toLocaleString(undefined, { maximumFractionDigits: 3 })}${suffix}`
}

function formatForecastRange(value?: CandidateDetail['market_research']['eps_forecast']['latest']) {
  if (!value) return '-'
  return `${formatForecastNumber(value.low)} – ${formatForecastNumber(value.high)}`
}

function anomalyValues(value: string) {
  try {
    const parsed = JSON.parse(value)
    return Array.isArray(parsed) ? parsed.join(' · ') : String(parsed || '-')
  } catch {
    return value || '-'
  }
}

function anomalyEmotionLabel(value: number) { return value === 1 ? '偏多' : value === 2 ? '偏空' : '中性' }

function analystRecommendationLabel(value?: string) {
  const labels: Record<string, string> = { strong_buy: '强烈买入', buy: '买入', hold: '持有', underperform: '跑输', sell: '卖出', strong_sell: '强烈卖出', no_opinion: '无观点', unknown: '未评级' }
  return labels[value || 'unknown'] || value || '未评级'
}

function analystRecommendationTagType(value?: string) {
  if (value === 'strong_buy' || value === 'buy') return 'success'
  if (value === 'sell' || value === 'strong_sell' || value === 'underperform') return 'danger'
  return 'info'
}

function formatAnalystPrice(micros?: number, currency?: string) {
  if (!micros) return '-'
  const prefix = currency || '$'
  return `${prefix}${(micros / 1_000_000).toLocaleString(undefined, { maximumFractionDigits: 2 })}`
}

function formatFairValuePrice(value?: number | null, currency?: string) {
  if (value === undefined || value === null || !Number.isFinite(value)) return '-'
  const prefix = currency || '$'
  return `${prefix}${value.toLocaleString(undefined, { maximumFractionDigits: 2 })}`
}

function analystRatingProvenanceRows(snapshot: CandidateDetail['analyst_rating']['latest']) {
  if (!snapshot) return []
  const providerUpdatedAt = snapshot.provider_updated_at_text || '提供方未返回精确更新时间'
  const fetchedAt = formatDateTime(snapshot.fetched_at)
  const distribution = `强烈买入 ${snapshot.strong_buy_count} · 买入 ${snapshot.buy_count} · 持有 ${snapshot.hold_count} · 跑输 ${snapshot.underperform_count} · 卖出 ${snapshot.sell_count}`
  return [
    { result: '共识评级', value: analystRecommendationLabel(snapshot.recommendation), source: 'Longbridge InstitutionRating / Summary.Recommend', providerUpdatedAt, fetchedAt, note: '提供方汇总结论，不代表单一机构或分析师。' },
    { result: '覆盖数与分布', value: `${snapshot.analyst_count} 位覆盖；${distribution}`, source: 'Longbridge InstitutionRating / Latest.Evaluate', providerUpdatedAt, fetchedAt, note: '覆盖数及各评级档位均为提供方聚合口径。' },
    { result: '平均目标价', value: formatAnalystPrice(snapshot.target_average_micros, snapshot.currency), source: 'Longbridge InstitutionRating / Summary.Target', providerUpdatedAt, fetchedAt, note: '目标价为聚合值，非本系统估值计算。' },
    { result: '目标区间与参考价', value: `${formatAnalystPrice(snapshot.target_low_micros, snapshot.currency)} - ${formatAnalystPrice(snapshot.target_high_micros, snapshot.currency)}；参考 ${formatAnalystPrice(snapshot.reference_price_micros, snapshot.currency)}`, source: 'Longbridge InstitutionRating / Latest.Target', providerUpdatedAt, fetchedAt, note: '区间和参考收盘价以提供方返回值为准。' }
  ]
}

function analystProviderTimeText(snapshot: CandidateDetail['analyst_rating']['latest']) {
  if (!snapshot) return '-'
  return snapshot.provider_updated_at_text || `提供方未返回；本地同步于 ${formatDateTime(snapshot.fetched_at)}`
}

function companyProfileWebsiteURL(value?: string) {
  const website = (value || '').trim()
  if (!website) return '#'
  return /^https?:\/\//i.test(website) ? website : `https://${website}`
}

function openBusinessModelEditor() {
  if (!candidateDetail.value) return
  const model = candidateDetail.value.business_model
  businessModelEditor.ticker = candidateDetail.value.score.ticker
  businessModelEditor.business_model = model.model === 'not_applicable' ? 'unknown' : model.model || 'unknown'
  businessModelEditor.revenue_repeatable_confirmed = !!model.revenue_repeatable_confirmed
  businessModelEditor.reason = model.reason || ''
  businessModelEditor.source_url = model.source_url || ''
  businessModelEditor.operator = model.operator || 'local_user'
  businessModelEditor.review_due_at = model.review_due_at ? model.review_due_at.slice(0, 10) : ''
  businessModelEditorVisible.value = true
}

async function saveBusinessModel() {
  if (!businessModelEditor.ticker || !businessModelEditor.reason.trim() || !businessModelEditor.operator.trim()) {
    ElMessage.warning('请填写确认依据和确认人')
    return
  }
  businessModelSaving.value = true
  try {
    await apiClient.post(`/discovery/candidates/${businessModelEditor.ticker}/business-model`, {
      business_model: businessModelEditor.business_model,
      revenue_repeatable_confirmed: businessModelEditor.revenue_repeatable_confirmed,
      reason: businessModelEditor.reason,
      source_url: businessModelEditor.source_url,
      operator: businessModelEditor.operator,
      review_due_at: businessModelEditor.review_due_at || null,
    })
    const res = await apiClient.get<ApiResponse<CandidateDetail>>(`/discovery/candidates/${businessModelEditor.ticker}/detail`)
    candidateDetail.value = res.data.data
    businessModelEditorVisible.value = false
    ElMessage.success('业务模型已确认；下一次小盘工作流会按新规则重新评分')
    await load()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '保存业务模型失败')
  } finally {
    businessModelSaving.value = false
  }
}

async function addToCandidateWatches(row: CandidateScore) {
  watchingTicker.value = row.ticker
  try {
    await apiClient.post('/discovery/candidate-watches', {
      ticker: row.ticker,
      note: qualityTierLabel(row.quality_tier),
      research_status: 'inbox',
    })
    ElMessage.success(`${row.ticker} 已加入候选关注列表`)
    await Promise.all([load(), loadReviewQueue()])
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加入关注失败')
  } finally {
    watchingTicker.value = ''
  }
}

async function openWatchList() {
  watchDialogVisible.value = true
  await loadCandidateWatches()
}

async function openResearchPortfolio(ticker = '') {
  researchPortfolioVisible.value = true
  await loadResearchPortfolio()
  if (ticker) newResearchPosition(ticker)
}

async function loadResearchPortfolio() {
  researchPortfolioLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<CandidateResearchPortfolio>>('/discovery/research-positions')
    researchPortfolio.value = res.data.data
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载研究组合失败')
  } finally {
    researchPortfolioLoading.value = false
  }
}

function newResearchPosition(ticker = '') {
  researchPositionEditor.ticker = ticker
  researchPositionEditor.existing = false
  researchPositionEditor.max_weight_pct = 0
  researchPositionEditor.reference_cost_usd = undefined
  researchPositionEditor.max_daily_volume_participation_pct = 0
  researchPositionEditor.event_risk_note = ''
  researchPositionEditor.liquidity_note = ''
  researchPositionEditor.note = ''
  researchPositionEditorVisible.value = true
}

function editResearchPosition(row: CandidateResearchPosition) {
  researchPositionEditor.ticker = row.ticker
  researchPositionEditor.existing = true
  researchPositionEditor.max_weight_pct = row.max_weight_pct || 0
  researchPositionEditor.reference_cost_usd = row.reference_cost_usd ?? undefined
  researchPositionEditor.max_daily_volume_participation_pct = row.max_daily_volume_participation_pct || 0
  researchPositionEditor.event_risk_note = row.event_risk_note || ''
  researchPositionEditor.liquidity_note = row.liquidity_note || ''
  researchPositionEditor.note = row.note || ''
  researchPositionEditorVisible.value = true
}

async function saveResearchPosition() {
  if (!researchPositionEditor.ticker.trim()) {
    ElMessage.warning('请填写 Ticker')
    return
  }
  researchPositionSaving.value = true
  try {
    await apiClient.post('/discovery/research-positions', {
      ticker: researchPositionEditor.ticker,
      max_weight_pct: researchPositionEditor.max_weight_pct,
      reference_cost_usd: researchPositionEditor.reference_cost_usd,
      clear_reference_cost_usd: researchPositionEditor.reference_cost_usd == null,
      max_daily_volume_participation_pct: researchPositionEditor.max_daily_volume_participation_pct,
      event_risk_note: researchPositionEditor.event_risk_note,
      liquidity_note: researchPositionEditor.liquidity_note,
      note: researchPositionEditor.note,
    })
    ElMessage.success('研究仓位已保存')
    researchPositionEditorVisible.value = false
    await loadResearchPortfolio()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '保存研究仓位失败')
  } finally {
    researchPositionSaving.value = false
  }
}

async function deleteResearchPosition(id: number) {
  try {
    await ElMessageBox.confirm('删除该手工研究仓位？不会影响关注列表或任何真实账户。', '确认删除', { type: 'warning' })
  } catch {
    return
  }
  try {
    await apiClient.delete(`/discovery/research-positions/${id}`)
    ElMessage.success('研究仓位已删除')
    await loadResearchPortfolio()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '删除研究仓位失败')
  }
}

async function loadCandidateWatches() {
  watchLoading.value = true
  try {
    const status = showArchivedWatches.value ? 'archived' : 'active'
    const res = await apiClient.get<ApiResponse<PageResult<CandidateWatch>>>('/discovery/candidate-watches', { params: { page: 1, page_size: 100, status } })
    watchRows.value = res.data.data.items || []
  } finally {
    watchLoading.value = false
  }
}

async function loadReviewQueue() {
  const res = await apiClient.get<ApiResponse<CandidateReviewQueue>>('/discovery/candidate-review-queue')
  reviewQueue.value = res.data.data
}

async function editCandidateWatch(row: CandidateWatch) {
  watchEditor.ticker = row.ticker
  watchEditor.note = row.note || ''
  watchEditor.research_status = row.research_status || 'inbox'
  watchEditor.thesis = row.thesis || ''
  watchEditor.market_concern = row.market_concern || ''
  watchEditor.falsifiable_judgment = row.falsifiable_judgment || ''
  watchEditor.catalyst = row.catalyst || ''
  watchEditor.catalyst_source = row.catalyst_source || ''
  watchEditor.catalyst_date = row.catalyst_date ? row.catalyst_date.slice(0, 10) : ''
  watchEditor.risk_notes = row.risk_notes || ''
  watchEditor.invalidation = row.invalidation || ''
  watchEditor.next_review_at = row.next_review_at ? row.next_review_at.slice(0, 10) : ''
  watchEditorVisible.value = true
}

async function archiveCandidateWatch(row: CandidateWatch) {
  await saveCandidateWatch(row, { note: row.note || '', status: 'archived' })
}

async function restoreCandidateWatch(row: CandidateWatch) {
  await saveCandidateWatch(row, { note: row.note || '', status: 'active' })
}

async function saveCandidateWatch(row: CandidateWatch, payload: Record<string, unknown>) {
  await apiClient.post('/discovery/candidate-watches', { ticker: row.ticker, ...payload })
  ElMessage.success('关注列表已更新')
  await Promise.all([loadCandidateWatches(), loadReviewQueue(), load()])
}

async function captureCandidateWatchBaseline(row: CandidateWatch) {
  try {
    await ElMessageBox.confirm(`将当前已发布的 ${row.ticker} 价格、市值、评分与基本面数据设为关注基准？此操作只对尚无基准的旧记录生效。`, '设为当前基准', { type: 'warning' })
  } catch {
    return
  }
  try {
    await apiClient.post('/discovery/candidate-watches', {
      ticker: row.ticker,
      note: row.note || '',
      status: row.status,
      research_status: row.research_status,
      capture_baseline: true,
    })
    ElMessage.success(`${row.ticker} 已从当前批次开始跟踪`)
    await Promise.all([loadCandidateWatches(), load()])
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '设置关注基准失败')
  }
}

async function saveCandidateResearch() {
  if (!watchEditor.ticker) return
  watchSaving.value = true
  try {
    await apiClient.post('/discovery/candidate-watches', {
      ticker: watchEditor.ticker,
      note: watchEditor.note,
      research_status: watchEditor.research_status,
      thesis: watchEditor.thesis,
      market_concern: watchEditor.market_concern,
      falsifiable_judgment: watchEditor.falsifiable_judgment,
      catalyst: watchEditor.catalyst,
      catalyst_source: watchEditor.catalyst_source,
      catalyst_date: watchEditor.catalyst_date ? `${watchEditor.catalyst_date}T00:00:00Z` : undefined,
      clear_catalyst_date: !watchEditor.catalyst_date,
      risk_notes: watchEditor.risk_notes,
      invalidation: watchEditor.invalidation,
      next_review_at: watchEditor.next_review_at ? `${watchEditor.next_review_at}T00:00:00Z` : undefined,
      clear_next_review_at: !watchEditor.next_review_at,
    })
    ElMessage.success('研究记录已保存')
    watchEditorVisible.value = false
    await Promise.all([loadCandidateWatches(), loadReviewQueue(), load()])
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '保存研究记录失败')
  } finally {
    watchSaving.value = false
  }
}

async function deleteCandidateWatch(id: number) {
  await apiClient.delete(`/discovery/candidate-watches/${id}`)
  ElMessage.success('已取消关注')
  await Promise.all([loadCandidateWatches(), loadReviewQueue(), load()])
}

async function previewSummary() {
  summaryLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<CandidateNotificationPreview>>('/discovery/candidates/notification-preview')
    notificationPreview.value = res.data.data
    summary.value = res.data.data.summary
    notificationPreviewStatus.value = res.data.data.suppressed_reason
      ? `通知被抑制：${res.data.data.suppressed_reason}；本次不会发送 Telegram。`
      : '配置已启用；本次仅 dry-run 预检，不会发送 Telegram。'
    summaryVisible.value = true
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '预检候选通知失败')
  } finally {
    summaryLoading.value = false
  }
}

async function sendNotification() {
  if (!notificationPreview.value || !notificationPreview.value.enabled || notificationPreview.value.suppressed_reason) return
  const warning = forceCandidateNotification.value
    ? '确认强制重发当前小盘候选摘要到 Telegram？这会绕过当天同批次防重复保护。'
    : '确认发送当前小盘候选摘要到 Telegram？发送后会记录通知批次。'
  await ElMessageBox.confirm(warning, forceCandidateNotification.value ? '确认强制重发' : '确认发送', { type: 'warning' })
  sendingNotification.value = true
  try {
    const payload: CandidateNotificationSendInput = { confirm: true, force: forceCandidateNotification.value }
    const res = await apiClient.post<ApiResponse<CandidateNotificationSendResult>>('/discovery/candidates/notification-send', payload)
    ElMessage.success(`候选通知已发送，批次 #${res.data.data.batch.id}`)
    notificationPreview.value = res.data.data.preview
    summary.value = res.data.data.preview.summary
    forceCandidateNotification.value = false
    summaryVisible.value = false
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '发送候选通知失败')
  } finally {
    sendingNotification.value = false
  }
}

function search() {
  page.value = 1
  load()
}

function reset() {
  filters.grade = ''
  filters.ticker = ''
  filters.eligible_a = ''
  filters.eligible_b = ''
  filters.sector_category = ''
  filters.quality_tier = ''
  filters.change_status = ''
  filters.technical_signal = ''
  filters.research_readiness = ''
  filters.exclude_research_readiness = ['blocked']
  filters.exclude_quality_tags = []
  filters.max_ev_sales = undefined
  filters.min_net_cash_to_market_cap_pct = undefined
  filters.price_freshness = ''
  filters.upcoming_earnings = false
  filters.followed = false
  advancedFiltersVisible.value = false
  search()
}

function quickFilterActive(kind: string) {
  if (kind === 'default_candidates') return !filters.research_readiness && filters.exclude_research_readiness.length === 1 && filters.exclude_research_readiness[0] === 'blocked'
  if (kind === 'actionable') return filters.research_readiness === 'ready'
  if (kind === 'verification') return filters.research_readiness === 'research_only'
  if (kind === 'blocked') return filters.research_readiness === 'blocked'
  if (kind === 'strong_b') return filters.quality_tier === 'strong_b'
  if (kind === 'improved') return filters.change_status === 'improved'
  if (kind === 'exclude_low_liquidity') return filters.exclude_quality_tags.includes('low_liquidity')
  if (kind === 'price_attention') return filters.price_freshness === 'attention'
  if (kind === 'upcoming_earnings') return filters.upcoming_earnings
  if (kind === 'followed') return filters.followed
  return false
}

function toggleQuickFilter(kind: string) {
  if (kind === 'strong_b') {
    filters.quality_tier = filters.quality_tier === 'strong_b' ? '' : 'strong_b'
  } else if (kind === 'improved') {
    filters.change_status = filters.change_status === 'improved' ? '' : 'improved'
  } else if (kind === 'exclude_low_liquidity') {
    filters.exclude_quality_tags = filters.exclude_quality_tags.includes('low_liquidity') ? [] : ['low_liquidity']
  } else if (kind === 'price_attention') {
    filters.price_freshness = filters.price_freshness === 'attention' ? '' : 'attention'
  } else if (kind === 'upcoming_earnings') {
    filters.upcoming_earnings = !filters.upcoming_earnings
  } else if (kind === 'followed') {
    filters.followed = !filters.followed
  }
  search()
}

function showDefaultCandidates() {
  filters.research_readiness = ''
  filters.exclude_research_readiness = ['blocked']
  search()
}

function setReadinessFilter(status: 'ready' | 'research_only' | 'blocked') {
  if (filters.research_readiness === status) {
    showDefaultCandidates()
    return
  }
  filters.research_readiness = status
  filters.exclude_research_readiness = []
  search()
}

function onPageChange(next: number) {
  page.value = next
  load()
}

function onSortChange({ prop, order }: { prop?: string; order?: 'ascending' | 'descending' | null }) {
  sortState.sort_by = order && prop ? prop : ''
  sortState.sort_order = order === 'ascending' ? 'asc' : order === 'descending' ? 'desc' : ''
  page.value = 1
  load()
}

function gradeLabel(grade: string) {
  if (grade === 'A') return 'A级'
  if (grade === 'B') return 'B级'
  return '排除'
}

function gradeTagType(grade: string) {
  if (grade === 'A') return 'success'
  if (grade === 'B') return 'warning'
  return 'info'
}

function qualityTierLabel(tier?: string) {
  if (tier === 'a') return 'A级'
  if (tier === 'strong_b') return '强B'
  if (tier === 'standard_b') return '普通B'
  if (tier === 'watch_b') return '观察B'
  if (tier === 'excluded') return '已排除'
  return '-'
}

function qualityTierTagType(tier?: string) {
  if (tier === 'a' || tier === 'strong_b') return 'success'
  if (tier === 'standard_b') return 'warning'
  if (tier === 'watch_b') return 'info'
  return 'info'
}

function changeStatusLabel(status?: string) {
  if (status === 'new') return '新增'
  if (status === 'improved') return '改善'
  if (status === 'weakened') return '转弱'
  if (status === 'unchanged') return '延续'
  return '-'
}

function changeStatusTagType(status?: string) {
  if (status === 'new' || status === 'improved') return 'success'
  if (status === 'weakened') return 'danger'
  if (status === 'unchanged') return 'info'
  return 'info'
}

function scoreHistoryDelta(value?: number | null) {
  if (value === undefined || value === null) return '-'
  return value > 0 ? `+${value}` : `${value}`
}

function reviewPriorityBaseScore(row: CandidateScore) {
  return Math.floor(((row.quality_adjusted_score ?? row.total_score ?? 0) * 60) / 100)
}

function formatReviewPriorityPoints(value?: number | null) {
  if (value === undefined || value === null) return '-'
  return value > 0 ? `+${value}` : `${value}`
}

function scoreHistoryReasonSummary(reasons?: CandidateChangeReason[]) {
  if (!reasons?.length) return '无核心字段变化'
  return reasons.map((reason) => `${reason.label}：${reason.previous || '-'} → ${reason.current || '-'}`).join('；')
}

function candidateSignalEventLabel(eventType?: string) {
  if (eventType === 'entered_a') return '首次进入 A 级'
  if (eventType === 'entered_b') return '首次进入 B 级'
  if (eventType === 'upgraded_quality') return '质量显著提升'
  return eventType || '候选事件'
}

function displayQualityTags(tags?: string[]) {
  return (tags || []).filter((tag) => tag !== 'no_insider_buy').slice(0, 3)
}

function qualityTagLabel(tag: string) {
  const labels: Record<string, string> = {
    low_revenue_base: '低收入基数',
    extreme_revenue_growth: '极端增长',
    low_liquidity: '低流动性',
    financials_missing: '财务缺失',
    active_capital_risk: '融资风险',
    secondary_price_source: '补充价格源',
    quarterly_growth_conflicts_with_annual: '季度增长转弱',
  }
  return labels[tag] || tag
}

function researchStatusLabel(status?: string) {
  if (status === 'researching') return '研究中'
  if (status === 'conviction') return '重点关注'
  if (status === 'rejected') return '淘汰'
  return '待研究'
}

function researchStatusTagType(status?: string) {
  if (status === 'conviction') return 'success'
  if (status === 'researching') return 'warning'
  if (status === 'rejected') return 'info'
  return 'primary'
}

function reviewQueueStateLabel(state?: string, days?: number) {
  if (state === 'overdue') return `已逾期 ${Math.abs(days || 0)} 天`
  if (state === 'due_today') return '今天复查'
  if (typeof days === 'number') return `${days} 天后复查`
  return '待复查'
}

function reviewQueueStateTagType(state?: string) {
  if (state === 'overdue') return 'danger'
  if (state === 'due_today') return 'warning'
  return 'info'
}

function effectivenessCohortLabel(grade: string) {
  if (grade === 'all') return '全部 A/B'
  return `${grade}级`
}

function effectivenessWindow(cohort: CandidateEffectivenessCohort, horizon: number) {
  return cohort.windows.find((item) => item.horizon_days === horizon)
}

function qualityTagType(tag: string) {
  if (tag === 'active_capital_risk' || tag === 'financials_missing') return 'danger'
  if (tag === 'low_revenue_base' || tag === 'extreme_revenue_growth' || tag === 'low_liquidity') return 'warning'
  return 'info'
}

function priceFreshnessTagType(status?: string) {
  if (status === 'current') return 'success'
  if (status === 'previous_trading_day') return 'warning'
  if (status === 'stale' || status === 'future' || status === 'missing') return 'danger'
  return 'info'
}

function lineageStatusLabel(status?: string) {
  if (status === 'valid') return '可用'
  if (status === 'partial') return '部分覆盖'
  if (status === 'missing') return '缺失'
  return status || '-'
}

function lineageStatusTagType(status?: string) {
  if (status === 'valid') return 'success'
  if (status === 'partial') return 'warning'
  if (status === 'missing') return 'danger'
  return 'info'
}

function priceFreshnessTooltip(row: CandidateScore) {
  if (row.price_freshness_status === 'current') return '与最近已收盘交易日一致'
  if (row.price_freshness_status === 'previous_trading_day') return `最近已收盘交易日（较本批次有效日早 ${row.price_age_calendar_days ?? '-'} 个自然日）`
  if (row.price_freshness_status === 'stale') return `价格已过期（相差 ${row.price_age_calendar_days ?? '-'} 个自然日）`
  if (row.price_freshness_status === 'future') return '价格日期晚于本批次有效交易日，需要复核'
  if (row.price_freshness_status === 'missing') return '未取得该标的价格'
  return '价格日期无法与本批次有效交易日比较'
}

function priceEvidenceTooltip(row: CandidateScore) {
  const source = priceSourceLabel(row.price_source)
  return `列表/技术分析使用最新本地行情：${source}，交易日 ${formatDate(row.price_trade_date)}。${priceFreshnessTooltip(row)}`
}

function priceSourceLabel(source?: string) {
  if (source === 'local-cache') return '本地前一交易日回退'
  return source || '本地行情缓存'
}

function sectorTagType(score?: number) {
  if (!Number.isFinite(score)) return 'info'
  if (Number(score) >= 7) return 'success'
  if (Number(score) >= 5) return 'warning'
  return 'info'
}

function healthStatusLabel(status: string) {
  if (status === 'ok') return '正常'
  if (status === 'degraded') return '降级'
  if (status === 'missing') return '缺少批次'
  return status || '-'
}

function readinessLabel(status?: string) {
  if (status === 'ready') return '可行动'
  if (status === 'research_only') return '待核验'
  if (status === 'blocked') return '已阻断'
  return '未知'
}

function researchNextStepTagType(priority?: string) {
  if (priority === 'blocked') return 'danger'
  if (priority === 'review') return 'warning'
  return 'success'
}

function researchNextStepAlertType(priority?: string) {
  if (priority === 'blocked') return 'error'
  if (priority === 'review') return 'warning'
  return 'success'
}

function researchNextStepPriorityLabel(priority?: string) {
  if (priority === 'blocked') return '先处理阻断'
  if (priority === 'review') return '需要核验'
  return '可进入研究'
}

function readinessTagType(status?: string) {
  if (status === 'ready') return 'success'
  if (status === 'research_only') return 'warning'
  if (status === 'blocked') return 'danger'
  return 'info'
}

function investabilityLabel(status?: string) {
  if (status === 'tradable') return '可交易'
  if (status === 'constrained') return '受限'
  if (status === 'blocked') return '阻断'
  return '未知'
}

function investabilityTagType(status?: string) {
  if (status === 'tradable') return 'success'
  if (status === 'constrained') return 'warning'
  if (status === 'blocked') return 'danger'
  return 'info'
}

function investabilityAlertType(status?: string) {
  if (status === 'tradable') return 'success'
  if (status === 'constrained') return 'warning'
  return 'error'
}

function investabilityTooltipLines(value?: CandidateInvestability) {
  if (!value) return ['尚未计算可交易性闸门']
  const lines = [`${investabilityLabel(value.status)}：基于日线价格与成交额，不构成交易建议`]
  lines.push(`平均日成交额：${formatUSD(value.average_dollar_volume_usd)}，样本 ${value.sample_days} 日`)
  if (value.suggested_max_daily_notional_usd > 0) lines.push(`研究上限参考：日成交额的 ${value.max_adv_participation_pct}% ≈ ${formatUSD(value.suggested_max_daily_notional_usd)}`)
  lines.push(`点差：${value.spread_evidence_status === 'not_available_eod' ? '免费日线源未覆盖，需自行核验' : value.spread_evidence_status}`)
  for (const reason of value.reasons || []) lines.push(investabilityReasonLabel(reason))
  return lines
}

function investabilityDetailTitle(value?: CandidateInvestability) {
  return `状态：${investabilityLabel(value?.status)}。仅代表现有日线证据下的研究流动性，不是买卖建议。`
}

function investabilityDetailDescription(value?: CandidateInvestability) {
  return investabilityTooltipLines(value).slice(1).join('；')
}

function investabilityReasonLabel(reason: string) {
  const labels: Record<string, string> = {
    price_evidence_unavailable: '价格证据不可用', price_freshness_unknown: '价格日期无法校验', price_not_recent_close: '不是最近有效收盘价',
    liquidity_history_unavailable: '缺少同源日线流动性历史', liquidity_history_short: '同源日线历史少于 15 日',
    average_dollar_volume_below_100k: '平均日成交额低于 $100K', average_dollar_volume_below_500k: '平均日成交额低于 $500K',
    sub_dollar_price: '股价低于 $1', extreme_daily_volatility: '日波动率极高', reverse_split_risk: '存在反向拆股风险',
    going_concern_risk: '存在持续经营风险', price_source_unknown: '价格来源未知',
  }
  return labels[reason] || reason
}

function dilutionLabel(status?: string) {
  if (status === 'stable') return '稳定'
  if (status === 'elevated_dilution') return '稀释偏高'
  if (status === 'high_dilution') return '高稀释'
  if (status === 'shares_reduced') return '股本减少'
  return '资料不足'
}

function dilutionTagType(status?: string) {
  if (status === 'stable' || status === 'shares_reduced') return 'success'
  if (status === 'elevated_dilution') return 'warning'
  if (status === 'high_dilution') return 'danger'
  return 'info'
}

function dilutionAlertType(status?: string) {
  if (status === 'stable' || status === 'shares_reduced') return 'success'
  if (status === 'elevated_dilution') return 'warning'
  if (status === 'high_dilution') return 'error'
  return 'info'
}

function dilutionTooltipLines(value?: CandidateDilutionTrend) {
  if (!value || value.status === 'missing') {
    return [
      '资料不足：尚无两份相隔至少 90 日、且已被 SEC 接收的股本快照。',
      '下一次安全宇宙同步会从 SEC companyfacts 补充历史封面页股数。',
    ]
  }
  const lines = [`${dilutionLabel(value.status)}：${formatPerformance(value.share_change_pct)}，观察窗口 ${value.observation_days} 日`]
  lines.push(`已发行股数：${formatVolume(value.prior_shares)}（${value.prior_instant}）→ ${formatVolume(value.latest_shares)}（${value.latest_instant}）`)
  lines.push('仅比较 SEC 封面页已发行股数；不等于完全稀释后股数，拆股及资本事件需结合风险公告判断。')
  return lines
}

function dilutionDetailTitle(value?: CandidateDilutionTrend) {
  const suffix = value?.status === 'high_dilution' ? '高稀释会将该标的降为“待核验”。' : '该指标不改写 A/B 基础评分。'
  return `状态：${dilutionLabel(value?.status)}。${suffix}`
}

function insiderCoverageAlertType(status?: string) {
  if (status === 'covered_transactions') return 'success'
  if (status === 'covered_no_filings' || status === 'covered_no_transactions') return 'info'
  if (status === 'partial') return 'warning'
  return 'error'
}

function insiderCoverageTitle(coverage: DiscoveryInsiderCoverage) {
  const labels: Record<string, string> = {
    covered_transactions: 'Form 4 覆盖完成：已解析到交易记录',
    covered_no_filings: 'Form 4 覆盖完成：观察窗口内没有申报',
    covered_no_transactions: 'Form 4 覆盖完成：申报中没有可用交易行',
    partial: 'Form 4 仅部分覆盖：结论需谨慎',
    unavailable: 'Form 4 覆盖不可用：不能据此判断没有内幕买入',
  }
  return labels[coverage.status] || `Form 4 覆盖状态：${coverage.status || '未知'}`
}

function insiderCoverageDescription(coverage: DiscoveryInsiderCoverage) {
  const failures = coverage.permanent_document_failures + coverage.transient_document_failures + coverage.malformed_documents
  const detail = `观察到 ${coverage.eligible_filings} 份 Form 4，下载 ${coverage.downloaded_documents} 份，成功解析 ${coverage.parsed_documents} 份，交易行 ${coverage.transaction_count} 条。`
  if (!failures) return detail
  return `${detail} 未完整覆盖 ${failures} 份（永久缺失 ${coverage.permanent_document_failures}、暂时失败 ${coverage.transient_document_failures}、格式异常 ${coverage.malformed_documents}）。`
}

function readinessTooltipLines(row: CandidateScore) {
  const readiness = row.research_readiness
  if (!readiness) return ['尚未计算数据充分性状态']
  const lines = [`证据状态：${readinessLabel(readiness.status)}；${readiness.status === 'ready' ? '关键研究证据完整，可进入默认通知' : '不进入默认通知，仍可作为研究对象查看'}`]
  if (readiness.financial_period_end) lines.push(`财务期末：${readiness.financial_period_end}（${readiness.financial_staleness_days} 天前）`)
  const duplicateReasonByInsiderStatus: Record<string, string> = {
    source_unavailable: 'insider_source_unavailable',
    partial: 'insider_coverage_partial',
    unavailable: 'insider_coverage_unavailable',
    coverage_missing: 'insider_coverage_missing',
  }
  if (readiness.insider_evidence_status === 'source_unavailable') lines.push('内幕交易：数据源暂不可用，无法判断是否存在合格买入')
  else if (readiness.insider_evidence_status === 'covered_qualified_purchase') lines.push('内幕交易：已覆盖，发现合格的关键人员公开市场买入')
  else if (readiness.insider_evidence_status === 'covered_no_qualified_purchase') lines.push('内幕交易：已覆盖，但没有合格的关键人员公开市场买入')
  else if (readiness.insider_evidence_status === 'covered_no_filings') lines.push('内幕交易：已覆盖，观察窗口没有可解析 Form 4 申报')
  else if (readiness.insider_evidence_status === 'covered_no_transactions') lines.push('内幕交易：已覆盖，Form 4 未含可用交易行')
  else if (readiness.insider_evidence_status === 'partial') lines.push('内幕交易：部分覆盖，至少一份 Form 4 无法完整解析')
  else if (readiness.insider_evidence_status === 'unavailable') lines.push('内幕交易：覆盖不可用，不能据此判断没有买入')
  else if (readiness.insider_evidence_status === 'coverage_missing') lines.push('内幕交易：缺少本批次覆盖明细')
  else if (readiness.insider_evidence_status) lines.push('内幕交易：旧批次未保留覆盖明细')
  for (const reason of readiness.reasons || []) {
    // The evidence line above already gives a human-readable explanation.
    // Do not repeat the same condition again as a raw readiness reason.
    if (reason === duplicateReasonByInsiderStatus[readiness.insider_evidence_status || '']) continue
    lines.push(readinessReasonLabel(reason))
  }
  return lines
}

function readinessReasonLabel(reason: string) {
  const labels: Record<string, string> = {
    market_cap_unavailable: '市值证据不可用',
    market_price_unavailable: '价格证据不可用或无效',
    market_price_freshness_unknown: '价格日期无法校验',
    market_price_not_current: '价格未达到最近已收盘交易日要求',
    financial_metrics_unavailable: '财务指标不可用',
    financial_period_stale: '财务期已超过时效窗口',
    insider_source_unavailable: '内幕交易来源不可用',
    insider_coverage_missing: '内幕交易覆盖明细缺失',
    insider_coverage_partial: '内幕交易仅部分覆盖',
    insider_coverage_unavailable: '内幕交易覆盖不可用',
    biotech_business_model_unconfirmed: '生物医药业务模型尚未确认',
    biotech_business_model_review_due: '生物医药业务模型需要复查',
  }
  return labels[reason] || reason
}

function businessModelLabel(model?: string) {
  if (model === 'commercial') return '已商业化'
  if (model === 'clinical_pre_revenue') return '临床前/临床期'
  if (model === 'mixed_or_licensing') return '授权/里程碑混合'
  if (model === 'unknown') return '待确认'
  if (model === 'not_applicable') return '不适用'
  return model || '待确认'
}

function formatHealthIssue(issue: string) {
  const [code, count] = issue.split(':')
  if (code === 'missing_financials') return `财务指标不可用：${count || 0}`
  if (code === 'missing_insider_data' || code === 'missing_insiders') return `内幕来源缺失：${count || 0}`
  if (code === 'missing_market_cap') return `缺市值：${count || 0}`
  if (code === 'price_previous_trading_day') return `使用前一交易日价格：${count || 0}`
  if (code === 'stale_prices') return `价格已过期：${count || 0}`
  if (code === 'missing_prices') return `价格缺失：${count || 0}`
  if (code === 'research_only_candidates') return `待核验候选：${count || 0}`
  if (code === 'blocked_candidates') return `已阻断候选：${count || 0}`
  if (code === 'candidate_insider_records') return `候选内幕记录覆盖：${count || 0}`
  if (code === 'insider_coverage_partial') return `内幕交易部分覆盖：${count || 0}`
  if (code === 'insider_coverage_unavailable') return `内幕交易覆盖不可用：${count || 0}`
  if (code === 'pending_financial_recalculations') return `待财务重算：${count || 0}`
  if (code === 'candidate_recent_filings') return `候选近期 SEC 公告覆盖：${count || 0}`
  if (code === 'no_current_published_prescreen_batch') return '暂无已发布的小盘候选批次'
  return issue
}

function healthInsiderDataLabel(status?: string) {
  if (status === 'available') return '已同步'
  if (status === 'missing') return '缺失'
  return status || '-'
}

function formatUSD(value?: number | null) {
	if (!value) return '-'
	if (value >= 1_000_000_000) return `$${(value / 1_000_000_000).toFixed(2)}B`
	return `$${(value / 1_000_000).toFixed(1)}M`
}

function formatMultiple(value?: number | null) {
  if (!Number.isFinite(value) || Number(value) < 0) return 'N/A'
  return `${Number(value).toFixed(2)}x`
}

function fractionToPct(value?: number | null) {
  if (!Number.isFinite(value)) return undefined
  return Number(value) * 100
}

function netCash(valuation?: CandidateScore['valuation']) {
  if (!valuation || !Number.isFinite(valuation.cash_usd) || !Number.isFinite(valuation.total_debt_usd)) return undefined
  return Number(valuation.cash_usd) - Number(valuation.total_debt_usd)
}

function valuationReasonText(reasons?: string[]) {
  const labels: Record<string, string> = {
    market_cap_or_price_unavailable: '市值或价格证据不可用',
    cash_unavailable: '现金/短期投资事实缺失',
    total_debt_unavailable: '流动与非流动债务事实不完整',
    ttm_revenue_unavailable: '未能取得连续四个季度的收入',
    ttm_gross_profit_unavailable: '未能取得连续四个季度的毛利',
  }
  return (reasons || []).map((reason) => labels[reason] || reason).join('；')
}

function formatCriteriaUSD(value: number) {
  if (!Number.isFinite(value)) return '-'
  if (value >= 1_000_000_000) return `$${(value / 1_000_000_000).toFixed(0)}B`
  return `$${(value / 1_000_000).toFixed(0)}M`
}

function formatPrice(value?: number, currency?: string) {
  if (!Number.isFinite(value)) return '-'
  const prefix = currency === 'USD' || !currency ? '$' : `${currency} `
  return `${prefix}${Number(value).toFixed(2)}`
}

function formatVolume(value?: number) {
  if (!Number.isFinite(value)) return '-'
  if (Number(value) >= 1_000_000) return `${(Number(value) / 1_000_000).toFixed(1)}M`
  if (Number(value) >= 1_000) return `${(Number(value) / 1_000).toFixed(1)}K`
  return String(value)
}

function formatPerformance(value?: number | null) {
	if (!Number.isFinite(value)) return '-'
	const num = Number(value)
	return `${num >= 0 ? '+' : ''}${num.toFixed(1)}%`
}

function formatWatchMetricChange(value?: number | null) {
  if (!Number.isFinite(value)) return '—'
  const change = Number(value)
  return `${change >= 0 ? '+' : ''}${change.toFixed(1)}%`
}

function formatWatchScoreChange(value?: number | null) {
  if (!Number.isFinite(value)) return '—'
  const change = Number(value)
  return `${change >= 0 ? '+' : ''}${change} 分`
}

function watchChangeClass(value?: number | null) {
  if (!Number.isFinite(value) || Number(value) === 0) return 'watch-change-neutral'
  return Number(value) > 0 ? 'watch-change-positive' : 'watch-change-negative'
}

function relativeStrengthSummary(technical?: CandidateScore['technical'], window = 20) {
  const relative = technical?.relative_strength
  if (!relative || relative.status === 'missing') return 'IWM 本地基准数据缺失'
  if (relative.status === 'insufficient_candidate_history') return '个股历史不足'
  if (relative.status === 'insufficient_benchmark_history') return `IWM 匹配交易日不足（${relative.matched_sample_days || 0}）`
  const candidateReturn = window === 60 ? relative.candidate_return_60d_pct : relative.candidate_return_20d_pct
  const benchmarkReturn = window === 60 ? relative.benchmark_return_60d_pct : relative.benchmark_return_20d_pct
  const excessReturn = window === 60 ? relative.excess_return_60d_pct : relative.excess_return_20d_pct
  if (!Number.isFinite(excessReturn)) return `${window} 日数据不足（匹配 ${relative.matched_sample_days || 0} 日）`
  return `${formatPerformance(excessReturn)} 超额（个股 ${formatPerformance(candidateReturn)} / IWM ${formatPerformance(benchmarkReturn)}）`
}

function anchoredVWAPSummary(technical?: CandidateScore['technical']) {
  const value = technical?.anchored_vwap
  if (!value || value.status === 'anchor_unavailable') return '暂无可审计候选事件'
  if (value.status === 'anchor_outside_local_history') return '事件早于本地日线历史'
  if (value.status !== 'ready') return '锚定后有效日线不足 3 日'
  return `${formatPrice(value.approximate_vwap_usd, 'USD')}（${formatPerformance(value.distance_pct)}，${value.trading_days} 日）`
}

function anchoredVWAPEventSummary(technical?: CandidateScore['technical']) {
  const value = technical?.anchored_vwap
  if (!value || value.status === 'anchor_unavailable') return '暂无可审计候选事件'
  if (!value.anchor_label) return '-'
  return `${value.anchor_label}｜${formatDate(value.anchor_trade_date)}｜按日线收盘价×成交量近似，不是盘中 VWAP`
}

function tradeSetupLabel(status?: string) {
  if (status === 'entry_candidate') return '入场候选'
  if (status === 'watching') return '观察中'
  if (status === 'exit_warning') return '离场预警'
  if (status === 'invalidated') return '趋势失效'
  return '计划不可用'
}

function tradeSetupTagType(status?: string) {
  if (status === 'entry_candidate') return 'success'
  if (status === 'exit_warning') return 'warning'
  if (status === 'invalidated') return 'danger'
  return 'info'
}

function tradeSetupSummary(technical?: CandidateScore['technical']) {
  const setup = technical?.trade_setup
  const statusSince = setup?.status_since ? '；当前状态始于 ' + formatDateTime(setup.status_since) : '；状态起始时间将在下次日线同步后记录'
  if (!setup || setup.status === 'unavailable') return (setup?.reasons?.[0] || '日线历史不足') + statusSince
  const stop = formatTradeStop(technical)
  if (setup.status === 'entry_candidate') return setup.entry_trigger + '；止损 ' + stop + statusSince
  if (setup.exit_reason) return setup.exit_reason + '；止损 ' + stop + statusSince
  return (setup.reasons?.[0] || '等待触发') + '；计划止损 ' + stop + statusSince
}

function formatTradeStop(technical?: CandidateScore['technical']) {
  const setup = technical?.trade_setup
  if (!setup || !Number.isFinite(setup.stop_loss_usd) || setup.stop_loss_usd <= 0) return '-'
  return `${formatPrice(setup.stop_loss_usd, 'USD')}（风险 ${formatPct(setup.risk_pct)}）`
}

function formatTradeTarget(technical?: CandidateScore['technical']) {
  const setup = technical?.trade_setup
  if (!setup || setup.take_profit_zone_low_usd <= 0 || setup.take_profit_zone_high_usd <= 0) return '-'
  return `${formatPrice(setup.take_profit_zone_low_usd, 'USD')} - ${formatPrice(setup.take_profit_zone_high_usd, 'USD')}`
}

function formatRatio(value?: number | null) {
  if (!Number.isFinite(value) || Number(value) <= 0) return '-'
  return `${Number(value).toFixed(2)}x`
}

function technicalStatusLabel(technical?: CandidateScore['technical']) {
  if (technical?.status === 'data_insufficient') return '历史不足'
  if (technical?.status === 'missing') return '无行情数据'
  return '暂无技术数据'
}

function technicalStatusDescription(technical?: CandidateScore['technical']) {
  if (technical?.status === 'data_insufficient') {
    return `技术分析需要至少 ${technical.required_sample_days || 21} 个有效交易日，当前仅有 ${technical.sample_days || 0} 个；不会据此生成信号。`
  }
  if (technical?.status === 'missing') return '暂无可用的日线行情，无法计算技术信号。'
  return '暂无技术分析数据。'
}

function sectorPercentage(count: number) {
  const totalCount = overview.value?.total || 0
  if (!totalCount) return 0
  return Number(((count / totalCount) * 100).toFixed(1))
}

function formatPct(value?: number | null) {
	return Number.isFinite(value) ? `${Number(value).toFixed(1)}%` : '-'
}

function formatPlainUSD(value?: number) {
  if (!Number.isFinite(value)) return '-'
  return `$${Number(value).toLocaleString()}`
}

function revenueGrowthCalculationTooltipLines(row: CandidateScore, period: 'quarterly_yoy' | 'quarterly_qoq' | 'annual_yoy' | 'annual_qoq') {
  const info = row.revenue_growth_explanation
  if (!info) {
    return ['来源：candidate_score_snapshots.revenue_growth_pct；详情数据未返回，无法展开原始收入计算。']
  }
  const isQuarterly = period.startsWith('quarterly')
  const isQoQ = period.endsWith('qoq')
  const currentRevenue = isQuarterly ? info.latest_quarter_revenue_usd : info.latest_annual_revenue_usd
  const priorRevenue = isQuarterly
    ? (isQoQ ? info.previous_quarter_revenue_usd : info.prior_year_quarter_revenue_usd)
    : info.prior_annual_revenue_usd
  const resultPct = period === 'quarterly_yoy'
    ? info.quarterly_revenue_yoy_pct
    : period === 'quarterly_qoq'
      ? info.quarterly_revenue_qoq_pct
      : period === 'annual_yoy'
        ? info.annual_revenue_yoy_pct
        : info.annual_revenue_qoq_pct
  const title = period === 'quarterly_yoy'
    ? '季度收入同比 YoY'
    : period === 'quarterly_qoq'
      ? '季度收入环比 QoQ'
      : period === 'annual_yoy'
        ? '年度收入同比 YoY'
        : '年度收入环比'
  const currentLabel = isQuarterly ? '最新季度收入' : '最新年度收入'
  const priorLabel = period === 'quarterly_yoy'
    ? '去年同期季度收入'
    : period === 'quarterly_qoq'
      ? '上一季度收入'
      : '上一年度收入'
  return [
    `${title}`,
    `来源：${info.source || 'SEC companyfacts / financial_metric_snapshots'}`,
    `${currentLabel}：${formatPlainUSD(currentRevenue)}`,
    `${priorLabel}：${formatPlainUSD(priorRevenue)}`,
    `公式：（${currentLabel} - ${priorLabel}）/ ${priorLabel} × 100%`,
    `代入：（${formatPlainUSD(currentRevenue)} - ${formatPlainUSD(priorRevenue)}）/ ${formatPlainUSD(priorRevenue)} × 100% = ${formatPct(resultPct)}`,
    `质量标记：${financialQualityFlagsLabel(info.quality_flags_json)}`,
  ]
}

function revenueGrowthQuarterly(row: CandidateScore) {
  return row.revenue_growth_explanation?.revenue_growth_available
    ? row.revenue_growth_explanation.quarterly_revenue_yoy_pct
    : row.revenue_growth_pct
}

function revenueGrowthAnnual(row: CandidateScore) {
  return row.revenue_growth_explanation?.revenue_growth_available
    ? row.revenue_growth_explanation.annual_revenue_yoy_pct
    : row.revenue_growth_pct
}

function revenueGrowthQuarterlyQoQ(row: CandidateScore) {
  return row.revenue_growth_explanation?.revenue_growth_available
    ? row.revenue_growth_explanation.quarterly_revenue_qoq_pct
    : NaN
}

function revenueGrowthAnnualQoQ(row: CandidateScore) {
  return row.revenue_growth_explanation?.revenue_growth_available
    ? row.revenue_growth_explanation.annual_revenue_qoq_pct
    : NaN
}

function capitalRiskTooltipLines(row: CandidateScore, grade: 'A' | 'B') {
  const risks = (row.capital_risk_summaries || []).filter((risk) => grade === 'A' ? risk.blocks_a : risk.blocks_b)
  if (!risks.length) {
    return [`${grade}级阻断：当前列表未返回具体风险原因，请打开详情查看融资/稀释风险。`]
  }
  const lines = [`${grade}级阻断原因：`]
  risks.forEach((risk, index) => {
    lines.push(`${index + 1}. ${capitalRiskKindLabel(risk.kind)}｜${capitalRiskSeverityLabel(risk.severity)}｜${formatDate(risk.effective_at)}`)
    lines.push(`   ${risk.reason || '-'}`)
  })
  return lines
}

function capitalRiskKindLabel(kind: string) {
  const labels: Record<string, string> = {
    registered_financing: '注册融资',
    atm_program: 'ATM 发行计划',
    reverse_split: '反向拆股',
    going_concern: '持续经营警告',
    warrants: '认股权证/稀释',
    shelf_registration: 'Shelf 注册',
    offering: '发行/融资',
  }
  return labels[kind] || kind || '-'
}

function capitalRiskSeverityLabel(severity: string) {
  if (severity === 'high') return '高风险'
  if (severity === 'medium') return '中风险'
  if (severity === 'low') return '低风险'
  return severity || '-'
}

function candidatePositiveSignals(detail: CandidateDetail) {
  const signals: string[] = []
  if (detail.score.grade === 'A') signals.push('A级候选')
  if (detail.score.grade === 'B' && detail.score.total_score >= 70) signals.push('高分B级')
  if (detail.score.revenue_growth_pct >= 40) signals.push('收入高增长')
  if (detail.score.cash_runway_months >= 12) signals.push('现金 runway 充足')
  if (detail.score.recent_qualified_insider) signals.push('近期合格内幕买入')
  if (detail.sector?.score >= 7) signals.push('赛道评分较高')
  for (const signal of detail.technical?.signals || []) signals.push(signal.label)
  return signals.length ? signals : ['暂无强信号']
}

function candidateRiskSignals(detail: CandidateDetail) {
  const risks: string[] = []
  if (detail.score.active_blocks_a || detail.score.active_blocks_b) risks.push('存在阻断风险')
  if (detail.score.cash_runway_months > 0 && detail.score.cash_runway_months < 9) risks.push('现金 runway 偏短')
  if (!detail.score.recent_qualified_insider) risks.push('缺少合格内幕买入')
  if (detail.financial?.quality_flags_json?.includes('low_revenue_base')) risks.push('收入基数偏低')
  if (detail.financial?.quality_flags_json?.includes('low_prior_revenue_base')) risks.push('同比基数极低')
  if (detail.financial?.quality_flags_json?.includes('extreme_revenue_growth')) risks.push('增长异常需核验')
  if (detail.financial?.quality_flags_json?.includes('quarterly_growth_not_confirmed_qoq')) risks.push('季度环比未确认增长')
  if (detail.financial?.quality_flags_json?.includes('quarterly_growth_conflicts_annual')) risks.push('季度与年度增长方向冲突')
  const riskSummary = detail.capital_risk_summary
  if (riskSummary?.active_events) risks.push(`活跃融资/稀释风险 ${riskSummary.active_events} 条`)
  else if (riskSummary?.recent_inactive_events) risks.push(`近 180 日融资/稀释事件 ${riskSummary.recent_inactive_events} 条`)
  return risks.length ? risks : ['暂无明显风险']
}

function capitalRiskSummaryTitle(detail: CandidateDetail) {
  const summary = detail.capital_risk_summary
  if (!summary) return '没有当前或近 180 日融资/稀释风险'
  if (summary.active_events) return `当前有 ${summary.active_events} 条活跃融资/稀释风险`
  if (summary.recent_inactive_events) return `近 180 日有 ${summary.recent_inactive_events} 条已失效融资/稀释事件`
  return '没有当前或近 180 日融资/稀释风险'
}

function capitalRiskSummaryDescription(detail: CandidateDetail) {
  const summary = detail.capital_risk_summary
  if (!summary) return '详情表仅展示当前活跃或近 180 日事件。'
  const historical = summary.historical_inactive_count || 0
  if (!historical) return '详情表仅展示当前活跃或近 180 日事件。'
  return `另有 ${historical} 条更早的已失效历史记录，仅保留为审计证据，不作为当前风险或评分阻断。`
}

function formatMonths(value: number) {
  if (Number(value) >= 999) return '经营现金流为正'
  return Number.isFinite(value) && value > 0 ? `${value.toFixed(1)} 月` : '-'
}

function cashRunwayTooltip(value: number) {
  if (Number(value) >= 999) return 'TTM 经营现金流与自由现金流口径未显示现金消耗，因此显示“经营现金流为正”；它不是 999 个月的真实 runway，排序时按 60 个月上限处理。'
  return '现金 runway = 可用现金及短期投资 ÷ TTM 经营现金流/自由现金流中更保守的月度消耗。'
}

function financialQualityFlagsLabel(payload?: string) {
  if (!payload) return '-'
  let flags: string[] = []
  try { flags = JSON.parse(payload) } catch { return payload }
  const labels: Record<string, string> = {
    low_revenue_base: '最新季度收入基数偏低', low_prior_revenue_base: '去年同期收入基数低于 $1M',
    extreme_revenue_growth: '同比增幅超过 200%', quarterly_growth_not_confirmed_qoq: '高同比增长未获环比确认',
    quarterly_growth_conflicts_annual: '季度增长与年度增长方向冲突', cash_flow_positive_runway_not_applicable: '经营现金流为正，runway 不适用',
  }
  return flags.map((flag) => labels[flag] || flag).join('；') || '-'
}

function formatDate(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString()
}

function formatBytes(value?: number | null) {
  const bytes = Math.max(0, value || 0)
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let amount = bytes / 1024
  let index = 0
  while (amount >= 1024 && index < units.length - 1) {
    amount /= 1024
    index++
  }
  return `${amount.toFixed(amount >= 10 || index === 0 ? 0 : 1)} ${units[index]}`
}

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime())
    ? value
    : date.toLocaleString('zh-CN', { hour12: false })
}

function discoverySyncStatusLabel(status?: string) {
  if (status === 'running') return '运行中'
  if (status === 'published') return '已成功'
  if (status === 'failed' || status === 'market_failed') return '失败'
  return status || '未知'
}

function discoverySyncStatusTagType(status?: string): 'primary' | 'success' | 'danger' | 'info' {
  if (status === 'running') return 'primary'
  if (status === 'published') return 'success'
  if (status === 'failed' || status === 'market_failed') return 'danger'
  return 'info'
}

function discoverySyncPhaseLabel(phase?: string) {
  const labels: Record<string, string> = {
    security_universe: 'SEC 数据同步（元数据、财务、Form 4、融资风险）',
    incremental_listing_discovery: '新增上市标的发现（轻量 SEC 补数）',
    market_prescreen: '行情补全与候选评分',
    technical_history: '技术历史预热',
    completed: '工作流已完成',
    failed: '工作流已中止',
  }
  return labels[phase || ''] || phase || '等待开始'
}

function discoverySyncDuration(run: DiscoverySyncRun) {
  const start = new Date(run.started_at).getTime()
  const end = run.completed_at ? new Date(run.completed_at).getTime() : discoverySyncNow.value
  if (Number.isNaN(start) || Number.isNaN(end) || end < start) return '-'
  const seconds = Math.floor((end - start) / 1000)
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const remainingSeconds = seconds % 60
  if (hours > 0) return `${hours}小时${minutes}分`
  if (minutes > 0) return `${minutes}分${remainingSeconds}秒`
  return `${remainingSeconds}秒`
}

type TechnicalHistoryChartPoint = {
  tradeDate: string
  close: number
  ma20: number | null
  ma50: number | null
  ma200: number | null
  volume: number
  source: string
  backfilled: boolean
  x: number
  priceY: number
  ma20Y: number | null
  ma50Y: number | null
  ma200Y: number | null
  volumeY: number
}

type TechnicalHistoryRange = '1w' | '1m' | '3m' | '6m' | '1y' | 'all'

type TechnicalHistoryChart = {
  points: TechnicalHistoryChartPoint[]
  pricePolyline: string
  ma20Polyline: string
  ma50Polyline: string
  ma200Polyline: string
  barWidth: number
  minClose: number
  maxClose: number
  startDate: string
  middleDate: string
  endDate: string
}

function buildTechnicalHistoryChart(rows: CandidateTechnicalHistoryRow[], selectedRange: TechnicalHistoryRange): TechnicalHistoryChart {
  const history = [...rows]
    .filter((row) => Number.isFinite(row.close_usd) && row.close_usd > 0)
    .sort((a, b) => a.trade_date.localeCompare(b.trade_date))
  if (!history.length) {
    return { points: [], pricePolyline: '', ma20Polyline: '', ma50Polyline: '', ma200Polyline: '', barWidth: 0, minClose: 0, maxClose: 0, startDate: '', middleDate: '', endDate: '' }
  }
  const closes = history.map((row) => row.close_usd)
  const ma20Values = history.map((_, index) => {
    if (index < 19) return null
    const window = closes.slice(index - 19, index + 1)
    return window.reduce((sum, close) => sum + close, 0) / window.length
  })
  const ma50Values = history.map((_, index) => {
    if (index < 49) return null
    const window = closes.slice(index - 49, index + 1)
    return window.reduce((sum, close) => sum + close, 0) / window.length
  })
  const ma200Values = history.map((_, index) => {
    if (index < 199) return null
    const window = closes.slice(index - 199, index + 1)
    return window.reduce((sum, close) => sum + close, 0) / window.length
  })
  const visible = history.map((row, index) => ({ row, index })).filter(({ row }) => filterTechnicalHistoryRange(history, selectedRange).some((displayed) => displayed.trade_date === row.trade_date))
  if (!visible.length) {
    return { points: [], pricePolyline: '', ma20Polyline: '', ma50Polyline: '', ma200Polyline: '', barWidth: 0, minClose: 0, maxClose: 0, startDate: '', middleDate: '', endDate: '' }
  }
  const chartPrices = visible.flatMap(({ index }) => [closes[index], ma20Values[index], ma50Values[index], ma200Values[index]].filter((value): value is number => value != null))
  const minClose = Math.min(...chartPrices)
  const maxClose = Math.max(...chartPrices)
  const closeRange = Math.max(maxClose - minClose, Math.max(maxClose * 0.02, 0.01))
  const maxVolume = Math.max(...visible.map(({ row }) => Math.max(row.volume || 0, 0)), 1)
  const pointCount = visible.length
  const barWidth = Math.max(2, Math.min(18, 540 / pointCount))
  const points = visible.map(({ row, index }, displayIndex) => {
    const x = pointCount === 1 ? 360 : (displayIndex / (pointCount - 1)) * 720
    const priceY = 174 - ((row.close_usd - minClose) / closeRange) * 142
    const ma20 = ma20Values[index]
    const ma20Y = ma20 == null ? null : 174 - ((ma20 - minClose) / closeRange) * 142
    const ma50 = ma50Values[index]
    const ma50Y = ma50 == null ? null : 174 - ((ma50 - minClose) / closeRange) * 142
    const ma200 = ma200Values[index]
    const ma200Y = ma200 == null ? null : 174 - ((ma200 - minClose) / closeRange) * 142
    const volumeY = 240 - (Math.max(row.volume || 0, 0) / maxVolume) * 42
    return {
      tradeDate: row.trade_date,
      close: row.close_usd,
      ma20,
      ma50,
      ma200,
      volume: row.volume,
      source: row.source,
      backfilled: row.backfilled,
      x,
      priceY,
      ma20Y,
      ma50Y,
      ma200Y,
      volumeY,
    }
  })
  const middle = visible[Math.floor((visible.length - 1) / 2)].row
  return {
    points,
    pricePolyline: points.map((point) => `${point.x},${point.priceY}`).join(' '),
    ma20Polyline: points.filter((point) => point.ma20Y != null).map((point) => `${point.x},${point.ma20Y}`).join(' '),
    ma50Polyline: points.filter((point) => point.ma50Y != null).map((point) => `${point.x},${point.ma50Y}`).join(' '),
    ma200Polyline: points.filter((point) => point.ma200Y != null).map((point) => `${point.x},${point.ma200Y}`).join(' '),
    barWidth,
    minClose,
    maxClose,
    startDate: visible[0].row.trade_date,
    middleDate: middle.trade_date,
    endDate: visible[visible.length - 1].row.trade_date,
  }
}

function filterTechnicalHistoryRange(rows: CandidateTechnicalHistoryRow[], selected: TechnicalHistoryRange) {
  const normalized = [...rows].sort((a, b) => a.trade_date.localeCompare(b.trade_date))
  if (selected === 'all' || !normalized.length) return normalized
  const latest = new Date(`${normalized[normalized.length - 1].trade_date}T12:00:00Z`)
  if (selected === '1w') latest.setUTCDate(latest.getUTCDate() - 6)
  if (selected === '1m') latest.setUTCMonth(latest.getUTCMonth() - 1)
  if (selected === '3m') latest.setUTCMonth(latest.getUTCMonth() - 3)
  if (selected === '6m') latest.setUTCMonth(latest.getUTCMonth() - 6)
  if (selected === '1y') latest.setUTCFullYear(latest.getUTCFullYear() - 1)
  const cutoff = latest.toISOString().slice(0, 10)
  return normalized.filter((row) => row.trade_date >= cutoff)
}

onMounted(() => {
	const ticker = route.query.ticker
	if (typeof ticker === 'string') filters.ticker = ticker.toUpperCase()
  load()
  loadCriteria()
  void loadAIProviders()
  discoverySyncPoll = window.setInterval(() => {
    void loadDiscoverySyncStatus()
  }, 15_000)
})

onUnmounted(() => {
  if (discoverySyncPoll) window.clearInterval(discoverySyncPoll)
  if (candidateSupplementalTimer) window.clearTimeout(candidateSupplementalTimer)
  if (candidateAIPollingTimer !== undefined) window.clearTimeout(candidateAIPollingTimer)
})
</script>

<style scoped>
.discovery-sync-status-card {
  margin-bottom: 12px;
}

.eligibility-check-alert {
  margin-bottom: 14px;
}

.eligibility-comparison-alert {
  margin: 10px 0;
}

.eligibility-check-form,
.eligibility-history-toolbar {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin: 4px 0 14px;
}

.eligibility-check-form :deep(.el-form-item) {
  margin-bottom: 0;
}

.eligibility-check-form :deep(.el-input),
.eligibility-history-toolbar :deep(.el-input) {
  width: 220px;
}

.eligibility-check-meta,
.eligibility-check-table {
  margin-top: 14px;
}

.eligibility-history-pagination {
  margin-top: 14px;
  justify-content: flex-end;
}

.discovery-sync-status-main {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px 20px;
  flex-wrap: wrap;
}

.discovery-sync-status-title,
.discovery-sync-status-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.discovery-sync-status-title > span,
.discovery-sync-status-meta {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.discovery-sync-error {
  margin-top: 10px;
}

.discovery-storage-alert {
  margin-bottom: 12px;
}

.discovery-storage-alert :deep(.el-alert__title) {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.health-alert {
  margin-bottom: 12px;
}

.candidate-page-header {
  align-items: flex-start;
}

.candidate-page-title {
  min-width: 280px;
  flex: 1 1 300px;
}

.candidate-page-actions {
  flex: 0 1 940px;
  justify-content: flex-end;
}

.criteria-card {
  margin-bottom: 12px;
}

.criteria-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.criteria-heading > div {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 8px;
}

.criteria-heading span,
.criteria-note {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.criteria-tags {
  display: flex;
}

.criteria-note {
  margin-top: 8px;
}

.criteria-state-legend {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 10px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.review-queue-card {
  margin-bottom: 12px;
}

.review-queue-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}

.review-queue-heading > div {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 8px;
}

.review-queue-heading span {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.overview-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(150px, 1fr)) minmax(220px, 1.4fr);
  gap: 12px;
  margin-bottom: 12px;
}

.overview-card :deep(.el-card__body) {
  display: flex;
  min-height: 72px;
  flex-direction: column;
  justify-content: center;
  gap: 4px;
}

.overview-card strong {
  color: var(--el-text-color-primary);
  font-size: 24px;
  line-height: 1.1;
}

.overview-card small,
.overview-label {
  color: var(--el-text-color-secondary);
}

.overview-wide strong {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.summary-alert {
  margin-bottom: 12px;
}

.summary-dialog {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.summary-message :deep(textarea) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', monospace;
  line-height: 1.5;
}

.force-resend-checkbox {
  margin-right: auto;
}

.watch-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 12px;
}

.watch-change-positive {
  color: var(--el-color-success);
  font-weight: 600;
}

.watch-change-negative {
  color: var(--el-color-danger);
  font-weight: 600;
}

.watch-change-neutral,
.watch-baseline-missing {
  color: var(--el-text-color-secondary);
}

.quick-filter-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 14px;
}

.quick-filter-label {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.table-view-label {
  margin-left: auto;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.current-list-count {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.candidate-supplemental-loading {
  color: var(--el-color-primary);
  font-size: 12px;
}

.candidate-table-compact :deep(.el-table__cell) {
  padding-top: 6px;
  padding-bottom: 6px;
}

.candidate-detail {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.candidate-ai-card { margin-top: 16px; }
.ai-analysis-content { white-space: pre-wrap; line-height: 1.65; padding: 12px; border-radius: 4px; background: var(--el-fill-color-light); }

.capital-risk-summary {
  margin-bottom: 10px;
}

.lineage-batch-meta {
  margin: 12px 0;
}

.lineage-table {
  margin-top: 12px;
}

.score-history-table {
  margin-top: 12px;
}

.analyst-rating-provenance-title {
  margin-top: 16px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  font-weight: 600;
}

.analyst-rating-provenance-table {
  margin-top: 8px;
}

.signal-event-history {
  margin-top: 16px;
}

.signal-event-heading {
  margin-bottom: 10px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  font-weight: 600;
}

.detail-summary-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

.detail-summary-title {
  margin-bottom: 8px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.research-next-step-reasons {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 10px;
}

.metric-help {
  cursor: help;
  border-bottom: 1px dotted var(--el-text-color-secondary);
}

.filter-suffix {
  margin-left: 6px;
  color: var(--el-text-color-secondary);
}

.technical-signal-row {
  margin-bottom: 12px;
}

.technical-history-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 16px 0 8px;
}

.technical-history-controls {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  flex-wrap: wrap;
}

.technical-history-title {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.technical-history-asof-alert {
  margin-bottom: 10px;
}

.history-source {
  color: var(--el-text-color-secondary);
}

.technical-chart {
  overflow: hidden;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 4px;
  background: var(--el-fill-color-blank);
}

.technical-chart-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  padding: 10px 12px 0;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.technical-chart-legend span {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.technical-chart-line-key,
.technical-chart-ma20-key,
.technical-chart-ma50-key,
.technical-chart-ma200-key,
.technical-chart-bar-key {
  display: inline-block;
  width: 14px;
  height: 3px;
  border-radius: 2px;
  background: var(--el-color-primary);
}

.technical-chart-bar-key {
  height: 8px;
  background: var(--el-color-primary-light-7);
}

.technical-chart-ma20-key {
  background: var(--el-color-warning);
}

.technical-chart-ma50-key {
  background: var(--el-color-success);
}

.technical-chart-ma200-key {
  background: var(--el-color-danger);
}

.technical-chart-svg {
  display: block;
  width: 100%;
  height: 280px;
}

.technical-chart-grid {
  stroke: var(--el-border-color-lighter);
  stroke-dasharray: 3 4;
}

.technical-chart-volume {
  fill: var(--el-color-primary-light-7);
  opacity: 0.8;
}

.technical-chart-price {
  stroke: var(--el-color-primary);
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 3;
}

.technical-chart-ma20 {
  stroke: var(--el-color-warning);
  stroke-dasharray: 6 4;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2.5;
}

.technical-chart-ma50 {
  stroke: var(--el-color-success);
  stroke-dasharray: 3 4;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2.5;
}

.technical-chart-ma200 {
  stroke: var(--el-color-danger);
  stroke-dasharray: 2 4;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2.5;
}

.technical-chart-point {
  fill: var(--el-color-primary);
  stroke: var(--el-fill-color-blank);
  stroke-width: 1.5;
}

.technical-chart-axis-label {
  fill: var(--el-text-color-secondary);
  font-size: 15px;
}

.technical-chart-note {
  padding: 0 12px 10px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.research-portfolio-summary {
  margin: 12px 0;
}

.research-portfolio-toolbar {
  display: flex;
  justify-content: flex-end;
  margin: 12px 0;
}

.portfolio-sector {
  display: inline-block;
  margin-right: 8px;
}

.research-version-history {
  margin-top: 12px;
}

.risk-tag {
  cursor: help;
}

.metric-tooltip {
  max-width: 520px;
  line-height: 1.6;
}

.score-tooltip {
  min-width: 250px;
  line-height: 1.6;
}

.score-tooltip-title {
  margin-bottom: 6px;
  color: var(--el-text-color-secondary);
}

.score-reason,
.score-tooltip-total {
  display: flex;
  justify-content: space-between;
  gap: 20px;
}

.score-tooltip-total {
  margin-top: 5px;
  padding-top: 5px;
  border-top: 1px solid rgba(255, 255, 255, 0.2);
}

.score-tooltip-note {
  margin-top: 6px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.trade-setup-history {
  margin-top: 18px;
  padding-top: 14px;
  border-top: 1px solid var(--el-border-color-lighter);
}

.trade-setup-timeline {
  margin: 14px 0 0;
}

.trade-setup-event-detail {
  margin-top: 5px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

@media (max-width: 1200px) {
  .overview-grid {
    grid-template-columns: repeat(2, minmax(150px, 1fr));
  }
}

@media (max-width: 1400px) {
  .candidate-page-header {
    flex-wrap: wrap;
  }

  .candidate-page-title,
  .candidate-page-actions {
    flex-basis: 100%;
  }

  .candidate-page-actions {
    justify-content: flex-start;
  }
}

@media (max-width: 720px) {
  .overview-grid,
  .detail-summary-grid {
    grid-template-columns: 1fr;
  }

  .candidate-page-title {
    min-width: 0;
  }

  .table-view-label {
    margin-left: 0;
  }
}
</style>
