<template>
 <section class="module-editor">
  <el-alert type="info" :closable="false" title="模块与接口开关控制外部数据调用；自动研究开关与调度任务分别控制后台更新。关闭不删除缓存。" description="供应商全局暂停优先。必需接口和依赖顺序由系统保证；认证握手属于共享基础设施，不能作为单一模块接口关闭。已派发的请求不会被撤销。" />
  <p class="muted">凭据已配置不代表所有接口已授权或有股票覆盖。接口“最近结果”仅为最近保留的请求记录，不代替数据覆盖。未保存草稿不会发请求。</p>
  <el-collapse>
   <el-collapse-item v-for="module in modules" :key="module.key" :name="module.key">
    <template #title><div class="module-title"><strong>{{ module.label }}</strong><el-tag size="small">{{ providerName(module.provider) }}</el-tag><el-tag size="small" :type="module.status==='ready_unverified'?'success':'info'">{{ status(module.status) }}</el-tag><el-tag v-if="dirty(module)" size="small" type="warning">未保存</el-tag></div></template>
    <template v-if="drafts[module.key]">
     <p><strong>影响页面：</strong>{{ module.pages.join('、') }}</p>
     <el-form-item v-if="module.source_options" label="功能数据源"><el-select v-model="drafts[module.key]!.provider" :aria-label="`${module.label}数据源`" :disabled="!!busy"><el-option v-for="source in module.source_options" :key="source.provider" :label="`${providerName(source.provider)} · ${source.reason}`" :value="source.provider" :disabled="!source.implemented" /></el-select></el-form-item>
     <p class="flow" aria-label="固定调用依赖顺序"><strong>固定流程：</strong>{{ module.flow.join(' → ') }} <el-tag size="small" type="info">不可任意调整</el-tag></p>
     <div class="module-control"><span>允许该模块的外部数据调用</span><el-switch v-model="drafts[module.key]!.enabled" :aria-label="`${module.label}允许调用`" :disabled="!!busy" /><span class="muted">关闭后页面仍可读取原有本地记录</span></div>
     <el-table :data="displayInterfaces(module)" border size="small">
      <el-table-column prop="label" label="接口" min-width="145" />
      <el-table-column label="允许调用（草稿）" width="150"><template #default="{row}"><el-tooltip :content="row.required?'必需接口：先关闭模块才能关闭此接口':'可选接口：关闭后保留缓存，不补零'"><el-switch :model-value="drafts[module.key]!.interfaces[row.key]" :disabled="!!busy || (drafts[module.key]!.enabled && row.required)" :aria-label="`${module.label} ${row.label}允许调用`" @change="(value:boolean|string|number)=>setInterface(module,row.key,!!value)" /></el-tooltip></template></el-table-column>
      <el-table-column label="角色 / 依赖" min-width="145"><template #default="{row}"><el-tag size="small" :type="row.required?'warning':'info'">{{ row.required?'必需':'可选' }}</el-tag><p class="muted" v-if="row.depends_on?.length">先调用 {{ row.depends_on.map((key:string)=>module.interfaces.find(api=>api.key===key)?.label||key).join('、') }}</p></template></el-table-column>
      <el-table-column label="接口地址 / 命令" min-width="260"><template #default="{row}"><code v-for="endpoint in row.endpoints" :key="endpoint">{{ endpoint }}</code></template></el-table-column>
      <el-table-column prop="purpose" label="用途与口径" min-width="190" />
      <el-table-column label="最近请求结果" min-width="155"><template #default="{row}">{{ apiStateLabel(row.last_status) }}<p class="muted">{{ date(row.last_called_at) }}</p></template></el-table-column>
     </el-table>
     <p v-if="module.note" class="muted">{{ module.note }}</p>
     <p v-for="warning in module.warnings" :key="warning" class="warning">{{ warning }}</p>
     <el-alert v-if="moduleValidation(module,drafts[module.key]!)" type="warning" :closable="false" :title="moduleValidation(module,drafts[module.key]!)" />
     <el-alert v-if="drafts[module.key]!.revision!==module.revision" type="warning" :closable="false" title="其他窗口已修改此模块。当前草稿保留，但必须重新读取本模块配置后再保存。" />
     <el-space wrap class="actions"><el-button type="primary" :loading="busy===module.key" :disabled="!!busy || !dirty(module) || !!moduleValidation(module,drafts[module.key]!)" @click="saveModule(module)">保存本模块</el-button><el-button :disabled="!!busy" @click="reset(module)">恢复本模块已保存配置</el-button><el-button link @click="$router.push('/scheduler')">查看调度任务</el-button></el-space>
     <div class="background"><strong>后台同步状态</strong><p v-if="!(module.auto_capabilities||[]).length" class="muted">由独立任务控制自动运行；允许接口调用不会自动启用任务。</p><div v-for="cap in autoCapabilities(module)" :key="cap.key" class="module-control"><span>{{ cap.label }}</span><el-tag :type="cap.auto_enabled?'success':'info'">{{ cap.auto_enabled?'自动同步已开启':'自动同步已关闭' }}</el-tag></div><el-button v-if="(module.auto_capabilities||[]).length" link type="primary" @click="emit('navigate','data')">调整同步策略与标的预算</el-button><p v-if="module.task_names?.length" class="muted">关联任务：{{ module.task_names.join('、') }}。任务启用状态见“任务调度”。</p></div>
    </template>
   </el-collapse-item>
  </el-collapse>
  <el-card v-if="priceRoute" class="price-route" shadow="never">
   <template #header><strong>候选 / 监控行情备源顺序</strong></template>
   <el-alert type="info" :closable="false" :title="priceRoute.scope" />
   <p>当前生效顺序：{{ priceRoute.order.map(providerName).join(' → ') }}</p>
   <p class="muted">下方为未保存草稿；支持 Longbridge / Futu 美股日线主源与备源，不跨供应商拼接机构数据。不自动调用 API 或重跑历史。</p>
   <el-alert v-if="!priceRoute.editable" type="warning" :closable="false" title="当前为自动 / 文件 / Stooq 配置，请在“连接与凭据”中迁移行情配置，再调整主源与备源顺序。" />
   <div class="route-sources"><div v-for="source in priceRoute.sources" :key="source.key"><el-checkbox :model-value="priceDraft.includes(source.key)" :disabled="!!busy || !priceRoute.editable || (!source.ready && !priceDraft.includes(source.key))" :aria-label="`${providerName(source.key)}加入行情链`" @change="(value:boolean|string|number)=>selectSource(source.key,!!value)">{{ providerName(source.key) }}</el-checkbox><p class="muted">{{ source.reason }}</p></div></div>
   <ol class="priority-list"><li v-for="(source,i) in priceDraft" :key="source"><span>{{ i===0?'主源':'备源 '+i }} · {{ providerName(source) }}</span><el-space><el-button size="small" :aria-label="`${providerName(source)}优先级上移`" :disabled="!!busy||!priceRoute.editable||i===0" @click="priceDraft=moveAPIPriceSource(priceDraft,i,-1)">上移</el-button><el-button size="small" :aria-label="`${providerName(source)}优先级下移`" :disabled="!!busy||!priceRoute.editable||i===priceDraft.length-1" @click="priceDraft=moveAPIPriceSource(priceDraft,i,1)">下移</el-button></el-space></li></ol>
   <el-alert v-if="priceExpected!==priceRoute.configured" type="warning" :closable="false" title="行情配置已被其他窗口修改，请恢复已保存配置后重新调整。" />
   <el-space><el-button type="primary" :loading="busy==='price-route'" :disabled="!!busy||!priceRoute.editable||!priceDirty||!priceDraft.length||priceExpected!==priceRoute.configured" @click="savePriceRoute">保存行情调用顺序</el-button><el-button :disabled="!!busy" @click="resetPriceRoute">恢复已保存行情配置</el-button></el-space>
  </el-card>
 </section>
</template>
<script setup lang="ts">
import {computed,reactive,ref,watch} from 'vue'
import {ElMessage,ElMessageBox} from 'element-plus'
import {apiClient} from '@/api/client'
import type {APIModuleView,APICapability,APIPriceRoute} from '@/api/providers'
import {apiStateLabel,apiErrorMessage} from '@/utils/apiManagement'
import {moduleDraft,moduleDisables,moduleValidation,moveAPIPriceSource,type ModuleDraft} from '@/utils/apiModules'
const props=defineProps<{modules:APIModuleView[];capabilities:APICapability[];priceRoute?:APIPriceRoute}>()
const emit=defineEmits<{saved:[];navigate:[section:string]}>()
const drafts=reactive<Record<string,ModuleDraft>>({}),baselines=reactive<Record<string,string>>({}),busy=ref(''),priceDraft=ref<string[]>([]),priceExpected=ref(''),priceBaseline=ref('')
const date=(v?:string)=>v?new Date(v).toLocaleString('zh-CN',{timeZone:'Asia/Hong_Kong'}):'未记录'
const providerName=(key:string)=>({longbridge:'Longbridge',futu:'Futu',stooq:'Stooq'} as Record<string,string>)[key]||key
const status=(key:string)=>({ready_unverified:'依赖已配置，覆盖待验证',disabled:'暂停模块更新',provider_paused:'供应商已暂停',not_configured:'凭据未配置',dependency_missing:'缺少必需接口'} as Record<string,string>)[key]||key
const displayInterfaces=(module:APIModuleView)=>{const provider=drafts[module.key]?.provider;if(!provider||provider===module.provider)return module.interfaces;return module.source_options?.find(source=>source.provider===provider)?.interfaces.map(api=>({...api,last_status:'not_recorded'}))||module.interfaces}
const dirty=(module:APIModuleView)=>JSON.stringify(drafts[module.key])!==baselines[module.key]
const reset=(module:APIModuleView)=>{drafts[module.key]=moduleDraft(module);baselines[module.key]=JSON.stringify(drafts[module.key])}
watch(()=>props.modules,modules=>{for(const module of modules){if(!drafts[module.key]||!dirty(module))reset(module)}},{immediate:true})
watch(()=>props.priceRoute,route=>{if(route&&(!priceBaseline.value||JSON.stringify(priceDraft.value)===priceBaseline.value)){priceDraft.value=[...route.order];priceExpected.value=route.configured;priceBaseline.value=JSON.stringify(priceDraft.value)}},{immediate:true})
const priceDirty=computed(()=>JSON.stringify(priceDraft.value)!==priceBaseline.value)
function resetPriceRoute(){const route=props.priceRoute;if(route){priceDraft.value=[...route.order];priceExpected.value=route.configured;priceBaseline.value=JSON.stringify(priceDraft.value)}}
const autoCapabilities=(module:APIModuleView)=>props.capabilities.filter(cap=>(module.auto_capabilities||[]).includes(cap.key))
function setInterface(module:APIModuleView,key:string,enabled:boolean){const draft=drafts[module.key]!;draft.interfaces[key]=enabled;if(!enabled){for(const api of module.interfaces){if((api.depends_on||[]).includes(key))draft.interfaces[api.key]=false}}}
function selectSource(key:string,enabled:boolean){priceDraft.value=enabled?[...priceDraft.value.filter(v=>v!==key),key]:priceDraft.value.filter(v=>v!==key)}
async function saveModule(module:APIModuleView){const draft=drafts[module.key]!;if(moduleValidation(module,draft)){ElMessage.warning(moduleValidation(module,draft));return}const disabling=moduleDisables(module,draft);if(disabling){try{await ElMessageBox.confirm(`变更后影响：${module.pages.join('、')}。切换来源后仅展示该来源缓存，不混合历史。共享任务可能仅更新剩余模块；已派发请求不会撤销，历史缓存会保留。确认保存？`,'确认模块 / 接口影响',{type:'warning',confirmButtonText:'确定',cancelButtonText:'取消'})}catch{return}}busy.value=module.key;try{await apiClient.put(`/providers/modules/${module.key}`,{...draft,confirm_impact:disabling});delete drafts[module.key];delete baselines[module.key];ElMessage.success('接口调用策略已保存；未触发外部查询');emit('saved')}catch(err){ElMessage.error(apiErrorMessage(err))}finally{busy.value=''}}

async function savePriceRoute(){try{await ElMessageBox.confirm(`将候选 / 监控行情顺序设为：${priceDraft.value.map(providerName).join(' → ')}。后续同步按此顺序尝试有效数据；大盘趋势和机构持仓不受影响。确认？`,'保存行情路由',{type:'warning',confirmButtonText:'确定',cancelButtonText:'取消'})}catch{return}busy.value='price-route';try{await apiClient.put('/providers/price-route',{order:priceDraft.value,expected:priceExpected.value});priceBaseline.value='';ElMessage.success('顺序已保存，下次同步生效；未重跑历史');emit('saved')}catch(err){ElMessage.error(apiErrorMessage(err))}finally{busy.value=''}}
</script>
<style scoped>
.module-editor{display:flex;flex-direction:column;gap:14px}.module-title,.module-control{display:flex;gap:12px;align-items:center;flex-wrap:wrap}.module-title{padding:8px 0;line-height:1.6}.module-control{margin:12px 0}.muted{font-size:12px;color:var(--el-text-color-secondary);line-height:1.7}.flow{line-height:1.8}.warning{font-size:13px;color:var(--el-color-warning)}code{display:block;overflow-wrap:anywhere;font-size:12px}.actions,.background{margin-top:16px}.background{border-top:1px solid var(--el-border-color-light);padding-top:14px}.route-sources{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px;margin-top:18px}.priority-list{padding-left:22px}.priority-list li{padding:10px 0}.priority-list li>span{display:inline-block;min-width:190px}@media(max-width:700px){.route-sources{grid-template-columns:repeat(2,minmax(0,1fr))}.module-editor :deep(.el-collapse-item__header){height:auto;min-height:48px}.priority-list li>span{display:block;margin-bottom:8px}}
</style>
