<template>
 <div class="api-management" v-loading="loading">
  <div class="page-heading"><div><h2>数据源与 API</h2><p>统一管理行情数据源、AI 模型、Telegram、公共数据源及调用策略</p></div><el-space wrap><el-button :loading="loading" @click="load">读取最新本地状态</el-button><el-button @click="navigate('connections')">配置连接与凭据</el-button></el-space></div>
  <el-alert v-if="error" type="error" :closable="false" :title="error" />
  <div class="integration-content">
   <el-alert v-if="data" type="info" :closable="false" show-icon :title="data.notice" />
   <p v-if="data" class="muted">快照 {{ date(data.generated_at) }} · 统计窗口 {{ date(data.window_start) }} 至当前 · {{ data.time_zone }} · 页面读取不查询外部 API</p>
   <el-tabs v-model="active">
    <el-tab-pane label="连接与凭据" name="connections" />
    <el-tab-pane label="行情与接口" name="modules"><APIModuleEditor v-if="data" :modules="data.modules || []" :capabilities="data.capabilities" :price-route="data.price_route" @saved="load" @navigate="navigate" /></el-tab-pane>
    <el-tab-pane label="同步策略" name="data" />
    <el-tab-pane label="任务调度" name="tasks">
     <el-table :data="data?.tasks || []" border><el-table-column label="任务" min-width="210"><template #default="{row}">{{ taskName(row.task_name) }}</template></el-table-column><el-table-column label="启用" width="85"><template #default="{row}"><el-switch :model-value="row.enabled" :disabled="!!busy" :aria-label="`${taskName(row.task_name)}启用`" @change="(value:boolean|string|number)=>setTask(row,!!value)" /></template></el-table-column><el-table-column prop="cron_expr" label="调度规则" width="135" /><el-table-column label="最近结果" width="115"><template #default="{row}">{{ apiStateLabel(row.last_status) }}</template></el-table-column><el-table-column label="下一次运行" min-width="175"><template #default="{row}">{{ date(row.next_run_at) }}</template></el-table-column><el-table-column label="操作" width="110"><template #default="{row}"><el-button link @click="$router.push('/scheduler')">调度详情</el-button></template></el-table-column></el-table>
     <p class="muted">Futu 默认暂停且后台机构任务默认关闭；完成授权、试查后再启用。禁用任务不会删除历史记录。</p>
    </el-tab-pane>
    <el-tab-pane label="AI 模型" name="ai" />
    <el-tab-pane label="通知与 Telegram" name="notifications" />
    <el-tab-pane label="公共数据源" name="public-sources">
     <p class="muted">公共源无需 API Key。SEC 的 User-Agent 与同步策略在“同步策略”中管理。以下地址目前由代码或启动环境配置，尚不支持在页面编辑；调用统计目前仅覆盖 Longbridge / Futu。</p>
     <el-table :data="publicSources" border><el-table-column prop="name" label="数据源" min-width="180" /><el-table-column prop="purpose" label="用途" min-width="230" /><el-table-column prop="configuration" label="配置方式" min-width="260" /></el-table>
    </el-tab-pane>
    <el-tab-pane label="能力与口径" name="capabilities">
     <el-table :data="data?.capabilities || []" border>
      <el-table-column type="expand"><template #default="{row}"><div style="padding:12px" v-for="task in row.schedules || []" :key="task.task_name"><strong>{{ task.task_name }} · {{ task.enabled ? '任务已启用' : '任务未启用' }}</strong><p>下次运行：{{ date(task.next_run_at) }}；实际轮转集合：{{ task.universe_size || '独立队列' }}；无成功查询回执：{{ task.receipt_missing }}；查询回执超期：{{ task.receipt_stale }}</p><p v-if="task.minimum_rounds">完整轮转至少 {{ task.minimum_rounds }} 轮；理论最早完成：{{ date(task.earliest_full_rotation_at) }}；研究新鲜度目标 {{ task.research_ttl_hours }} 小时。{{ task.freshness_feasible === false ? '当前预算与频率不足以让整个集合持续满足新鲜度目标。' : '' }}</p><p>{{ task.note }}</p></div><p v-if="!row.schedules?.length" style="padding:12px">跟随相关业务任务和价格链配置；请在任务调度中核对。</p></template></el-table-column><el-table-column prop="label" label="业务能力" min-width="170" /><el-table-column label="数据源" width="125"><template #default="{row}">{{ name(row.provider) }}</template></el-table-column>
      <el-table-column label="后台状态" width="125"><template #default="{row}"><span>{{ capabilityState(row) }}</span></template></el-table-column>
      <el-table-column label="每轮标的上限" width="120"><template #default="{row}">{{ row.issuer_budget || '见具体任务' }}</template></el-table-column><el-table-column prop="metric_scope" label="口径与边界" min-width="250" />
     </el-table>
     <p class="muted">标的预算不等于 API 请求预算。机构合计与单家机构记录不互相回退、不相加；行情主源与备源统一在“行情与接口”中配置。</p>
    </el-tab-pane>
    <el-tab-pane label="覆盖与新鲜度" name="coverage">
     <div class="filters"><el-input v-model="ticker" placeholder="输入股票代码，如 CBRS" clearable aria-label="覆盖与日志标的" /><el-button @click="load">查询本地记录</el-button></div>
     <p class="muted">默认展示已启用监控股票与当前候选，最多 25 只；“同步已超期”表示最近确认时间超过本地阈值，不代表定时任务失败。任务按预算轮转，并非每次更新全部股票；查询返回相同内容也应计为已确认。同步时间不等于报告期。</p>
     <el-table :data="coverageRows" border empty-text="暂无监控或候选，可输入股票代码查询">
      <el-table-column prop="ticker" label="股票" width="95" fixed />
      <el-table-column v-for="column in coverageColumns" :key="column.key" :label="column.label" min-width="155"><template #default="{row}"><el-tooltip :content="coverageTooltip(row.cells[column.key])"><el-tag :type="coverageType(row.cells[column.key]?.status)">{{ apiStateLabel(row.cells[column.key]?.status) }}</el-tag></el-tooltip></template></el-table-column>
     </el-table>
     <div class="trial"><h3>Futu 机构合计历史试查</h3><p class="muted">显式外部请求：每次最多 2 页 × 50 个报告期；24 小时缓存；只查当前股票，不重跑候选，也不调用 AI。</p><el-space wrap><el-input v-model="trialTicker" placeholder="AAPL / CBRS / STI" aria-label="Futu试查股票" /><el-button type="primary" :loading="busy==='trial'" :disabled="futuPaused" @click="trial">试查并保存机构合计历史</el-button></el-space><el-alert v-if="trialMessage" type="info" :closable="false" :title="trialMessage" /><el-button v-if="trialMessage" link @click="$router.push({path:'/institutional-holdings',query:{ticker:trialTicker.toUpperCase()}})">查看分开的机构合计与单家机构历史</el-button></div>
    </el-tab-pane>
    <el-tab-pane label="调用趋势与审计" name="calls">
     <div class="filters"><el-select v-model="provider" aria-label="调用日志供应商"><el-option label="两家供应商" value="" /><el-option label="Longbridge" value="longbridge" /><el-option label="Futu" value="futu" /></el-select><el-input v-model="ticker" placeholder="股票代码（日志与覆盖）" clearable aria-label="调用日志股票" /><el-checkbox v-model="failuresOnly">仅失败日志</el-checkbox><el-button @click="load">读取本地统计</el-button></div>
     <p class="muted">供应商筛选影响趋势与日志；股票和失败筛选只影响日志，不改变账户总调用量。</p>
     <div class="trend-chart" role="img" aria-label="最近七日各供应商实际 API 调用量；失败数独立列出">
      <div class="trend-axis"><span>香港日期 / 数据源</span><span>实际请求次数（横条相对本窗口最大值）</span><span>失败 / 限流</span></div>
      <div v-for="row in data?.trends || []" :key="row.day+row.provider" class="trend-row"><span>{{ row.day }} · {{ name(row.provider) }}</span><div><el-progress :percentage="trendPercent(row.requests)" :show-text="false" /><strong>{{ row.requests }}</strong></div><span>{{ row.failures }} / {{ row.rate_limited }}</span></div>
      <el-empty v-if="!data?.trends.length" :image-size="40" description="尚无记录；不把启用前的历史调用量补成零" />
     </div>
     <el-table :data="data?.calls || []" border empty-text="本筛选下暂无调用记录"><el-table-column label="调用时间" min-width="165"><template #default="{row}">{{ date(row.started_at) }}</template></el-table-column><el-table-column prop="provider" label="来源" width="105" /><el-table-column prop="endpoint" label="接口 / 命令" min-width="250" /><el-table-column prop="ticker" label="标的" min-width="100" show-overflow-tooltip /><el-table-column label="触发" width="95"><template #default="{row}">{{ row.trigger==='manual'?'手动':row.trigger==='background'?'后台':row.trigger }}</template></el-table-column><el-table-column label="结果" width="110"><template #default="{row}">{{ apiStateLabel(row.status) }}</template></el-table-column><el-table-column label="错误分类" width="130"><template #default="{row}">{{ row.error_kind?apiStateLabel(row.error_kind):'—' }}</template></el-table-column><el-table-column prop="elapsed_ms" label="耗时 ms" width="95" /></el-table>
     <p class="muted">最近 7 天最多展示 100 条日志。统计包含每次重试和分页请求；日志不保存授权头、查询参数、响应正文或供应商原始错误。</p>
    </el-tab-pane>
   </el-tabs>
   <div v-show="active === 'connections'" class="connection-controls">
    <h3>券商授权与调用控制</h3><p class="muted">先保存凭据，再显式测试连接；全局暂停和每日请求预算优先于各功能的接口开关。</p>
   <div class="provider-grid">
    <el-card v-for="item in data?.providers || []" :key="item.policy.provider">
     <template #header><div class="card-heading"><strong>{{ name(item.policy.provider) }}</strong><el-tag :type="item.policy.paused?'info':'success'">{{ item.policy.paused?'暂停外部调用':'允许外部调用' }}</el-tag></div></template>
     <el-descriptions :column="2" border size="small">
      <el-descriptions-item label="授权状态" :span="2">{{ apiStateLabel(item.authorization) }}</el-descriptions-item>
      <el-descriptions-item label="近 7 日实际调用">{{ item.requests }}</el-descriptions-item><el-descriptions-item label="失败 / 限流">{{ item.failures }} / {{ item.rate_limited }}</el-descriptions-item>
      <el-descriptions-item label="已完成请求平均耗时">{{ item.requests > (item.pending || 0) ? `${Math.round(item.average_ms)} ms` : '未记录' }}</el-descriptions-item><el-descriptions-item label="供应商剩余额度">{{ item.vendor_quota ?? '未知，不推算' }}</el-descriptions-item>
      <el-descriptions-item label="最近记录的请求成功" :span="2">{{ date(item.last_success_at) }}</el-descriptions-item>
      <el-descriptions-item label="本地调用记录起点" :span="2">{{ date(item.policy.created_at) }}</el-descriptions-item>
     </el-descriptions>
     <p class="muted">进行中 / 未完成 {{ item.pending || 0 }} 次；“请求成功”不代表股票数据有覆盖，也不代表所有研究接口均有权限。</p>
     <el-form v-if="draft[item.policy.provider]" label-position="top" class="policy-form">
      <el-form-item label="暂停所有已接入外部调用"><el-switch v-model="draft[item.policy.provider]!.paused" :aria-label="`${name(item.policy.provider)}暂停调用`" /></el-form-item>
      <el-form-item label="每日实际请求预算（0 = 不设上限）"><el-input-number v-model="draft[item.policy.provider]!.daily_budget" :min="0" :max="100000" :aria-label="`${name(item.policy.provider)}每日请求预算`" /></el-form-item>
      <el-form-item label="账户级本地最小间隔（毫秒）"><el-input-number v-model="draft[item.policy.provider]!.min_interval_ms" :min="100" :max="60000" :step="100" :aria-label="`${name(item.policy.provider)}请求间隔`" /></el-form-item>
     </el-form>
     <p>今日已预留 / 派发 {{ item.policy.daily_used }} 次 · 本地上限 {{ item.policy.daily_budget || '未设置' }}</p>
     <el-progress v-if="item.policy.daily_budget" :percentage="Math.round(apiBudgetPercent(item.policy.daily_used,item.policy.daily_budget))" :status="item.policy.daily_used>=item.policy.daily_budget?'warning':undefined" />
     <el-space wrap class="actions">
      <el-button type="primary" :loading="busy===`save-${item.policy.provider}`" @click="savePolicy(item.policy.provider)">保存控制策略</el-button>
      <el-button :disabled="item.policy.paused || !item.credential_configured" :loading="busy===`probe-${item.policy.provider}`" @click="probe(item.policy.provider)">测试只读连接</el-button>
      <el-button v-if="item.policy.provider==='futu'" :loading="busy==='authorize'" @click="authorize">{{ item.credential_configured?'重新只读授权':'开始只读授权' }}</el-button>
      <el-button v-if="item.policy.provider==='futu' && item.credential_configured" type="danger" plain @click="disconnect">断开授权</el-button>
     </el-space>
     <FutuCredentialsEditor v-if="item.policy.provider==='futu'" @saved="load" />
    </el-card>
   </div>
   <el-alert v-if="probeMessage" :type="probeOK?'success':'warning'" :closable="false" :title="probeMessage" />
   </div>
   <ConfigurationEditor v-show="!!configurationSection" scope="integrations" :section="configurationSection" :price-provider="data?.price_route?.configured" @saved="load" @navigate="navigate" />
  </div>
 </div>
</template>
<script setup lang="ts">
import {computed,onMounted,reactive,ref,watch} from 'vue'
import {useRoute,useRouter} from 'vue-router'
import {integrationSection,type ConfigurationSection} from '@/utils/configurationSections'
import {ElMessage,ElMessageBox} from 'element-plus'
import {apiClient} from '@/api/client'
import type {ApiResponse,TaskConfig} from '@/api/types'
import type {APIManagementOverview,APIProviderPolicy,APICoverageCell} from '@/api/providers'
import {apiBudgetPercent,apiStateLabel,apiErrorMessage} from '@/utils/apiManagement'
import ConfigurationEditor from '@/components/ConfigurationEditor.vue'
import APIModuleEditor from '@/components/APIModuleEditor.vue'
import FutuCredentialsEditor from '@/components/FutuCredentialsEditor.vue'
const data=ref<APIManagementOverview|null>(null),loading=ref(false),error=ref(''),busy=ref(''),active=ref('connections')
const route=useRoute(),router=useRouter()
const configurationSection=computed<ConfigurationSection | undefined>(()=>integrationSection(active.value))
const apiSections=['connections','modules','data','tasks','ai','notifications','public-sources','capabilities','coverage','calls']
function requestedSection(value:unknown){return value==='configuration'?'connections':value==='discovery'?'data':typeof value==='string'&&apiSections.includes(value)?value:'connections'}
function navigate(section:string){active.value=requestedSection(section)}
watch(()=>route.query.section,value=>{active.value=requestedSection(value)},{immediate:true})
watch(active,value=>{if(route.query.section!==value)void router.replace({query:{...route.query,section:value}})})
const publicSources = [
 {name:'SEC EDGAR',purpose:'公告、财务、内部交易与机构持仓',configuration:'User-Agent / 同步策略可编辑；基础地址由启动环境配置'},
 {name:'Nasdaq Trader',purpose:'美股上市股票目录',configuration:'目录地址由启动环境配置'},
 {name:'BEA / BLS / 美联储 / Census / DOL',purpose:'宏观日历与经济数据发布',configuration:'公共页面与日历地址由代码配置'},
 {name:'FRED / Treasury / EIA',purpose:'经济指标、国债收益率与能源数据',configuration:'公共 CSV / 页面地址由代码配置'},
 {name:'Cboe',purpose:'基金身份辅助核验',configuration:'公共页面地址由代码配置'},
]
const provider=ref(''),ticker=ref(''),failuresOnly=ref(false),trialTicker=ref('AAPL'),trialMessage=ref(''),probeMessage=ref(''),probeOK=ref(false)
const draft=reactive<Record<string,APIProviderPolicy>>({})
const name=(value:string)=>value==='futu'?'Futu':'Longbridge'
const date=(value?:string|null)=>value?new Date(value).toLocaleString('zh-CN',{timeZone:'Asia/Hong_Kong'}):'未记录'
const futuPaused=computed(()=>!data.value?.providers.find(item=>item.policy.provider==='futu')?.credential_configured || data.value?.providers.find(item=>item.policy.provider==='futu')?.policy.paused!==false)
const coverageColumns=[{key:'company',label:'公司资料（当前数据源）'},{key:'analyst',label:'共识（当前数据源）'},{key:'eps',label:'Longbridge EPS'},{key:'valuation',label:'Longbridge 估值'},{key:'ownership',label:'Longbridge 单家机构'},{key:'aggregate',label:'Futu 机构合计'}]
const coverageRows=computed(()=>{const rows=new Map<string,{ticker:string;cells:Record<string,APICoverageCell>}>();for(const cell of data.value?.coverage||[]){if(!rows.has(cell.ticker))rows.set(cell.ticker,{ticker:cell.ticker,cells:{}});rows.get(cell.ticker)!.cells[cell.capability]=cell}return [...rows.values()]})
function coverageTooltip(cell?:APICoverageCell){
 const checked=cell?.checked_at
 const threshold=cell?.ttl_hours?`；超期阈值：${cell.ttl_hours/24} 天`:''
 return checked?`最近成功查询：${date(checked)}；数据快照保存：${date(cell?.snapshot_at)}${threshold}`:`数据快照保存：${date(cell?.snapshot_at || cell?.synced_at)}；尚无独立成功查询记录，暂按快照时间判断${threshold}`
}
function capabilityState(row:any){return ({scheduled:'任务已开启',task_disabled:'任务未启用',capability_disabled:'功能已关闭',disabled:'模块已关闭',provider_paused:'供应商暂停',not_configured:'凭据未配置',dependency_missing:'必需接口已关闭',unimplemented:'未实现'} as Record<string,string>)[row.effective_status || ''] || (row.config_key ? (row.auto_enabled?'已开启':'已关闭') : (row.implemented?'跟随相关任务':'未实现'))}
const coverageType=(value?:string)=>value==='available'?'success':value==='stale'||value==='partial'?'warning':'info'
const trendPercent=(count:number)=>Math.round(count/Math.max(1,...(data.value?.trends||[]).map(item=>item.requests))*100)
const taskLabels:Record<string,string>={watch_target_market_sync:'监控标的行情',market_trend_sync:'大盘趋势与市场温度',price_action_cycle_replay:'价格周期回放',watch_target_earnings_sync:'监控标的财报预告',ipo_listing_reconcile_sync:'IPO 上市核验',macro_calendar_sync:'宏观日历',longbridge_institutional_ownership_sync:'Longbridge 单家机构历史',futu_institutional_ownership_sync:'Futu 机构合计历史',longbridge_candidate_research_sync:'Longbridge 候选市场研究',longbridge_watch_target_research_sync:'Longbridge 监控市场研究',longbridge_candidate_valuation_sync:'Longbridge 候选估值',longbridge_watch_target_valuation_sync:'Longbridge 监控估值',longbridge_candidate_option_research_sync:'Longbridge 候选期权',longbridge_watch_target_option_research_sync:'Longbridge 监控期权'}
const taskName=(value:string)=>taskLabels[value]||value
async function load(){loading.value=true;error.value='';try{const response=await apiClient.get<ApiResponse<APIManagementOverview>>('/providers/overview',{params:{provider:provider.value||undefined,ticker:ticker.value.trim().toUpperCase()||undefined,failures_only:failuresOnly.value}});data.value=response.data.data;for(const item of data.value.providers)draft[item.policy.provider]={...item.policy}}catch(err){error.value=apiErrorMessage(err)}finally{loading.value=false}}
async function action(key:string,run:()=>Promise<void>){if(busy.value)return;busy.value=key;try{await run()}catch(err){ElMessage.error(apiErrorMessage(err))}finally{busy.value=''}}
async function savePolicy(source:string){await action(`save-${source}`,async()=>{await apiClient.put(`/providers/${source}/policy`,draft[source]);ElMessage.success('本地控制策略已保存');await load()})}
async function setTask(task:TaskConfig,enabled:boolean){await action(`task-${task.id}`,async()=>{await apiClient.put(`/task-configs/${task.id}`,{cron_expr:task.cron_expr,enabled});await load()})}
async function probe(source:string){await action(`probe-${source}`,async()=>{const response=await apiClient.post<ApiResponse<{status:string;message?:string}>>(`/providers/${source}/probe`,null,{timeout:35000});probeOK.value=response.data.data.status==='ok';probeMessage.value=response.data.data.message||'只读请求已完成，请查看调用日志';await load()})}
async function authorize(){await action('authorize',async()=>{const popup=window.open('about:blank','_blank');try{const response=await apiClient.post<ApiResponse<{authorization_url:string}>>('/providers/futu/oauth/start',null,{timeout:25000});const url=new URL(response.data.data.authorization_url);if(url.origin!=='https://webapi.futunn.com')throw new Error('授权地址不受信任');if(!popup)throw new Error('浏览器阻止了授权窗口，请允许弹窗后重试');popup.opener=null;popup.location.href=url.toString();ElMessage.info('请仅授权 quote:read；完成后点击“读取最新本地状态”。授权不会自动启用同步')}catch(err){popup?.close();throw err}})}
async function disconnect(){try{await ElMessageBox.confirm('断开 Futu 授权并暂停外部请求？已缓存机构历史会保留。','断开只读授权')}catch{return}await action('disconnect',async()=>{await apiClient.post('/providers/futu/disconnect');await load()})}
async function trial(){const symbol=trialTicker.value.trim().toUpperCase();if(!/^[A-Z][A-Z0-9.-]{0,19}$/.test(symbol)){ElMessage.warning('请输入有效的美股代码');return}await action('trial',async()=>{const response=await apiClient.post<ApiResponse<{refresh:{pages:number;points:number;cached:boolean;status:string}}>>(`/providers/futu/ownership/${encodeURIComponent(symbol)}/refresh`,null,{timeout:65000});const result=response.data.data.refresh;trialTicker.value=symbol;trialMessage.value=result.cached?`读取 24 小时缓存：${apiStateLabel(result.status)}`:`${apiStateLabel(result.status)}：请求 ${result.pages} 页，保存 ${result.points} 条报告期记录；不代表实时机构持仓`;ticker.value=symbol;await load()})}
onMounted(load)
</script>
<style scoped>
.integration-content{display:flex;flex-direction:column;gap:16px}.connection-controls{display:flex;flex-direction:column;gap:12px}.connection-controls h3{margin:0}.api-management{display:flex;flex-direction:column;gap:16px;min-width:0}.page-heading,.card-heading{display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:12px}.page-heading h2{margin:0}.page-heading p,.muted{color:var(--el-text-color-secondary);font-size:12px;line-height:1.7}.provider-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}.policy-form{margin-top:16px;display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:0 12px}.actions{margin-top:12px}.filters{display:flex;gap:12px;flex-wrap:wrap;margin-bottom:12px}.filters .el-input,.filters .el-select{width:220px}.trial{margin-top:24px}.trial .el-input{width:220px}.trial .el-alert{margin-top:12px}.trend-chart{margin:16px 0}.trend-axis,.trend-row{display:grid;grid-template-columns:240px minmax(120px,1fr) 100px;gap:12px;padding:8px 0;align-items:center}.trend-axis{color:var(--el-text-color-secondary);font-size:12px}.trend-row>div{display:flex;gap:10px;align-items:center}.trend-row .el-progress{flex:1}.trend-row strong{min-width:40px;font-weight:500}.api-management :deep(.el-descriptions__table){table-layout:fixed;overflow-wrap:anywhere}@media(max-width:900px){.provider-grid{grid-template-columns:1fr}}@media(max-width:640px){.policy-form{grid-template-columns:1fr}.trend-axis{display:none}.trend-row{grid-template-columns:1fr}.filters .el-input,.filters .el-select{width:100%}}
</style>
