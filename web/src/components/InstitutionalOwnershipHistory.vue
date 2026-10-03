<template>
  <div class="ownership-history">
    <FutuInstitutionalHistory :history="history" @refreshed="emit('futu-refreshed', $event)" />
    <OwnershipProviderSection class="longbridge-history" title="Longbridge · 单家主要机构历史">
      <template #actions><el-button size="small" :loading="longbridgeRefreshing" :disabled="!history.ticker || longbridgeDisabled || longbridgeRefreshing" @click="emit('refresh-longbridge')">刷新 Longbridge 主要机构历史</el-button></template>
      <p class="note">仅刷新当前股票的 Longbridge 主要机构历史；24 小时内使用已有缓存，单次最多请求 20 家股东明细，授权、暂停和请求预算仍生效。</p>
      <p class="note" role="status">{{ history.history_status === 'not_synced' ? '未同步：等待每日后台轮转，或手动刷新。' : history.history_status === 'no_confirmed_history' ? '已查询但暂无已确认机构历史：可能无覆盖或明细仍待轮转，不代表零持仓。' : '本地历史仅部分覆盖。' }} 最近主要股东列表查询：{{ history.history_synced_at ? new Date(history.history_synced_at).toLocaleString() : '未记录' }}；股东明细同步时间见下表。</p>
      <el-alert type="info" :closable="false" show-icon title="主要机构持仓变化 · 部分覆盖，不是全体机构总占比" description="比例分母由 Longbridge 定义，尚未核验总股本/流通股口径。不汇总不同机构的比例；比例变化也可能受股本变化影响，不等于实际买卖。" />
      <div v-if="holders.length" class="controls">
        <el-select v-model="selected" aria-label="选择机构" placeholder="选择机构"><el-option v-for="holder in holders" :key="holder.id" :label="holder.name" :value="holder.id" /></el-select>
        <el-select v-model="basis" aria-label="趋势日期口径"><el-option label="按提供方记录日期" value="provider_date" /><el-option label="按明确持仓截止日" value="holding_date" /></el-select>
        <el-select v-model="limit" aria-label="历史展示范围"><el-option label="最近 8 个有效日期" :value="8" /><el-option label="最近 4 个有效日期" :value="4" /><el-option label="全部本地历史" :value="0" /></el-select>
      </div>
      <p class="note">{{ basis === 'holding_date' ? '横轴：提供方明确返回的持仓截止日。' : '横轴：提供方 filing_date 原始日期（样本常为季末），不是已核验的真实提交日或交易日。' }} 缺失数据保持空白；最新滚动比例不混入季度历史，报告季度与记录日期不一致的记录不参与趋势。同步时间不参与趋势计算。</p>
      <svg v-if="validCount >= 2" class="trend" viewBox="0 0 760 240" role="img" :aria-label="`${selectedName} 公司持股比例历史，按${basis === 'holding_date' ? '持仓截止日' : '提供方日期'}展示`">
        <line x1="65" y1="195" x2="730" y2="195" class="axis" />
        <line x1="65" y1="25" x2="65" y2="195" class="axis" />
        <text x="8" y="32">{{ bounds.max.toFixed(2) }}%</text><text x="8" y="195">{{ bounds.min.toFixed(2) }}%</text>
        <polyline v-for="(segment, index) in segments" :key="index" :points="segment" fill="none" stroke="var(--el-color-primary)" stroke-width="2" />
        <g v-for="(point, index) in series" :key="point.date"><template v-if="point.ratio !== null"><circle :cx="x(index)" :cy="y(point.ratio)" r="4" fill="var(--el-color-primary)"><title>{{ point.date }} · {{ point.ratio.toFixed(2) }}%</title></circle><text v-if="series.length <= 8" :x="x(index)" :y="y(point.ratio)-10" text-anchor="middle">{{ point.ratio.toFixed(2) }}%</text></template><text v-if="index === series.length-1 || index % Math.ceil(series.length/6) === 0" :x="x(index)" y="218" :text-anchor="index===0?'start':index===series.length-1?'end':'middle'">{{ point.date }}</text></g>
      </svg>
      <el-alert v-else type="info" :closable="false" :title="holders.length ? '等待样本：当前机构在所选日期口径下不足两个有效比例点，暂不绘制趋势。' : '尚无已确认机构历史；等待后台轮转或手动刷新。个人及类型未确认的股东不计入机构趋势。'" />
      <el-table :data="series" size="small" border class="history-table" empty-text="提供方未返回可用历史，不补零" max-height="360">
        <el-table-column prop="period" label="提供方期间" min-width="120" />
        <el-table-column label="持仓截止日" width="120"><template #default="{row}">{{ row.holding_date || '未提供' }}</template></el-table-column>
        <el-table-column label="提供方记录日期" width="140"><template #default="{row}">{{ row.provider_date || '未提供' }}</template></el-table-column>
        <el-table-column label="真实提交日" width="120"><template #default="{row}">{{ row.filing_date || '未核验' }}</template></el-table-column>
        <el-table-column label="持股比例" width="110" align="right"><template #default="{row}">{{ pct(row.percent_of_shares) }}</template></el-table-column>
        <el-table-column label="比例变化" width="110" align="right"><template #default="{row,$index}">{{ delta(row.percent_of_shares, $index ? series[$index-1]?.percent_of_shares : null) }}</template></el-table-column>
        <el-table-column label="持股数量" width="140" align="right"><template #default="{row}">{{ count(row.shares_held) }}</template></el-table-column>
        <el-table-column label="股数变化（未复权）" width="160" align="right"><template #default="{row,$index}">{{ count($index ? ownershipDelta(row.shares_held, series[$index-1]?.shares_held) : null) }}</template></el-table-column>
        <el-table-column label="本地同步时间" min-width="170"><template #default="{row}">{{ row.fetched_at ? new Date(row.fetched_at).toLocaleString() : '-' }}</template></el-table-column>
        <el-table-column label="来源" width="90"><template #default="{row}"><el-link :href="row.source_url" target="_blank" rel="noopener noreferrer">Longbridge</el-link></template></el-table-column>
      </el-table>
      <p v-for="warning in history.history_warnings || []" :key="warning" class="note">{{ warning }}</p>
      <details v-if="selected" class="raw"><summary>当前机构的全部提供方记录（含未进入趋势的期间）</summary><el-table :data="(history.ownership_history || []).filter(row => row.holder_id === selected)" size="small" border max-height="320"><el-table-column prop="period" label="提供方期间" width="130" /><el-table-column prop="provider_date" label="提供方记录日期" width="145" /><el-table-column label="持股比例" width="110"><template #default="{row}">{{ pct(row.percent_of_shares) }}</template></el-table-column><el-table-column label="持股数量" min-width="140"><template #default="{row}">{{ count(row.shares_held) }}</template></el-table-column></el-table></details>
      <details class="raw"><summary>本地机构披露记录（{{ history.institutional_holders.length }} 条；报告日含义由提供方定义）</summary>
        <el-table :data="history.institutional_holders" size="small" border max-height="320"><el-table-column prop="holder_name" label="机构" min-width="200" /><el-table-column label="公司持股比例" width="130"><template #default="{row}">{{ pct(row.percent_of_shares) }}</template></el-table-column><el-table-column label="披露股数变动" width="140"><template #default="{row}">{{ count(row.shares_changed) }}</template></el-table-column><el-table-column prop="report_date" label="提供方报告日" width="130" /></el-table>
      </details>
      <details v-if="history.other_holders?.length" class="raw"><summary>其他股东：个人 / 内部人士 / 公司 / 类型未确认（{{ history.other_holders.length }} 条，不纳入机构趋势）</summary><el-table :data="history.other_holders" size="small" border max-height="280"><el-table-column prop="holder_name" label="股东" min-width="200" /><el-table-column prop="owner_type" label="提供方类型" width="120" /><el-table-column label="持股比例" width="110"><template #default="{row}">{{ pct(row.percent_of_shares) }}</template></el-table-column><el-table-column prop="report_date" label="提供方报告日" width="130" /></el-table></details>
      <div class="fund-history">
        <h4>Longbridge · 基金 / ETF 组合权重披露</h4>
        <p class="note">基金组合权重与公司持股比例口径不同，不可相加；未覆盖或未续披露不代表零持仓。此处读取本地基金披露，由市场研究任务同步；上方按钮仅刷新主要机构历史。</p>
        <el-table :data="history.fund_holders" size="small" border max-height="360" empty-text="暂无 Longbridge 基金或 ETF 披露，不视为零持仓">
          <el-table-column prop="fund_name" label="基金 / ETF" min-width="220" show-overflow-tooltip />
          <el-table-column prop="fund_symbol" label="代码" width="110" />
          <el-table-column label="组合权重" width="120" align="right"><template #default="{ row }">{{ pct(row.position_ratio) }}</template></el-table-column>
          <el-table-column prop="report_date" label="提供方报告日" width="140" />
          <el-table-column label="本地同步时间" min-width="170"><template #default="{ row }">{{ row.fetched_at ? new Date(row.fetched_at).toLocaleString() : '未记录' }}</template></el-table-column>
          <el-table-column label="来源" width="100"><template #default="{ row }"><el-link v-if="row.source_url" :href="row.source_url" target="_blank" rel="noopener noreferrer">Longbridge</el-link><span v-else>Longbridge</span></template></el-table-column>
        </el-table>
      </div>
    </OwnershipProviderSection>
  </div>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import FutuInstitutionalHistory from './FutuInstitutionalHistory.vue'
import OwnershipProviderSection from './OwnershipProviderSection.vue'
import type { TickerInstitutionalHoldingHistory } from '@/api/types'
import { finiteOwnershipValue, ownershipDelta, ownershipSeries } from '@/utils/institutionalOwnership'
const props = defineProps<{ history: TickerInstitutionalHoldingHistory; longbridgeRefreshing?: boolean; longbridgeDisabled?: boolean }>()
const emit = defineEmits<{ 'futu-refreshed': [history: TickerInstitutionalHoldingHistory]; 'refresh-longbridge': [] }>()
const selected = ref(''), basis = ref<'holding_date'|'provider_date'>('provider_date'), limit = ref(8)
const holders = computed(() => {const map=new Map<string,{id:string;name:string}>();for(const row of props.history.ownership_history || []){if(!map.has(row.holder_id))map.set(row.holder_id,{id:row.holder_id,name:row.holder_name})}return [...map.values()].sort((a,b)=>a.name.localeCompare(b.name))})
watch(holders, list => { if (!list.some(holder => holder.id === selected.value)) selected.value = list[0]?.id || '' }, { immediate: true })
const selectedName = computed(()=>holders.value.find(holder=>holder.id===selected.value)?.name || '')
const series = computed(()=>ownershipSeries(props.history.ownership_history || [], selected.value, basis.value, limit.value))
const validCount = computed(()=>series.value.filter(point=>point.ratio !== null).length)
const bounds = computed(()=>{const values=series.value.flatMap(point=>point.ratio===null?[]:[point.ratio]); return {min:Math.max(0,Math.min(...values)-0.5),max:Math.max(...values)+0.5}})
const x=(index:number)=>{const rows=series.value;const first=Date.parse(rows[0]?.date||''),last=Date.parse(rows[rows.length-1]?.date||'');return 65+(Date.parse(rows[index]?.date||'')-first)*665/Math.max(1,last-first)}
const y=(value:number)=>195-(value-bounds.value.min)*170/(bounds.value.max-bounds.value.min)
const segments=computed(()=>{const result:string[]=[];let current:string[]=[];series.value.forEach((point,index)=>{if(point.ratio===null){if(current.length)result.push(current.join(' '));current=[]}else current.push(`${x(index)},${y(point.ratio)}`)});if(current.length)result.push(current.join(' '));return result})
function pct(value:unknown){const number=finiteOwnershipValue(value);return number===null?'-':`${number.toFixed(2)}%`}
function count(value:unknown){const number=finiteOwnershipValue(value);return number===null?'-':number.toLocaleString(undefined,{maximumFractionDigits:0})}
function delta(a:unknown,b:unknown){const value=ownershipDelta(a,b);return value===null?'-':`${value>=0?'+':''}${value.toFixed(2)} pp`}
</script>
<style scoped>
.ownership-history{display:grid;gap:24px;min-width:0}.fund-history{margin-top:24px;padding-top:18px;border-top:1px solid var(--el-border-color-lighter)}.fund-history h4{margin:0;font-size:14px}
.controls{display:flex;gap:12px;flex-wrap:wrap;margin-top:14px}.controls .el-select{width:240px}.note{font-size:13px;color:var(--el-text-color-secondary);line-height:1.6}.trend{width:100%;max-height:280px;background:var(--el-fill-color-blank);font-size:12px;fill:var(--el-text-color-regular)}.axis{stroke:var(--el-border-color)}.history-table,.raw{margin-top:14px}.raw summary{cursor:pointer;margin-bottom:10px;color:var(--el-text-color-secondary)}
</style>
