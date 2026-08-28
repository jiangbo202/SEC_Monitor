<template>
  <section class="page insider-page">
    <div class="page-header">
      <div><h1>{{ t('pages.insiderTrading.title') }}</h1><p class="page-subtitle">仅展示当前小盘候选或启用监控标的的本地 Form 4 交易事实；页面查询不会访问 SEC。</p></div>
      <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
    </div>
    <div class="metric-strip">
      <div><span>交易记录</span><strong>{{ summary.transactions }}</strong><small>{{ summary.issuers }} 家发行人</small></div>
      <div><span>取得 / 买入</span><strong class="positive">{{ summary.purchases }}</strong><small>{{ money(summary.buy_value_usd) }}</small></div>
      <div><span>处置 / 卖出</span><strong class="negative">{{ summary.sales }}</strong><small>{{ money(summary.sell_value_usd) }}</small></div>
      <div><span>披露金额净额</span><strong :class="summary.net_value_usd >= 0 ? 'positive' : 'negative'">{{ signedMoney(summary.net_value_usd) }}</strong><small>取得金额 − 处置金额</small></div>
    </div>
    <el-form :inline="true" :model="filters" class="toolbar compact-toolbar">
      <el-form-item label="Ticker"><el-input v-model="filters.ticker" clearable @keyup.enter="query" /></el-form-item>
      <el-form-item label="方向"><el-select fit-input-width v-model="filters.direction" clearable><el-option label="取得 / 买入" value="buy" /><el-option label="处置 / 卖出" value="sell" /></el-select></el-form-item>
      <el-form-item label="证据"><el-select fit-input-width v-model="filters.qualified" clearable><el-option label="计入研究" value="true" /><el-option label="仅供复核" value="false" /></el-select></el-form-item>
      <el-form-item><el-button type="primary" :loading="loading" @click="query">{{ t('common.query') }}</el-button><el-button @click="reset">重置</el-button></el-form-item>
    </el-form>
    <el-table :data="rows" v-loading="loading" border empty-text="当前筛选范围内暂无已解析内幕交易">
      <el-table-column prop="transaction_date" label="交易日" width="112"><template #default="{ row }">{{ formatDate(row.transaction_date) }}</template></el-table-column>
      <el-table-column prop="ticker" label="Ticker" width="90" fixed><template #default="{ row }"><strong>{{ row.ticker || '-' }}</strong></template></el-table-column>
      <el-table-column label="申报人 / 职务" min-width="205" show-overflow-tooltip><template #default="{ row }"><div class="owner"><strong>{{ row.owner_name || '未披露' }}</strong><small>{{ roleLabel(row) }}</small></div></template></el-table-column>
      <el-table-column label="方向" width="108"><template #default="{ row }"><el-tag :type="row.direction === 'buy' ? 'success' : row.direction === 'sell' ? 'danger' : 'info'" effect="plain">{{ directionLabel(row) }}</el-tag></template></el-table-column>
      <el-table-column label="股数" width="115" align="right"><template #default="{ row }">{{ number(row.shares) }}</template></el-table-column>
      <el-table-column label="价格" width="110" align="right"><template #default="{ row }">{{ price(row.price_usd) }}</template></el-table-column>
      <el-table-column label="披露金额" width="130" align="right"><template #default="{ row }"><strong>{{ money(row.value_usd) }}</strong></template></el-table-column>
      <el-table-column label="研究口径" width="120"><template #default="{ row }"><el-tooltip :content="row.qualified ? '满足当前内幕交易研究规则' : exclusionLabel(row.exclusion_reason)"><el-tag :type="row.qualified ? 'success' : 'warning'" effect="plain">{{ row.qualified ? '计入研究' : '需复核' }}</el-tag></el-tooltip></template></el-table-column>
      <el-table-column label="证券" width="88"><template #default="{ row }">{{ row.derivative ? '衍生品' : '普通股' }}</template></el-table-column>
      <el-table-column label="证据" width="78" fixed="right"><template #default="{ row }"><el-link v-if="row.source_url" :href="row.source_url" target="_blank" type="primary">SEC</el-link><span v-else>-</span></template></el-table-column>
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />
  </section>
</template>
<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse } from '@/api/types'
import { useI18n } from '@/i18n'
interface InsiderRow { id:number; ticker:string; owner_name:string; officer_title:string; role:string; transaction_date:string; transaction_code:string; direction:string; derivative:boolean; shares:number; price_usd:number; value_usd:number; qualified:boolean; exclusion_reason:string; source_url:string }
interface Summary { transactions:number; issuers:number; purchases:number; sales:number; buy_value_usd:number; sell_value_usd:number; net_value_usd:number }
interface Result { items:InsiderRow[]; total:number; page:number; page_size:number; summary:Summary }
const emptySummary = ():Summary => ({ transactions:0, issuers:0, purchases:0, sales:0, buy_value_usd:0, sell_value_usd:0, net_value_usd:0 })
const { t } = useI18n(); const route = useRoute(); const loading = ref(false); const rows = ref<InsiderRow[]>([]); const total = ref(0); const page = ref(1); const pageSize = 20; const summary = reactive<Summary>(emptySummary()); const filters = reactive({ ticker:'', direction:'', qualified:'true' })
async function load() { loading.value = true; try { const res = await apiClient.get<ApiResponse<Result>>('/insider-transactions', { params:{ ...filters, page:page.value, page_size:pageSize } }); rows.value=res.data.data.items; total.value=res.data.data.total; Object.assign(summary, res.data.data.summary) } catch (err:any) { ElMessage.error(err?.response?.data?.message || '加载内幕交易事实失败') } finally { loading.value=false } }
function query(){ page.value=1; return load() }
function reset(){ Object.assign(filters,{ticker:'',direction:'',qualified:'true'}); query() }
function formatDate(value?:string){ return value ? new Date(value).toISOString().slice(0,10) : '-' }
function number(value?:number){ return Number.isFinite(value) ? Number(value).toLocaleString('en-US',{maximumFractionDigits:2}) : '-' }
function price(value?:number){ return value ? `$${Number(value).toLocaleString('en-US',{maximumFractionDigits:4})}` : '-' }
function money(value?:number){ if(!Number.isFinite(value)||!value)return '-'; const abs=Math.abs(Number(value)); return abs>=1e6?`$${(abs/1e6).toFixed(1)}M`:abs>=1e3?`$${(abs/1e3).toFixed(1)}K`:`$${abs.toFixed(0)}` }
function signedMoney(value:number){ const text=money(value); return text==='-'?'-':`${value>=0?'+':'−'}${text}` }
function roleLabel(row:InsiderRow){ return [row.officer_title,row.role].filter(Boolean).join(' · ') || '身份待复核' }
function directionLabel(row:InsiderRow){ if(row.direction==='buy')return row.transaction_code==='P'?'公开市场买入':'取得'; if(row.direction==='sell')return row.transaction_code==='S'?'公开市场卖出':'处置'; return row.transaction_code || '其他' }
function exclusionLabel(value:string){ const labels:Record<string,string>={missing_price:'缺少成交价',derivative:'衍生证券交易',not_open_market:'非公开市场交易',role_not_qualified:'申报人身份不在当前口径'}; return labels[value]||value||'未满足当前研究口径，请核对 SEC 原文' }
onMounted(()=>{ const ticker=route.query.ticker; if(typeof ticker==='string')filters.ticker=ticker.toUpperCase(); load() })
</script>
<style scoped>
.metric-strip{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));border:1px solid var(--el-border-color-lighter);border-radius:8px;background:var(--el-bg-color);margin-bottom:12px}.metric-strip>div{padding:12px 16px;display:grid;gap:2px;border-right:1px solid var(--el-border-color-lighter)}.metric-strip>div:last-child{border-right:0}.metric-strip span,.metric-strip small,.owner small{color:var(--el-text-color-secondary);font-size:12px}.metric-strip strong{font-size:24px;line-height:1.2}.positive{color:var(--el-color-success)}.negative{color:var(--el-color-danger)}.compact-toolbar{margin-bottom:12px}.owner{display:grid;gap:2px}@media(max-width:900px){.metric-strip{grid-template-columns:repeat(2,1fr)}.metric-strip>div:nth-child(2){border-right:0}}
</style>
