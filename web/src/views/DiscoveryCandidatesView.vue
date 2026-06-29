<template>
  <div>
    <div class="page-header">
      <div>
        <h1>小盘股候选</h1>
        <p>基于公开 SEC 文件、财务指标、内幕交易和融资风险生成的研究候选列表。</p>
      </div>
      <el-button :loading="loading" @click="load">刷新</el-button>
    </div>

    <el-card shadow="never" class="filter-card">
      <el-form :inline="true" :model="filters">
        <el-form-item label="等级">
          <el-select v-model="filters.grade" clearable style="width: 120px">
            <el-option label="A级" value="A" />
            <el-option label="B级" value="B" />
            <el-option label="排除" value="excluded" />
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
        <el-form-item>
          <el-button type="primary" @click="search">查询</el-button>
          <el-button @click="reset">重置</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-table :data="rows" v-loading="loading" border empty-text="暂无候选">
      <el-table-column prop="grade" label="等级" width="90" align="center">
        <template #default="{ row }">
          <el-tag :type="gradeTagType(row.grade)" effect="dark">{{ gradeLabel(row.grade) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="ticker" label="Ticker" width="110" />
      <el-table-column prop="total_score" label="总分" width="90" align="right" />
      <el-table-column prop="market_cap_usd" label="市值" width="130" align="right">
        <template #default="{ row }">{{ formatUSD(row.market_cap_usd) }}</template>
      </el-table-column>
      <el-table-column prop="revenue_growth_pct" label="收入增长" width="110" align="right">
        <template #default="{ row }">{{ formatPct(row.revenue_growth_pct) }}</template>
      </el-table-column>
      <el-table-column prop="cash_runway_months" label="现金 runway" width="120" align="right">
        <template #default="{ row }">{{ formatMonths(row.cash_runway_months) }}</template>
      </el-table-column>
      <el-table-column label="核心信号" min-width="220">
        <template #default="{ row }">
          <el-space wrap>
            <el-tag v-if="row.recent_qualified_insider" type="success" effect="plain">内部人买入</el-tag>
            <el-tag v-if="row.active_blocks_a" type="danger" effect="plain">阻断A</el-tag>
            <el-tag v-if="row.active_blocks_b" type="danger" effect="plain">阻断B</el-tag>
            <el-tag v-if="!row.active_blocks_a && !row.active_blocks_b" type="info" effect="plain">无阻断风险</el-tag>
          </el-space>
        </template>
      </el-table-column>
      <el-table-column label="分项" min-width="260">
        <template #default="{ row }">
          增长 {{ row.revenue_growth_score }} / 现金 {{ row.cash_runway_score }} / 内幕 {{ row.insider_score }} / 稀释 {{ row.dilution_risk_score }}
        </template>
      </el-table-column>
      <el-table-column prop="reason_code" label="原因" min-width="140" />
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
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, CandidateScore, PageResult } from '@/api/types'

const rows = ref<CandidateScore[]>([])
const loading = ref(false)
const page = ref(1)
const pageSize = 20
const total = ref(0)
const filters = reactive({ grade: '', ticker: '', eligible_a: '', eligible_b: '' })

function requestParams() {
  const params: Record<string, string | number> = { page: page.value, page_size: pageSize }
  if (filters.grade) params.grade = filters.grade
  if (filters.ticker) params.ticker = filters.ticker.trim().toUpperCase()
  if (filters.eligible_a) params.eligible_a = filters.eligible_a
  if (filters.eligible_b) params.eligible_b = filters.eligible_b
  return params
}

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<CandidateScore>>>('/discovery/candidates', { params: requestParams() })
    rows.value = res.data.data.items || []
    total.value = res.data.data.total || 0
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '加载候选失败')
  } finally {
    loading.value = false
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
  search()
}

function onPageChange(next: number) {
  page.value = next
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

function formatUSD(value: number) {
  if (!value) return '-'
  if (value >= 1_000_000_000) return `$${(value / 1_000_000_000).toFixed(2)}B`
  return `$${(value / 1_000_000).toFixed(1)}M`
}

function formatPct(value: number) {
  return Number.isFinite(value) ? `${value.toFixed(1)}%` : '-'
}

function formatMonths(value: number) {
  return Number.isFinite(value) && value > 0 ? `${value.toFixed(1)} 月` : '-'
}

onMounted(load)
</script>
