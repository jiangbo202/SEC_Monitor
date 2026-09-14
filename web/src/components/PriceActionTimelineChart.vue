<template>
  <div v-if="chart.points.length" class="timeline-chart">
    <div class="chart-legend">
      <span><i class="candle up" />日线蜡烛</span>
      <span><i class="line ema10" />EMA10</span>
      <span><i class="line ema20" />EMA20</span>
      <span><i class="line ema50" />EMA50</span>
      <span><i class="transition-key" />阶段切换</span>
      <span class="legend-hint">点击图中日期查看当日快照</span>
    </div>
    <div class="chart-canvas">
      <svg ref="svgRef" class="chart-svg" viewBox="0 0 1000 390" preserveAspectRatio="none" role="img" :aria-label="`${ticker} 价格行为时间线`" @mousemove="handleHover" @mouseleave="clearHover">
        <line v-for="y in [30,85,140,195,250]" :key="`grid-${y}`" x1="45" :y1="y" x2="990" :y2="y" class="grid" />
        <rect v-for="point in chart.points" :key="`phase-${point.date}`" :x="point.x-chart.step/2" y="258" :width="chart.step+0.5" height="28" :fill="phaseColor(point.snapshot.phase)" opacity=".72" @click="select(point.snapshot)" />
        <polyline :points="chart.ema10" class="ema10-line" />
        <polyline :points="chart.ema20" class="ema20-line" />
        <polyline :points="chart.ema50" class="ema50-line" />
        <g v-for="point in chart.points" :key="`candle-${point.date}`" class="clickable" @click="select(point.snapshot)">
          <line :x1="point.x" :x2="point.x" :y1="point.highY" :y2="point.lowY" :class="point.close >= point.open ? 'candle-up' : 'candle-down'" />
          <rect :x="point.x-chart.candleWidth/2" :y="Math.min(point.openY,point.closeY)" :width="chart.candleWidth" :height="Math.max(Math.abs(point.openY-point.closeY),1.5)" :class="point.close >= point.open ? 'candle-up-fill' : 'candle-down-fill'" />
          <circle v-if="point.changed" :cx="point.x" cy="272" r="4.5" class="transition-dot" />
          <title>{{ tooltip(point) }}</title>
        </g>
        <line v-if="selectedX != null" :x1="selectedX" :x2="selectedX" y1="22" y2="374" class="selected-line" />
        <line v-if="hoverPoint" :x1="hoverPoint.x" :x2="hoverPoint.x" y1="22" y2="374" class="hover-line" />
        <circle v-if="hoverPoint" :cx="hoverPoint.x" :cy="hoverPoint.closeY" r="4" class="hover-close" />
        <line x1="45" y1="320" x2="990" y2="320" class="rsi-limit" /><line x1="45" y1="360" x2="990" y2="360" class="rsi-limit" />
        <polyline :points="chart.rsi" class="rsi-line" />
        <circle v-if="hoverPoint?.snapshot.rsi14 != null" :cx="hoverPoint.x" :cy="rsiY(hoverPoint.snapshot.rsi14)" r="3.5" class="hover-rsi" />
        <text x="4" y="35" class="axis-label">{{ formatPrice(chart.maxPrice) }}</text><text x="4" y="253" class="axis-label">{{ formatPrice(chart.minPrice) }}</text>
        <text x="4" y="324" class="axis-label">70</text><text x="4" y="364" class="axis-label">30</text><text x="4" y="344" class="axis-label">RSI</text>
        <text x="45" y="305" class="axis-label">{{ chart.startDate }}</text><text x="518" y="305" text-anchor="middle" class="axis-label">{{ chart.middleDate }}</text><text x="990" y="305" text-anchor="end" class="axis-label">{{ chart.endDate }}</text>
      </svg>
      <div v-if="hoverPoint" class="chart-tooltip" :class="{'align-left':hoverAlignLeft}" :style="hoverStyle">
        <div class="tooltip-heading"><strong>{{ hoverPoint.date }}</strong><span>{{ phaseLabel(hoverPoint.snapshot.phase) }} · {{ hoverPoint.snapshot.confidence }}%</span></div>
        <div class="tooltip-prices">
          <span>开盘<strong>{{ formatPrice(hoverPoint.open) }}</strong></span>
          <span>最高<strong>{{ formatPrice(hoverPoint.high) }}</strong></span>
          <span>最低<strong>{{ formatPrice(hoverPoint.low) }}</strong></span>
          <span>收盘<strong>{{ formatPrice(hoverPoint.close) }}</strong></span>
        </div>
        <div class="tooltip-indicators">
          <span>EMA10 {{ formatPrice(hoverPoint.snapshot.ema10_usd) }}</span><span>EMA20 {{ formatPrice(hoverPoint.snapshot.ema20_usd) }}</span><span>EMA50 {{ formatPrice(hoverPoint.snapshot.ema50_usd) }}</span>
          <span>RSI {{ formatNumber(hoverPoint.snapshot.rsi14) }}</span><span>KDJ {{ formatNumber(hoverPoint.snapshot.kdj_k) }} / {{ formatNumber(hoverPoint.snapshot.kdj_d) }} / {{ formatNumber(hoverPoint.snapshot.kdj_j) }}</span>
        </div>
        <div class="tooltip-meta"><span>成交量 {{ formatShares(hoverPoint.volume) }}</span><span>量比 {{ formatNumber(hoverPoint.snapshot.volume_ratio_20) }}×</span><span>相对 IWM {{ formatSignedPct(hoverPoint.snapshot.relative_iwm_20d_pct) }}</span></div>
        <div v-if="hoverPoint.changed" class="tooltip-transition">阶段切换：{{ phaseLabel(hoverPoint.snapshot.previous_phase) }} → {{ phaseLabel(hoverPoint.snapshot.phase) }}</div>
        <small>快照记录 {{ formatDateTime(hoverPoint.snapshot.recorded_at) }}</small>
      </div>
    </div>
    <div class="phase-legend"><span v-for="phase in phasesInView" :key="phase"><i :style="{background:phaseColor(phase)}" />{{ phaseLabel(phase) }}</span></div>
  </div>
  <el-empty v-else :image-size="58" description="所选范围暂无可匹配的日线与阶段快照" />
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { CandidateTechnicalHistoryRow, PriceActionPhaseSnapshot } from '@/api/types'

const props = defineProps<{ticker:string; timeline:PriceActionPhaseSnapshot[]; history:CandidateTechnicalHistoryRow[]; selectedDate?:string}>()
const emit = defineEmits<{select:[snapshot:PriceActionPhaseSnapshot]}>()

type ChartPoint={date:string;x:number;open:number;high:number;low:number;close:number;volume:number;openY:number;highY:number;lowY:number;closeY:number;changed:boolean;snapshot:PriceActionPhaseSnapshot}
const phasePalette:Record<string,string>={reversal_extension:'#79bbff',wedge_pop:'#95d475',ema_crossback:'#53a8ff',base_break:'#67c23a',exhaustion_extension:'#eebe77',wedge_drop:'#f89898',unconfirmed:'#c8c9cc',unavailable:'#dedfe0'}
const phaseColor=(phase:string)=>phasePalette[phase]||'#c8c9cc'
const phaseLabel=(phase:string)=>({reversal_extension:'反转延伸',wedge_pop:'楔形突破',ema_crossback:'均线回踩',base_break:'平台突破',exhaustion_extension:'衰竭延伸',wedge_drop:'楔形跌破',unconfirmed:'阶段未确认',unavailable:'数据不足'} as Record<string,string>)[phase]||phase

const chart=computed(()=>{
  const historyByDate=new Map(props.history.map(row=>[row.trade_date,row]))
  const pairs=props.timeline.map(snapshot=>({snapshot,row:historyByDate.get(snapshot.trade_date)})).filter((item):item is {snapshot:PriceActionPhaseSnapshot;row:CandidateTechnicalHistoryRow}=>!!item.row&&item.row.ohlc_available)
  if(!pairs.length)return{points:[] as ChartPoint[],ema10:'',ema20:'',ema50:'',rsi:'',step:0,candleWidth:0,minPrice:0,maxPrice:0,startDate:'',middleDate:'',endDate:''}
  const values=pairs.flatMap(({snapshot,row})=>[row.low_usd,row.high_usd,snapshot.ema10_usd,snapshot.ema20_usd,snapshot.ema50_usd]).filter(value=>Number.isFinite(value)&&value>0)
  const minPrice=Math.min(...values),maxPrice=Math.max(...values),span=Math.max(maxPrice-minPrice,maxPrice*.02,.01)
  const count=pairs.length,step=count===1?945:945/(count-1),xAt=(i:number)=>count===1?518:45+i*step,priceY=(value:number)=>250-((value-minPrice)/span)*220
  const points=pairs.map(({snapshot,row},index):ChartPoint=>({date:snapshot.trade_date,x:xAt(index),open:row.open_usd,high:row.high_usd,low:row.low_usd,close:row.close_usd,volume:row.volume,openY:priceY(row.open_usd),highY:priceY(row.high_usd),lowY:priceY(row.low_usd),closeY:priceY(row.close_usd),changed:!!snapshot.previous_phase&&snapshot.previous_phase!==snapshot.phase,snapshot}))
  const line=(key:'ema10_usd'|'ema20_usd'|'ema50_usd')=>points.filter(point=>point.snapshot[key]>0).map(point=>`${point.x},${priceY(point.snapshot[key])}`).join(' ')
  const rsi=points.filter(point=>point.snapshot.rsi14!=null).map(point=>`${point.x},${380-(Number(point.snapshot.rsi14)/100)*100}`).join(' ')
  return{points,ema10:line('ema10_usd'),ema20:line('ema20_usd'),ema50:line('ema50_usd'),rsi,step:Math.max(step,1),candleWidth:Math.max(1.5,Math.min(8,step*.58)),minPrice,maxPrice,startDate:pairs[0].snapshot.trade_date,middleDate:pairs[Math.floor((count-1)/2)].snapshot.trade_date,endDate:pairs[count-1].snapshot.trade_date}
})
const phasesInView=computed(()=>[...new Set(chart.value.points.map(point=>point.snapshot.phase))])
const selectedX=computed(()=>chart.value.points.find(point=>point.date===props.selectedDate)?.x??null)
const svgRef=ref<SVGSVGElement|null>(null)
const hoverPoint=ref<ChartPoint|null>(null)
const hoverLeft=ref(0)
const hoverTop=ref(0)
const hoverAlignLeft=ref(false)
const hoverStyle=computed(()=>({left:`${hoverLeft.value}px`,top:`${hoverTop.value}px`}))
function select(snapshot:PriceActionPhaseSnapshot){emit('select',snapshot)}
function formatPrice(value:number){return Number.isFinite(value)&&value>0?`$${value.toFixed(2)}`:'-'}
function formatNumber(value?:number|null){return value==null||!Number.isFinite(value)?'-':value.toFixed(1)}
function formatShares(value:number){return Number.isFinite(value)?Math.round(value).toLocaleString('en-US'):'-'}
function formatSignedPct(value?:number|null){return value==null||!Number.isFinite(value)?'-':`${value>=0?'+':''}${value.toFixed(1)}%`}
function formatDateTime(value?:string){if(!value)return'-';const date=new Date(value);return Number.isNaN(date.getTime())?value:date.toLocaleString(undefined,{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false})}
function rsiY(value:number){return 380-(Number(value)/100)*100}
function handleHover(event:MouseEvent){
  const svg=svgRef.value
  if(!svg||!chart.value.points.length)return
  const rect=svg.getBoundingClientRect()
  const localX=Math.max(0,Math.min(rect.width,event.clientX-rect.left))
  const viewX=(localX/Math.max(rect.width,1))*1000
  hoverPoint.value=chart.value.points.reduce((nearest,point)=>Math.abs(point.x-viewX)<Math.abs(nearest.x-viewX)?point:nearest)
  hoverAlignLeft.value=localX>rect.width-285
  hoverLeft.value=localX+(hoverAlignLeft.value?-12:12)
  hoverTop.value=Math.max(8,Math.min(event.clientY-rect.top-12,rect.height-230))
}
function clearHover(){hoverPoint.value=null}
function tooltip(point:ChartPoint){const item=point.snapshot;return `${point.date}｜开 ${formatPrice(point.open)} 高 ${formatPrice(point.high)} 低 ${formatPrice(point.low)} 收 ${formatPrice(point.close)}｜${phaseLabel(item.phase)} ${item.confidence}%｜RSI ${formatNumber(item.rsi14)}｜KDJ ${formatNumber(item.kdj_k)}/${formatNumber(item.kdj_d)}/${formatNumber(item.kdj_j)}`}
</script>

<style scoped>
.timeline-chart{border:1px solid var(--el-border-color-lighter);border-radius:9px;padding:12px;background:var(--el-bg-color)}.chart-legend,.phase-legend{display:flex;align-items:center;flex-wrap:wrap;gap:8px 15px;color:var(--el-text-color-secondary);font-size:12px}.chart-legend i,.phase-legend i{display:inline-block;margin-right:5px;vertical-align:middle}.legend-hint{margin-left:auto}.candle{width:12px;height:10px;border:2px solid}.candle.up{border-color:#67c23a;background:rgba(103,194,58,.18)}.line{width:20px;border-top:3px solid}.line.ema10{border-color:#409eff}.line.ema20{border-color:#e6a23c}.line.ema50{border-color:#67c23a}.transition-key{width:9px;height:9px;border-radius:50%;background:#303133}.chart-canvas{position:relative}.chart-svg{width:100%;height:390px;display:block}.grid{stroke:#ebeef5;stroke-dasharray:3 4}.ema10-line,.ema20-line,.ema50-line,.rsi-line{fill:none;stroke-width:2}.ema10-line{stroke:#409eff}.ema20-line{stroke:#e6a23c}.ema50-line{stroke:#67c23a}.rsi-line{stroke:#9b59b6}.rsi-limit{stroke:#dcdfe6;stroke-dasharray:4 4}.candle-up,.candle-down{stroke-width:1.2}.candle-up{stroke:#67c23a}.candle-down{stroke:#f56c6c}.candle-up-fill{fill:rgba(103,194,58,.25);stroke:#67c23a}.candle-down-fill{fill:rgba(245,108,108,.28);stroke:#f56c6c}.transition-dot{fill:#303133;stroke:#fff;stroke-width:1.5}.selected-line{stroke:#409eff;stroke-width:1;stroke-dasharray:4 3;pointer-events:none}.hover-line{stroke:#606266;stroke-width:1;stroke-dasharray:3 3;pointer-events:none}.hover-close{fill:#fff;stroke:#303133;stroke-width:1.5;pointer-events:none}.hover-rsi{fill:#fff;stroke:#9b59b6;stroke-width:1.5;pointer-events:none}.axis-label{fill:#909399;font-size:11px}.clickable{cursor:pointer}.chart-tooltip{position:absolute;z-index:3;width:260px;padding:10px 12px;border:1px solid rgba(255,255,255,.12);border-radius:8px;background:rgba(31,35,41,.94);color:#fff;box-shadow:0 8px 24px rgba(0,0,0,.18);font-size:12px;line-height:1.45;pointer-events:none;backdrop-filter:blur(4px)}.chart-tooltip.align-left{transform:translateX(-100%)}.tooltip-heading{display:flex;justify-content:space-between;gap:10px;padding-bottom:7px;border-bottom:1px solid rgba(255,255,255,.18)}.tooltip-heading span{color:#d5d8de}.tooltip-prices{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:5px 12px;padding:8px 0}.tooltip-prices span{display:flex;justify-content:space-between;color:#c8cbd1}.tooltip-prices strong{color:#fff}.tooltip-indicators,.tooltip-meta{display:flex;flex-wrap:wrap;gap:3px 10px;color:#d5d8de}.tooltip-indicators{padding:7px 0;border-top:1px solid rgba(255,255,255,.12);border-bottom:1px solid rgba(255,255,255,.12)}.tooltip-meta{padding-top:7px}.tooltip-transition{margin-top:7px;padding:5px 7px;border-radius:4px;background:rgba(64,158,255,.18);color:#b9dcff}.chart-tooltip small{display:block;margin-top:6px;color:#aeb3bc}.phase-legend{padding:4px 0 0 42px}.phase-legend i{width:10px;height:10px;border-radius:2px}@media(max-width:800px){.chart-svg{height:300px}.legend-hint{width:100%;margin-left:0}.chart-tooltip{width:230px}.tooltip-indicators{gap:3px 7px}}
</style>
