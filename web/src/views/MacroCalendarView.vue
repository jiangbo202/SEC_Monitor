<template>
  <div class="page-container macro-calendar-page">
    <div class="page-header">
      <div>
        <h1>宏观日历</h1>
        <p>分开跟踪经济数据发布与利率 / 流动性背景，并单列可追溯的市场一致预期补充。</p>
      </div>
      <div class="header-actions">
        <el-button type="primary" :loading="syncing" @click="syncOfficialCalendar">刷新日历</el-button>
        <el-button @click="load">刷新</el-button>
      </div>
    </div>

    <el-alert type="info" :closable="false" show-icon class="macro-source-alert">
      <template #title>官方数据为主，Longbridge 市场日历为补充</template>
      <div>经济数据与收益率曲线仍以 BEA、BLS、Census、DOL、EIA、美联储和财政部的第一方记录为准。Longbridge 三星美国宏观事件单列显示，提供前值、市场预期、公布值及重要性，不覆盖官方记录。</div>
      <div class="macro-sync-note">自动同步：<code>macro_calendar_sync</code> 会同步官方日历与 Longbridge 市场日历；也可随时手动刷新。</div>
    </el-alert>

    <el-card shadow="never">
      <div class="macro-toolbar">
        <el-button-group>
          <el-button :type="filters.view === '' ? 'primary' : 'default'" @click="showView('')">全部</el-button>
          <el-button :type="filters.view === 'economic' ? 'primary' : 'default'" @click="showView('economic')">经济数据</el-button>
          <el-button :type="filters.view === 'rates' ? 'primary' : 'default'" @click="showView('rates')">利率 / 流动性</el-button>
        </el-button-group>
        <el-button-group>
          <el-button :type="filters.status === 'published' ? 'primary' : 'default'" @click="showLatest">最新数据</el-button>
          <el-button :type="filters.status === 'scheduled' ? 'primary' : 'default'" @click="showPending">待公布 {{ pendingCount }}</el-button>
        </el-button-group>
        <el-select fit-input-width v-model="filters.category" clearable placeholder="全部数据类别" style="width: 170px">
          <el-option label="个人收入与支出 / PCE" value="personal_income_outlays" />
          <el-option label="GDP / 实际消费" value="gdp" />
          <el-option label="就业报告 / 非农" value="employment" />
          <el-option label="初请失业金" value="initial_claims" />
		  <el-option label="EIA 周度石油库存" value="petroleum_inventories" />
          <el-option label="CPI / 核心 CPI" value="cpi" />
          <el-option label="PPI / 核心 PPI" value="ppi" />
          <el-option label="JOLTS 职位空缺" value="jolts" />
          <el-option label="零售销售" value="retail_sales" />
          <el-option label="耐用品订单" value="durable_goods" />
          <el-option label="新屋开工 / 营建许可" value="housing_starts" />
          <el-option label="新屋销售" value="new_home_sales" />
          <el-option label="国际贸易" value="international_trade" />
          <el-option label="预先贸易指标" value="advance_trade" />
		  <el-option label="美债名义收益率曲线（3M–30Y）" value="treasury_yields" />
		  <el-option label="美债实际收益率曲线（TIPS）" value="treasury_real_yields" />
          <el-option label="FOMC 会议" value="fomc" />
		  <el-option label="Longbridge 高重要性市场日历" value="market_calendar" />
        </el-select>
        <el-select fit-input-width v-model="filters.frequency" clearable placeholder="全部频率" style="width: 130px">
		  <el-option label="每日" value="daily" />
          <el-option label="每周" value="weekly" />
          <el-option label="月度" value="monthly" />
          <el-option label="季度" value="quarterly" />
          <el-option label="政策会议" value="meeting" />
        </el-select>
        <el-select fit-input-width v-model="filters.sort" style="width: 160px">
          <el-option label="时间：最近优先" value="desc" />
          <el-option label="时间：最早优先" value="asc" />
        </el-select>
        <el-date-picker v-model="filters.range" type="daterange" value-format="YYYY-MM-DD" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" />
        <el-button type="primary" @click="applyFilters">查询</el-button>
        <el-button @click="resetFilters">重置</el-button>
        <span class="macro-count">共 {{ page.total }} 项日历记录</span>
      </div>

      <div v-if="filters.status === 'published' && latestReleases.length" class="macro-latest-grid">
        <div
          v-for="release in latestReleases"
          :key="release.category"
          class="macro-latest-card"
          :class="{ 'is-active': filters.category === release.category }"
          role="button"
          tabindex="0"
          :title="`查看 ${categoryLabel(release.category)} 的历史公布记录`"
          @click="showCategory(release.category)"
          @keyup.enter="showCategory(release.category)"
          @keyup.space.prevent="showCategory(release.category)"
        >
          <div class="macro-latest-label">最新 {{ categoryLabel(release.category) }}</div>
          <el-tooltip :content="publishedValueDescription(release)" placement="top">
            <div :class="['macro-latest-value', `is-${publishedValueDirection(release)}`]">{{ formatValue(primaryObservation(release.observations)?.actual_value, primaryObservation(release.observations)?.unit) }}</div>
          </el-tooltip>
          <div class="macro-latest-metric">{{ primaryObservation(release.observations)?.indicator_name || '待解析官方指标' }}</div>
          <div class="macro-latest-meta">前值 {{ formatValue(primaryObservation(release.observations)?.previous_value, primaryObservation(release.observations)?.unit) }} · {{ formatDateTime(release.scheduled_at) }}</div>
        </div>
      </div>

      <div v-if="filters.category && trendSeries.length" ref="trendCardRef" class="macro-trend-card">
        <div class="macro-trend-header">
          <div>
            <div class="macro-trend-title">{{ categoryLabel(filters.category) }} · 发布周期趋势</div>
            <div class="macro-trend-subtitle">同一类别的全部官方指标同时绘制；不同量纲各按自身区间缩放，悬浮数据点可查看原始值。</div>
          </div>
        </div>
        <div class="macro-trend-legend">
          <span v-for="series in trendSeries" :key="series.code" class="macro-trend-legend-item"><i :style="{ backgroundColor: series.color }" />{{ series.label }}：{{ formatValue(series.latest.value, series.unit) }}</span>
        </div>
        <div class="macro-trend-meta">已保存 {{ trendPeriods.length }} 个发布周期 · {{ trendAxisExplanation }}<template v-if="!trendUsesSharedAxis">，不代表不同指标之间的绝对大小比较。</template></div>
        <div class="macro-trend-plot" :class="{ 'is-multi-series': trendSeries.length > 1 }">
          <svg class="macro-trend-chart" viewBox="0 0 1000 180" preserveAspectRatio="none" role="img" :aria-label="`${categoryLabel(filters.category)} 发布周期趋势`">
            <defs><linearGradient v-for="series in trendSeries" :id="series.gradientId" :key="series.gradientId" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" :stop-color="series.color" stop-opacity="0.20" /><stop offset="100%" :stop-color="series.color" stop-opacity="0.01" /></linearGradient></defs>
            <g v-for="(y, index) in [24, 70, 116, 162]" :key="y"><text x="4" :y="y + 4" class="macro-trend-y-label">{{ trendAxisLabels[index] }}</text><line x1="64" :y1="y" x2="1000" :y2="y" class="macro-trend-grid-line" /></g>
            <g v-for="series in trendSeries" :key="series.code">
              <path v-if="series.areaPath && trendSeries.length === 1" :d="series.areaPath" class="macro-trend-area" :style="{ fill: `url(#${series.gradientId})` }" />
              <path :d="series.path" class="macro-trend-line" :style="{ stroke: series.color }" />
              <circle v-for="point in series.points" :key="`${series.code}-${point.date}`" :cx="point.x" :cy="point.y" r="5" class="macro-trend-dot" :style="{ fill: series.color }" :aria-label="trendPointLabel(series, point)" @mouseenter="showTrendTooltip($event, series, point)" @mousemove="showTrendTooltip($event, series, point)" @mouseleave="hideTrendTooltip" />
              <text v-if="series.points.length === 1" :x="series.points[0].x" :y="series.points[0].y - 14" text-anchor="middle" class="macro-trend-single-value" :style="{ fill: series.color }">{{ formatValue(series.points[0].value, series.unit) }}</text>
            </g>
          </svg>
        </div>
        <div class="macro-trend-axis"><span>{{ trendPeriods[0] }}</span><span>{{ trendPeriods[trendPeriods.length - 1] }}</span></div>
        <div v-if="trendPeriods.length === 1" class="macro-trend-single-period">唯一已保存周期：{{ trendPeriods[0] }}；以上各点均为该次官方发布的原始数据。</div>
        <div v-if="trendTooltip" class="macro-trend-tooltip" :style="{ left: `${trendTooltip.x}px`, top: `${trendTooltip.y}px` }">
          <div class="macro-trend-tooltip-title"><i :style="{ backgroundColor: trendTooltip.color }" />{{ trendTooltip.label }}</div>
          <div class="macro-trend-tooltip-value">{{ formatValue(trendTooltip.value, trendTooltip.unit) }}</div>
          <div class="macro-trend-tooltip-meta">{{ trendTooltip.date }}<span v-if="trendTooltip.delta != null"> · 较上期 {{ trendTooltip.delta >= 0 ? '+' : '' }}{{ trendTooltip.delta.toFixed(1) }}{{ trendTooltip.unit }}</span><span v-else> · 首期数据</span></div>
        </div>
      </div>

      <div class="macro-table-title">{{ filters.status === 'scheduled' ? '待公布日历' : '已公布记录' }}</div>
      <el-table :data="page.items" v-loading="loading" row-key="id" border class="macro-release-table" :empty-text="filters.status === 'scheduled' ? '暂无待公布事件。' : '暂无已公布记录；点击“刷新日历”开始同步。'">
        <el-table-column type="expand" width="48">
          <template #default="{ row }">
            <div class="macro-observation-detail">
              <div v-if="row.related_sources?.length" class="macro-source-association">
                <div class="macro-observation-title">关联来源</div>
                <div class="macro-source-association-note">按“事件类别 + 美国公布日期”关联；官方记录为主，Longbridge 仅补充预期和市场日历字段。</div>
                <div class="macro-source-links">
                  <el-tag v-for="source in row.related_sources" :key="`${source.provider}-${source.source_url}`" :type="source.official ? 'success' : 'info'" effect="plain">
                    <el-link :href="source.source_url" target="_blank" :type="source.official ? 'success' : 'primary'" :underline="false">{{ providerLabel(source.provider) }}{{ source.official ? '（主）' : '（补充）' }}</el-link>
                  </el-tag>
                </div>
              </div>
              <div class="macro-observation-title">公布结果与市场预期</div>
              <el-table :data="row.observations" size="small" border empty-text="该官方公告尚未解析到本页支持的指标；可通过“官方来源”查看原始公告。">
                <el-table-column prop="indicator_name" label="指标" min-width="210" />
                <el-table-column label="实际值" width="120" align="right"><template #default="{ row: observation }">{{ formatValue(observation.actual_value, observation.unit) }}</template></el-table-column>
                <el-table-column label="前值" width="120" align="right"><template #default="{ row: observation }">{{ formatValue(observation.previous_value, observation.unit) }}</template></el-table-column>
                <el-table-column label="市场预期" width="120" align="right"><template #default="{ row: observation }">{{ formatValue(observation.forecast_value, observation.unit) }}</template></el-table-column>
                <el-table-column prop="frequency" label="频率" width="90"><template #default="{ row: observation }">{{ frequencyLabel(observation.frequency) }}</template></el-table-column>
                <el-table-column prop="source_field" label="官方字段" min-width="260" show-overflow-tooltip />
                <el-table-column label="公告时间" width="172"><template #default="{ row: observation }">{{ formatDateTime(observation.provider_updated_at) }}</template></el-table-column>
                <el-table-column label="来源" width="90"><template #default="{ row: observation }"><el-link :href="observation.source_url" target="_blank" type="primary">官方原文</el-link></template></el-table-column>
              </el-table>
              <div class="macro-disclaimer">官方记录的前值来自本地已保存的上一期公布值；Longbridge 行为市场日历补充，预期值不会覆盖官方数据，也不自动推断利多或利空。</div>
            </div>
          </template>
        </el-table-column>
		<el-table-column label="数据类别" width="168"><template #default="{ row }"><el-tooltip :content="categoryLabel(row.category)" placement="top"><el-tag class="macro-category-tag" effect="plain">{{ categoryLabel(row.category) }}</el-tag></el-tooltip></template></el-table-column>
        <el-table-column label="频率" width="88"><template #default="{ row }">{{ releaseFrequencyLabel(row.category) }}</template></el-table-column>
		<el-table-column prop="title" label="日历事件" min-width="320"><template #default="{ row }"><el-tooltip :content="row.title" placement="top"><span class="macro-cell-overflow">{{ row.title }}</span></el-tooltip></template></el-table-column>
        <el-table-column prop="reference_period" label="数据期" width="135" />
        <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="row.status === 'published' ? 'success' : 'info'" effect="plain">{{ row.status === 'published' ? '已公布' : '待公布' }}</el-tag></template></el-table-column>
        <el-table-column label="关联来源" width="130"><template #default="{ row }"><el-tooltip :content="relatedSourceTooltip(row.related_sources)" placement="top"><el-tag v-if="row.related_sources?.length > 1" type="success" effect="plain">{{ row.related_sources.length }} 个来源</el-tag><span v-else>仅当前来源</span></el-tooltip></template></el-table-column>
        <el-table-column label="前值" width="130" align="right"><template #default="{ row }"><el-tooltip :content="primaryMetricDescription(row)" placement="top"><span>{{ formatValue(primaryObservation(row.observations)?.previous_value, primaryObservation(row.observations)?.unit) }}</span></el-tooltip></template></el-table-column>
        <el-table-column label="预测值" width="118" align="right"><template #default="{ row }"><span v-if="primaryObservation(row.observations)?.forecast_value != null">{{ formatValue(primaryObservation(row.observations)?.forecast_value, primaryObservation(row.observations)?.unit) }}</span><el-tooltip v-else content="官方机构不发布市场一致预期；仅 Longbridge 市场日历补充预期值。" placement="top"><el-tag type="info" effect="plain">-</el-tag></el-tooltip></template></el-table-column>
        <el-table-column label="公布值" width="168" align="right"><template #default="{ row }"><el-tooltip :content="publishedValueDescription(row)" placement="top"><strong :class="publishedValueClass(row)">{{ publishedValueLabel(row) }}</strong></el-tooltip></template></el-table-column>
        <el-table-column label="影响" width="118"><template #default="{ row }"><el-tooltip content="市场影响需要以公布值相对可追溯预测值的“意外程度”计算；当前未接入预测值，故不做利多/利空判断。" placement="top"><el-tag type="info" effect="plain">{{ impactLabel(row) }}</el-tag></el-tooltip></template></el-table-column>
		<el-table-column label="重要性" width="118"><template #default="{ row }"><el-tooltip :content="row.market_importance ? 'Longbridge 市场日历重要性（星级）。' : '系统规则分级，不是官方评级。'" placement="top"><el-tag type="warning" effect="plain">{{ row.market_importance ? `${row.market_importance} 星` : importanceLabel(row.category) }}</el-tag></el-tooltip></template></el-table-column>
		<el-table-column label="公布时间（上海）" width="175"><template #default="{ row }"><el-tooltip :content="releaseTimeTooltip(row)" placement="top"><span>{{ releaseTimeLabel(row) }}</span></el-tooltip></template></el-table-column>
		<el-table-column label="来源" width="105"><template #default="{ row }"><el-link :href="row.source_url" target="_blank" type="primary">{{ providerLabel(row.provider) }}</el-link></template></el-table-column>
      </el-table>

      <div class="pagination-row">
        <el-pagination v-model:current-page="page.current" v-model:page-size="page.pageSize" :total="page.total" :page-sizes="[25, 50, 100]" layout="total, sizes, prev, pager, next" @current-change="load" @size-change="load" />
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, MacroObservation, MacroRelease, MacroReleaseSource, PageResult } from '@/api/types'

type TrendPoint = { date: string, value: number, releaseIndex: number, delta: number | null, x: number, y: number }
type TrendSeries = { code: string, label: string, unit: string, color: string, gradientId: string, points: TrendPoint[], latest: TrendPoint, path: string, areaPath: string }
type TrendTooltip = Pick<TrendPoint, 'date' | 'value' | 'delta'> & { label: string, unit: string, color: string, x: number, y: number }

const loading = ref(false)
const syncing = ref(false)
const filters = reactive<{ status: 'published' | 'scheduled', view: '' | 'economic' | 'rates', category: string, frequency: string, sort: 'asc' | 'desc', range: string[] }>({ status: 'published', view: '', category: '', frequency: '', sort: 'desc', range: [] })
const page = reactive<{ items: MacroRelease[], total: number, current: number, pageSize: number }>({ items: [], total: 0, current: 1, pageSize: 50 })
const pendingCount = ref(0)
const latestItems = ref<MacroRelease[]>([])
const categoryHistory = ref<MacroRelease[]>([])
const trendCardRef = ref<HTMLElement>()
const trendTooltip = ref<TrendTooltip | null>(null)
const trendPalette = ['#409eff', '#67c23a', '#e6a23c', '#f56c6c', '#9254de', '#14b8a6', '#ec4899', '#64748b']
const latestReleases = computed(() => {
  const seen = new Set<string>()
  return latestItems.value.filter((release) => {
    if (!release.observations?.some((item) => item.actual_value != null)) return false
    if (seen.has(release.category)) return false
    seen.add(release.category)
    return true
  })
})
const trendIndicatorOptions = computed(() => {
  const indicators = new Map<string, { code: string, label: string, unit: string }>()
  for (const release of categoryHistory.value) {
    for (const observation of release.observations || []) {
      if (observation.actual_value == null || indicators.has(observation.indicator_code)) continue
      indicators.set(observation.indicator_code, { code: observation.indicator_code, label: observation.indicator_name, unit: observation.unit || '' })
    }
  }
  const options = Array.from(indicators.values())
  const primary = primaryObservation(categoryHistory.value[categoryHistory.value.length - 1]?.observations || [])
  if (primary) {
    const index = options.findIndex((item) => item.code === primary.indicator_code)
    if (index > 0) options.unshift(options.splice(index, 1)[0])
  }
  return options
})
const trendPeriods = computed(() => categoryHistory.value.map((release) => release.reference_period || formatDateOnly(release.scheduled_at)))
const trendSharedRange = computed(() => {
  const activeIndicators = trendIndicatorOptions.value.filter((indicator) => categoryHistory.value.some((release) =>
    (release.observations || []).some((item) => item.indicator_code === indicator.code && item.actual_value != null),
  ))
  const units = [...new Set(activeIndicators.map((indicator) => indicator.unit || ''))]
  if (units.length !== 1) return null
  const values = categoryHistory.value.flatMap((release) => (release.observations || [])
    .filter((item) => activeIndicators.some((indicator) => indicator.code === item.indicator_code) && item.actual_value != null)
    .map((item) => Number(item.actual_value))
    .filter(Number.isFinite))
  if (!values.length) return null
  const minimum = Math.min(...values)
  const maximum = Math.max(...values)
  const padding = maximum === minimum ? Math.max(Math.abs(maximum) * 0.08, 1) : (maximum - minimum) * 0.08
  return { unit: units[0], low: minimum - padding, high: maximum + padding }
})
const trendUsesSharedAxis = computed(() => trendSharedRange.value !== null)
const trendAxisExplanation = computed(() => trendSharedRange.value
  ? `统一纵轴：${trendSharedRange.value.unit || '相同单位'}实际数值`
  : '各线按自身区间缩放，纵轴仅表示相对高低')
const trendAxisLabels = computed(() => {
  const range = trendSharedRange.value
  if (!range) return ['相对高', '', '', '相对低']
  const span = range.high - range.low
  return [range.high, range.high - span / 3, range.high - span * 2 / 3, range.low]
    .map((value) => formatValue(value, range.unit))
})
const trendSeries = computed<TrendSeries[]>(() => trendIndicatorOptions.value.map((indicator, seriesIndex) => {
  const rawPoints = categoryHistory.value.map((release, index) => {
    const observation = (release.observations || []).find((item) => item.indicator_code === indicator.code)
    if (observation?.actual_value == null) return null
    return { date: trendPeriods.value[index], value: Number(observation.actual_value), releaseIndex: index }
  }).filter((point): point is { date: string, value: number, releaseIndex: number } => point !== null)
  const values = rawPoints.map((point) => point.value)
  const minimum = Math.min(...values)
  const maximum = Math.max(...values)
  const padding = maximum === minimum ? Math.max(Math.abs(maximum) * 0.08, 1) : (maximum - minimum) * 0.08
  const low = trendSharedRange.value?.low ?? minimum - padding
  const high = trendSharedRange.value?.high ?? maximum + padding
  const points = rawPoints.map((point, index) => ({
    ...point,
    delta: index === 0 ? null : point.value - rawPoints[index - 1].value,
    // 一期发布同时包含多个官方指标时，没有第二个时间点可形成折线。
    // 将指标横向排开，避免其数值标签堆叠在同一时间坐标上。
    x: categoryHistory.value.length === 1
      ? 100 + (800 * seriesIndex) / Math.max(trendIndicatorOptions.value.length - 1, 1)
      : 70 + (900 * point.releaseIndex) / (categoryHistory.value.length - 1),
    y: 156 - ((point.value - low) / (high - low)) * 126,
  }))
  const path = points.map((point, index) => `${index === 0 ? 'M' : 'L'} ${point.x.toFixed(2)} ${point.y.toFixed(2)}`).join(' ')
  return {
    ...indicator,
    color: trendPalette[seriesIndex % trendPalette.length],
    gradientId: `macro-trend-area-${seriesIndex}`,
    points,
    latest: points[points.length - 1],
    path,
    areaPath: points.length > 1 ? `${path} L ${points[points.length - 1].x.toFixed(2)} 166 L ${points[0].x.toFixed(2)} 166 Z` : '',
  }
}).filter((series) => series.points.length > 0))

function trendPointLabel(series: TrendSeries, point: TrendPoint) {
  return `${series.label}，${point.date}，${formatValue(point.value, series.unit)}${point.delta == null ? '' : `，较上期 ${point.delta >= 0 ? '+' : ''}${point.delta.toFixed(1)}${series.unit}`}`
}

function showTrendTooltip(event: MouseEvent, series: TrendSeries, point: TrendPoint) {
  const bounds = trendCardRef.value?.getBoundingClientRect()
  if (!bounds) return
  const localX = event.clientX - bounds.left
  const localY = event.clientY - bounds.top
  const tooltipWidth = 280
  const tooltipHeight = 96
  const x = localX > bounds.width * 0.62
    ? Math.max(12, localX - tooltipWidth - 14)
    : Math.min(localX + 14, bounds.width - tooltipWidth - 12)
  const above = localY - tooltipHeight - 14
  trendTooltip.value = {
    label: series.label,
    unit: series.unit,
    color: series.color,
    date: point.date,
    value: point.value,
    delta: point.delta,
    x,
    y: above >= 66 ? above : Math.min(localY + 16, bounds.height - tooltipHeight - 10),
  }
}

function hideTrendTooltip() { trendTooltip.value = null }

async function load() {
  loading.value = true
  try {
    const response = await apiClient.get<ApiResponse<PageResult<MacroRelease>>>('/macro/releases', { params: { page: page.current, page_size: page.pageSize, status: filters.status || undefined, view: filters.view || undefined, category: filters.category || undefined, frequency: filters.frequency || undefined, sort: filters.sort, from: filters.range[0], to: filters.range[1] } })
    page.items = response.data.data.items || []
    page.total = response.data.data.total || 0
    await Promise.all([loadPendingCount(), loadLatestReleases(), loadCategoryHistory(filters.category)])
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载宏观日历失败')
  } finally {
    loading.value = false
  }
}

async function loadLatestReleases() {
  const response = await apiClient.get<ApiResponse<PageResult<MacroRelease>>>('/macro/releases', {
    params: { page: 1, page_size: 100, status: 'published', view: filters.view || undefined, sort: 'desc' },
  })
  latestItems.value = response.data.data.items || []
}

async function loadCategoryHistory(category: string) {
  if (!category) {
    categoryHistory.value = []
    return
  }
  const response = await apiClient.get<ApiResponse<PageResult<MacroRelease>>>('/macro/releases', {
    params: { page: 1, page_size: 200, status: 'published', category, sort: 'asc' },
  })
  if (filters.category !== category) return
  categoryHistory.value = response.data.data.items || []
}

async function loadPendingCount() {
  try {
    const response = await apiClient.get<ApiResponse<PageResult<MacroRelease>>>('/macro/releases', { params: { page: 1, page_size: 1, status: 'scheduled' } })
    pendingCount.value = response.data.data.total || 0
  } catch {
    // The active list is still usable if the badge count cannot be refreshed.
  }
}

async function syncOfficialCalendar() {
  syncing.value = true
  try {
    const response = await apiClient.post<ApiResponse<{ scheduled_found: number, releases_saved: number, published: number, observations: number, warnings: string[] }>>('/macro/releases/sync')
    const result = response.data.data
    ElMessage.success(`已同步 ${result.scheduled_found} 项日历，保存 ${result.observations} 个数据项`)
    if (result.warnings?.length) ElMessage.warning(`同步完成，但有 ${result.warnings.length} 条提示`)
    await load()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '同步宏观日历失败')
  } finally {
    syncing.value = false
  }
}

function applyFilters() { page.current = 1; void load() }
function resetFilters() { filters.status = 'published'; filters.view = ''; filters.category = ''; filters.frequency = ''; filters.sort = 'desc'; filters.range = []; page.current = 1; void load() }
function showLatest() { filters.status = 'published'; page.current = 1; void load() }
function showPending() { filters.status = 'scheduled'; page.current = 1; void load() }
function showView(view: '' | 'economic' | 'rates') { filters.view = view; filters.category = ''; page.current = 1; void load() }
function showCategory(category: string) {
  filters.status = 'published'
  filters.category = filters.category === category ? '' : category
  filters.sort = 'desc'
  page.current = 1
  void load()
}
function formatValue(value?: number | null, unit?: string) { return Number.isFinite(value) ? `${Number(value).toFixed(1)}${unit || ''}` : '-' }
function formatDateOnly(value?: string | null) { return value ? formatDateTime(value).slice(0, 10).replace(/\//g, '-') : '-' }
function frequencyLabel(value: string) { return value === 'daily' ? '每日' : value === 'weekly' ? '每周' : value === 'monthly' ? '月度' : value === 'quarterly' ? '季度' : value === 'meeting' ? '政策会议' : value || '-' }
function categoryLabel(value: string) {
  return value === 'personal_income_outlays' ? '个人收入与支出 / PCE'
    : value === 'gdp' ? 'GDP / 实际消费'
        : value === 'employment' ? '就业报告 / 非农'
            : value === 'initial_claims' ? '初请失业金'
			  : value === 'petroleum_inventories' ? 'EIA 周度石油库存'
            : value === 'cpi' ? 'CPI / 核心 CPI'
            : value === 'ppi' ? 'PPI / 核心 PPI'
              : value === 'jolts' ? 'JOLTS 职位空缺'
                : value === 'retail_sales' ? '零售销售'
                  : value === 'durable_goods' ? '耐用品订单'
                    : value === 'housing_starts' ? '新屋开工 / 营建许可'
                      : value === 'new_home_sales' ? '新屋销售'
                        : value === 'international_trade' ? '国际贸易'
                          : value === 'advance_trade' ? '预先贸易指标'
                            : value === 'treasury_yields' ? '美债名义收益率曲线（3M–30Y）'
                              : value === 'treasury_real_yields' ? '美债实际收益率曲线（TIPS）'
								: value === 'fomc' ? 'FOMC 会议' : value === 'market_calendar' ? 'Longbridge 高重要性日历' : value || '-'
}
function releaseFrequencyLabel(category: string) { return ['treasury_yields', 'treasury_real_yields'].includes(category) ? '每日' : ['initial_claims', 'petroleum_inventories'].includes(category) ? '每周' : category === 'gdp' ? '季度' : category === 'fomc' ? '政策会议' : category === 'market_calendar' ? '市场日历' : '月度' }
function providerLabel(value?: string) { return value === 'bea' ? 'BEA 链接' : value === 'bls' ? 'BLS 链接' : value === 'fred' ? 'FRED（原始来源：BLS）' : value === 'census' ? 'Census 链接' : value === 'dol' ? 'DOL 链接' : value === 'eia' ? 'EIA 链接' : value === 'treasury' ? '财政部链接' : value === 'federal_reserve' ? '美联储链接' : value === 'longbridge' ? 'Longbridge' : '来源链接' }
function releaseTimeLabel(row: MacroRelease) { return row.release_stage === 'fred_mirror' ? `${formatDateOnly(row.scheduled_at)}（数据期）` : formatDateTime(row.scheduled_at) }
function releaseTimeTooltip(row: MacroRelease) { return row.release_stage === 'fred_mirror' ? 'FRED 镜像按 BLS 数据期索引，不代表精确的官方公布时点。' : '按上海时区显示官方公布时间。' }
function relatedSourceTooltip(sources?: MacroReleaseSource[]) { return sources?.length ? sources.map((source) => `${providerLabel(source.provider)}${source.official ? '（官方主源）' : '（市场补充）'}：${source.title}`).join('\n') : '仅当前来源' }
function formatDateTime(value?: string | null) { if (!value) return '-'; const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }) }
function primaryObservation(observations: MacroObservation[]) {
  const codes = ['treasury_10y_yield', 'treasury_10y_real_yield', 'commercial_crude_oil_inventory_mmbbl', 'initial_claims_k', 'nonfarm_payrolls_change_k', 'job_openings_m', 'cpi_mom', 'ppi_mom', 'retail_sales_mom', 'core_pce_yoy', 'real_gdp_qoq_annualized', 'core_pce_mom', 'pce_mom', 'real_pce_qoq_annualized']
  return codes.map((code) => observations?.find((item) => item.indicator_code === code)).find(Boolean) || observations?.[0]
}
function primaryMetricDescription(release: MacroRelease) {
  const observation = primaryObservation(release.observations)
  return observation ? `主指标：${observation.indicator_name}；展开行可查看本次公告的全部官方指标。` : '官方公告尚未解析到主指标。'
}
function publishedValueDirection(release: MacroRelease) {
  const observation = primaryObservation(release.observations)
  if (observation?.actual_value == null || observation.previous_value == null) return 'flat'
  if (observation.actual_value > observation.previous_value) return 'up'
  if (observation.actual_value < observation.previous_value) return 'down'
  return 'flat'
}
function publishedValueClass(release: MacroRelease) { return `macro-published-value is-${publishedValueDirection(release)}` }
function publishedValueLabel(release: MacroRelease) {
  const observation = primaryObservation(release.observations)
  const actual = formatValue(observation?.actual_value, observation?.unit)
  if (observation?.actual_value == null || observation.previous_value == null) return actual
  const delta = observation.actual_value - observation.previous_value
  if (delta === 0) return actual
  const direction = delta > 0 ? '↑' : '↓'
  const sign = delta > 0 ? '+' : ''
  return `${actual}(${direction} ${sign}${delta.toFixed(1)}${observation.unit || ''})`
}
function publishedValueDescription(release: MacroRelease) {
  const observation = primaryObservation(release.observations)
  if (!observation) return '官方公告尚未解析到主指标。'
  if (observation.actual_value == null || observation.previous_value == null) return `${primaryMetricDescription(release)} 当前缺少可比较的前值。`
  const delta = observation.actual_value - observation.previous_value
  const sign = delta > 0 ? '+' : ''
  return `${primaryMetricDescription(release)} 公布值较前值 ${sign}${delta.toFixed(1)} 个百分点；颜色仅表示数值变化，不代表利多或利空。`
}
function importanceLabel(category: string) { return ['personal_income_outlays', 'gdp', 'employment', 'initial_claims', 'petroleum_inventories', 'cpi', 'ppi', 'jolts', 'retail_sales', 'durable_goods', 'housing_starts', 'new_home_sales', 'international_trade', 'advance_trade', 'treasury_yields', 'treasury_real_yields', 'fomc'].includes(category) ? '高（系统分级）' : '未评级' }
function impactLabel(release: MacroRelease) { return release.status === 'published' ? '待比较预测' : '待公布' }

onMounted(load)
</script>

<style scoped>
.macro-source-alert { margin-bottom: 12px; }
.macro-category-tag, .macro-cell-overflow { display: block; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.macro-category-tag { width: fit-content; }
.macro-sync-note { margin-top: 6px; font-size: 12px; color: var(--el-text-color-secondary); }
.macro-toolbar { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin-bottom: 12px; }
.macro-count { margin-left: auto; color: var(--el-text-color-secondary); font-size: 13px; }
.macro-latest-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(250px, 1fr)); gap: 12px; margin: 0 0 16px; }
.macro-latest-card { border: 1px solid var(--el-border-color-lighter); border-radius: 8px; padding: 14px 16px; background: var(--el-fill-color-lighter); cursor: pointer; transition: border-color .15s ease, box-shadow .15s ease, transform .15s ease; }
.macro-latest-card:hover, .macro-latest-card:focus-visible { border-color: var(--el-color-primary-light-5); box-shadow: 0 4px 12px rgb(64 158 255 / 14%); outline: none; transform: translateY(-1px); }
.macro-latest-card.is-active { border-color: var(--el-color-primary); box-shadow: inset 0 0 0 1px var(--el-color-primary-light-7); background: var(--el-color-primary-light-9); }
.macro-trend-card { position: relative; margin: 0 0 16px; border: 1px solid var(--el-border-color-lighter); border-radius: 12px; padding: 14px 16px 12px; background: linear-gradient(145deg, var(--el-fill-color-lighter), var(--el-bg-color)); overflow: visible; }
.macro-trend-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.macro-trend-title { font-size: 16px; font-weight: 600; color: var(--el-text-color-primary); }
.macro-trend-subtitle, .macro-trend-meta { margin-top: 4px; font-size: 12px; color: var(--el-text-color-secondary); }
.macro-trend-legend { display: flex; flex-wrap: wrap; gap: 8px 16px; margin-top: 12px; }
.macro-trend-legend-item { display: inline-flex; align-items: center; gap: 6px; padding: 4px 8px; border: 1px solid var(--el-border-color-lighter); border-radius: 999px; color: var(--el-text-color-regular); font-size: 12px; background: rgb(255 255 255 / 70%); }
.macro-trend-legend-item i { display: inline-block; width: 10px; height: 10px; border-radius: 50%; }
.macro-trend-plot { margin-top: 10px; padding: 8px 12px 0; border: 1px solid rgb(64 158 255 / 12%); border-radius: 10px; background: radial-gradient(circle at top right, rgb(64 158 255 / 8%), transparent 42%), linear-gradient(180deg, rgb(255 255 255 / 80%), rgb(248 250 252 / 72%)); }
.macro-trend-chart { display: block; width: 100%; height: 158px; margin: 0; overflow: visible; }
.macro-trend-grid-line { stroke: #dbeafe; stroke-dasharray: 3 7; }
.macro-trend-y-label { fill: var(--el-text-color-secondary); font-size: 12px; text-anchor: start; }
.macro-trend-area { pointer-events: none; }
.macro-trend-line { fill: none; stroke: var(--el-color-primary); stroke-width: 3; vector-effect: non-scaling-stroke; stroke-linecap: round; stroke-linejoin: round; filter: drop-shadow(0 1px 1px rgb(15 23 42 / 10%)); }
.macro-trend-plot.is-multi-series .macro-trend-line { stroke-width: 2.5; }
.macro-trend-dot { fill: var(--el-color-primary); stroke: white; stroke-width: 2.5; vector-effect: non-scaling-stroke; cursor: pointer; transition: r .15s ease, filter .15s ease; }
.macro-trend-dot:hover { r: 7; filter: drop-shadow(0 2px 3px rgb(64 158 255 / 35%)); }
.macro-trend-axis { display: flex; justify-content: space-between; padding-left: 64px; color: var(--el-text-color-secondary); font-size: 12px; }
.macro-trend-single-value { font-size: 22px; font-weight: 700; }
.macro-trend-single-period { margin-top: 8px; color: var(--el-text-color-secondary); font-size: 12px; }
.macro-trend-tooltip { position: absolute; z-index: 3; min-width: 200px; max-width: 280px; padding: 10px 12px; border: 1px solid rgb(255 255 255 / 18%); border-radius: 8px; color: #f8fafc; background: rgb(31 41 55 / 96%); box-shadow: 0 8px 24px rgb(15 23 42 / 24%); pointer-events: none; }
.macro-trend-tooltip-title { display: flex; align-items: center; gap: 6px; font-size: 12px; line-height: 1.35; color: #dbeafe; }
.macro-trend-tooltip-title i { display: inline-block; flex: 0 0 auto; width: 8px; height: 8px; border-radius: 50%; }
.macro-trend-tooltip-value { margin-top: 4px; font-size: 20px; font-weight: 700; line-height: 1.2; }
.macro-trend-tooltip-meta { margin-top: 4px; font-size: 12px; line-height: 1.35; color: #cbd5e1; }
.macro-latest-label { color: var(--el-text-color-secondary); font-size: 13px; }
.macro-latest-value { margin-top: 4px; color: var(--el-color-primary); font-size: 22px; font-weight: 700; }
.macro-latest-value.is-up { color: var(--el-color-success); }
.macro-latest-value.is-down { color: var(--el-color-danger); }
.macro-latest-metric { margin-top: 4px; color: var(--el-text-color-primary); font-size: 13px; }
.macro-latest-meta { margin-top: 8px; color: var(--el-text-color-secondary); font-size: 12px; }
.macro-table-title { margin: 2px 0 10px; color: var(--el-text-color-primary); font-weight: 600; }
.macro-published-value.is-up { color: var(--el-color-success); }
.macro-published-value.is-down { color: var(--el-color-danger); }
.macro-published-value.is-flat { color: var(--el-text-color-primary); }
.macro-observation-detail { padding: 4px 14px 12px 54px; }
.macro-observation-title { margin: 8px 0; font-weight: 600; color: var(--el-text-color-primary); }
.macro-source-association { margin-bottom: 14px; padding: 10px 12px; border: 1px solid var(--el-border-color-lighter); border-radius: 8px; background: var(--el-fill-color-lighter); }
.macro-source-association .macro-observation-title { margin-top: 0; }
.macro-source-association-note { color: var(--el-text-color-secondary); font-size: 12px; line-height: 1.55; }
.macro-source-links { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 8px; }
.macro-disclaimer { margin-top: 10px; color: var(--el-text-color-secondary); font-size: 12px; line-height: 1.6; }
.pagination-row { display: flex; justify-content: flex-end; margin-top: 12px; }
@media (max-width: 900px) { .macro-count { width: 100%; margin-left: 0; } .macro-observation-detail { padding-left: 8px; } }
</style>
