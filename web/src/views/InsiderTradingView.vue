<template>
  <section class="page insider-page">
    <div class="page-header">
      <div><h1>{{ t('pages.insiderTrading.title') }}</h1><p class="page-subtitle">仅展示当前小盘候选或启用监控标的的本地 Form 4 交易事实；页面查询不会访问 SEC。</p></div>
      <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
    </div>
    <div class="metric-strip">
      <div><span>交易记录</span><strong>{{ summary.transactions }}</strong><small>{{ summary.issuers }} 家发行人</small></div>
      <div><span>公开市场买入</span><strong class="positive">{{ summary.purchases }}</strong><small>有成交价 {{ summary.priced_purchases }} 笔 · {{ money(summary.buy_value_usd) }}</small></div>
      <div><span>公开市场卖出</span><strong class="negative">{{ summary.sales }}</strong><small>有成交价 {{ summary.priced_sales }} 笔 · {{ money(summary.sell_value_usd) }} · 计划内 {{ summary.planned_sales }}</small></div>
      <div><span>公开市场现金净额</span><strong :class="summary.net_value_usd >= 0 ? 'positive' : 'negative'">{{ signedMoney(summary.net_value_usd) }}</strong><small>仅含有成交价的 P/S 普通股交易；其他取得 {{ summary.other_acquisitions }} 笔、处置 {{ summary.other_dispositions }} 笔不计金额</small></div>
    </div>
    <el-tabs v-model="activeTab" @tab-change="onTabChange">
      <el-tab-pane label="交易记录" name="transactions">
    <el-form :inline="true" :model="filters" class="toolbar compact-toolbar">
      <el-form-item label="Ticker"><el-input v-model="filters.ticker" clearable @keyup.enter="query" /></el-form-item>
      <el-form-item label="来源"><el-select class="source-filter" v-model="filters.source" clearable placeholder="全部来源"><el-option label="监控标的" value="watch" /><el-option label="小盘候选" value="candidate" /></el-select></el-form-item>
      <el-form-item label="方向"><el-select class="direction-filter" v-model="filters.direction" clearable placeholder="全部方向"><el-option label="取得 / 买入" value="buy" /><el-option label="处置 / 卖出" value="sell" /></el-select></el-form-item>
      <el-form-item label="证据"><el-select class="evidence-filter" v-model="filters.qualified" clearable placeholder="全部证据"><el-option label="计入研究" value="true" /><el-option label="仅供复核" value="false" /></el-select></el-form-item>
      <el-form-item label="10b5-1"><el-select class="plan-filter" v-model="filters.ten_b5_1_status" clearable placeholder="全部计划"><el-option label="已确认计划" value="confirmed" /><el-option label="可能关联" value="possible" /><el-option label="未披露计划" value="not_disclosed" /></el-select></el-form-item>
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
      <el-table-column label="10b5-1 计划" width="142"><template #default="{ row }"><el-tooltip :disabled="!planTooltip(row)" :content="planTooltip(row)"><el-tag :type="planTagType(row)" effect="plain">{{ planLabel(row) }}</el-tag></el-tooltip></template></el-table-column>
      <el-table-column label="研究口径" width="120"><template #default="{ row }"><el-tooltip :content="row.qualified ? '满足当前内幕交易研究规则' : exclusionLabel(row.exclusion_reason)"><el-tag :type="row.qualified ? 'success' : 'warning'" effect="plain">{{ row.qualified ? '计入研究' : '需复核' }}</el-tag></el-tooltip></template></el-table-column>
      <el-table-column label="证券" width="88"><template #default="{ row }">{{ row.derivative ? '衍生品' : '普通股' }}</template></el-table-column>
      <el-table-column label="证据" width="78" fixed="right"><template #default="{ row }"><el-link v-if="row.source_url" :href="row.source_url" target="_blank" type="primary">SEC</el-link><span v-else>-</span></template></el-table-column>
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />
      </el-tab-pane>
      <el-tab-pane label="10b5-1 计划" name="plans">
        <el-form :inline="true" class="toolbar compact-toolbar">
          <el-form-item label="Ticker"><el-input v-model="planFilters.ticker" clearable @keyup.enter="queryPlans" /></el-form-item>
          <el-form-item label="来源"><el-select class="source-filter" v-model="planFilters.source" clearable placeholder="全部来源"><el-option label="监控标的" value="watch" /><el-option label="小盘候选" value="candidate" /></el-select></el-form-item>
          <el-form-item label="状态"><el-select class="status-filter" v-model="planFilters.status" clearable placeholder="全部状态"><el-option label="已有执行" value="executing" /><el-option label="已登记" value="active" /><el-option label="已终止" value="terminated" /><el-option label="已到期" value="expired" /></el-select></el-form-item>
          <el-form-item><el-button type="primary" :loading="planLoading" @click="queryPlans">查询</el-button><el-button @click="resetPlans">重置</el-button><el-button type="warning" plain :loading="backfillLoading" @click="backfillPlans">扫描历史原文</el-button></el-form-item>
        </el-form>
        <el-alert class="plan-coverage" :type="planCoverageAlertType" :closable="false" show-icon>
          <template #title>{{ planCoverageTitle }}</template>
          <div class="coverage-detail">{{ planCoverageDetail }}</div>
        </el-alert>
        <el-alert class="plan-boundary" type="info" :closable="false" title="计划额度与剩余额度仅在公开文件明确披露时显示；未披露不代表额度为零。" />
        <el-table :data="plans" v-loading="planLoading" border :empty-text="planEmptyText">
          <el-table-column prop="ticker" label="Ticker" width="88" fixed><template #default="{ row }"><strong>{{ row.ticker || '-' }}</strong></template></el-table-column>
          <el-table-column label="申报人 / 职务" min-width="190"><template #default="{ row }"><div class="owner"><strong>{{ row.owner_name }}</strong><small>{{ row.officer_title || '职务未披露' }}</small></div></template></el-table-column>
          <el-table-column label="采用日期" width="112"><template #default="{ row }">{{ formatDate(row.adoption_date) }}</template></el-table-column>
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tooltip :content="planStatusTooltip(row.status)"><el-tag type="success" effect="plain">{{ planStatusLabel(row.status) }}</el-tag></el-tooltip></template></el-table-column>
          <el-table-column label="已执行" width="128" align="right"><template #default="{ row }"><strong>{{ number(row.executed_shares) }}</strong><div class="cell-note">{{ row.execution_count }} 笔</div></template></el-table-column>
          <el-table-column label="披露金额" width="120" align="right"><template #default="{ row }">{{ money(row.executed_value_usd) }}</template></el-table-column>
          <el-table-column label="计划上限" width="128" align="right"><template #default="{ row }">{{ row.maximum_shares_known ? number(row.maximum_shares) : '未披露' }}</template></el-table-column>
          <el-table-column label="已知剩余" width="128" align="right"><template #default="{ row }">{{ row.remaining_shares_known ? number(row.remaining_shares) : '无法计算' }}</template></el-table-column>
          <el-table-column label="最近执行" width="112"><template #default="{ row }">{{ formatDate(row.last_execution_date) }}</template></el-table-column>
          <el-table-column label="证据" width="88" fixed="right"><template #default="{ row }"><el-tooltip :content="row.evidence_summary || 'Form 4 结构化披露'"><el-link v-if="row.primary_source_url" :href="row.primary_source_url" target="_blank" type="primary">{{ row.primary_source_form || 'SEC' }}</el-link><span v-else>-</span></el-tooltip></template></el-table-column>
        </el-table>
        <el-pagination class="pagination" layout="total, prev, pager, next" :total="planTotal" :page-size="pageSize" v-model:current-page="planPage" @current-change="loadPlans" />
      </el-tab-pane>
    </el-tabs>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse } from '@/api/types'
import { useI18n } from '@/i18n'
import { insiderRouteState } from '@/utils/researchRouteState'
interface InsiderRow { id:number; ticker:string; owner_name:string; officer_title:string; role:string; transaction_date:string; transaction_code:string; direction:string; derivative:boolean; shares:number; price_usd:number; value_usd:number; qualified:boolean; exclusion_reason:string; is_10b5_1:boolean; ten_b5_1_status:string; ten_b5_1_plan_adoption_date?:string; ten_b5_1_evidence:string; research_interpretation:string; source_url:string }
interface Summary { transactions:number; issuers:number; purchases:number; sales:number; priced_purchases:number; priced_sales:number; other_acquisitions:number; other_dispositions:number; planned_sales:number; buy_value_usd:number; sell_value_usd:number; net_value_usd:number }
interface Result { items:InsiderRow[]; total:number; page:number; page_size:number; summary:Summary }
interface InsiderPlan { id:number; ticker:string; company_name:string; owner_name:string; officer_title:string; adoption_date:string; status:string; evidence_confidence:string; maximum_shares_known:boolean; maximum_shares:number; executed_shares:number; executed_value_usd:number; remaining_shares_known:boolean; remaining_shares:number; execution_count:number; evidence_count:number; last_execution_date?:string; primary_source_form:string; primary_source_url:string; evidence_summary:string }
interface PlanCoverage { status:string; required_parser_version:string; scoped_transactions:number; parsed_transactions:number; confirmed_plan_transactions:number; registered_plans:number; coverage_pct:number; last_sync_completed_at?:string }
interface PlanResult { items:InsiderPlan[]; total:number; page:number; page_size:number; coverage:PlanCoverage }
interface PlanBackfillResult { pending_form4_documents:number; parsed_form4_documents:number; failed_form4_documents:number; updated_transactions:number; parsed_form144_documents:number; registered_plans:number; warnings:string[]; coverage:PlanCoverage }
const emptySummary = ():Summary => ({ transactions:0, issuers:0, purchases:0, sales:0, priced_purchases:0, priced_sales:0, other_acquisitions:0, other_dispositions:0, planned_sales:0, buy_value_usd:0, sell_value_usd:0, net_value_usd:0 })
const emptyPlanCoverage=():PlanCoverage=>({status:'pending',required_parser_version:'',scoped_transactions:0,parsed_transactions:0,confirmed_plan_transactions:0,registered_plans:0,coverage_pct:0})
const { t } = useI18n(); const route = useRoute(); const activeTab=ref('transactions'); const loading = ref(false); const rows = ref<InsiderRow[]>([]); const total = ref(0); const page = ref(1); const pageSize = 20; const summary = reactive<Summary>(emptySummary()); const filters = reactive({ ticker:'', source:'', direction:'', qualified:'', ten_b5_1_status:'' }); const planLoading=ref(false); const plans=ref<InsiderPlan[]>([]); const planTotal=ref(0); const planPage=ref(1); const planFilters=reactive({ticker:'',source:'',status:''}); const planCoverage=reactive<PlanCoverage>(emptyPlanCoverage()); const backfillLoading=ref(false)
async function load() { loading.value = true; try { const res = await apiClient.get<ApiResponse<Result>>('/insider-transactions', { params:{ ...filters, page:page.value, page_size:pageSize } }); rows.value=res.data.data.items; total.value=res.data.data.total; Object.assign(summary, res.data.data.summary) } catch (err:any) { ElMessage.error(err?.response?.data?.message || '加载内幕交易事实失败') } finally { loading.value=false } }
function query(){ page.value=1; return load() }
function reset(){ Object.assign(filters,{ticker:'',source:'',direction:'',qualified:'',ten_b5_1_status:''}); query() }
async function loadPlans(){ planLoading.value=true; try{ const res=await apiClient.get<ApiResponse<PlanResult>>('/insider-trading-plans',{params:{...planFilters,page:planPage.value,page_size:pageSize}}); plans.value=res.data.data.items; planTotal.value=res.data.data.total; Object.assign(planCoverage,res.data.data.coverage||emptyPlanCoverage()) }catch(err:any){ ElMessage.error(err?.response?.data?.message||'加载 10b5-1 计划失败') }finally{ planLoading.value=false } }
function queryPlans(){planPage.value=1;return loadPlans()} function resetPlans(){Object.assign(planFilters,{ticker:'',source:'',status:''});queryPlans()} function onTabChange(name:string|number){if(name==='plans'&&!plans.value.length)loadPlans()}
async function backfillPlans(){
  try{ await ElMessageBox.confirm('将只扫描当前小盘候选和启用监控标的中尚未被新版解析器覆盖的 SEC Form 4，并补查相关 Form 144。首次运行可能需要数分钟。','扫描 10b5-1 历史原文',{confirmButtonText:'开始扫描',cancelButtonText:'取消',type:'warning'}) }catch{return}
  backfillLoading.value=true
  try{ const res=await apiClient.post<ApiResponse<PlanBackfillResult>>('/insider-trading-plans/backfill'); const result=res.data.data; ElMessage.success(`历史回填完成：Form 4 ${result.parsed_form4_documents}/${result.pending_form4_documents} 份，登记计划 ${result.registered_plans} 个${result.failed_form4_documents?`，失败 ${result.failed_form4_documents} 份`:''}`); await Promise.all([load(),loadPlans()]) }catch(err:any){ ElMessage.error(err?.response?.data?.message||'10b5-1 历史回填失败') }finally{ backfillLoading.value=false }
}
function formatDate(value?:string){ return value ? new Date(value).toISOString().slice(0,10) : '-' }
function number(value?:number){ return Number.isFinite(value) ? Number(value).toLocaleString('en-US',{maximumFractionDigits:2}) : '-' }
function price(value?:number){ return value ? `$${Number(value).toLocaleString('en-US',{maximumFractionDigits:4})}` : '-' }
function money(value?:number){ if(!Number.isFinite(value)||!value)return '-'; const abs=Math.abs(Number(value)); return abs>=1e6?`$${(abs/1e6).toFixed(1)}M`:abs>=1e3?`$${(abs/1e3).toFixed(1)}K`:`$${abs.toFixed(0)}` }
function signedMoney(value:number){ const text=money(value); return text==='-'?'-':`${value>=0?'+':'−'}${text}` }
function roleLabel(row:InsiderRow){ return [row.officer_title,row.role].filter(Boolean).join(' · ') || '身份待复核' }
function directionLabel(row:InsiderRow){ if(row.direction==='buy')return row.transaction_code==='P'?'公开市场买入':'取得'; if(row.direction==='sell')return row.transaction_code==='S'?'公开市场卖出':'处置'; return row.transaction_code || '其他' }
function planLabel(row:InsiderRow){ if(row.ten_b5_1_status==='confirmed')return row.ten_b5_1_plan_adoption_date?`计划内 · ${formatDate(row.ten_b5_1_plan_adoption_date)}`:'已确认计划'; if(row.ten_b5_1_status==='possible')return '可能关联'; return '未披露' }
function planTagType(row:InsiderRow){ return row.ten_b5_1_status==='confirmed'?'success':row.ten_b5_1_status==='possible'?'warning':'info' }
function planTooltip(row:InsiderRow){ if(row.ten_b5_1_evidence)return row.ten_b5_1_evidence; if(row.research_interpretation==='planned_sale_reduced_bearish')return '已确认按预先制定的 10b5-1 计划执行，卖出信号已降权；仍需结合计划采用、修改和终止情况判断。'; if(row.ten_b5_1_status==='not_disclosed')return '本地 Form 4 未发现 10b5-1 结构化标记或明确脚注，不等同于确认属于计划外交易。'; return '' }
function planStatusLabel(value:string){return ({executing:'已有执行',active:'已登记',terminated:'已终止',expired:'已到期',unknown:'待核验'} as Record<string,string>)[value]||value||'待核验'}
function planStatusTooltip(value:string){return ({executing:'已关联至少一笔明确属于该计划的 Form 4 执行记录。',active:'已从公开文件确认并登记计划，但暂未关联明确的 Form 4 执行记录。',terminated:'公开文件已披露该计划终止。',expired:'公开文件披露的计划期限已经届满。',unknown:'现有公开证据不足以确定计划状态。'} as Record<string,string>)[value]||'现有公开证据不足以确定计划状态。'}
const planCoverageAlertType=computed(()=>planCoverage.status==='complete'?'success':planCoverage.status==='complete_no_confirmed_plans'?'info':'warning')
const planCoverageTitle=computed(()=>({pending:'新版 10b5-1 解析尚未覆盖当前范围',partial:'新版解析仅覆盖了部分历史记录',complete_no_confirmed_plans:'当前范围已完成新版解析，暂未发现采用日期明确的计划',complete:'当前范围已完成新版解析',empty_scope:'当前没有小盘候选或启用的监控标的',no_transactions:'当前范围暂无内幕交易记录'} as Record<string,string>)[planCoverage.status]||'10b5-1 数据覆盖状态待核验')
const planCoverageDetail=computed(()=>{ const counts=`已解析 ${number(planCoverage.parsed_transactions)} / ${number(planCoverage.scoped_transactions)} 条（${Number(planCoverage.coverage_pct||0).toFixed(1)}%）`; const sync=planCoverage.last_sync_completed_at?`；最近内幕同步 ${formatDateTime(planCoverage.last_sync_completed_at)}`:''; const evidence=planCoverage.confirmed_plan_transactions?`；其中 ${number(planCoverage.confirmed_plan_transactions)} 条含明确采用日期`:'；未确认不等于不存在计划'; return `${counts}${evidence}${sync}` })
const planEmptyText=computed(()=>planCoverage.status==='pending'?'等待新版 10b5-1 数据解析，当前空列表不代表没有计划':planCoverage.status==='partial'?'当前仅完成部分解析，空列表不能作为“没有计划”的结论':'当前范围暂无采用日期明确的 10b5-1 计划')
function formatDateTime(value?:string){if(!value)return '-'; const date=new Date(value); return Number.isNaN(date.getTime())?'-':date.toLocaleString('zh-CN',{hour12:false})}
function exclusionLabel(value:string){ const labels:Record<string,string>={missing_price:'缺少成交价',derivative:'衍生证券交易',not_open_market:'非公开市场交易',role_not_qualified:'申报人身份不在当前口径'}; return labels[value]||value||'未满足当前研究口径，请核对 SEC 原文' }
onMounted(()=>{
  const state=insiderRouteState(route.query)
  activeTab.value=state.tab
  if(state.tab==='plans'){
    planFilters.ticker=state.planTicker
    void loadPlans()
  }else if(state.transactionTicker){
    filters.ticker=state.transactionTicker
  }
  load()
})
</script>
<style scoped>
.metric-strip{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));border:1px solid var(--el-border-color-lighter);border-radius:8px;background:var(--el-bg-color);margin-bottom:12px}.metric-strip>div{padding:12px 16px;display:grid;gap:2px;border-right:1px solid var(--el-border-color-lighter)}.metric-strip>div:last-child{border-right:0}.metric-strip span,.metric-strip small,.owner small,.cell-note{color:var(--el-text-color-secondary);font-size:12px}.metric-strip strong{font-size:24px;line-height:1.2}.positive{color:var(--el-color-success)}.negative{color:var(--el-color-danger)}.compact-toolbar{margin-bottom:12px}.compact-toolbar :deep(.el-form-item){margin-right:8px}.source-filter{width:128px}.direction-filter,.evidence-filter,.status-filter{width:120px}.plan-filter{width:136px}.owner{display:grid;gap:2px}.plan-coverage,.plan-boundary{margin-bottom:10px}.coverage-detail{color:var(--el-text-color-secondary);font-size:12px;margin-top:3px}@media(max-width:900px){.metric-strip{grid-template-columns:repeat(2,1fr)}.metric-strip>div:nth-child(2){border-right:0}}
</style>
