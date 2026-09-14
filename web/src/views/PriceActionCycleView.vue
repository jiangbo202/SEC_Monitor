<template>
  <section class="cycle-page">
    <div class="page-header cycle-header">
      <div>
        <h1>价格周期</h1>
        <p>集中观察当前小盘候选和已启用监控标的的价格行为阶段；结果来自本地日线，不改变基本面评分。</p>
      </div>
      <el-button :loading="loading" @click="load">刷新看台</el-button>
    </div>

    <el-alert type="info" :closable="false" show-icon class="cycle-note" title="阶段是价格、成交量和均线结构的研究分类，不代表固定时间周期或自动交易指令。阶段未确认是正常结果。" />

    <el-tabs v-model="activeTab" class="cycle-tabs">
      <el-tab-pane label="阶段看台" name="overview">
    <div class="cycle-summary">
      <button v-for="item in phaseSummary" :key="item.phase" class="cycle-summary-card" :class="[`is-${item.phase}`, { active: filters.phase === item.phase }]" @click="togglePhase(item.phase)">
        <span>{{ phaseLabel(item.phase) }}</span>
        <strong>{{ item.count }}</strong>
        <small>{{ phaseHint(item.phase) }}</small>
      </button>
    </div>

    <el-card shadow="never" class="cycle-path-card">
      <div class="cycle-path">
        <template v-for="(phase, index) in primaryPhases" :key="phase">
          <button :class="{ active: filters.phase === phase }" @click="togglePhase(phase)">
            <span>{{ phaseLabel(phase) }}</span><strong>{{ countFor(phase) }}</strong>
          </button>
          <span v-if="index < primaryPhases.length - 1" class="cycle-arrow">→</span>
        </template>
      </div>
      <small>状态允许跳转或回退；系统不会强制每只股票依次经过全部阶段。</small>
    </el-card>

    <el-card shadow="never" class="cycle-toolbar">
      <el-form inline size="small" @submit.prevent="">
        <el-form-item label="Ticker"><el-input v-model="filters.ticker" clearable style="width:140px" /></el-form-item>
        <el-form-item label="来源"><el-select v-model="filters.source" clearable placeholder="全部来源" style="width:150px"><el-option label="小盘候选" value="candidate" /><el-option label="监控标的" value="watch" /></el-select></el-form-item>
        <el-form-item label="阶段"><el-select v-model="filters.phase" clearable placeholder="全部阶段" style="width:160px"><el-option v-for="phase in allPhases" :key="phase" :label="phaseLabel(phase)" :value="phase" /></el-select></el-form-item>
        <el-form-item label="置信度"><el-select v-model="filters.confidence" clearable placeholder="全部" style="width:120px"><el-option label="≥ 80%" :value="80" /><el-option label="≥ 60%" :value="60" /><el-option label="≥ 40%" :value="40" /></el-select></el-form-item>
        <el-button @click="reset">重置</el-button>
      </el-form>
      <span>显示 {{ filteredRows.length }} / {{ rows.length }} 个标的 · 规则 {{ ruleVersion }}</span>
    </el-card>

    <el-alert v-if="staleCount || missingCount" type="warning" :closable="false" show-icon class="cycle-note">
      <template #title>日线完整性门控：{{ staleCount }} 个结论落后于 IWM {{ health?.iwm_latest_trade_date || '-' }}，{{ missingCount }} 个完整 OHLC 样本不足；这些结果仅供查看，不应进入当日研究优先级。</template>
    </el-alert>

    <el-table :data="filteredRows" v-loading="loading" border size="small" max-height="640" class="cycle-table" empty-text="当前范围暂无可展示的价格阶段">
      <el-table-column label="标的" width="205">
        <template #default="{ row }"><div class="cycle-identity"><el-link type="primary" @click="openRow(row)">{{ row.ticker }}</el-link><span :title="row.company_name">{{ row.company_name || '-' }}</span></div></template>
      </el-table-column>
      <el-table-column label="来源" width="140"><template #default="{ row }"><el-space size="small"><el-tag v-if="row.candidate" size="small" effect="plain">小盘候选</el-tag><el-tag v-if="row.watch" size="small" type="success" effect="plain">监控标的</el-tag></el-space></template></el-table-column>
      <el-table-column label="当前阶段" width="130"><template #default="{ row }"><el-tag :type="phaseTagType(row.phase.phase)" effect="plain">{{ phaseLabel(row.phase.phase) }}</el-tag></template></el-table-column>
      <el-table-column label="置信度" width="90" align="right"><template #default="{ row }">{{ row.phase.status === 'ready' ? `${row.phase.confidence}%` : '-' }}</template></el-table-column>
      <el-table-column label="持续" width="86" align="right"><template #default="{ row }">{{ row.phase.duration_trading_days ? `${row.phase.duration_trading_days}日` : '-' }}</template></el-table-column>
      <el-table-column label="相对 IWM" width="108" align="right"><template #default="{ row }"><span :class="signedClass(row.technical.relative_strength?.excess_return_20d_pct)">{{ signedPct(row.technical.relative_strength?.excess_return_20d_pct) }}</span></template></el-table-column>
      <el-table-column label="主要证据" min-width="260" show-overflow-tooltip><template #default="{ row }">{{ row.phase.evidence?.slice(0, 2).join('；') || '尚无完整阶段证据' }}</template></el-table-column>
      <el-table-column label="下一确认" min-width="220" show-overflow-tooltip><template #default="{ row }">{{ row.phase.next_confirmation || '-' }}</template></el-table-column>
      <el-table-column label="数据日" width="138"><template #default="{ row }"><span>{{ row.phase.trade_date || '-' }}</span><el-tag v-if="row.phase.status !== 'ready' || row.phase.freshness_status !== 'current'" size="small" type="warning" effect="plain" :title="row.phase.freshness_detail">{{ row.phase.status !== 'ready' ? '样本不足' : row.phase.freshness_status === 'stale' ? '滞后' : '缺失' }}</el-tag></template></el-table-column>
      <el-table-column label="操作" width="126" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openTimeline(row)">回溯</el-button><el-button link @click="openRow(row)">详情</el-button></template></el-table-column>
    </el-table>

    <el-card shadow="never" class="cycle-transitions">
      <template #header><div class="transition-header"><strong>最近阶段切换</strong><span>只记录日线收盘后的已确认变化，首次建立基线不作为切换。</span></div></template>
      <el-timeline v-if="transitions.length">
        <el-timeline-item v-for="item in transitions" :key="item.id" :timestamp="`${item.trade_date} · ${item.ticker}`" placement="top">
          <div class="transition-row">
            <el-link type="primary" @click="openTransition(item)">{{ item.ticker }}</el-link>
            <el-tag size="small" effect="plain" type="info">{{ phaseLabel(item.previous_phase) }}</el-tag>
            <span>→</span>
            <el-tag size="small" effect="plain" :type="phaseTagType(item.phase)">{{ phaseLabel(item.phase) }}</el-tag>
            <strong>{{ item.confidence }}%</strong>
            <span>{{ item.evidence?.slice(0, 2).join('；') || '阶段证据已保存' }}</span>
          </div>
        </el-timeline-item>
      </el-timeline>
      <el-empty v-else :image-size="46" description="暂无已确认的阶段切换；系统会从下一次阶段变化开始记录" />
    </el-card>
      </el-tab-pane>

      <el-tab-pane label="标的回溯" name="timeline">
        <el-card shadow="never" class="timeline-toolbar">
          <el-form inline size="small" @submit.prevent="loadTimeline">
            <el-form-item label="标的">
              <el-select v-model="timelineFilters.ticker" filterable placeholder="选择标的" style="width:220px" @change="handleTimelineTickerChange">
                <el-option v-for="row in rows" :key="row.ticker" :label="`${row.ticker} · ${row.company_name || '-'}`" :value="row.ticker" />
              </el-select>
            </el-form-item>
            <el-form-item label="规则版本">
              <el-select v-model="timelineFilters.ruleVersion" placeholder="当前正式规则" style="width:250px" @change="loadTimeline">
                <el-option v-for="version in timeline?.available_rule_versions || []" :key="version" :label="ruleVersionLabel(version)" :value="version" />
              </el-select>
            </el-form-item>
            <el-form-item label="日期">
              <el-date-picker v-model="timelineFilters.dates" type="daterange" value-format="YYYY-MM-DD" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" style="width:260px" @change="loadTimeline" />
            </el-form-item>
            <el-button type="primary" :loading="timelineLoading" @click="loadTimeline">查询</el-button>
            <el-button @click="resetTimelineRange">全部历史</el-button>
          </el-form>
          <span v-if="timeline">{{ timeline.items.length }} 个交易日 · {{ timelineEvents.length }} 次阶段切换</span>
        </el-card>

        <el-alert v-if="timeline && !technicalHistory.length" type="warning" :closable="false" show-icon title="阶段快照存在，但该标的缺少可匹配的本地 OHLC 日线；暂时无法绘制蜡烛图。" />
        <el-card shadow="never" class="timeline-visual" v-loading="timelineLoading">
          <template #header><div class="transition-header"><strong>{{ timelineFilters.ticker || '请选择标的' }} · 价格行为时间线</strong><span>阶段背景、切换点和指标均为当日收盘后保存的历史快照，不使用未来数据。</span></div></template>
          <PriceActionTimelineChart v-if="timeline" :ticker="timeline.ticker" :timeline="timeline.items" :history="technicalHistory" :selected-date="timelineSelected?.trade_date" @select="timelineSelected=$event" />
          <el-empty v-else :image-size="64" description="请选择当前候选或监控标的查看历史" />
        </el-card>

        <div v-if="timelineSelected" class="timeline-detail-grid">
          <el-card shadow="never">
            <template #header><div class="timeline-selected-title"><strong>{{ timelineSelected.trade_date }}</strong><el-tag :type="phaseTagType(timelineSelected.phase)" effect="plain">{{ phaseLabel(timelineSelected.phase) }}</el-tag><span>{{ timelineSelected.confidence }}%</span></div></template>
            <el-descriptions :column="3" border size="small">
              <el-descriptions-item label="收盘">{{ price(timelineSelected.close_usd) }}</el-descriptions-item><el-descriptions-item label="RSI(14)">{{ decimal(timelineSelected.rsi14 ?? undefined) }}</el-descriptions-item><el-descriptions-item label="KDJ">{{ decimal(timelineSelected.kdj_k ?? undefined) }} / {{ decimal(timelineSelected.kdj_d ?? undefined) }} / {{ decimal(timelineSelected.kdj_j ?? undefined) }}</el-descriptions-item>
              <el-descriptions-item label="EMA10">{{ price(timelineSelected.ema10_usd) }}</el-descriptions-item><el-descriptions-item label="EMA20">{{ price(timelineSelected.ema20_usd) }}</el-descriptions-item><el-descriptions-item label="EMA50">{{ price(timelineSelected.ema50_usd) }}</el-descriptions-item>
              <el-descriptions-item label="量比">{{ decimal(timelineSelected.volume_ratio_20) }}×</el-descriptions-item><el-descriptions-item label="相对IWM">{{ signedPct(timelineSelected.relative_iwm_20d_pct) }}</el-descriptions-item><el-descriptions-item label="ATR14">{{ price(timelineSelected.atr14_usd) }}</el-descriptions-item>
            </el-descriptions>
            <div class="timeline-evidence"><strong>当日支持证据</strong><span>{{ timelineSelected.evidence?.join('；') || '无' }}</span><strong>反向证据</strong><span>{{ timelineSelected.counter_evidence?.join('；') || '无' }}</span><strong>下一确认</strong><span>{{ timelineSelected.next_confirmation || '-' }}</span><strong>失效条件</strong><span>{{ timelineSelected.invalidation || '-' }}</span></div>
          </el-card>
          <el-card shadow="never">
            <template #header><strong>阶段切换记录</strong></template>
            <el-timeline v-if="timelineEvents.length" class="ticker-timeline">
              <el-timeline-item v-for="item in timelineEvents" :key="item.id" :timestamp="item.trade_date" placement="top" :type="phaseTagType(item.phase)">
                <button class="timeline-event" @click="timelineSelected=item"><span>{{ phaseLabel(item.previous_phase) }} → {{ phaseLabel(item.phase) }}</span><strong>{{ item.confidence }}%</strong><small>{{ item.evidence?.slice(0,2).join('；') || '阶段条件发生变化' }}</small></button>
              </el-timeline-item>
            </el-timeline>
            <el-empty v-else :image-size="40" description="所选范围内没有阶段切换" />
          </el-card>
        </div>
      </el-tab-pane>

      <el-tab-pane label="效果评估" name="effectiveness">
        <div class="validation-toolbar">
          <el-alert :type="effectiveness?.can_influence_research_priority ? 'success' : 'warning'" :closable="false" show-icon>
            <template #title>{{ validationStatusLabel(effectiveness?.status) }} · {{ effectiveness?.status_detail || '尚未运行历史回放' }}</template>
          </el-alert>
          <el-button type="primary" :loading="replaying" @click="replayHistory">重新回放</el-button>
        </div>
        <div class="validation-kpis">
          <div><span>阶段事件</span><strong>{{ effectiveness?.event_count || 0 }}</strong></div>
          <div><span>20日成熟样本</span><strong>{{ effectiveness?.mature_20_count || 0 }} / {{ effectiveness?.minimum_samples || 30 }}</strong></div>
          <div><span>独立信号日</span><strong>{{ effectiveness?.distinct_signal_dates || 0 }} / {{ effectiveness?.minimum_distinct_signal_dates || 5 }}</strong></div>
          <div><span>IWM 配对覆盖</span><strong>{{ pct(effectiveness?.benchmark_coverage_pct) }}</strong></div>
          <div><span>研究优先级门控</span><strong :class="effectiveness?.can_influence_research_priority ? 'positive' : 'negative'">{{ effectiveness?.can_influence_research_priority ? '允许' : '禁止' }}</strong></div>
        </div>
        <el-card shadow="never" class="validation-card">
          <template #header><div class="transition-header"><strong>阶段效果</strong><span>胜率按阶段方向计算；置信区间为平均收益的 95% 区间。</span></div></template>
          <el-table :data="effectiveness?.phases || []" border size="small" empty-text="请先运行历史回放">
            <el-table-column label="阶段" width="120"><template #default="{ row }"><el-tag :type="phaseTagType(row.phase)" effect="plain">{{ phaseLabel(row.phase) }}</el-tag></template></el-table-column>
            <el-table-column label="事件 / 独立日" width="110" align="right"><template #default="{ row }">{{ row.event_count }} / {{ row.distinct_signal_dates }}</template></el-table-column>
            <el-table-column v-for="horizon in [1,5,20,60]" :key="horizon" :label="`${horizon}日收益`" width="102" align="right"><template #default="{ row }">{{ signedPct(effectWindow(row,horizon)?.average_return_pct) }}<small class="table-sub">n={{ effectWindow(row,horizon)?.sample_count || 0 }}</small></template></el-table-column>
            <el-table-column label="20日超额" width="100" align="right"><template #default="{ row }">{{ signedPct(effectWindow(row,20)?.average_excess_pct) }}</template></el-table-column>
            <el-table-column label="20日胜率" width="92" align="right"><template #default="{ row }">{{ pct(effectWindow(row,20)?.win_rate_pct) }}</template></el-table-column>
            <el-table-column label="MFE / MAE" width="132" align="right"><template #default="{ row }">{{ signedPct(row.max_favorable_pct_20) }} / {{ signedPct(row.max_adverse_pct_20) }}</template></el-table-column>
            <el-table-column label="最差回撤" width="96" align="right"><template #default="{ row }">{{ signedPct(row.worst_max_drawdown_pct_20) }}</template></el-table-column>
            <el-table-column label="平均R" width="82" align="right"><template #default="{ row }">{{ decimal(row.average_r_multiple_20) }}</template></el-table-column>
            <el-table-column label="假突破" width="86" align="right"><template #default="{ row }">{{ pct(row.false_breakout_pct) }}</template></el-table-column>
            <el-table-column label="转换成功" width="96" align="right"><template #default="{ row }">{{ pct(row.transition_success_pct) }}</template></el-table-column>
          </el-table>
        </el-card>
        <el-card shadow="never" class="validation-card">
          <template #header><div class="segment-heading"><strong>分组验证（20日）</strong><el-select v-model="segmentDimension" size="small" style="width:160px"><el-option v-for="item in segmentDimensions" :key="item.value" :label="item.label" :value="item.value" /></el-select></div></template>
          <el-table :data="filteredSegments" border size="small" max-height="390" empty-text="暂无该维度的成熟样本">
            <el-table-column prop="bucket" label="分组" min-width="160" />
            <el-table-column label="阶段" width="120"><template #default="{ row }">{{ phaseLabel(row.phase) }}</template></el-table-column>
            <el-table-column label="样本" width="75" align="right"><template #default="{ row }">{{ row.window_20.sample_count }}</template></el-table-column>
            <el-table-column label="收益" width="95" align="right"><template #default="{ row }">{{ signedPct(row.window_20.average_return_pct) }}</template></el-table-column>
            <el-table-column label="IWM超额" width="100" align="right"><template #default="{ row }">{{ signedPct(row.window_20.average_excess_pct) }}</template></el-table-column>
            <el-table-column label="胜率" width="90" align="right"><template #default="{ row }">{{ pct(row.window_20.win_rate_pct) }}</template></el-table-column>
            <el-table-column label="95%置信区间" min-width="180" align="right"><template #default="{ row }">{{ signedPct(row.window_20.confidence_low_pct) }} ～ {{ signedPct(row.window_20.confidence_high_pct) }}</template></el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="规则管理" name="rules">
        <div class="rule-health-grid">
          <el-card shadow="never"><span>计算覆盖</span><strong>{{ health?.ready_count || 0 }} / {{ health?.scope_count || 0 }}</strong><small>完整 OHLC 缺失 {{ health?.ohlc_missing_count || 0 }} · 复权阻断 {{ health?.adjustment_blocked_count || 0 }}</small></el-card>
          <el-card shadow="never"><span>IWM 历史</span><strong>{{ healthStatusLabel(health?.iwm_status) }}</strong><small>{{ health?.iwm_sample_days || 0 }} 日 · {{ health?.iwm_latest_trade_date || '-' }}</small></el-card>
          <el-card shadow="never"><span>最近回放</span><strong>{{ healthStatusLabel(health?.last_replay_status) }}</strong><small>{{ formatDateTime(health?.last_replay_at) }} · {{ health?.last_replay_duration_ms || 0 }}ms</small></el-card>
          <el-card shadow="never"><span>效果推进</span><strong>{{ validationStatusLabel(health?.effectiveness_status) }}</strong><small>结果至 {{ health?.effectiveness_latest_date || '-' }}</small></el-card>
        </div>
        <el-card shadow="never" class="rule-config-card">
          <template #header><div class="transition-header"><strong>正式与影子规则</strong><span>影子规则不会覆盖正式阶段；只有验证门槛全部通过后才能晋升。</span></div></template>
          <el-form inline size="small">
            <el-form-item label="正式参数"><el-select v-model="configDraft.active_profile" style="width:150px"><el-option v-for="item in cycleConfig?.profiles || []" :key="item.name" :label="item.label" :value="item.name" :disabled="item.name === configDraft.shadow_profile" /></el-select></el-form-item>
            <el-form-item label="影子参数"><el-select v-model="configDraft.shadow_profile" style="width:150px"><el-option v-for="item in cycleConfig?.profiles || []" :key="item.name" :label="item.label" :value="item.name" :disabled="item.name === configDraft.active_profile" /></el-select></el-form-item>
            <el-button type="primary" :loading="savingConfig" @click="saveCycleConfig">保存配置</el-button>
            <el-button :disabled="!cycleConfig?.previous_profile" @click="rollbackCycleConfig">回退上一版本</el-button>
          </el-form>
          <div v-if="shadow" class="shadow-comparison">
            <span>当前一致率</span><strong>{{ pct(shadow.agreement_pct) }}</strong><span>{{ shadow.agreement_count }} 一致 / {{ shadow.disagreement_count }} 分歧</span>
            <el-tag :type="shadow.promotion_eligible ? 'success' : 'warning'" effect="plain">{{ shadow.promotion_eligible ? '允许晋升' : '禁止晋升' }}</el-tag>
            <small v-if="shadow.promotion_block_reason">{{ shadow.promotion_block_reason }}</small>
          </div>
        </el-card>
        <div class="profile-grid">
          <el-card v-for="profile in cycleConfig?.profiles || []" :key="profile.name" shadow="never" :class="{ 'active-profile': profile.name === cycleConfig?.active_profile }">
            <template #header><div class="profile-title"><strong>{{ profile.label }}</strong><el-tag v-if="profile.name === cycleConfig?.active_profile" type="success">正式</el-tag><el-tag v-else-if="profile.name === cycleConfig?.shadow_profile" type="warning">影子</el-tag></div></template>
            <el-descriptions :column="1" size="small">
              <el-descriptions-item label="规则版本">{{ profile.rule_version }}</el-descriptions-item>
              <el-descriptions-item label="EMA">{{ profile.ema_fast_period }} / {{ profile.ema_medium_period }} / {{ profile.ema_slow_period }}</el-descriptions-item>
              <el-descriptions-item label="ATR延伸">{{ profile.atr_extension_threshold }}×</el-descriptions-item>
              <el-descriptions-item label="放量 / 高潮量">{{ profile.volume_expansion_ratio }}× / {{ profile.climax_volume_ratio }}×</el-descriptions-item>
              <el-descriptions-item label="振幅收缩">≥ {{ profile.range_contraction_pct }}%</el-descriptions-item>
              <el-descriptions-item label="确认 / 置信度">{{ profile.confirmation_days }} 日 / ≥ {{ profile.minimum_confidence }}%</el-descriptions-item>
            </el-descriptions>
          </el-card>
        </div>
      </el-tab-pane>
    </el-tabs>

    <el-drawer v-model="drawerVisible" :title="selected ? `${selected.ticker} · 价格行为循环` : '价格行为循环'" size="520px">
      <template v-if="selected">
        <div class="cycle-detail-hero"><el-tag size="large" :type="phaseTagType(selected.phase.phase)" effect="dark">{{ phaseLabel(selected.phase.phase) }}</el-tag><strong>{{ selected.phase.status === 'ready' ? `${selected.phase.confidence}%` : '待补数据' }}</strong><span>{{ selected.phase.trade_date || '-' }} · {{ selected.phase.rule_version }}</span></div>
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="EMA10">{{ price(selected.phase.ema10_usd) }}</el-descriptions-item><el-descriptions-item label="EMA20">{{ price(selected.phase.ema20_usd) }}</el-descriptions-item>
          <el-descriptions-item label="EMA50">{{ price(selected.phase.ema50_usd) }}</el-descriptions-item><el-descriptions-item label="ATR14">{{ price(selected.phase.atr14_usd) }}</el-descriptions-item>
          <el-descriptions-item label="距EMA20">{{ decimal(selected.phase.distance_to_ema20_atr) }} ATR</el-descriptions-item><el-descriptions-item label="20日量比">{{ decimal(selected.phase.volume_ratio_20) }}×</el-descriptions-item>
          <el-descriptions-item label="振幅收缩">{{ signedPct(selected.phase.range_contraction_pct) }}</el-descriptions-item><el-descriptions-item label="阶段持续">{{ selected.phase.duration_trading_days ? `${selected.phase.duration_trading_days} 个交易日` : '-' }}</el-descriptions-item>
        </el-descriptions>
        <section class="cycle-detail-section"><h3>支持证据</h3><ul><li v-for="item in selected.phase.evidence" :key="item">{{ item }}</li></ul><el-empty v-if="!selected.phase.evidence?.length" :image-size="42" description="暂无完整证据" /></section>
        <section v-if="selected.phase.counter_evidence?.length" class="cycle-detail-section"><h3>反向证据</h3><ul><li v-for="item in selected.phase.counter_evidence" :key="item">{{ item }}</li></ul></section>
        <section class="cycle-detail-section"><h3>下一确认</h3><p>{{ selected.phase.next_confirmation || '-' }}</p><h3>失效条件</h3><p>{{ selected.phase.invalidation || '-' }}</p></section>
        <el-space><el-button type="primary" @click="openTimeline(selected)">历史回溯</el-button><el-button @click="openWorkspace(selected.ticker)">打开研究台</el-button><el-button v-if="selected.candidate" @click="openCandidate(selected.ticker)">查看候选</el-button><el-button v-if="selected.watch" @click="openTarget(selected.ticker)">查看监控</el-button></el-space>
      </template>
    </el-drawer>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import PriceActionTimelineChart from '@/components/PriceActionTimelineChart.vue'
import type { ApiResponse, CandidateScore, CandidateTechnicalAnalysis, CandidateTechnicalHistoryRow, PageResult, PriceActionCycleAnalysis, PriceActionPhaseSnapshot, PriceActionTimeline, TickerTechnicalHistory, WatchTarget } from '@/api/types'

type CycleRow = { ticker:string; company_name:string; candidate:boolean; watch:boolean; watchId?:number; technical:CandidateTechnicalAnalysis; phase:PriceActionCycleAnalysis }
type PhaseTransition = { id:number; ticker:string; trade_date:string; phase:string; previous_phase:string; confidence:number; evidence?:string[] }
type EffectWindow = { horizon_days:number; sample_count:number; pending_count:number; benchmark_sample_count:number; distinct_signal_dates:number; average_return_pct?:number; win_rate_pct?:number; average_benchmark_pct?:number; average_excess_pct?:number; confidence_low_pct?:number; confidence_high_pct?:number }
type PhaseEffect = { phase:string; event_count:number; distinct_signal_dates:number; average_duration_days?:number; transition_success_pct?:number; false_breakout_pct?:number; max_favorable_pct_20?:number; max_adverse_pct_20?:number; worst_max_drawdown_pct_20?:number; average_r_multiple_20?:number; windows:EffectWindow[] }
type EffectSegment = { dimension:string; bucket:string; phase:string; window_20:EffectWindow }
type Effectiveness = { generated_at:string; profile:string; rule_version:string; status:string; status_detail:string; can_influence_research_priority:boolean; event_count:number; mature_20_count:number; distinct_signal_dates:number; minimum_samples:number; minimum_distinct_signal_dates:number; benchmark_coverage_pct:number; minimum_benchmark_coverage_pct:number; latest_signal_date:string; latest_outcome_date:string; phases:PhaseEffect[]; segments:EffectSegment[] }
type RuleProfile = { name:string; label:string; rule_version:string; ema_fast_period:number; ema_medium_period:number; ema_slow_period:number; atr_period:number; breakout_lookback_days:number; atr_extension_threshold:number; volume_expansion_ratio:number; climax_volume_ratio:number; range_contraction_pct:number; confirmation_days:number; minimum_confidence:number }
type CycleConfig = { active_profile:string; shadow_profile:string; previous_profile:string; profiles:RuleProfile[]; updated_at:string }
type ShadowComparison = { active_profile:string; active_rule_version:string; shadow_profile:string; shadow_rule_version:string; compared_tickers:number; agreement_count:number; disagreement_count:number; agreement_pct:number; shadow_validated:boolean; promotion_eligible:boolean; promotion_block_reason?:string }
type CycleHealth = { status:string; scope_count:number; ready_count:number; coverage_pct:number; ohlc_missing_count:number; adjustment_blocked_count:number; iwm_status:string; iwm_sample_days:number; iwm_latest_trade_date:string; active_rule_version:string; rule_version_distribution:Record<string,number>; last_replay_status:string; last_replay_at?:string; last_replay_duration_ms:number; effectiveness_status:string; effectiveness_latest_date:string; current_through_latest_iwm:boolean; scope_current_count:number; scope_stale_count:number; scope_missing_count:number }
const router = useRouter()
const route = useRoute()
const loading = ref(false)
const replaying = ref(false)
const savingConfig = ref(false)
const activeTab = ref('overview')
const rows = ref<CycleRow[]>([])
const transitions = ref<PhaseTransition[]>([])
const effectiveness = ref<Effectiveness | null>(null)
const cycleConfig = ref<CycleConfig | null>(null)
const shadow = ref<ShadowComparison | null>(null)
const health = ref<CycleHealth | null>(null)
const configDraft = reactive({active_profile:'standard',shadow_profile:'conservative'})
const segmentDimension = ref('market_regime')
const drawerVisible = ref(false)
const selected = ref<CycleRow | null>(null)
const timelineLoading = ref(false)
const timeline = ref<PriceActionTimeline | null>(null)
const technicalHistory = ref<CandidateTechnicalHistoryRow[]>([])
const timelineSelected = ref<PriceActionPhaseSnapshot | null>(null)
const timelineFilters = reactive<{ticker:string;ruleVersion:string;dates:string[]}>({ticker:'',ruleVersion:'',dates:[]})
const filters = reactive<{ticker:string;source:string;phase:string;confidence:number|undefined}>({ticker:'',source:'',phase:'',confidence:undefined})
const primaryPhases = ['reversal_extension','wedge_pop','ema_crossback','base_break','exhaustion_extension','wedge_drop']
const allPhases = [...primaryPhases,'unconfirmed','unavailable']
const emptyPhase = ():PriceActionCycleAnalysis => ({status:'unavailable',phase:'unavailable',label:'数据不足',confidence:0,rule_version:'cycle_v1_daily',trade_date:'',freshness_status:'missing',evidence:['尚未保存足够的完整 OHLC 日线'],counter_evidence:[],next_confirmation:'补齐至少 50 个有效交易日的 OHLCV',invalidation:'-',ema10_usd:0,ema20_usd:0,ema50_usd:0,ma200_usd:0,ma200_available:false,atr14_usd:0,distance_to_ema20_atr:0,range_contraction_pct:0,volume_ratio_20:0,duration_trading_days:0})
const filteredRows = computed(()=>rows.value.filter(row=>(!filters.ticker||row.ticker.includes(filters.ticker.trim().toUpperCase()))&&(!filters.source||(filters.source==='candidate'?row.candidate:row.watch))&&(!filters.phase||row.phase.phase===filters.phase)&&(!filters.confidence||row.phase.confidence>=filters.confidence)))
const phaseSummary = computed(()=>allPhases.map(phase=>({phase,count:countFor(phase)})))
const ruleVersion = computed(()=>rows.value.find(row=>row.phase.rule_version)?.phase.rule_version||'cycle_v1_daily')
const staleCount = computed(()=>Math.max(health.value?.scope_stale_count||0,rows.value.filter(row=>row.phase.freshness_status==='stale').length))
const missingCount = computed(()=>Math.max(health.value?.scope_missing_count||0,rows.value.filter(row=>row.phase.freshness_status==='missing'||row.phase.status!=='ready').length))
const filteredSegments = computed(()=>(effectiveness.value?.segments||[]).filter(item=>item.dimension===segmentDimension.value&&item.window_20.sample_count>0))
const timelineEvents = computed(()=>(timeline.value?.items||[]).filter(item=>!!item.previous_phase&&item.previous_phase!==item.phase).slice().reverse())
const segmentDimensions = [{label:'市场环境',value:'market_regime'},{label:'市值',value:'market_cap'},{label:'流动性',value:'liquidity'},{label:'相对强弱',value:'relative_strength'},{label:'置信度',value:'confidence'},{label:'来源',value:'source'}]

async function load(){
  loading.value=true
  try {
    const [candidateRes,watchRes,transitionRes,configRes,effectRes,shadowRes,healthRes]=await Promise.all([
      apiClient.get<ApiResponse<PageResult<CandidateScore>>>('/discovery/candidates',{params:{page:1,page_size:200}}),
      apiClient.get<ApiResponse<PageResult<WatchTarget>>>('/watch-targets',{params:{page:1,page_size:200,status:'enabled'}}),
      apiClient.get<ApiResponse<PhaseTransition[]>>('/price-action-cycle/transitions',{params:{limit:30}}),
      apiClient.get<ApiResponse<CycleConfig>>('/price-action-cycle/config'),
      apiClient.get<ApiResponse<Effectiveness>>('/price-action-cycle/effectiveness'),
      apiClient.get<ApiResponse<ShadowComparison>>('/price-action-cycle/shadow-comparison'),
      apiClient.get<ApiResponse<CycleHealth>>('/price-action-cycle/health'),
    ])
    const merged=new Map<string,CycleRow>()
    for(const item of candidateRes.data.data.items||[]){
      const technical=(item.technical||{}) as CandidateTechnicalAnalysis
      merged.set(item.ticker,{ticker:item.ticker,company_name:item.company_name||'',candidate:true,watch:false,technical,phase:technical.price_action||emptyPhase()})
    }
    for(const item of watchRes.data.data.items||[]){
      const technical=(item.technical||{}) as CandidateTechnicalAnalysis
      const current=merged.get(item.ticker)
      if(current){
        current.watch=true
        current.watchId=item.id
        if((technical.price_action?.trade_date||'')>(current.phase.trade_date||'')){
          current.technical=technical
          current.phase=technical.price_action||emptyPhase()
        }
      }else{
        merged.set(item.ticker,{ticker:item.ticker,company_name:item.company_name||'',candidate:false,watch:true,watchId:item.id,technical,phase:technical.price_action||emptyPhase()})
      }
    }
    rows.value=[...merged.values()].sort((a,b)=>b.phase.confidence-a.phase.confidence||a.ticker.localeCompare(b.ticker))
    transitions.value=transitionRes.data.data||[]
    cycleConfig.value=configRes.data.data
    configDraft.active_profile=cycleConfig.value.active_profile
    configDraft.shadow_profile=cycleConfig.value.shadow_profile
    effectiveness.value=effectRes.data.data
    shadow.value=shadowRes.data.data
    health.value=healthRes.data.data
  }catch(err:any){
    ElMessage.error(err?.response?.data?.message||'加载价格周期失败')
  }finally{
    loading.value=false
  }
}
async function handleTimelineTickerChange(){timelineFilters.ruleVersion='';timelineFilters.dates=[];await loadTimeline()}
async function resetTimelineRange(){timelineFilters.dates=[];await loadTimeline()}
async function loadTimeline(){
  const ticker=timelineFilters.ticker.trim().toUpperCase()
  if(!ticker){timeline.value=null;technicalHistory.value=[];timelineSelected.value=null;return}
  timelineLoading.value=true
  try{
    const row=rows.value.find(item=>item.ticker===ticker)
    const params:Record<string,string|number|boolean>={changes_only:false,limit:1000}
    if(timelineFilters.ruleVersion)params.rule_version=timelineFilters.ruleVersion
    if(timelineFilters.dates.length===2){params.from=timelineFilters.dates[0];params.to=timelineFilters.dates[1]}
    const timelineRequest=apiClient.get<ApiResponse<PriceActionTimeline>>(`/price-action-cycle/tickers/${encodeURIComponent(ticker)}/timeline`,{params})
    const historyRequest=row?.candidate
      ? apiClient.get<ApiResponse<{technical_history:CandidateTechnicalHistoryRow[]}>>(`/discovery/candidates/${encodeURIComponent(ticker)}/detail`)
      : row?.watchId
        ? apiClient.get<ApiResponse<TickerTechnicalHistory>>(`/watch-targets/${row.watchId}/technical-history`)
        : Promise.resolve(null)
    const [timelineResponse,historyResponse]=await Promise.all([timelineRequest,historyRequest])
    timeline.value=timelineResponse.data.data
    timelineFilters.ruleVersion=timeline.value.rule_version
    technicalHistory.value=historyResponse?.data.data
      ? ('technical_history' in historyResponse.data.data ? historyResponse.data.data.technical_history : historyResponse.data.data.history)||[]
      : []
    timelineSelected.value=timeline.value.items[timeline.value.items.length-1]||null
  }catch(err:any){
    timeline.value=null;technicalHistory.value=[];timelineSelected.value=null
    ElMessage.error(err?.response?.data?.message||'加载标的价格时间线失败')
  }finally{timelineLoading.value=false}
}
async function replayHistory(){
  replaying.value=true
  try{
    await apiClient.post('/price-action-cycle/replay',null,{timeout:180000})
    ElMessage.success('历史阶段回放已完成')
    await load()
    activeTab.value='effectiveness'
  }catch(err:any){ElMessage.error(err?.response?.data?.message||'历史回放失败')}finally{replaying.value=false}
}
async function saveCycleConfig(){
  savingConfig.value=true
  try{
    await apiClient.put('/price-action-cycle/config',configDraft)
    ElMessage.success('价格周期配置已保存')
    await load()
    activeTab.value='rules'
  }catch(err:any){ElMessage.error(err?.response?.data?.message||'配置保存失败')}finally{savingConfig.value=false}
}
async function rollbackCycleConfig(){
  savingConfig.value=true
  try{await apiClient.post('/price-action-cycle/config/rollback');ElMessage.success('已回退上一正式参数');await load();activeTab.value='rules'}catch(err:any){ElMessage.error(err?.response?.data?.message||'无法回退')}finally{savingConfig.value=false}
}
function countFor(phase:string){return rows.value.filter(row=>row.phase.phase===phase).length}
function togglePhase(phase:string){filters.phase=filters.phase===phase?'':phase}
function reset(){filters.ticker='';filters.source='';filters.phase='';filters.confidence=undefined}
function openRow(row:CycleRow){selected.value=row;drawerVisible.value=true}
function openTimeline(row:CycleRow){drawerVisible.value=false;activeTab.value='timeline';timelineFilters.ticker=row.ticker;timelineFilters.ruleVersion='';timelineFilters.dates=[];loadTimeline()}
function openTransition(item:PhaseTransition){const row=rows.value.find(value=>value.ticker===item.ticker);if(row)openRow(row);else filters.ticker=item.ticker}
function openWorkspace(ticker:string){router.push({path:'/ticker-workspace',query:{ticker}})}
function openCandidate(ticker:string){router.push({path:'/discovery-candidates',query:{ticker}})}
function openTarget(ticker:string){router.push({path:'/targets',query:{ticker}})}
function phaseLabel(value:string){return({reversal_extension:'反转延伸',wedge_pop:'楔形突破',ema_crossback:'均线回踩',base_break:'平台突破',exhaustion_extension:'衰竭延伸',wedge_drop:'楔形跌破',unconfirmed:'阶段未确认',unavailable:'数据不足'} as Record<string,string>)[value]||value||'数据不足'}
function phaseHint(value:string){return({reversal_extension:'反转观察',wedge_pop:'早期突破',ema_crossback:'回踩确认',base_break:'趋势延续',exhaustion_extension:'保护利润',wedge_drop:'离场预警',unconfirmed:'等待共振',unavailable:'待补日线'} as Record<string,string>)[value]||''}
function phaseTagType(value:string){if(value==='wedge_pop'||value==='base_break')return'success';if(value==='ema_crossback'||value==='reversal_extension')return'primary';if(value==='exhaustion_extension')return'warning';if(value==='wedge_drop')return'danger';return'info'}
function signedPct(value?:number|null){return Number.isFinite(Number(value))?`${Number(value)>=0?'+':''}${Number(value).toFixed(2)}%`:'-'}
function signedClass(value?:number|null){return Number(value)>0?'positive':Number(value)<0?'negative':''}
function price(value?:number){return value&&Number.isFinite(value)?`$${value.toFixed(2)}`:'-'}
function decimal(value?:number){return Number.isFinite(Number(value))?Number(value).toFixed(2):'-'}
function pct(value?:number|null){return Number.isFinite(Number(value))?`${Number(value).toFixed(1)}%`:'-'}
function effectWindow(row:PhaseEffect,horizon:number){return row.windows?.find(item=>item.horizon_days===horizon)}
function validationStatusLabel(value?:string){return({validated:'已验证',validating:'观察中',unverified:'未验证'} as Record<string,string>)[value||'']||'未验证'}
function healthStatusLabel(value?:string){return({ready:'就绪',success:'成功',running:'运行中',failed:'失败',missing:'缺失',warning:'需关注',ok:'正常'} as Record<string,string>)[value||'']||'尚无记录'}
function formatDateTime(value?:string){return value?new Date(value).toLocaleString('zh-CN',{hour12:false}):'-'}
function ruleVersionLabel(value:string){if(value.includes('conservative'))return`稳健 · ${value}`;if(value.includes('sensitive'))return`敏感 · ${value}`;if(value.includes('daily_timeline'))return`标准 · ${value}`;return`历史 · ${value}`}
onMounted(async()=>{filters.ticker=String(route.query.ticker||'').trim().toUpperCase();const tab=String(route.query.tab||'');if(['overview','timeline','effectiveness','rules'].includes(tab))activeTab.value=tab;await load();if(activeTab.value==='timeline'&&filters.ticker){timelineFilters.ticker=filters.ticker;await loadTimeline()}})
</script>

<style scoped>
.cycle-page{display:flex;flex-direction:column;gap:14px}.cycle-header{align-items:flex-start}.cycle-header h1{margin:0}.cycle-header p{margin:5px 0 0;color:var(--el-text-color-secondary)}.cycle-note{margin:0}.cycle-tabs :deep(.el-tabs__content){overflow:visible}.cycle-summary{display:grid;grid-template-columns:repeat(4,minmax(150px,1fr));gap:10px}.cycle-summary-card{appearance:none;text-align:left;border:1px solid var(--el-border-color-light);border-radius:9px;background:var(--el-bg-color);padding:12px 14px;cursor:pointer;color:var(--el-text-color-primary)}.cycle-summary-card:hover,.cycle-summary-card.active{border-color:var(--el-color-primary);box-shadow:0 0 0 1px var(--el-color-primary-light-5)}.cycle-summary-card span,.cycle-summary-card small{display:block;color:var(--el-text-color-secondary)}.cycle-summary-card strong{display:block;font-size:27px;line-height:1.2;margin:3px 0}.cycle-path-card :deep(.el-card__body){padding:14px 18px}.cycle-path{display:flex;align-items:center;justify-content:space-between;gap:8px;margin-bottom:8px}.cycle-path button{flex:1;border:0;border-radius:7px;padding:9px;background:var(--el-fill-color-light);cursor:pointer}.cycle-path button.active{background:var(--el-color-primary-light-8);color:var(--el-color-primary)}.cycle-path button span,.cycle-path button strong{display:block}.cycle-path button strong{font-size:20px;margin-top:3px}.cycle-arrow{color:var(--el-text-color-placeholder)}.cycle-path-card small{color:var(--el-text-color-secondary)}.cycle-toolbar :deep(.el-card__body){display:flex;justify-content:space-between;align-items:center;padding:10px 14px}.cycle-toolbar .el-form-item{margin-bottom:0}.cycle-toolbar>span{color:var(--el-text-color-secondary);font-size:12px}.cycle-identity{display:flex;align-items:center;gap:8px;min-width:0}.cycle-identity .el-link{font-weight:700}.cycle-identity span{white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:var(--el-text-color-secondary)}.positive{color:var(--el-color-success)}.negative{color:var(--el-color-danger)}.cycle-detail-hero{display:flex;align-items:center;gap:12px;margin-bottom:16px}.cycle-detail-hero strong{font-size:24px}.cycle-detail-hero span{margin-left:auto;color:var(--el-text-color-secondary);font-size:12px}.cycle-detail-section{margin:18px 0}.cycle-detail-section h3{font-size:14px;margin:12px 0 7px}.cycle-detail-section p,.cycle-detail-section li{line-height:1.65;color:var(--el-text-color-regular)}.cycle-detail-section ul{padding-left:20px}.validation-toolbar{display:grid;grid-template-columns:1fr auto;gap:12px;align-items:center}.validation-kpis,.rule-health-grid{display:grid;grid-template-columns:repeat(5,1fr);gap:10px;margin:12px 0}.validation-kpis>div,.rule-health-grid .el-card{border:1px solid var(--el-border-color-light);border-radius:8px;background:var(--el-bg-color);padding:12px 14px}.validation-kpis span,.rule-health-grid span,.rule-health-grid small{display:block;color:var(--el-text-color-secondary)}.validation-kpis strong,.rule-health-grid strong{display:block;font-size:22px;margin:4px 0}.rule-health-grid{grid-template-columns:repeat(4,1fr)}.validation-card,.rule-config-card{margin-top:12px}.table-sub{display:block;color:var(--el-text-color-secondary)}.segment-heading,.profile-title{display:flex;justify-content:space-between;align-items:center}.shadow-comparison{display:flex;align-items:center;gap:12px;border-top:1px solid var(--el-border-color-lighter);padding-top:12px;margin-top:4px}.shadow-comparison strong{font-size:22px}.shadow-comparison small{color:var(--el-text-color-secondary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.profile-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:12px;margin-top:12px}.profile-grid .active-profile{border-color:var(--el-color-success)}@media(max-width:1100px){.cycle-summary{grid-template-columns:repeat(2,1fr)}.cycle-path{overflow-x:auto}.cycle-path button{min-width:120px}.cycle-toolbar :deep(.el-card__body){align-items:flex-start;flex-direction:column;gap:8px}.validation-kpis,.rule-health-grid,.profile-grid{grid-template-columns:repeat(2,1fr)}}
.cycle-transitions :deep(.el-card__body){padding:14px 18px 4px}.transition-header{display:flex;align-items:baseline;gap:12px}.transition-header span{font-size:12px;color:var(--el-text-color-secondary)}.transition-row{display:flex;align-items:center;flex-wrap:wrap;gap:8px}.transition-row>span:last-child{min-width:220px;flex:1;color:var(--el-text-color-secondary);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.timeline-toolbar :deep(.el-card__body){display:flex;align-items:center;justify-content:space-between;gap:12px;padding:10px 14px}.timeline-toolbar .el-form-item{margin-bottom:0}.timeline-toolbar>span{white-space:nowrap;color:var(--el-text-color-secondary);font-size:12px}.timeline-visual{margin-top:12px}.timeline-visual :deep(.el-card__body){padding:12px}.timeline-detail-grid{display:grid;grid-template-columns:minmax(0,1.45fr) minmax(320px,.55fr);gap:12px;margin-top:12px}.timeline-selected-title{display:flex;align-items:center;gap:10px}.timeline-selected-title span{color:var(--el-text-color-secondary)}.timeline-evidence{display:grid;grid-template-columns:90px 1fr;gap:8px 12px;margin-top:12px;font-size:13px;line-height:1.55}.timeline-evidence strong{color:var(--el-text-color-secondary)}.ticker-timeline{max-height:330px;overflow:auto;padding:4px 8px}.timeline-event{position:relative;width:100%;border:0;background:transparent;text-align:left;cursor:pointer;color:var(--el-text-color-primary);padding:0 42px 0 0}.timeline-event span,.timeline-event small{display:block}.timeline-event strong{position:absolute;right:0;top:0}.timeline-event small{margin-top:4px;color:var(--el-text-color-secondary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}@media(max-width:1100px){.timeline-toolbar :deep(.el-card__body){align-items:flex-start;flex-direction:column}.timeline-detail-grid{grid-template-columns:1fr}}
</style>
