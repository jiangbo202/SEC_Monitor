<template>
  <section class="page">
    <div class="page-header"><div><h1>机构持仓</h1><p>主要机构历史仅作中期研究背景，不是全体机构总占比或实时资金流。</p></div><el-button v-if="viewMode==='institution'" type="primary" :loading="syncing" @click="syncNow">同步最新 13F</el-button></div>
    <el-tabs v-model="viewMode">
      <el-tab-pane label="按标的看变化" name="ticker">
        <el-card shadow="never"><el-form inline @submit.prevent="loadTicker"><el-form-item label="Ticker"><el-input v-model="ticker" placeholder="NVDA" clearable @keyup.enter="loadTicker" /></el-form-item><el-button type="primary" :loading="tickerLoading" @click="loadTicker">查询本地持仓</el-button><el-button :disabled="!loadedTicker || tickerLoading || refreshing" :loading="refreshing" @click="refreshHistory">刷新主要机构历史</el-button></el-form><p class="note">24 小时缓存；单次最多请求 20 家股东明细。打开或查询页面不会调用 Longbridge。</p></el-card>
        <el-alert v-if="tickerMessage" type="info" :closable="false" class="gap" :title="tickerMessage" />
        <el-card shadow="never" class="gap" v-loading="tickerLoading"><template #header><strong>{{ loadedTicker || '标的' }} · 主要机构持股比例历史</strong></template><InstitutionalOwnershipHistory :history="history" /></el-card>
        <el-card shadow="never" class="gap"><template #header><strong>基金 / ETF 组合权重披露（与公司持股比例不同）</strong></template><el-table :data="history.fund_holders" size="small" border empty-text="暂无基金披露，不视为零持仓" max-height="360"><el-table-column prop="fund_symbol" label="代码" width="110" /><el-table-column prop="fund_name" label="基金" min-width="220" /><el-table-column label="组合权重" width="120"><template #default="{row}">{{ pct(row.position_ratio) }}</template></el-table-column><el-table-column prop="report_date" label="提供方报告日" width="140" /></el-table></el-card>
      </el-tab-pane>
      <el-tab-pane label="按机构看 13F" name="institution">
        <el-table :data="investorsWithUpcoming" v-loading="loading" border @row-click="open"><el-table-column prop="firm" label="机构" min-width="280" show-overflow-tooltip/><el-table-column prop="cik" label="CIK" width="125"/><el-table-column prop="report_date" label="报告期" width="125" sortable/><el-table-column prop="filing_date" label="提交日" width="125" sortable/><el-table-column prop="due_date" label="下次截止" width="125"/><el-table-column label="组合市值" width="160" align="right"><template #default="{row}">{{ money(row.total_value_usd) }}</template></el-table-column><el-table-column prop="total_holdings" label="持仓数" width="100" align="right"/><el-table-column label="公告" width="80"><template #default="{row}"><el-link :href="row.source_url" target="_blank">原文</el-link></template></el-table-column></el-table>
      </el-tab-pane>
    </el-tabs>
    <el-drawer v-model="visible" :title="detail?.filing?.firm || '机构详情'" size="80%"><template v-if="detail"><el-descriptions :column="3" border><el-descriptions-item label="报告期">{{ detail.filing.report_date }}</el-descriptions-item><el-descriptions-item label="提交日">{{ detail.filing.filing_date }}</el-descriptions-item><el-descriptions-item label="组合市值">{{ money(detail.filing.total_value_usd) }}</el-descriptions-item></el-descriptions><h3>最新披露持仓</h3><el-table :data="detail.holdings||[]" border max-height="520"><el-table-column prop="issuer" label="证券" min-width="230"/><el-table-column prop="cusip" label="CUSIP" width="110"/><el-table-column prop="shares" label="股数" width="140" align="right"/><el-table-column label="市值" width="160" align="right"><template #default="{row}">{{ money(row.value_usd) }}</template></el-table-column><el-table-column label="组合权重" width="110" align="right"><template #default="{row}">{{ pct(row.weight_pct) }}</template></el-table-column></el-table></template></el-drawer>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { TickerInstitutionalHoldingHistory } from '@/api/types'
import InstitutionalOwnershipHistory from '@/components/InstitutionalOwnershipHistory.vue'
import { finiteOwnershipValue } from '@/utils/institutionalOwnership'
const route=useRoute(),router=useRouter()
const viewMode=ref('ticker'),ticker=ref(''),loadedTicker=ref(''),tickerLoading=ref(false),tickerMessage=ref(''),refreshing=ref(false)
const emptyHistory=():TickerInstitutionalHoldingHistory=>({ticker:'',institutional_holders:[],fund_holders:[],ownership_history:[],other_holders:[],message:''})
const history=ref(emptyHistory()),investors=ref<any[]>([]),loading=ref(false),syncing=ref(false),visible=ref(false),detail=ref<any>(null)
let requestSequence=0
async function fetchTicker(symbol:string){const sequence=++requestSequence;tickerLoading.value=true;loadedTicker.value='';history.value=emptyHistory();tickerMessage.value='';try{const response=await apiClient.get(`/discovery/institutional-holdings/${encodeURIComponent(symbol)}`);if(sequence!==requestSequence)return;history.value=response.data.data||emptyHistory();loadedTicker.value=symbol;tickerMessage.value=history.value.message||''}catch(error:any){if(sequence===requestSequence)ElMessage.error(error?.response?.data?.message||'加载标的持仓失败')}finally{if(sequence===requestSequence)tickerLoading.value=false}}
async function loadTicker(){const symbol=ticker.value.trim().toUpperCase();if(!/^[A-Z0-9][A-Z0-9.-]{0,19}$/.test(symbol)){ElMessage.warning('请输入有效标的代码');return}ticker.value=symbol;if(route.query.ticker!==symbol)await router.replace({query:{...route.query,ticker:symbol}});else await fetchTicker(symbol)}
async function refreshHistory(){const symbol=loadedTicker.value,sequence=requestSequence;refreshing.value=true;try{const response=await apiClient.post(`/discovery/institutional-holdings/${encodeURIComponent(symbol)}/refresh`,null,{timeout:95000});if(sequence!==requestSequence)return;history.value=response.data.data.research;tickerMessage.value=history.value.message||'';const warnings=response.data.data.refresh.warnings||[];if(warnings.length)ElMessage.warning('已保存可用披露，请查看历史覆盖说明');else ElMessage.success('主要机构历史已更新（重复请求使用 24 小时缓存）')}catch(error:any){if(sequence===requestSequence)ElMessage.error(error?.response?.data?.message||'刷新失败，原有本地记录保留')}finally{refreshing.value=false}}
const investorsWithUpcoming=computed(()=>investors.value.map(row=>{const date=new Date(`${row.report_date}T00:00:00Z`);if(!Number.isFinite(date.getTime()))return{...row,due_date:'-'};const next=new Date(Date.UTC(date.getUTCFullYear(),date.getUTCMonth()+4,0));next.setUTCDate(next.getUTCDate()+45);return{...row,due_date:next.toISOString().slice(0,10)}}))
async function load(){loading.value=true;try{investors.value=(await apiClient.get('/institutional-filings')).data.data||[]}catch{ElMessage.error('加载机构持仓失败')}finally{loading.value=false}}
async function syncNow(){syncing.value=true;try{const response=await apiClient.post('/institutional-filings/sync');ElMessage.success(response.data.data.message);await load()}finally{syncing.value=false}}
async function open(row:any){detail.value=(await apiClient.get(`/institutional-filings/${row.cik}`)).data.data;visible.value=true}
function pct(value:unknown){const number=finiteOwnershipValue(value);return number===null?'-':`${number.toFixed(2)}%`}
function money(value:any){return `$${Number(value||0).toLocaleString()}`}
watch(()=>route.query.ticker,value=>{const symbol=typeof value==='string'?value.trim().toUpperCase():'';ticker.value=symbol;if(symbol)void fetchTicker(symbol);else{requestSequence++;loadedTicker.value='';history.value=emptyHistory();tickerMessage.value='';tickerLoading.value=false}},{immediate:true})
onMounted(()=>void load())
</script>
<style scoped>.gap{margin-top:12px}.note{font-size:13px;color:var(--el-text-color-secondary)}:deep(.el-form-item){margin-bottom:8px}</style>
