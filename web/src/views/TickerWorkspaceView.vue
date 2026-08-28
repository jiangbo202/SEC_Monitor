<template>
  <section class="page ticker-workspace">
    <div class="page-header">
      <div><h1>标的研究台</h1><p>把本地已保存的基本面、行情、SEC、内幕交易、机构持仓和 AI 研判汇总到一个入口。</p></div>
      <el-button :loading="loading" @click="load">刷新本地快照</el-button>
    </div>
    <el-card shadow="never" class="query-card">
      <el-form inline @submit.prevent="search">
        <el-form-item label="Ticker"><el-input v-model="ticker" placeholder="NVDA" clearable @keyup.enter="search" /></el-form-item>
        <el-button type="primary" @click="search">打开研究台</el-button>
        <el-button v-if="symbol" @click="go('/ticker-evaluation')">查看评估历史</el-button>
      </el-form>
    </el-card>

    <el-empty v-if="!symbol" description="输入标的后聚合本地研究数据" />
    <template v-else>
      <el-alert v-if="errors.length" type="warning" :closable="false" show-icon class="section-gap" :title="`部分模块暂无数据：${errors.join('、')}`" description="其余模块仍可独立使用；本页不会为了补齐数据而请求第三方服务。" />
      <div class="summary-grid section-gap">
        <article><span>标的</span><strong>{{ symbol }}</strong><small>{{ companyName || '本地研究档案' }}</small></article>
        <article><span>基本面评分</span><strong>{{ scoreValue }}</strong><small>{{ evaluation?.status === 'ready' ? '评估数据完整' : '按已有证据展示' }}</small></article>
        <article><span>收盘价</span><strong>{{ price(evaluation?.candidate_score?.price_close_usd) }}</strong><small>{{ evaluation?.candidate_score?.price_trade_date?.slice?.(0, 10) || '-' }}</small></article>
        <article><span>技术状态</span><strong class="compact-value">{{ technicalStatus }}</strong><small>短线复核 {{ evaluation?.candidate_score?.review_priority_score ?? '-' }}</small></article>
        <article><span>研究更新</span><strong class="compact-value">{{ formatDate(evaluation?.evaluated_at) }}</strong><small>本地快照，不触发外部请求</small></article>
      </div>

      <el-row :gutter="12" class="section-gap">
        <el-col :xs="24" :lg="14">
          <el-card shadow="never" class="full-height">
            <template #header><div class="card-head"><strong>研究结论与交易状态</strong><el-link type="primary" @click="go('/ticker-evaluation')">完整评估</el-link></div></template>
            <div class="decision-grid">
              <div><span>基本面</span><b>{{ evaluation?.candidate_score?.total_score ?? '-' }} / 100</b></div>
              <div><span>短线复核</span><b>{{ evaluation?.candidate_score?.review_priority_score ?? '-' }} / 100</b></div>
              <div><span>入场触发</span><b>{{ tradeSetup.entry_trigger || '等待触发条件' }}</b></div>
              <div><span>止损 / 止盈</span><b>{{ price(tradeSetup.stop_loss_usd) }} / {{ takeProfit }}</b></div>
            </div>
            <div class="tag-row"><el-tag v-for="item in technicalSignals" :key="item" effect="plain">{{ item }}</el-tag><span v-if="!technicalSignals.length" class="muted">暂无技术信号</span></div>
          </el-card>
        </el-col>
        <el-col :xs="24" :lg="10">
          <el-card shadow="never" class="full-height">
            <template #header><div class="card-head"><strong>资本与机构视角</strong><el-link type="primary" @click="go('/institutional-holdings')">季度变化</el-link></div></template>
            <div class="decision-grid two"><div><span>机构披露</span><b>{{ holdings.institutional_holders.length }} 条</b></div><div><span>基金披露</span><b>{{ holdings.fund_holders.length }} 条</b></div><div><span>现金储备</span><b>{{ months(evaluation?.candidate_score?.cash_runway_months) }}</b></div><div><span>资本风险</span><b>{{ evaluation?.candidate_score?.active_blocks_a || evaluation?.candidate_score?.active_blocks_b ? '存在阻断' : '未见硬阻断' }}</b></div></div>
          </el-card>
        </el-col>
      </el-row>

      <el-row :gutter="12" class="section-gap">
        <el-col :xs="24" :lg="12"><el-card shadow="never"><template #header><div class="card-head"><strong>SEC 与重大事件</strong><el-link type="primary" @click="go('/filings')">查看全部</el-link></div></template><el-table :data="filings" size="small" max-height="300" empty-text="暂无本地 SEC 公告"><el-table-column prop="filing_type" label="类型" width="82" /><el-table-column prop="filing_date" label="日期" width="105" /><el-table-column prop="title" label="标题" min-width="180" show-overflow-tooltip /><el-table-column label="证据" width="58"><template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">SEC</el-link></template></el-table-column></el-table></el-card></el-col>
        <el-col :xs="24" :lg="12"><el-card shadow="never"><template #header><div class="card-head"><strong>内幕交易事实</strong><el-link type="primary" @click="go('/insider-trading')">查看全部</el-link></div></template><el-table :data="insiders" size="small" max-height="300" empty-text="暂无合格内幕交易"><el-table-column prop="transaction_date" label="日期" width="105" /><el-table-column prop="owner_name" label="申报人" min-width="150" show-overflow-tooltip /><el-table-column label="方向" width="72"><template #default="{ row }"><el-tag :type="row.direction === 'buy' ? 'success' : 'danger'" effect="plain">{{ row.direction === 'buy' ? '买入' : '卖出' }}</el-tag></template></el-table-column><el-table-column label="金额" width="110" align="right"><template #default="{ row }">{{ money(row.value_usd) }}</template></el-table-column></el-table></el-card></el-col>
      </el-row>

      <el-card shadow="never" class="section-gap">
        <template #header><div class="card-head"><strong>AI 研判版本</strong><el-link type="primary" @click="go('/ai-analyses')">比较全部版本</el-link></div></template>
        <el-table :data="analyses" size="small" empty-text="暂无手动 AI 研判"><el-table-column prop="provider_name" label="供应商" width="120" /><el-table-column prop="model" label="模型" width="170" /><el-table-column prop="template_name" label="模板" min-width="150" /><el-table-column prop="status" label="状态" width="90"><template #default="{ row }"><el-tag :type="row.status === 'success' ? 'success' : 'danger'" effect="plain">{{ row.status === 'success' ? '成功' : '失败' }}</el-tag></template></el-table-column><el-table-column label="结论" min-width="250" show-overflow-tooltip><template #default="{ row }">{{ row.structured_result?.conclusion || row.error_message || '-' }}</template></el-table-column><el-table-column prop="requested_at" label="时间" width="165"><template #default="{ row }">{{ formatDate(row.requested_at) }}</template></el-table-column></el-table>
      </el-card>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'

const route = useRoute(); const router = useRouter()
const ticker = ref(''); const loading = ref(false); const evaluation = ref<any>(null); const filings = ref<any[]>([]); const insiders = ref<any[]>([]); const analyses = ref<any[]>([]); const holdings = ref<any>({ institutional_holders: [], fund_holders: [] }); const errors = ref<string[]>([])
const symbol = computed(() => ticker.value.trim().toUpperCase())
const companyName = computed(() => evaluation.value?.company_name || filings.value[0]?.company_name || '')
const scoreValue = computed(() => evaluation.value?.candidate_score?.total_score ?? '-')
const technicalStatus = computed(() => evaluation.value?.candidate_score?.technical?.status || '暂无结论')
const tradeSetup = computed(() => evaluation.value?.candidate_score?.technical?.trade_setup || {})
const takeProfit = computed(() => tradeSetup.value.take_profit_zone_low_usd != null ? `${price(tradeSetup.value.take_profit_zone_low_usd)}–${price(tradeSetup.value.take_profit_zone_high_usd)}` : '-')
const technicalSignals = computed(() => evaluation.value?.candidate_score?.technical?.signals || evaluation.value?.candidate_score?.technical?.signal_labels || [])
function unwrapItems(response:any){return response?.data?.data?.items || response?.data?.data || []}
async function load(){if(!symbol.value)return;loading.value=true;errors.value=[];const requests=[['评估',apiClient.get('/ticker-evaluations',{params:{ticker:symbol.value,page:1,page_size:2}})],['SEC',apiClient.get('/filings',{params:{ticker:symbol.value,page:1,page_size:8}})],['内幕交易',apiClient.get('/insider-transactions',{params:{ticker:symbol.value,qualified:true,page:1,page_size:8}})],['机构持仓',apiClient.get(`/discovery/institutional-holdings/${encodeURIComponent(symbol.value)}`)],['AI',apiClient.get('/ai/analyses',{params:{ticker:symbol.value,page:1,page_size:5}})]] as const;const results=await Promise.allSettled(requests.map(([,p])=>p));results.forEach((result,index)=>{const name=requests[index][0];if(result.status==='rejected'){errors.value.push(name);return}const data=result.value;if(name==='评估')evaluation.value=unwrapItems(data)[0]||null;else if(name==='SEC')filings.value=unwrapItems(data);else if(name==='内幕交易')insiders.value=unwrapItems(data);else if(name==='机构持仓')holdings.value=data.data.data||{institutional_holders:[],fund_holders:[]};else analyses.value=unwrapItems(data)});loading.value=false}
async function search(){const value=symbol.value;if(!value){ElMessage.warning('请输入标的代码');return}ticker.value=value;await router.replace({query:{ticker:value}});await load()}
function go(path:string){router.push({path,query:{ticker:symbol.value}})}
function price(value:any){return Number.isFinite(Number(value))?`$${Number(value).toFixed(2)}`:'-'}
function money(value:any){return Number.isFinite(Number(value))?`$${Number(value).toLocaleString('en-US',{maximumFractionDigits:0})}`:'-'}
function months(value:any){return Number.isFinite(Number(value))?`${Number(value).toFixed(1)} 个月`:'-'}
function formatDate(value?:string){return value?new Date(value).toLocaleString('zh-CN',{hour12:false,timeZone:'Asia/Shanghai'}):'-'}
onMounted(()=>{ticker.value=typeof route.query.ticker==='string'?route.query.ticker.toUpperCase():'';if(ticker.value)void load()})
</script>

<style scoped>
.query-card{margin-top:12px}.query-card :deep(.el-card__body){padding:10px 12px}.query-card :deep(.el-form-item){margin-bottom:0}.section-gap{margin-top:12px}.summary-grid{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));border:1px solid var(--el-border-color-light);border-radius:7px;background:var(--el-bg-color);overflow:hidden}.summary-grid article{padding:13px 15px;border-right:1px solid var(--el-border-color-lighter);min-width:0}.summary-grid article:last-child{border-right:0}.summary-grid span,.decision-grid span{display:block;color:var(--el-text-color-secondary);font-size:12px}.summary-grid strong{display:block;font-size:24px;margin:4px 0;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.summary-grid .compact-value{font-size:16px;margin-top:8px}.summary-grid small{color:var(--el-text-color-secondary);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;display:block}.card-head{display:flex;justify-content:space-between;align-items:center}.full-height{height:100%}.decision-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}.decision-grid div{padding-bottom:9px;border-bottom:1px solid var(--el-border-color-lighter)}.decision-grid b{display:block;margin-top:4px}.decision-grid.two{grid-template-columns:1fr 1fr}.tag-row{display:flex;gap:6px;flex-wrap:wrap;margin-top:12px}.muted{color:var(--el-text-color-secondary)}@media(max-width:900px){.summary-grid{grid-template-columns:1fr 1fr}.summary-grid article{border-bottom:1px solid var(--el-border-color-lighter)}}
</style>
