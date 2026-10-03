<template>
  <OwnershipProviderSection class="futu-history" title="Futu · 机构合计历史">
    <template #actions><el-button size="small" :loading="refreshing" :disabled="!history.ticker || refreshing" @click="refresh">刷新 Futu 机构合计历史</el-button></template>
    <p class="note">仅刷新当前股票的 Futu 机构合计数据；24 小时内使用已有缓存，授权、暂停和请求预算仍生效。</p>
    <el-alert v-if="refreshMessage" :title="refreshMessage" :type="refreshFailed ? 'error' : 'success'" :closable="false" />
    <el-alert type="info" :closable="false" title="富途提供的报告期机构合计，与下方 Longbridge 单家机构历史分开展示" description="分母由提供方定义；不是实时持仓，不相加、不跨数据源拼接。报告期比例变动不等于机构实际买卖；缺失不补零。" />
    <p class="note">{{ apiStateLabel(history.futu_aggregate_status || 'not_synced') }} · 最近查询：{{ date(history.futu_aggregate_synced_at) }}。<router-link to="/api-management?section=connections">在数据源与 API 中授权或试查</router-link></p>
    <svg v-if="valid >= 2" viewBox="0 0 760 240" class="trend" role="img" aria-label="最近八个报告期的富途机构合计持仓比例，缺失值断开">
      <line x1="65" y1="195" x2="730" y2="195" class="axis" /><line x1="65" y1="25" x2="65" y2="195" class="axis" />
      <text x="8" y="32">{{ bounds.max.toFixed(2) }}%</text><text x="8" y="195">{{ bounds.min.toFixed(2) }}%</text>
      <polyline v-for="(segment,i) in segments" :key="i" :points="segment" fill="none" stroke="var(--el-color-success)" stroke-width="2" />
      <g v-for="(row,i) in recent" :key="row.period"><circle v-if="finiteOwnershipValue(row.holder_pct)!==null" :cx="x(i)" :cy="y(row.holder_pct!)" r="4" fill="var(--el-color-success)"><title>{{ row.period }} · {{ pct(row.holder_pct) }}</title></circle><text :x="x(i)" y="220" :text-anchor="i===0?'start':i===recent.length-1?'end':'middle'">{{ row.period }}</text></g>
    </svg>
    <el-alert v-else type="info" :closable="false" title="不足两个有效报告期比例点，暂不绘制趋势。未同步或暂无覆盖都不代表零持仓。" />
    <el-table :data="rows" size="small" border max-height="360" empty-text="尚无本地机构合计记录">
      <el-table-column prop="period" label="报告期" width="110" />
      <el-table-column label="机构合计比例" width="130"><template #default="{row}">{{ pct(row.holder_pct) }}</template></el-table-column>
      <el-table-column label="提供方比例变动值" width="155"><template #default="{row}">{{ number(row.holder_pct_change) }}</template></el-table-column>
      <el-table-column label="机构数量" width="110"><template #default="{row}">{{ number(row.institution_quantity) }}</template></el-table-column>
      <el-table-column label="合计持股数" min-width="140"><template #default="{row}">{{ number(row.holder_quantity) }}</template></el-table-column>
      <el-table-column label="提供方更新时间" min-width="170"><template #default="{row}">{{ date(row.provider_updated_at) }}</template></el-table-column>
      <el-table-column label="本地同步时间" min-width="170"><template #default="{row}">{{ date(row.fetched_at) }}</template></el-table-column>
      <el-table-column label="来源" width="80"><template #default="{row}"><el-link :href="row.source_url" target="_blank" rel="noopener noreferrer">Futu</el-link></template></el-table-column>
    </el-table>
    <p class="note">横轴为提供方报告季度；比例变动值保留提供方原值，未确认口径前不转换为涨跌幅或百分点。</p>
  </OwnershipProviderSection>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import OwnershipProviderSection from './OwnershipProviderSection.vue'
import { apiClient } from '@/api/client'
import type { ApiResponse } from '@/api/types'
import type { TickerInstitutionalHoldingHistory } from '@/api/types'
import { apiStateLabel, apiErrorMessage } from '@/utils/apiManagement'
import { finiteOwnershipValue } from '@/utils/institutionalOwnership'
const props=defineProps<{history:TickerInstitutionalHoldingHistory}>()
const emit=defineEmits<{refreshed:[history:TickerInstitutionalHoldingHistory]}>()
const refreshing=ref(false),refreshMessage=ref(''),refreshFailed=ref(false)
let requestSequence=0,controller:AbortController|undefined
function cancelRefresh(){requestSequence++;controller?.abort();controller=undefined;refreshing.value=false;refreshMessage.value=''}
watch(()=>props.history.ticker,cancelRefresh)
onBeforeUnmount(cancelRefresh)
async function refresh(){
  const ticker=props.history.ticker
  if(!ticker||refreshing.value)return
  const sequence=++requestSequence
  controller=new AbortController()
  const requestController=controller
  refreshing.value=true;refreshMessage.value='';refreshFailed.value=false
  try{
    const response=await apiClient.post<ApiResponse<{refresh:{cached:boolean;status:string;points:number;pages:number};research:TickerInstitutionalHoldingHistory}>>(`/providers/futu/ownership/${encodeURIComponent(ticker)}/refresh`,null,{timeout:65000,signal:requestController.signal})
    if(sequence!==requestSequence||props.history.ticker!==ticker)return
    const result=response.data.data
    emit('refreshed',result.research)
    refreshMessage.value=result.refresh.cached?`已读取 Futu 24 小时缓存：${apiStateLabel(result.refresh.status)}`:`Futu ${apiStateLabel(result.refresh.status)}：请求 ${result.refresh.pages} 页，保存 ${result.refresh.points} 条报告期记录`
  }catch(error){
    if(sequence!==requestSequence||requestController.signal.aborted)return
    refreshFailed.value=true;refreshMessage.value=`Futu 刷新失败，已有数据保留：${apiErrorMessage(error)}`
  }finally{
    if(sequence===requestSequence){refreshing.value=false;controller=undefined}
  }
}
const rows=computed(()=>[...(props.history.futu_aggregate_history||[])].sort((a,b)=>a.period.localeCompare(b.period)))
const recent=computed(()=>rows.value.slice(-8))
const values=computed(()=>recent.value.flatMap(row=>{const v=finiteOwnershipValue(row.holder_pct);return v===null?[]:[v]}))
const valid=computed(()=>values.value.length)
const bounds=computed(()=>({min:Math.max(0,Math.min(...values.value)-1),max:Math.max(...values.value)+1}))
const quarter=(period:string)=>Number(period.slice(0,4))*4+Number(period.slice(-1))-1
const x=(i:number)=>65+(quarter(recent.value[i]!.period)-quarter(recent.value[0]!.period))*665/Math.max(1,quarter(recent.value[recent.value.length-1]!.period)-quarter(recent.value[0]!.period))
const y=(v:number)=>195-(v-bounds.value.min)*170/(bounds.value.max-bounds.value.min)
const segments=computed(()=>{const result:string[]=[];let current:string[]=[];recent.value.forEach((row,i)=>{const v=finiteOwnershipValue(row.holder_pct);if(v===null || (i>0 && quarter(row.period)-quarter(recent.value[i-1]!.period)>1)){if(current.length)result.push(current.join(' '));current=[]}if(v!==null)current.push(`${x(i)},${y(v)}`)});if(current.length)result.push(current.join(' '));return result})
const pct=(v:unknown)=>{const n=finiteOwnershipValue(v);return n===null?'—':`${n.toFixed(2)}%`}
const number=(v:unknown)=>{const n=finiteOwnershipValue(v);return n===null?'—':n.toLocaleString(undefined,{maximumFractionDigits:4})}
const date=(v?:string)=>v?new Date(v).toLocaleString('zh-CN',{timeZone:'Asia/Hong_Kong'}):'未记录'
</script>
<style scoped>
.note{font-size:13px;color:var(--el-text-color-secondary);line-height:1.7}.trend{width:100%;max-height:280px;font-size:12px;fill:var(--el-text-color-regular)}.axis{stroke:var(--el-border-color)}.el-table{margin-top:14px}
</style>
