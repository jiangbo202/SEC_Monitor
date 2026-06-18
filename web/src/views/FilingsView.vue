<template>
  <section class="page">
    <div class="page-header">
      <h1>SEC 公告</h1>
      <el-button type="primary" :loading="refreshing" @click="refresh">刷新数据</el-button>
    </div>
    <el-form :inline="true" :model="filters" class="toolbar">
      <el-form-item label="快捷筛选">
        <div class="quick-filter-row">
          <el-check-tag v-for="item in quickFilters" :key="item.label" :checked="activeQuickFilter === item.label" @change="applyQuickFilter(item)">
            {{ item.label }}
          </el-check-tag>
        </div>
      </el-form-item>
      <el-form-item label="Ticker"><el-input v-model="filters.ticker" clearable /></el-form-item>
      <el-form-item label="公司"><el-input v-model="filters.company_name" clearable /></el-form-item>
      <el-form-item>
        <template #label>
          <span class="filing-type-label">
            类型
            <el-tooltip content="查看 SEC Filing 类型说明" placement="top">
              <el-button :icon="QuestionFilled" link class="help-button" @click="typeHelpVisible = true" />
            </el-tooltip>
          </span>
        </template>
        <el-select
          v-model="filters.filing_type"
          clearable
          filterable
          :filter-method="filterFilingTypes"
          placeholder="选择或搜索类型"
          class="filing-type-select"
          @visible-change="onFilingTypeDropdownVisible"
        >
          <el-option
            v-for="item in visibleFilingTypes"
            :key="item.code"
            :label="`${item.code} - ${item.name}`"
            :value="item.code"
          >
            <div class="filing-option">
              <strong>{{ item.code }}</strong>
              <span>{{ item.name }}</span>
            </div>
          </el-option>
        </el-select>
      </el-form-item>
      <el-form-item label="开始"><el-date-picker v-model="filters.date_from" type="date" value-format="YYYY-MM-DD" /></el-form-item>
      <el-form-item label="结束"><el-date-picker v-model="filters.date_to" type="date" value-format="YYYY-MM-DD" /></el-form-item>
      <el-form-item label="通知">
        <el-select v-model="filters.notification_status" clearable style="width: 140px">
          <el-option label="已成功" value="success" />
          <el-option label="发送失败" value="failed" />
          <el-option label="未通知" value="unnotified" />
        </el-select>
      </el-form-item>
      <el-form-item><el-button :loading="loading" @click="load">查询</el-button></el-form-item>
    </el-form>
    <el-table :data="rows" v-loading="loading" border empty-text="暂无公告，可刷新数据或调整筛选条件" @sort-change="onSortChange">
      <el-table-column prop="filing_type" label="类型" width="140" sortable="custom">
        <template #default="{ row }">
          <div class="filing-type-cell">
            <strong>{{ row.filing_type }}</strong>
            <el-tag size="small" :type="filingImportance(row.filing_type).type" effect="plain">
              {{ filingImportance(row.filing_type).label }}
            </el-tag>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="ticker" label="Ticker" width="100" sortable="custom" />
      <el-table-column prop="company_name" label="公司名称" min-width="180" show-overflow-tooltip />
      <el-table-column prop="filing_date" label="Filing Date" width="140" sortable="custom">
        <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
      </el-table-column>
      <el-table-column prop="published_at" label="发布时间" width="170" sortable="custom">
        <template #default="{ row }">{{ formatDateTime(row.published_at) }}</template>
      </el-table-column>
      <el-table-column prop="pulled_at" label="同步时间" width="170" sortable="custom">
        <template #default="{ row }">{{ formatDateTime(row.pulled_at) }}</template>
      </el-table-column>
      <el-table-column prop="title" label="标题" min-width="260">
        <template #default="{ row }">
          <div class="filing-title-cell">
            <el-link class="filing-title-link" :href="row.filing_url" target="_blank" type="primary">
              {{ row.title || `${row.ticker} ${row.filing_type}` }}
            </el-link>
            <span>{{ row.company_name }} · {{ row.ticker }}</span>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="notification_status" label="通知" width="96" align="center">
        <template #default="{ row }">
          <el-tag v-if="row.notification_status" class="compact-status-tag" :type="notificationStatusType(row.notification_status)" effect="plain">
            {{ notificationStatusLabel(row.notification_status) }}
          </el-tag>
          <span v-else class="muted-text">未通知</span>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />

    <el-dialog v-model="typeHelpVisible" title="SEC Filing 类型说明" width="760px">
      <el-table :data="filingTypes" border height="460">
        <el-table-column prop="code" label="类型" width="110" />
        <el-table-column prop="name" label="名称" width="180" />
        <el-table-column prop="description" label="说明" min-width="320" />
      </el-table>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { QuestionFilled } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, Filing, PageResult } from '@/api/types'

interface FilingTypeInfo {
  code: string
  name: string
  description: string
}

const filingTypes: FilingTypeInfo[] = [
  { code: '8-K', name: '重大事件报告', description: '公司发生重大事件时提交，例如并购、管理层变动、重大协议、业绩预告或退市风险。' },
  { code: '10-K', name: '年度报告', description: '公司每个财年提交的完整年度报告，包含业务、风险、财务报表和管理层讨论。' },
  { code: '10-Q', name: '季度报告', description: '公司每个季度提交的报告，包含季度财务数据、经营讨论和风险变化。' },
  { code: 'S-1', name: '证券注册声明', description: '公司 IPO 或公开发行证券前提交的注册文件，披露业务、财务、股权和发行信息。' },
  { code: 'S-3', name: '简式注册声明', description: '符合条件的上市公司用于后续发行证券的简化注册文件。' },
  { code: '424B', name: '招股书补充文件', description: '证券发行相关的最终招股书或补充招股书，通常跟发行价格、规模和条款有关。' },
  { code: 'DEF 14A', name: '正式委托书', description: '股东大会投票材料，包含董事选举、高管薪酬、股东提案等事项。' },
  { code: 'PRE 14A', name: '初步委托书', description: '正式委托书提交前的初稿版本，内容可能还会调整。' },
  { code: '13F-HR', name: '机构持仓报告', description: '大型机构投资者按季度披露的美股持仓报告。' },
  { code: '13D', name: '主动持股披露', description: '投资者持股超过 5% 且可能影响公司控制权或经营时提交。' },
  { code: '13G', name: '被动持股披露', description: '投资者持股超过 5% 但通常为被动投资时提交。' },
  { code: '3', name: '内幕人初始持股', description: '董事、高管或大股东成为报告义务人时披露初始持股。' },
  { code: '4', name: '内幕人交易变动', description: '董事、高管或大股东买卖、授予、行权等持股变化披露。' },
  { code: '5', name: '内幕人年度补充报告', description: '内幕人年度补充披露未在 Form 4 中及时报告的交易。' },
  { code: '6-K', name: '外国发行人报告', description: '外国上市公司向 SEC 提交的重大信息披露或当地市场披露材料。' },
  { code: '20-F', name: '外国发行人年报', description: '外国上市公司提交的年度报告，类似美国公司的 10-K。' },
  { code: 'SC 13D/A', name: '13D 修订', description: '主动持股披露发生重要变化后的修订文件。' },
  { code: 'SC 13G/A', name: '13G 修订', description: '被动持股披露发生重要变化后的修订文件。' },
]

const loading = ref(false)
const refreshing = ref(false)
const route = useRoute()
const typeHelpVisible = ref(false)
const rows = ref<Filing[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const filters = reactive({ ticker: '', company_name: '', filing_type: '', date_from: '', date_to: '', notification_status: '' })
const sort = reactive({ sort_by: 'filing_date', sort_order: 'desc' })
const visibleFilingTypes = ref<FilingTypeInfo[]>(filingTypes)
const activeQuickFilter = ref('')
const quickFilters = [
  { label: '最近 7 天', dateDays: 7 },
  { label: '重大事件', filingType: '8-K' },
  { label: '年报 10-K', filingType: '10-K' },
  { label: '季报 10-Q', filingType: '10-Q' },
  { label: '内幕交易', filingType: '4' },
  { label: '融资 S-1', filingType: 'S-1' }
]

function filterFilingTypes(query: string) {
  const value = query.trim().toLowerCase()
  if (!value) {
    visibleFilingTypes.value = filingTypes
    return
  }
  visibleFilingTypes.value = filingTypes
    .filter((item) => {
      const code = item.code.toLowerCase()
      const name = item.name.toLowerCase()
      const description = item.description.toLowerCase()
      return code.includes(value) || name.includes(value) || description.includes(value)
    })
    .sort((a, b) => Number(!a.code.toLowerCase().startsWith(value)) - Number(!b.code.toLowerCase().startsWith(value)))
}

function onFilingTypeDropdownVisible(visible: boolean) {
  if (visible) {
    visibleFilingTypes.value = filingTypes
  }
}

function formatDate(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toISOString().slice(0, 10)
}

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

function filingImportance(type: string) {
  const normalized = type.toUpperCase()
  if (['8-K', '10-K', '10-Q', 'S-1', 'S-3', '424B'].some((value) => normalized.startsWith(value))) {
    return { label: '重点', type: 'danger' as const }
  }
  if (['4', '3', '5', '13F-HR', '13D', '13G'].some((value) => normalized === value || normalized.startsWith(value))) {
    return { label: '关注', type: 'warning' as const }
  }
  return { label: '普通', type: 'info' as const }
}

function notificationStatusType(status: string) {
  if (status === 'success') return 'success'
  if (status === 'failed') return 'danger'
  return 'info'
}

function notificationStatusLabel(status: string) {
  if (status === 'success') return '成功'
  if (status === 'failed') return '失败'
  return status || '-'
}

function applyQuickFilter(item: { label: string, dateDays?: number, filingType?: string }) {
  activeQuickFilter.value = activeQuickFilter.value === item.label ? '' : item.label
  filters.date_from = ''
  filters.date_to = ''
  filters.filing_type = ''
  if (activeQuickFilter.value) {
    if (item.dateDays) {
      const date = new Date()
      date.setDate(date.getDate() - item.dateDays)
      filters.date_from = date.toISOString().slice(0, 10)
    }
    if (item.filingType) {
      filters.filing_type = item.filingType
    }
  }
  page.value = 1
  load()
}

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<Filing>>>('/filings', { params: { ...filters, ...sort, page: page.value, page_size: pageSize } })
    rows.value = res.data.data.items
    total.value = res.data.data.total
  } finally {
    loading.value = false
  }
}

async function refresh() {
  refreshing.value = true
  try {
    const res = await apiClient.post<ApiResponse<{ new_filings: number }>>('/filings/refresh')
    ElMessage.success(`新增 ${res.data.data.new_filings} 条公告`)
    await load()
  } finally {
    refreshing.value = false
  }
}

function onSortChange({ prop, order }: { prop?: string, order?: string | null }) {
  sort.sort_by = prop || 'filing_date'
  sort.sort_order = order === 'ascending' ? 'asc' : 'desc'
  page.value = 1
  load()
}

onMounted(() => {
  const ticker = route.query.ticker
  if (typeof ticker === 'string') {
    filters.ticker = ticker
  }
  load()
})
</script>
