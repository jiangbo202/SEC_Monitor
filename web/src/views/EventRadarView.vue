<template>
  <section class="page">
    <div class="page-header">
      <div>
        <h1>{{ t('pages.eventRadar.title') }}</h1>
        <p class="page-subtitle">{{ t('pages.eventRadar.subtitle') }}</p>
      </div>
      <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
    </div>
	<el-alert type="info" :closable="false" show-icon class="event-guide" title="事件雷达优先使用 SEC Item 编号识别具体事项；“待解析”表示当前只有文件事实，系统不会推断事件方向或严重程度。" />
    <el-form :inline="true" :model="filters" class="toolbar">
      <el-form-item label="Ticker"><el-input v-model="filters.ticker" clearable /></el-form-item>
      <el-form-item :label="t('pages.eventRadar.filterMajor')">
        <el-select fit-input-width v-model="filters.filing_type" clearable style="width: 160px">
          <el-option v-for="item in majorTypes" :key="item" :label="item" :value="item" />
        </el-select>
      </el-form-item>
      <el-form-item><el-button :loading="loading" @click="load">{{ t('common.query') }}</el-button></el-form-item>
    </el-form>
    <el-table :data="eventRows" v-loading="loading" border :empty-text="t('pages.eventRadar.empty')">
      <el-table-column label="级别" width="82" fixed>
        <template #default="{ row }"><el-tag :type="priorityType(row.event.priority)" effect="plain">{{ row.event.priority }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="ticker" label="Ticker" width="100"><template #default="{row}"><el-link type="primary" @click="openWorkspace(row.ticker)">{{ row.ticker }}</el-link></template></el-table-column>
      <el-table-column prop="filing_type" :label="t('common.type')" width="105"><template #default="{ row }"><el-tag effect="plain">{{ row.filing_type }}</el-tag></template></el-table-column>
      <el-table-column label="事件事实 / 可能影响" min-width="390"><template #default="{ row }"><div class="event-fact"><div class="event-labels"><el-tag size="small" :type="row.event.status === 'identified' ? 'primary' : 'info'" effect="plain">{{ row.event.category }}</el-tag><span v-if="row.event.item_codes?.length">Item {{ row.event.item_codes.join('、') }}</span></div><strong>{{ row.event.fact }}</strong><small>{{ row.event.impact }}</small></div></template></el-table-column>
      <el-table-column label="建议复核" min-width="210"><template #default="{ row }">{{ row.event.action }}</template></el-table-column>
      <el-table-column prop="filing_date" :label="t('common.filingDate')" width="130">
        <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
      </el-table-column>
      <el-table-column label="证据" width="90" fixed="right"><template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">SEC 原文</el-link></template></el-table-column>
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { apiClient } from '@/api/client'
import type { ApiResponse, Filing, PageResult } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const majorTypes = ['8-K', 'S-1', 'S-3', '424B', '13D', 'SC 13D/A']
const loading = ref(false)
const rows = ref<Filing[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const filters = reactive({ ticker: '', filing_type: '' })
const eventRows = computed(() => rows.value.map((row) => ({ ...row, event: normalizeEvent(row) })))

function normalizeEvent(row: Filing) {
  if (row.event?.fact) return row.event
  const type = String(row.filing_type || '').toUpperCase()
  if (type === '8-K') return { item_codes: [], category: '待解析', priority: '待定', status: 'pending', fact: '8-K 具体事项尚未识别', impact: '当前仅确认公司提交了即时报告，不能据此判断事件方向或严重程度。', action: '打开 SEC 原文，确认 Item 编号、事实发生日和附件内容' }
  return { item_codes: [], category: '其他公告', priority: '待定', status: 'pending', fact: row.title || `${row.filing_type} 文件已提交`, impact: '尚未获得结构化事件事实。', action: '阅读原文并记录可核验事实与影响路径' }
}

function priorityType(value:string){return value==='高'?'danger':value==='中'?'warning':'info'}
function openWorkspace(ticker:string){router.push({path:'/ticker-workspace',query:{ticker}})}

async function load() {
  loading.value = true
  try {
    if (filters.filing_type) {
      const res = await apiClient.get<ApiResponse<PageResult<Filing>>>('/filings', { params: { ...filters, page: page.value, page_size: pageSize, sort_by: 'filing_date', sort_order: 'desc' } })
      rows.value = res.data.data.items
      total.value = res.data.data.total
      return
    }
    const batches = await Promise.all(majorTypes.map((type) => apiClient.get<ApiResponse<PageResult<Filing>>>('/filings', { params: { ticker: filters.ticker, filing_type: type, page: 1, page_size: 20, sort_by: 'filing_date', sort_order: 'desc' } })))
    const merged = batches.flatMap((res) => res.data.data.items).sort((a, b) => new Date(b.filing_date).getTime() - new Date(a.filing_date).getTime())
    rows.value = merged.slice((page.value - 1) * pageSize, page.value * pageSize)
    total.value = merged.length
  } finally {
    loading.value = false
  }
}

function formatDate(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toISOString().slice(0, 10)
}

onMounted(() => {
  const ticker = route.query.ticker
  if (typeof ticker === 'string') filters.ticker = ticker.toUpperCase()
  load()
})
</script>

<style scoped>
.event-guide{margin-bottom:12px}.event-fact{display:grid;gap:4px}.event-fact small,.event-labels span{color:var(--el-text-color-secondary);line-height:1.35}.event-labels{display:flex;align-items:center;gap:7px;font-size:12px}
</style>
