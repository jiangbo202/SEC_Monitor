<template>
  <section class="page">
    <div class="page-header">
      <h1>{{ t('pages.notificationLogs.title') }}</h1>
      <el-button :loading="loading" @click="loadActive">{{ t('common.refresh') }}</el-button>
    </div>

    <el-tabs v-model="activeTab" class="content-tabs" @tab-change="loadActive">
      <el-tab-pane :label="t('pages.notificationLogs.batches')" name="batches">
        <el-form :inline="true" :model="filters" class="toolbar">
          <el-form-item :label="t('pages.notificationLogs.source')">
            <el-select v-model="filters.source" clearable style="width: 150px">
              <el-option :label="t('pages.notificationLogs.sources.filing')" value="filing" />
              <el-option :label="t('pages.notificationLogs.sources.ipo')" value="ipo" />
              <el-option :label="t('pages.notificationLogs.sources.ipoOffering')" value="ipo_offering" />
              <el-option :label="t('pages.notificationLogs.sources.candidate')" value="candidate" />
			  <el-option label="交易计划" value="trade_setup" />
			  <el-option label="分析师共识" value="analyst_rating" />
			  <el-option label="财报预告" value="earnings_preview" />
			  <el-option label="运行健康" value="operational_health" />
			  <el-option label="系统测试" value="system_test" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('common.status')">
            <el-select v-model="filters.status" clearable style="width: 150px">
              <el-option :label="t('pages.notificationLogs.statuses.sent')" value="sent" />
              <el-option :label="t('pages.notificationLogs.statuses.suppressed')" value="suppressed" />
              <el-option :label="t('pages.notificationLogs.statuses.failed')" value="failed" />
              <el-option :label="t('pages.notificationLogs.statuses.deadLetter')" value="dead_letter" />
            </el-select>
          </el-form-item>
          <el-form-item><el-button @click="queryBatches">{{ t('common.query') }}</el-button></el-form-item>
          <el-form-item><el-button type="warning" plain :loading="requeueingFailed" @click="requeueFailed">重新入队失败通知（最多 100 条）</el-button></el-form-item>
        </el-form>

        <el-table :data="batches" v-loading="loading" border :empty-text="t('pages.notificationLogs.emptyBatches')" @expand-change="loadBatchItems">
          <el-table-column type="expand" width="44">
            <template #default="{ row }">
              <el-table :data="batchItems[row.id] || []" border class="sync-detail-table">
                <el-table-column prop="event_at" :label="t('common.time')" width="170"><template #default="{ row: item }">{{ formatDateTime(item.event_at) }}</template></el-table-column>
                <el-table-column prop="entity_kind" :label="t('pages.notificationLogs.entityKind')" width="110"><template #default="{ row: item }"><el-tag type="info" effect="plain">{{ entityKindLabel(item.entity_kind) }}</el-tag></template></el-table-column>
                <el-table-column prop="ticker" label="Ticker" width="90"><template #default="{ row: item }">{{ item.ticker || '-' }}</template></el-table-column>
                <el-table-column prop="company_name" :label="t('common.companyName')" min-width="180" show-overflow-tooltip />
                <el-table-column prop="filing_type" :label="t('common.type')" width="100"><template #default="{ row: item }">{{ notificationItemTypeLabel(item) }}</template></el-table-column>
                <el-table-column prop="reason" :label="t('pages.notificationLogs.reason')" width="150"><template #default="{ row: item }"><el-tag :type="reasonType(item.reason)" effect="plain">{{ reasonLabel(item.reason) }}</el-tag></template></el-table-column>
                <el-table-column prop="title" :label="t('common.title')" min-width="240">
                  <template #default="{ row: item }">
                    <el-link v-if="item.filing_url" :href="item.filing_url" target="_blank" type="primary">{{ item.title || item.filing_type }}</el-link>
                    <span v-else>{{ item.title || item.filing_type }}</span>
                  </template>
                </el-table-column>
              </el-table>
            </template>
          </el-table-column>
          <el-table-column prop="created_at" :label="t('common.time')" width="170"><template #default="{ row }">{{ formatDateTime(row.created_at) }}</template></el-table-column>
          <el-table-column prop="source" :label="t('pages.notificationLogs.source')" width="120"><template #default="{ row }"><el-tag :type="sourceTagType(row.source)" effect="plain">{{ sourceLabel(row.source) }}</el-tag></template></el-table-column>
          <el-table-column prop="trigger" :label="t('common.source')" width="120" />
          <el-table-column prop="item_count" :label="t('pages.notificationLogs.totalCount')" width="85" align="right" />
          <el-table-column prop="sent_count" :label="t('pages.notificationLogs.sentCount')" width="85" align="right" />
          <el-table-column prop="suppressed_count" :label="t('pages.notificationLogs.suppressedCount')" width="90" align="right" />
          <el-table-column prop="status" :label="t('common.status')" width="120"><template #default="{ row }"><el-tag :type="batchStatusType(row.status)" effect="plain">{{ batchStatusLabel(row.status) }}</el-tag></template></el-table-column>
          <el-table-column prop="retry_count" :label="t('common.retryCount')" width="85" align="right" />
          <el-table-column prop="next_retry_at" :label="t('pages.notificationLogs.nextRetryAt')" width="170"><template #default="{ row }">{{ formatDateTime(row.next_retry_at) }}</template></el-table-column>
          <el-table-column prop="suppression_summary" :label="t('pages.notificationLogs.summary')" min-width="190" show-overflow-tooltip />
		  <el-table-column prop="message_text" label="投递内容" min-width="240" show-overflow-tooltip />
          <el-table-column prop="error_message" :label="t('common.error')" min-width="180" show-overflow-tooltip />
          <el-table-column :label="t('common.actions')" width="105" fixed="right">
            <template #default="{ row }">
              <el-button v-if="row.status === 'failed' || row.status === 'dead_letter'" type="primary" link @click="requeue(row)">{{ t('pages.notificationLogs.requeue') }}</el-button>
            </template>
          </el-table-column>
        </el-table>
        <el-pagination class="pagination" layout="total, prev, pager, next" :total="batchesTotal" :page-size="pageSize" v-model:current-page="batchesPage" @current-change="loadBatches" />
      </el-tab-pane>

      <el-tab-pane :label="t('pages.notificationLogs.legacy')" name="legacy">
        <el-table :data="legacyRows" v-loading="loading" border :empty-text="t('pages.notificationLogs.empty')">
          <el-table-column prop="created_at" :label="t('common.time')" width="170"><template #default="{ row }">{{ formatDateTime(row.created_at) }}</template></el-table-column>
          <el-table-column prop="filing_id" label="Filing ID" min-width="180" show-overflow-tooltip />
          <el-table-column prop="channel" :label="t('pages.notificationLogs.channel')" width="100"><template #default="{ row }"><el-tag type="info" effect="plain">{{ row.channel }}</el-tag></template></el-table-column>
          <el-table-column prop="target" :label="t('common.target')" min-width="150" show-overflow-tooltip />
          <el-table-column prop="status" :label="t('common.status')" width="120"><template #default="{ row }"><el-tag :type="row.status === 'success' ? 'success' : 'danger'" effect="plain">{{ row.status === 'success' ? t('status.success') : t('status.failed') }}</el-tag></template></el-table-column>
          <el-table-column prop="retry_count" :label="t('common.retryCount')" width="80" align="right" />
          <el-table-column prop="error_message" :label="t('common.error')" min-width="220" show-overflow-tooltip />
        </el-table>
        <el-pagination class="pagination" layout="total, prev, pager, next" :total="legacyTotal" :page-size="pageSize" v-model:current-page="legacyPage" @current-change="loadLegacy" />
      </el-tab-pane>
    </el-tabs>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, NotificationBatch, NotificationBatchItem, NotificationLog, PageResult } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const route = useRoute()
const activeTab = ref('batches')
const loading = ref(false)
const batches = ref<NotificationBatch[]>([])
const batchItems = ref<Record<number, NotificationBatchItem[]>>({})
const legacyRows = ref<NotificationLog[]>([])
const batchesTotal = ref(0)
const legacyTotal = ref(0)
const batchesPage = ref(1)
const legacyPage = ref(1)
const pageSize = 20
const filters = reactive({ source: '', status: '' })
const requeueingFailed = ref(false)

async function loadBatches() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<NotificationBatch>>>('/notification-batches', { params: { ...filters, page: batchesPage.value, page_size: pageSize } })
    batches.value = res.data.data.items
    batchesTotal.value = res.data.data.total
  } finally { loading.value = false }
}

async function loadLegacy() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<NotificationLog>>>('/notification-logs', { params: { page: legacyPage.value, page_size: pageSize } })
    legacyRows.value = res.data.data.items
    legacyTotal.value = res.data.data.total
  } finally { loading.value = false }
}

async function loadBatchItems(row: NotificationBatch) {
  if (batchItems.value[row.id]) return
  const res = await apiClient.get<ApiResponse<PageResult<NotificationBatchItem>>>(`/notification-batches/${row.id}/items`, { params: { page: 1, page_size: 200 } })
  batchItems.value = { ...batchItems.value, [row.id]: res.data.data.items }
}

async function requeue(row: NotificationBatch) {
  loading.value = true
  try {
    await apiClient.post<ApiResponse<NotificationBatch>>(`/notification-batches/${row.id}/retry`)
    await loadBatches()
  } finally { loading.value = false }
}

async function requeueFailed() {
  requeueingFailed.value = true
  try {
    const response = await apiClient.post<ApiResponse<{ requeued: number; skipped: number }>>('/notification-batches/requeue-failed', { limit: 100 })
    const result = response.data.data
    ElMessage.success(`已重新入队 ${result.requeued} 条${result.skipped ? `，跳过 ${result.skipped} 条` : ''}；将由定时重试任务投递`)
    await loadBatches()
  } finally {
    requeueingFailed.value = false
  }
}

function loadActive() { return activeTab.value === 'batches' ? loadBatches() : loadLegacy() }
function queryBatches() { batchesPage.value = 1; return loadBatches() }
function sourceLabel(value: string) {
  if (value === 'candidate') return t('pages.notificationLogs.sources.candidate')
  if (value === 'ipo') return t('pages.notificationLogs.sources.ipo')
  if (value === 'ipo_offering') return t('pages.notificationLogs.sources.ipoOffering')
	if (value === 'trade_setup') return '交易计划'
	if (value === 'analyst_rating') return '分析师共识'
	if (value === 'earnings_preview') return '财报预告'
	if (value === 'operational_health') return '运行健康'
	if (value === 'system_test') return '系统测试'
  return t('pages.notificationLogs.sources.filing')
}
function sourceTagType(value: string) {
  if (value === 'candidate') return 'success'
  if (value === 'ipo' || value === 'ipo_offering') return 'warning'
	if (value === 'operational_health') return 'danger'
	if (value === 'trade_setup') return 'success'
  return 'info'
}
function batchStatusLabel(value: string) { return t(`pages.notificationLogs.statuses.${value === 'dead_letter' ? 'deadLetter' : value}`) }
function batchStatusType(value: string) { return value === 'sent' ? 'success' : value === 'failed' || value === 'dead_letter' ? 'danger' : 'warning' }
function reasonLabel(value: string) { return t(`pages.notificationLogs.reasons.${value}`) }
function reasonType(value: string) { return value === 'eligible' ? 'success' : value === 'delivery_failed' ? 'danger' : 'warning' }
function entityKindLabel(value: string) {
  if (value === 'candidate') return t('pages.notificationLogs.entityKinds.candidate')
  if (value === 'ipo_filing') return t('pages.notificationLogs.entityKinds.ipoFiling')
	if (value === 'trade_setup') return '交易计划'
	if (value === 'analyst_rating') return '分析师共识'
	if (value === 'earnings_preview') return '财报预告'
	if (value === 'operational_report') return '运行健康'
	if (value === 'connection_test') return '连接测试'
  return t('pages.notificationLogs.entityKinds.filing')
}
function notificationItemTypeLabel(item: NotificationBatchItem) {
  if (item.entity_kind === 'candidate' && (item.filing_type === 'A' || item.filing_type === 'B')) {
    return t('pages.notificationLogs.candidateGrade', { grade: item.filing_type })
  }
  return item.filing_type || '-'
}
function formatDateTime(value?: string | null) { if (!value) return '-'; const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString() }

onMounted(() => {
  const status = route.query.status
  if (typeof status === 'string' && ['sent', 'suppressed', 'failed', 'dead_letter'].includes(status)) {
    filters.status = status
  }
  const source = route.query.source
	if (typeof source === 'string' && ['filing', 'ipo', 'ipo_offering', 'candidate', 'trade_setup', 'analyst_rating', 'earnings_preview', 'operational_health', 'system_test'].includes(source)) {
    filters.source = source
  }
  return loadBatches()
})
</script>
