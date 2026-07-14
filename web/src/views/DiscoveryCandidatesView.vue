<template>
  <div>
    <div class="page-header">
      <div>
        <h1>小盘股候选</h1>
        <p>基于公开 SEC 文件、财务指标、内幕交易和融资风险生成的研究候选列表。</p>
      </div>
      <el-space>
        <el-button :loading="workflowLoading" type="primary" plain @click="runWorkflow">刷新候选工作流</el-button>
        <el-button :loading="technicalHistoryLoading" type="warning" plain @click="backfillTechnicalHistory">回填技术历史</el-button>
        <el-button :loading="watchLoading" @click="openWatchList">关注列表</el-button>
        <el-button :loading="effectivenessLoading" @click="openEffectiveness">效果评估</el-button>
        <el-button @click="exportCandidates">导出 CSV</el-button>
        <el-button @click="sectorDialogVisible = true">赛道分布</el-button>
        <el-button :loading="reportLoading" @click="openReport">查看日报</el-button>
        <el-button :loading="summaryLoading" @click="previewSummary">预检通知摘要</el-button>
        <el-button :loading="loading" @click="load">刷新</el-button>
      </el-space>
    </div>

    <el-alert
      v-if="health"
      :type="health.status === 'ok' ? 'success' : health.status === 'missing' ? 'warning' : 'error'"
      :closable="false"
      show-icon
      class="health-alert"
      :title="`数据健康：${healthStatusLabel(health.status)}｜候选 ${health.total_candidates}｜财务指标不可用 ${health.missing_financials}｜内幕来源 ${healthInsiderDataLabel(health.insider_data_status)}｜内幕记录 ${health.candidates_with_insider_records}/${health.total_candidates}｜合格买入 ${health.qualified_insider_candidates}｜价格当日 ${health.current_price_candidates}｜前一交易日 ${health.fallback_price_candidates}｜缺/过期 ${health.missing_price_candidates + health.stale_price_candidates}｜缺市值 ${health.missing_market_cap}｜活跃风险 ${health.active_risk_events}`"
      :description="health.issues.length ? health.issues.map(formatHealthIssue).join('；') : '当前候选证据链完整度正常。'"
    />

    <div v-if="overview" class="overview-grid">
      <el-card shadow="never" class="overview-card">
        <span class="overview-label">当前候选</span>
        <strong>{{ overview.total }}</strong>
        <small>A {{ overview.grade_counts?.A || 0 }} / B {{ overview.grade_counts?.B || 0 }}</small>
      </el-card>
      <el-card shadow="never" class="overview-card">
        <span class="overview-label">强B候选</span>
        <strong>{{ overview.quality_tier_counts?.strong_b || 0 }}</strong>
        <small>观察B {{ overview.quality_tier_counts?.watch_b || 0 }}</small>
      </el-card>
      <el-card shadow="never" class="overview-card">
        <span class="overview-label">变化</span>
        <strong>{{ overview.change_counts?.new || 0 }}</strong>
        <small>改善 {{ overview.change_counts?.improved || 0 }} / 退出 {{ overview.change_counts?.exited || 0 }}</small>
      </el-card>
      <el-card shadow="never" class="overview-card">
        <span class="overview-label">数据提示</span>
        <strong>{{ overview.quality_tag_counts?.low_revenue_base || 0 }}</strong>
        <small>低收入基数</small>
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
        <el-button :type="quickFilterActive('high_priority') ? 'primary' : 'default'" plain @click="toggleQuickFilter('high_priority')">
          高优先级
        </el-button>
        <el-button :type="filters.recommended_only ? 'primary' : 'default'" plain @click="toggleRecommendedOnly">
          主推荐
        </el-button>
        <el-button :type="quickFilterActive('strong_b') ? 'primary' : 'default'" plain @click="toggleQuickFilter('strong_b')">
          强B
        </el-button>
        <el-button :type="quickFilterActive('improved') ? 'primary' : 'default'" plain @click="toggleQuickFilter('improved')">
          改善
        </el-button>
        <el-button :type="quickFilterActive('exclude_low_liquidity') ? 'primary' : 'default'" plain @click="toggleQuickFilter('exclude_low_liquidity')">
          排除低流动性
        </el-button>
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
        <el-form-item label="赛道分类">
          <el-select v-model="filters.sector_category" clearable filterable style="width: 180px">
            <el-option v-for="category in sectorCategoryOptions" :key="category" :label="category" :value="category" />
          </el-select>
        </el-form-item>
        <el-form-item label="技术信号">
          <el-select v-model="filters.technical_signal" clearable style="width: 170px">
            <el-option label="上穿 20 日均线" value="cross_above_ma20" />
            <el-option label="突破 20 日最高收盘价" value="breakout_20d_high" />
            <el-option label="放量突破" value="volume_backed_breakout" />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="search">查询</el-button>
          <el-button @click="reset">重置</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-table :data="rows" v-loading="loading" border empty-text="暂无候选" @sort-change="onSortChange">
      <el-table-column prop="grade" label="等级" width="90" align="center">
        <template #default="{ row }">
          <el-tag :type="gradeTagType(row.grade)" effect="dark">{{ gradeLabel(row.grade) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="ticker" label="Ticker" width="110" sortable="custom" />
      <el-table-column prop="total_score" label="总分" width="90" align="right" sortable="custom" />
      <el-table-column prop="quality_adjusted_score" label="调整分" width="90" align="right">
        <template #default="{ row }">
          <el-tooltip v-if="row.quality_adjusted_score !== row.total_score" content="已按低基数、极端增长、低流动性或融资风险进行上限保护" placement="top">
            <span class="metric-help">{{ row.quality_adjusted_score ?? row.total_score }}</span>
          </el-tooltip>
          <span v-else>{{ row.quality_adjusted_score ?? row.total_score }}</span>
        </template>
      </el-table-column>
      <el-table-column prop="review_priority_score" label="优先级" width="100" align="right" sortable="custom">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="priority-tooltip">
                <div class="priority-tooltip-title">优先级（0–100）= 质量、变化、流动性和风险的复核排序分</div>
                <div v-for="reason in row.review_priority_reasons || []" :key="`${reason.label}-${reason.points}`" class="priority-reason">
                  <span>{{ reason.label }}</span>
                  <strong :class="reason.points >= 0 ? 'positive' : 'negative'">{{ formatSignedNumber(reason.points) }}</strong>
                </div>
                <div v-if="!(row.review_priority_reasons || []).length">暂无明细</div>
              </div>
            </template>
            <span class="metric-help">{{ row.review_priority_score ?? '-' }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="quality_tier" label="质量" width="110">
        <template #default="{ row }">
          <el-tag :type="qualityTierTagType(row.quality_tier)" effect="plain">{{ qualityTierLabel(row.quality_tier) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="change_status" label="变化" width="100">
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
        <template #default="{ row }">{{ formatUSD(row.market_cap_usd) }}</template>
      </el-table-column>
      <el-table-column prop="price_close_usd" label="价格" width="100" align="right" sortable="custom">
        <template #default="{ row }">{{ formatPrice(row.price_close_usd, row.price_currency) }}</template>
      </el-table-column>
      <el-table-column prop="price_volume" label="成交量" width="120" align="right" sortable="custom">
        <template #default="{ row }">{{ formatVolume(row.price_volume) }}</template>
      </el-table-column>
      <el-table-column label="市场质量" width="120" align="right">
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
      <el-table-column label="技术信号" min-width="170">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <div class="metric-tooltip">
                <template v-if="row.technical?.status === 'ready'">
                  <div>收盘价：{{ formatPrice(row.technical.close_usd, 'USD') }}</div>
                  <div>MA20：{{ formatPrice(row.technical.ma20_usd, 'USD') }}（{{ formatPerformance(row.technical.distance_to_ma20_pct) }}）</div>
                  <div>前 20 日最高收盘价：{{ formatPrice(row.technical.prior_20d_high_usd, 'USD') }}（{{ formatPerformance(row.technical.distance_to_20d_high_pct) }}）</div>
                  <div>量比：{{ formatRatio(row.technical.volume_ratio_20) }}（相对 20 日均量）</div>
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
      <el-table-column prop="quarterly_revenue_qoq_pct" label="季度环比" width="110" align="right" sortable="custom">
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
      <el-table-column prop="annual_revenue_yoy_pct" label="年度同比" width="110" align="right" sortable="custom">
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
      <el-table-column prop="annual_revenue_qoq_pct" label="年度环比" width="110" align="right" sortable="custom">
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
        <template #default="{ row }">{{ formatMonths(row.cash_runway_months) }}</template>
      </el-table-column>
      <el-table-column label="表现" width="140" align="right">
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
      <el-table-column label="核心信号" min-width="220">
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
      <el-table-column label="分项" min-width="260">
        <template #default="{ row }">
          增长 {{ row.revenue_growth_score }} / 现金 {{ row.cash_runway_score }} / 内幕 {{ row.insider_score }} / 稀释 {{ row.dilution_risk_score }}
        </template>
      </el-table-column>
      <el-table-column prop="reason_code" label="原因" min-width="140" />
      <el-table-column label="操作" width="180" fixed="right">
        <template #default="{ row }">
          <el-space>
            <el-button link type="primary" :loading="detailLoadingTicker === row.ticker" @click="openDetail(row)">详情</el-button>
            <el-button link type="primary" :loading="watchingTicker === row.ticker" @click="addToCandidateWatches(row)">关注</el-button>
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
          <el-descriptions-item v-if="notificationPreview" label="最小优先级">{{ notificationPreview.settings.min_review_priority_score || '-' }}</el-descriptions-item>
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
        <el-descriptions :column="2" border>
          <el-descriptions-item label="Ticker">{{ candidateDetail.score.ticker }}</el-descriptions-item>
          <el-descriptions-item label="公司">{{ candidateDetail.security.company_name }}</el-descriptions-item>
          <el-descriptions-item label="等级">{{ gradeLabel(candidateDetail.score.grade) }}</el-descriptions-item>
          <el-descriptions-item label="总分">{{ candidateDetail.score.total_score }}</el-descriptions-item>
          <el-descriptions-item label="市值">{{ formatUSD(candidateDetail.score.market_cap_usd) }}</el-descriptions-item>
          <el-descriptions-item label="SIC">{{ candidateDetail.security.sic || '-' }}</el-descriptions-item>
        </el-descriptions>

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
          <template #header>赛道解释</template>
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="分类">{{ candidateDetail.sector.category }}</el-descriptions-item>
            <el-descriptions-item label="标签">{{ candidateDetail.sector.label }}</el-descriptions-item>
            <el-descriptions-item label="SIC">{{ candidateDetail.sector.sic || '-' }}</el-descriptions-item>
            <el-descriptions-item label="赛道分">{{ candidateDetail.sector.score }}/10</el-descriptions-item>
            <el-descriptions-item label="说明" :span="2">{{ candidateDetail.sector.rationale }}</el-descriptions-item>
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
          <template #header>财务证据</template>
          <el-descriptions v-if="candidateDetail.financial" :column="2" border size="small">
            <el-descriptions-item label="季度收入 YoY">{{ formatPct(candidateDetail.financial.quarterly_revenue_yoy_pct) }}</el-descriptions-item>
            <el-descriptions-item label="季度收入 QoQ">{{ formatPct(candidateDetail.financial.quarterly_revenue_qoq_pct) }}</el-descriptions-item>
            <el-descriptions-item label="年度收入 YoY">{{ formatPct(candidateDetail.financial.annual_revenue_yoy_pct) }}</el-descriptions-item>
            <el-descriptions-item label="年度收入环比">{{ formatPct(candidateDetail.financial.annual_revenue_qoq_pct) }}</el-descriptions-item>
            <el-descriptions-item label="现金 Runway">{{ formatMonths(candidateDetail.financial.cash_runway_months) }}</el-descriptions-item>
            <el-descriptions-item label="毛利率">{{ candidateDetail.financial.gross_margin_available ? formatPct(candidateDetail.financial.gross_margin_pct) : '-' }}</el-descriptions-item>
            <el-descriptions-item label="质量标记">{{ candidateDetail.financial.quality_flags_json || '-' }}</el-descriptions-item>
          </el-descriptions>
          <el-empty v-else description="暂无财务证据" />
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
                <el-tag v-for="signal in candidateDetail.technical.signals" :key="signal.kind" type="success" effect="plain">{{ signal.label }}</el-tag>
                <el-tag v-if="!candidateDetail.technical.signals.length" type="info" effect="plain">暂无突破信号</el-tag>
              </el-space>
            </div>
            <el-descriptions :column="2" border size="small">
              <el-descriptions-item label="价格日期">{{ formatDate(candidateDetail.technical.trade_date) }}</el-descriptions-item>
              <el-descriptions-item label="有效样本">{{ candidateDetail.technical.sample_days }}/{{ candidateDetail.technical.required_sample_days }} 个交易日</el-descriptions-item>
              <el-descriptions-item label="收盘价">{{ formatPrice(candidateDetail.technical.close_usd, 'USD') }}</el-descriptions-item>
              <el-descriptions-item label="20 日均线">{{ formatPrice(candidateDetail.technical.ma20_usd, 'USD') }}（{{ formatPerformance(candidateDetail.technical.distance_to_ma20_pct) }}）</el-descriptions-item>
              <el-descriptions-item label="前 20 日最高收盘价">{{ formatPrice(candidateDetail.technical.prior_20d_high_usd, 'USD') }}（{{ formatPerformance(candidateDetail.technical.distance_to_20d_high_pct) }}）</el-descriptions-item>
              <el-descriptions-item label="量比">{{ formatRatio(candidateDetail.technical.volume_ratio_20) }}（20 日均量 {{ formatVolume(candidateDetail.technical.average_volume_20) }}）</el-descriptions-item>
            </el-descriptions>
          </template>
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
          <el-table :data="candidateDetail.capital_risks" size="small" border empty-text="暂无融资风险">
            <el-table-column prop="kind" label="类型" width="140" />
            <el-table-column prop="severity" label="严重度" width="90" />
            <el-table-column prop="reason" label="原因" min-width="220" show-overflow-tooltip />
            <el-table-column label="阻断" width="120"><template #default="{ row }">A: {{ row.blocks_a ? '是' : '否' }} / B: {{ row.blocks_b ? '是' : '否' }}</template></el-table-column>
          </el-table>
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

    <el-dialog v-model="watchDialogVisible" title="小盘候选关注列表" width="1080px">
      <div class="watch-toolbar">
        <el-switch v-model="showArchivedWatches" active-text="显示归档" @change="loadCandidateWatches" />
      </div>
      <el-table :data="watchRows" v-loading="watchLoading" border empty-text="暂无关注候选">
        <el-table-column prop="ticker" label="Ticker" width="110" />
        <el-table-column prop="company_name" label="公司" min-width="180" show-overflow-tooltip />
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
        <el-table-column label="优先级" width="90" align="right">
          <template #default="{ row }">{{ row.latest_score?.review_priority_score ?? '-' }}</template>
        </el-table-column>
        <el-table-column label="表现" width="100" align="right">
          <template #default="{ row }">{{ formatPerformance(row.latest_score?.performance?.return_5d ?? row.latest_score?.performance?.return_1d) }}</template>
        </el-table-column>
        <el-table-column prop="note" label="备注" min-width="140" show-overflow-tooltip />
        <el-table-column prop="research_status" label="研究状态" width="110">
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
        <el-form-item label="研究状态">
          <el-select v-model="watchEditor.research_status" style="width: 180px">
            <el-option label="待研究" value="inbox" />
            <el-option label="研究中" value="researching" />
            <el-option label="重点关注" value="conviction" />
            <el-option label="淘汰" value="rejected" />
          </el-select>
        </el-form-item>
        <el-form-item label="研究备注"><el-input v-model="watchEditor.note" type="textarea" :rows="2" /></el-form-item>
        <el-form-item label="研究论点"><el-input v-model="watchEditor.thesis" type="textarea" :rows="3" /></el-form-item>
        <el-form-item label="主要风险"><el-input v-model="watchEditor.risk_notes" type="textarea" :rows="2" /></el-form-item>
        <el-form-item label="失效条件"><el-input v-model="watchEditor.invalidation" type="textarea" :rows="2" /></el-form-item>
        <el-form-item label="下次复查"><el-date-picker v-model="watchEditor.next_review_at" type="date" value-format="YYYY-MM-DD" clearable /></el-form-item>
      </el-form>
      <template #footer><el-button @click="watchEditorVisible = false">取消</el-button><el-button type="primary" :loading="watchSaving" @click="saveCandidateResearch">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="effectivenessVisible" title="候选效果评估" width="920px">
      <el-alert :type="effectiveness?.benchmark_available ? 'info' : 'warning'" :closable="false" show-icon class="summary-alert"
        :title="effectiveness?.benchmark_available ? `收益相对 ${effectiveness.benchmark_ticker} 计算；只统计首次进入 A/B 后已有足够交易日的样本。` : `未找到 ${effectiveness?.benchmark_ticker || 'IWM'} 本地价格历史；当前仅展示候选自身表现。`" />
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
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient } from '@/api/client'
import type {
  ApiResponse,
  CandidateDetail,
  CandidateEffectivenessCohort,
  CandidateEffectivenessReport,
  CandidateHealth,
  CandidateNotificationPreview,
  CandidateNotificationSendInput,
  CandidateNotificationSendResult,
  CandidateOverview,
  CandidateReport,
  CandidateScore,
  CandidateSummary,
  CandidateWatch,
  DiscoveryWorkflowResult,
  PageResult,
  TechnicalHistoryBackfillResult,
} from '@/api/types'

const rows = ref<CandidateScore[]>([])
const overview = ref<CandidateOverview | null>(null)
const loading = ref(false)
const workflowLoading = ref(false)
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
const watchEditor = reactive({ ticker: '', note: '', research_status: 'inbox', thesis: '', risk_notes: '', invalidation: '', next_review_at: '' })
const showArchivedWatches = ref(false)
const effectivenessLoading = ref(false)
const effectivenessVisible = ref(false)
const effectiveness = ref<CandidateEffectivenessReport | null>(null)
const sectorDialogVisible = ref(false)
const candidateDetail = ref<CandidateDetail | null>(null)
const health = ref<CandidateHealth | null>(null)
const report = ref<CandidateReport | null>(null)
const reportVisible = ref(false)
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
  recommended_only: true,
  min_review_priority_score: 0,
  exclude_quality_tags: [] as string[],
})
const sortState = reactive({ sort_by: '', sort_order: '' })
const topSectorLabel = computed(() => {
  const entries = Object.entries(overview.value?.sector_counts || {}).sort((a, b) => b[1] - a[1])
  return entries[0]?.[0] || '-'
})
const topSectorCount = computed(() => {
  const entries = Object.entries(overview.value?.sector_counts || {}).sort((a, b) => b[1] - a[1])
  return entries[0]?.[1] || 0
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
  if (filters.recommended_only) params.recommended_only = 'true'
  if (filters.min_review_priority_score) params.min_review_priority_score = filters.min_review_priority_score
  if (filters.exclude_quality_tags.length) params.exclude_quality_tag = filters.exclude_quality_tags.join(',')
  if (sortState.sort_by) params.sort_by = sortState.sort_by
  if (sortState.sort_order) params.sort_order = sortState.sort_order
  return params
}

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<CandidateScore>>>('/discovery/candidates', { params: requestParams() })
    rows.value = res.data.data.items || []
    total.value = res.data.data.total || 0
    await Promise.all([loadHealth(), loadOverview()])
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载候选失败')
  } finally {
    loading.value = false
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

async function runWorkflow() {
  workflowLoading.value = true
  try {
    const res = await apiClient.post<ApiResponse<DiscoveryWorkflowResult>>('/discovery/candidates/refresh')
    health.value = res.data.data.health
    summary.value = res.data.data.summary
    await load()
    if (res.data.data.status === 'published') {
      ElMessage.success('小盘候选真实同步已完成')
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

async function backfillTechnicalHistory() {
  try {
    await ElMessageBox.confirm(
      '将仅对当前 A/B 小盘候选补齐近 35 个自然日的日线历史。任务会遵守已配置的行情源请求预算，可能需要数分钟；不会修改基本面评分或发送通知。',
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
      { lookback_days: 35 },
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
  try {
    const res = await apiClient.get<ApiResponse<CandidateDetail>>(`/discovery/candidates/${row.ticker}/detail`)
    candidateDetail.value = res.data.data
    detailVisible.value = true
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载候选详情失败')
  } finally {
    detailLoadingTicker.value = ''
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

async function editCandidateWatch(row: CandidateWatch) {
  watchEditor.ticker = row.ticker
  watchEditor.note = row.note || ''
  watchEditor.research_status = row.research_status || 'inbox'
  watchEditor.thesis = row.thesis || ''
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
  await loadCandidateWatches()
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
      risk_notes: watchEditor.risk_notes,
      invalidation: watchEditor.invalidation,
      next_review_at: watchEditor.next_review_at ? `${watchEditor.next_review_at}T00:00:00Z` : undefined,
	      clear_next_review_at: !watchEditor.next_review_at,
    })
    ElMessage.success('研究记录已保存')
    watchEditorVisible.value = false
    await loadCandidateWatches()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '保存研究记录失败')
  } finally {
    watchSaving.value = false
  }
}

async function deleteCandidateWatch(id: number) {
  await apiClient.delete(`/discovery/candidate-watches/${id}`)
  ElMessage.success('已取消关注')
  await loadCandidateWatches()
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
  filters.recommended_only = true
  filters.min_review_priority_score = 0
  filters.exclude_quality_tags = []
  search()
}

function quickFilterActive(kind: string) {
  if (kind === 'high_priority') return filters.min_review_priority_score > 0
  if (kind === 'strong_b') return filters.quality_tier === 'strong_b'
  if (kind === 'improved') return filters.change_status === 'improved'
  if (kind === 'exclude_low_liquidity') return filters.exclude_quality_tags.includes('low_liquidity')
  return false
}

function toggleQuickFilter(kind: string) {
  if (kind === 'high_priority') {
    filters.min_review_priority_score = filters.min_review_priority_score > 0 ? 0 : 70
  } else if (kind === 'strong_b') {
    filters.quality_tier = filters.quality_tier === 'strong_b' ? '' : 'strong_b'
  } else if (kind === 'improved') {
    filters.change_status = filters.change_status === 'improved' ? '' : 'improved'
  } else if (kind === 'exclude_low_liquidity') {
    filters.exclude_quality_tags = filters.exclude_quality_tags.includes('low_liquidity') ? [] : ['low_liquidity']
  }
  search()
}

function toggleRecommendedOnly() {
  filters.recommended_only = !filters.recommended_only
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

function priceFreshnessTooltip(row: CandidateScore) {
  if (row.price_freshness_status === 'current') return '与本批次有效交易日一致'
  if (row.price_freshness_status === 'previous_trading_day') return `回退至前一交易日（相差 ${row.price_age_calendar_days ?? '-'} 个自然日）`
  if (row.price_freshness_status === 'stale') return `价格已过期（相差 ${row.price_age_calendar_days ?? '-'} 个自然日）`
  if (row.price_freshness_status === 'future') return '价格日期晚于本批次有效交易日，需要复核'
  if (row.price_freshness_status === 'missing') return '未取得该标的价格'
  return '价格日期无法与本批次有效交易日比较'
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

function formatHealthIssue(issue: string) {
  const [code, count] = issue.split(':')
  if (code === 'missing_financials') return `财务指标不可用：${count || 0}`
  if (code === 'missing_insider_data' || code === 'missing_insiders') return `内幕来源缺失：${count || 0}`
  if (code === 'missing_market_cap') return `缺市值：${count || 0}`
  if (code === 'price_previous_trading_day') return `使用前一交易日价格：${count || 0}`
  if (code === 'stale_prices') return `价格已过期：${count || 0}`
  if (code === 'missing_prices') return `价格缺失：${count || 0}`
  if (code === 'candidate_insider_records') return `候选内幕记录覆盖：${count || 0}`
  if (code === 'no_current_published_prescreen_batch') return '暂无已发布的小盘候选批次'
  return issue
}

function healthInsiderDataLabel(status?: string) {
  if (status === 'available') return '已同步'
  if (status === 'missing') return '缺失'
  return status || '-'
}

function formatUSD(value: number) {
	if (!value) return '-'
	if (value >= 1_000_000_000) return `$${(value / 1_000_000_000).toFixed(2)}B`
	return `$${(value / 1_000_000).toFixed(1)}M`
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

function formatSignedNumber(value: number) {
  return `${value >= 0 ? '+' : ''}${value}`
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
    `质量标记：${info.quality_flags_json || '-'}`,
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
  if (detail.financial?.quality_flags_json?.includes('extreme_revenue_growth')) risks.push('增长异常需核验')
  if (detail.capital_risks?.length) risks.push(`融资/稀释事件 ${detail.capital_risks.length} 条`)
  return risks.length ? risks : ['暂无明显风险']
}

function formatMonths(value: number) {
  if (Number(value) >= 999) return '经营现金流为正'
  return Number.isFinite(value) && value > 0 ? `${value.toFixed(1)} 月` : '-'
}

function formatDate(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString()
}

onMounted(load)
</script>

<style scoped>
.health-alert {
  margin-bottom: 12px;
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

.candidate-detail {
  display: flex;
  flex-direction: column;
  gap: 12px;
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

.metric-help {
  cursor: help;
  border-bottom: 1px dotted var(--el-text-color-secondary);
}

.technical-signal-row {
  margin-bottom: 12px;
}

.risk-tag {
  cursor: help;
}

.metric-tooltip {
  max-width: 520px;
  line-height: 1.6;
}

.priority-tooltip {
  min-width: 260px;
  max-width: 360px;
  line-height: 1.6;
}

.priority-tooltip-title {
  margin-bottom: 6px;
  color: var(--el-text-color-secondary);
}

.priority-reason {
  display: flex;
  justify-content: space-between;
  gap: 20px;
}

.priority-reason .positive {
  color: var(--el-color-success-light-3);
}

.priority-reason .negative {
  color: var(--el-color-danger-light-3);
}

@media (max-width: 1200px) {
  .overview-grid {
    grid-template-columns: repeat(2, minmax(150px, 1fr));
  }
}
</style>
