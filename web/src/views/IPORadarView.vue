<template>
  <section class="page">
    <div class="page-header">
      <div>
        <h1>{{ t('pages.ipoRadar.title') }}</h1>
        <p class="page-subtitle">{{ t('pages.ipoRadar.subtitle') }}</p>
      </div>
      <el-button type="primary" :loading="refreshing" @click="refresh">{{ t('pages.ipoRadar.refresh') }}</el-button>
    </div>

    <div class="quality-strip">
      <el-alert :title="qualitySummary" type="info" :closable="false" show-icon />
      <div class="export-actions">
        <el-button @click="download('/api/exports/ipo-companies.csv')">{{ t('pages.ipoRadar.exportCompanies') }}</el-button>
        <el-button @click="download('/api/exports/ipo-filings.csv')">{{ t('pages.ipoRadar.exportFilings') }}</el-button>
      </div>
    </div>

    <div v-if="health" class="ipo-health-tags">
      <el-tag class="health-tag-action" type="warning" effect="plain" @click="applyAttention('listing_pending')">{{ t('pages.ipoRadar.attention.listingPending', { count: health.pending_listing }) }}</el-tag>
      <el-tag class="health-tag-action" type="warning" effect="plain" @click="applyAttention('parse_failed')">{{ t('pages.ipoRadar.attention.parseFailed', { count: health.unsupported_offering_events }) }}</el-tag>
      <el-tag class="health-tag-action" type="warning" effect="plain" @click="applyAttention('lifecycle_stale')">{{ t('pages.ipoRadar.attention.lifecycleStale', { count: health.stale_lifecycle_checks }) }}</el-tag>
      <el-tag class="health-tag-action" :type="health.failed_notification_batches || health.dead_letter_batches ? 'danger' : 'info'" effect="plain" @click="applyAttention('notification_failed')">{{ t('pages.ipoRadar.attention.notificationFailed', { count: health.failed_notification_batches + health.dead_letter_batches }) }}</el-tag>
      <el-tag type="info" effect="plain">{{ t('pages.ipoRadar.attention.missingMarketMapping', { count: health.missing_market_mapping }) }}</el-tag>
      <el-tag v-if="health.due_retry_batches || health.dead_letter_batches" type="danger" effect="plain">{{ t('pages.ipoRadar.attention.retryQueue', { due: health.due_retry_batches, dead: health.dead_letter_batches }) }}</el-tag>
    </div>

    <el-tabs v-model="activeTab" class="content-tabs" @tab-change="handleTabChange">
      <el-tab-pane :label="t('pages.ipoRadar.tabs.companies')" name="companies">
        <el-form :inline="true" :model="companyFilters" class="toolbar">
          <el-form-item :label="t('common.company')"><el-input v-model="companyFilters.company_name" clearable /></el-form-item>
          <el-form-item label="CIK"><el-input v-model="companyFilters.cik" clearable /></el-form-item>
          <el-form-item :label="t('common.status')">
            <el-select v-model="companyFilters.status" clearable style="width: 160px">
              <el-option v-for="item in ipoStatuses" :key="item.value" :label="item.label" :value="item.value" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('pages.ipoRadar.attention.label')">
            <el-select v-model="companyFilters.attention" clearable style="width: 170px">
              <el-option :label="t('pages.ipoRadar.attention.listingPendingOption')" value="listing_pending" />
              <el-option :label="t('pages.ipoRadar.attention.parseFailedOption')" value="parse_failed" />
              <el-option :label="t('pages.ipoRadar.attention.lifecycleStaleOption')" value="lifecycle_stale" />
              <el-option :label="t('pages.ipoRadar.attention.notificationFailedOption')" value="notification_failed" />
            </el-select>
          </el-form-item>
          <el-form-item><el-button :loading="companiesLoading" @click="loadCompanies">{{ t('common.query') }}</el-button></el-form-item>
        </el-form>

        <el-table
          :data="companies"
          v-loading="companiesLoading"
          border
          :empty-text="t('pages.ipoRadar.emptyCompanies')"
          :default-sort="{ prop: 'latest_update', order: 'descending' }"
          @expand-change="onExpandChange"
          @sort-change="onCompanySortChange"
        >
          <el-table-column type="expand">
            <template #default="{ row }">
              <el-table :data="filingDetails[row.cik] || []" border class="sync-detail-table">
                <el-table-column prop="filing_type" :label="t('common.type')" width="110">
                  <template #default="{ row: filing }"><el-tag type="warning" effect="plain">{{ filing.filing_type }}</el-tag></template>
                </el-table-column>
                <el-table-column prop="filing_date" :label="t('common.filingDate')" width="130">
                  <template #default="{ row: filing }">{{ formatDate(filing.filing_date) }}</template>
                </el-table-column>
                <el-table-column prop="accepted_at" :label="t('pages.ipoRadar.acceptedAt')" width="170">
                  <template #default="{ row: filing }">{{ formatDateTime(filing.accepted_at) }}</template>
                </el-table-column>
                <el-table-column prop="created_at" :label="t('common.syncTime')" width="170">
                  <template #default="{ row: filing }">{{ formatDateTime(filing.created_at) }}</template>
                </el-table-column>
                <el-table-column prop="title" :label="t('common.title')" min-width="280">
                  <template #default="{ row: filing }"><el-link :href="filing.filing_url" target="_blank" type="primary">{{ filing.title || filing.filing_type }}</el-link></template>
                </el-table-column>
                <el-table-column prop="notified_at" :label="t('pages.filings.notification')" width="110" align="center">
                  <template #default="{ row: filing }">
                    <el-tag v-if="filing.notified_at" class="compact-status-tag" type="success" effect="plain">{{ t('status.success') }}</el-tag>
                    <span v-else class="muted-text">{{ t('status.unnotified') }}</span>
                  </template>
                </el-table-column>
              </el-table>
            </template>
          </el-table-column>
          <el-table-column prop="status" :label="t('common.status')" width="130" fixed="left" sortable="custom">
            <template #default="{ row }">
              <el-tooltip :content="statusReasonText(row)" placement="top">
                <el-tag :type="ipoStatusType(row.status)" effect="plain">{{ ipoStatusLabel(row.status) }}</el-tag>
              </el-tooltip>
            </template>
          </el-table-column>
          <el-table-column prop="company_name" :label="t('common.companyName')" min-width="220" fixed="left" show-overflow-tooltip />
          <el-table-column prop="final_ticker" :label="t('pages.ipoRadar.finalTicker')" width="100"><template #default="{ row }">{{ row.final_ticker || row.matched_ticker || '-' }}</template></el-table-column>
          <el-table-column prop="latest_filing_type" :label="t('pages.ipoRadar.latestType')" width="120">
            <template #default="{ row }"><el-tag effect="plain">{{ row.latest_filing_type }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="filing_count" :label="t('pages.ipoRadar.filingCount')" width="90" align="right" />
          <el-table-column prop="latest_update" :label="t('pages.ipoRadar.latestUpdate')" width="170" sortable="custom">
            <template #default="{ row }">{{ formatDateTime(row.latest_accepted_at || row.latest_filing_date) }}</template>
          </el-table-column>
          <el-table-column prop="notified" :label="t('pages.filings.notification')" width="110" align="center">
            <template #default="{ row }">
              <el-tag v-if="row.notified" class="compact-status-tag" type="success" effect="plain">{{ t('status.success') }}</el-tag>
              <span v-else class="muted-text">{{ t('status.unnotified') }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.actions')" width="100" fixed="right">
            <template #default="{ row }">
              <el-button size="small" @click="openCompanyDetail(row)">{{ t('common.details') }}</el-button>
            </template>
          </el-table-column>
        </el-table>
        <el-pagination class="pagination" layout="total, prev, pager, next" :total="companiesTotal" :page-size="pageSize" v-model:current-page="companiesPage" @current-change="loadCompanies" />
      </el-tab-pane>

      <el-tab-pane :label="t('pages.ipoRadar.tabs.filings')" name="filings">
        <el-form :inline="true" :model="filingFilters" class="toolbar">
          <el-form-item :label="t('common.company')"><el-input v-model="filingFilters.company_name" clearable /></el-form-item>
          <el-form-item label="CIK"><el-input v-model="filingFilters.cik" clearable /></el-form-item>
          <el-form-item :label="t('common.type')"><el-input v-model="filingFilters.filing_type" clearable placeholder="S-1, EFFECT, 424B4" /></el-form-item>
          <el-form-item :label="t('pages.filings.notification')">
            <el-select v-model="filingFilters.notified" clearable style="width: 150px">
              <el-option :label="t('status.success')" value="yes" />
              <el-option :label="t('status.unnotified')" value="no" />
            </el-select>
          </el-form-item>
          <el-form-item><el-button :loading="filingsLoading" @click="loadFilings">{{ t('common.query') }}</el-button></el-form-item>
        </el-form>

        <el-table :data="filings" v-loading="filingsLoading" border :empty-text="t('pages.ipoRadar.emptyFilings')">
          <el-table-column prop="filing_type" :label="t('common.type')" width="110">
            <template #default="{ row }"><el-tag type="warning" effect="plain">{{ row.filing_type }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="company_name" :label="t('common.companyName')" min-width="210" show-overflow-tooltip />
          <el-table-column prop="cik" label="CIK" width="130" />
          <el-table-column prop="filing_date" :label="t('common.filingDate')" width="130">
            <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
          </el-table-column>
          <el-table-column prop="accepted_at" :label="t('pages.ipoRadar.acceptedAt')" width="170">
            <template #default="{ row }">{{ formatDateTime(row.accepted_at) }}</template>
          </el-table-column>
          <el-table-column prop="created_at" :label="t('common.syncTime')" width="170">
            <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
          </el-table-column>
          <el-table-column prop="title" :label="t('common.title')" min-width="300">
            <template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">{{ row.title || row.filing_type }}</el-link></template>
          </el-table-column>
          <el-table-column prop="notified_at" :label="t('pages.filings.notification')" width="110" align="center">
            <template #default="{ row }">
              <el-tag v-if="row.notified_at" class="compact-status-tag" type="success" effect="plain">{{ t('status.success') }}</el-tag>
              <span v-else class="muted-text">{{ t('status.unnotified') }}</span>
            </template>
          </el-table-column>
        </el-table>
        <el-pagination class="pagination" layout="total, prev, pager, next" :total="filingsTotal" :page-size="pageSize" v-model:current-page="filingsPage" @current-change="loadFilings" />
      </el-tab-pane>
    </el-tabs>

    <el-drawer v-model="detailVisible" :title="selectedCompany?.company_name || t('pages.ipoRadar.companyDetail')" size="640px">
      <div v-if="selectedCompany" class="detail-drawer-body">
        <el-descriptions :column="1" border>
          <el-descriptions-item label="CIK">{{ selectedCompany.cik }}</el-descriptions-item>
          <el-descriptions-item :label="t('common.status')">
            <el-tag :type="ipoStatusType(selectedCompany.status)" effect="plain">{{ ipoStatusLabel(selectedCompany.status) }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.statusReason')">{{ statusReasonText(selectedCompany) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.statusSource')">{{ statusSourceLabel(selectedCompany.status_source) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.automaticTicker')">{{ selectedCompany.automatic_ticker || '-' }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.automaticExchange')">{{ selectedCompany.automatic_exchange || '-' }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.automaticOfferPrice')">{{ selectedCompany.automatic_offer_price ? `$${selectedCompany.automatic_offer_price}` : '-' }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.automaticSharesOffered')">{{ formatNumber(selectedCompany.automatic_shares_offered) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.grossProceeds')">{{ formatMoney(selectedCompany.automatic_gross_proceeds) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.finalTicker')">{{ selectedCompany.final_ticker || selectedCompany.matched_ticker || '-' }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.exchange')">{{ selectedCompany.exchange || '-' }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.offerPrice')">{{ selectedCompany.offer_price ? `$${selectedCompany.offer_price}` : '-' }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.sharesOffered')">{{ formatNumber(selectedCompany.shares_offered) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.grossProceeds')">{{ formatMoney(selectedCompany.gross_proceeds) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.lifecycleCheckedAt')">{{ formatDateTime(selectedCompany.lifecycle_checked_at) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.listedVerifiedAt')">{{ formatDateTime(selectedCompany.listed_verified_at) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.listingDate')">{{ formatDate(selectedCompany.listing_date) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.marketDataSource')">{{ marketSourceLabel(selectedCompany) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.ipoRadar.marketDataUpdatedAt')">{{ formatDateTime(selectedCompany.market_data_updated_at) }}</el-descriptions-item>
        </el-descriptions>

        <el-divider>{{ t('pages.ipoRadar.offeringEvents') }}</el-divider>
        <el-table :data="selectedCompany ? offeringEvents[selectedCompany.cik] || [] : []" border :empty-text="t('pages.ipoRadar.emptyOfferingEvents')">
          <el-table-column prop="offering_type" :label="t('pages.ipoRadar.eventType')" width="120">
            <template #default="{ row }"><el-tag :type="offeringEventTagType(row.offering_type)" effect="plain">{{ offeringEventLabel(row.offering_type) }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="offer_price" :label="t('pages.ipoRadar.offerPrice')" width="95" align="right">
            <template #default="{ row }">{{ row.offer_price ? `$${row.offer_price}` : '-' }}</template>
          </el-table-column>
          <el-table-column prop="shares_offered" :label="t('pages.ipoRadar.sharesOffered')" width="130" align="right">
            <template #default="{ row }">{{ formatNumber(row.shares_offered) }}</template>
          </el-table-column>
          <el-table-column prop="gross_proceeds" :label="t('pages.ipoRadar.grossProceeds')" min-width="160" align="right">
            <template #default="{ row }">{{ formatMoney(row.gross_proceeds) }}</template>
          </el-table-column>
          <el-table-column prop="parse_message" :label="t('pages.ipoRadar.parseMessage')" min-width="180">
            <template #default="{ row }">{{ row.parse_message || '-' }}</template>
          </el-table-column>
          <el-table-column prop="filing_date" :label="t('common.filingDate')" width="120">
            <template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">{{ formatDate(row.filing_date) }}</el-link></template>
          </el-table-column>
          <el-table-column prop="notified_at" :label="t('pages.filings.notification')" width="100" align="center">
            <template #default="{ row }">
              <el-tag v-if="row.notified_at" type="success" effect="plain">{{ t('status.success') }}</el-tag>
              <span v-else class="muted-text">-</span>
            </template>
          </el-table-column>
        </el-table>

        <el-divider>{{ t('pages.ipoRadar.manualOverride') }}</el-divider>
        <el-form :model="overrideForm" label-width="120px">
          <el-form-item :label="t('common.status')">
            <el-select v-model="overrideForm.status_override" clearable>
              <el-option v-for="item in ipoStatuses" :key="item.value" :label="item.label" :value="item.value" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('pages.ipoRadar.finalTicker')">
            <el-input v-model="overrideForm.final_ticker" />
          </el-form-item>
          <el-form-item :label="t('pages.ipoRadar.exchange')">
            <el-input v-model="overrideForm.exchange" />
          </el-form-item>
          <el-form-item :label="t('pages.ipoRadar.offerPrice')">
            <el-input v-model="overrideForm.offer_price" inputmode="decimal" />
          </el-form-item>
          <el-form-item :label="t('pages.ipoRadar.sharesOffered')">
            <el-input-number v-model="overrideForm.shares_offered" :min="0" :precision="0" controls-position="right" />
          </el-form-item>
          <el-form-item :label="t('pages.ipoRadar.listingDate')">
            <el-date-picker v-model="overrideForm.listing_date" type="date" value-format="YYYY-MM-DD" clearable />
          </el-form-item>
          <el-form-item :label="t('pages.ipoRadar.overrideNote')">
            <el-input v-model="overrideForm.note" type="textarea" :rows="2" />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" :loading="savingOverride" @click="saveOverride">{{ t('common.save') }}</el-button>
          </el-form-item>
        </el-form>

        <el-divider>{{ t('pages.ipoRadar.timeline') }}</el-divider>
        <el-table :data="selectedCompany ? filingDetails[selectedCompany.cik] || [] : []" border>
          <el-table-column prop="filing_type" :label="t('common.type')" width="100">
            <template #default="{ row }"><el-tag type="warning" effect="plain">{{ row.filing_type }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="accepted_at" :label="t('pages.ipoRadar.acceptedAt')" width="170">
            <template #default="{ row }">{{ formatDateTime(row.accepted_at) }}</template>
          </el-table-column>
          <el-table-column prop="title" :label="t('common.title')" min-width="220">
            <template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">{{ row.title || row.filing_type }}</el-link></template>
          </el-table-column>
        </el-table>
      </div>
    </el-drawer>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, IPOCompany, IPOFiling, IPOOfferingEvent, IPORadarHealth, IPORadarRefreshResult, PageResult } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const ipoStatuses = [
  { value: 'new', label: t('pages.ipoRadar.statuses.new') },
  { value: 'updating', label: t('pages.ipoRadar.statuses.updating') },
  { value: 'effective', label: t('pages.ipoRadar.statuses.effective') },
  { value: 'priced', label: t('pages.ipoRadar.statuses.priced') },
  { value: 'listing_pending', label: t('pages.ipoRadar.statuses.listing_pending') },
  { value: 'listed', label: t('pages.ipoRadar.statuses.listed') },
  { value: 'withdrawn', label: t('pages.ipoRadar.statuses.withdrawn') },
  { value: 'stale', label: t('pages.ipoRadar.statuses.stale') }
]
const refreshing = ref(false)
const savingOverride = ref(false)
const activeTab = ref('companies')
const filingsLoading = ref(false)
const companiesLoading = ref(false)
const filings = ref<IPOFiling[]>([])
const companies = ref<IPOCompany[]>([])
const health = ref<IPORadarHealth | null>(null)
const filingsTotal = ref(0)
const companiesTotal = ref(0)
const filingsPage = ref(1)
const companiesPage = ref(1)
const pageSize = 20
const filingFilters = reactive({ company_name: '', cik: '', filing_type: '', notified: '' })
const companyFilters = reactive({ company_name: '', cik: '', status: '', attention: '' })
const companySort = reactive({ sort_by: '', sort_order: '' })
const filingDetails = ref<Record<string, IPOFiling[]>>({})
const offeringEvents = ref<Record<string, IPOOfferingEvent[]>>({})
const selectedCompany = ref<IPOCompany | null>(null)
const detailVisible = ref(false)
const overrideForm = reactive({ status_override: '', final_ticker: '', exchange: '', offer_price: '', shares_offered: 0, listing_date: '', note: '' })

const qualitySummary = computed(() => {
  const total = companiesTotal.value
  const incomplete = companies.value.filter((item) => item.status_confidence === 'medium').length
  return t('pages.ipoRadar.qualitySummary', { total, incomplete })
})

async function loadFilings() {
  filingsLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<IPOFiling>>>('/ipo-filings', { params: { ...filingFilters, page: filingsPage.value, page_size: pageSize } })
    filings.value = res.data.data.items
    filingsTotal.value = res.data.data.total
  } finally {
    filingsLoading.value = false
  }
}

async function loadCompanies() {
  companiesLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<IPOCompany>>>('/ipo-companies', { params: { ...companyFilters, ...companySort, page: companiesPage.value, page_size: pageSize } })
    companies.value = res.data.data.items
    companiesTotal.value = res.data.data.total
  } finally {
    companiesLoading.value = false
  }
}

async function loadHealth() {
  const res = await apiClient.get<ApiResponse<IPORadarHealth>>('/ipo-health')
  health.value = res.data.data
}

async function applyAttention(attention: string) {
  activeTab.value = 'companies'
  companyFilters.attention = attention
  companiesPage.value = 1
  await loadCompanies()
}

function onCompanySortChange({ prop, order }: { prop?: string, order?: string | null }) {
  if (!order) {
    companySort.sort_by = ''
    companySort.sort_order = ''
    companiesPage.value = 1
    loadCompanies()
    return
  }
  companySort.sort_by = prop === 'status' ? 'status' : 'latest_update'
  companySort.sort_order = order === 'ascending' ? 'asc' : 'desc'
  companiesPage.value = 1
  loadCompanies()
}

async function handleTabChange() {
  if (activeTab.value === 'companies' && companies.value.length === 0) {
    await loadCompanies()
  }
  if (activeTab.value === 'filings' && filings.value.length === 0) {
    await loadFilings()
  }
}

async function onExpandChange(row: IPOCompany) {
  if (filingDetails.value[row.cik]) return
  const res = await apiClient.get<ApiResponse<PageResult<IPOFiling>>>('/ipo-filings', { params: { cik: row.cik, sort: 'timeline', page: 1, page_size: 100 } })
  filingDetails.value = { ...filingDetails.value, [row.cik]: res.data.data.items }
}

async function openCompanyDetail(row: IPOCompany) {
  selectedCompany.value = row
  overrideForm.status_override = row.status_source === 'manual' ? row.status : ''
  overrideForm.final_ticker = row.override_final_ticker || ''
  overrideForm.exchange = row.override_exchange || ''
  overrideForm.offer_price = row.override_offer_price || ''
  overrideForm.shares_offered = row.override_shares_offered || 0
  overrideForm.listing_date = row.override_listing_date ? row.override_listing_date.slice(0, 10) : ''
  overrideForm.note = row.override_note || ''
  detailVisible.value = true
  await Promise.all([onExpandChange(row), loadOfferingEvents(row.cik)])
}

async function loadOfferingEvents(cik: string) {
  const res = await apiClient.get<ApiResponse<PageResult<IPOOfferingEvent>>>(`/ipo-companies/${encodeURIComponent(cik)}/offerings`, { params: { page: 1, page_size: 100 } })
  offeringEvents.value = { ...offeringEvents.value, [cik]: res.data.data.items }
}

async function saveOverride() {
  if (!selectedCompany.value) return
  if (overrideForm.offer_price && (!Number.isFinite(Number(overrideForm.offer_price)) || Number(overrideForm.offer_price) <= 0)) {
    ElMessage.error(t('pages.ipoRadar.invalidOfferPrice'))
    return
  }
  savingOverride.value = true
  try {
    await apiClient.put(`/ipo-companies/${encodeURIComponent(selectedCompany.value.cik)}/override`, overrideForm)
    ElMessage.success(t('messages.saved'))
    await loadCompanies()
    const updated = companies.value.find((item) => item.cik === selectedCompany.value?.cik)
    if (updated) {
      selectedCompany.value = updated
    }
  } finally {
    savingOverride.value = false
  }
}

function formatNumber(value?: number | null) {
  return value && value > 0 ? value.toLocaleString() : '-'
}

function formatMoney(value?: string | null) {
  if (!value) return '-'
  const amount = Number(value)
  return Number.isFinite(amount) ? `$${amount.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}` : `$${value}`
}

function marketSourceLabel(row: IPOCompany) {
  if (!row.market_data_source) return '-'
  return `${row.market_data_source === 'manual' ? t('pages.ipoRadar.marketSources.manual') : 'SEC'} · ${row.market_data_confidence || '-'}`
}

function ipoStatusLabel(status: string) {
  return t(`pages.ipoRadar.statuses.${status}`)
}

function ipoStatusType(status: string) {
  if (status === 'new') return 'success'
  if (status === 'updating') return 'primary'
  if (status === 'effective') return 'warning'
  if (status === 'priced') return 'warning'
  if (status === 'listing_pending') return 'warning'
  if (status === 'listed') return 'success'
  if (status === 'withdrawn') return 'danger'
  return 'info'
}

function offeringEventLabel(type: string) {
  return t(`pages.ipoRadar.offeringEventTypes.${type}`)
}

function offeringEventTagType(type: string) {
  if (type === 'initial') return 'success'
  if (type === 'correction') return 'warning'
  if (type === 'follow_on') return 'primary'
  return 'info'
}

function statusReasonText(row: IPOCompany) {
  const confidence = row.status_confidence ? t(`pages.ipoRadar.confidence.${row.status_confidence}`) : ''
  return confidence ? `${row.status_reason || '-'} · ${confidence}` : row.status_reason || '-'
}

function statusSourceLabel(source?: string) {
  return source === 'manual' ? t('pages.ipoRadar.sources.manual') : t('pages.ipoRadar.sources.system')
}

function download(url: string) {
  window.location.href = url
}

async function refresh() {
  refreshing.value = true
  try {
    const res = await apiClient.post<ApiResponse<IPORadarRefreshResult>>('/ipo-filings/refresh', null, { timeout: 120000 })
    ElMessage.success(t('messages.ipoRefreshDone', { count: res.data.data.new_filings, notified: res.data.data.notified }))
    filingDetails.value = {}
    offeringEvents.value = {}
    filingsPage.value = 1
    companiesPage.value = 1
    if (activeTab.value === 'companies') {
      await loadCompanies()
    } else {
      await loadFilings()
    }
    await loadHealth()
  } finally {
    refreshing.value = false
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

onMounted(async () => {
  await Promise.all([loadCompanies(), loadHealth()])
})
</script>

<style scoped>
.ipo-health-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 16px;
}

.health-tag-action {
  cursor: pointer;
}
</style>
