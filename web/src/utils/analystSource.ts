import type {AnalystRatingSnapshot} from '@/api/types'
export function analystDistribution(snapshot:AnalystRatingSnapshot){
 const labels=['强烈买入','买入','持有','跑输','卖出']
 const values=snapshot.provider==='futu'?[snapshot.strong_buy_pct,snapshot.buy_pct,snapshot.hold_pct,snapshot.underperform_pct,snapshot.sell_pct]:[snapshot.strong_buy_count,snapshot.buy_count,snapshot.hold_count,snapshot.underperform_count,snapshot.sell_count]
 return labels.map((label,i)=>`${label} ${values[i]==null?'—':values[i]}${values[i]!=null&&snapshot.provider==='futu'?'%':''}`).join(' · ')
}
export function futuAnalystProvenance(snapshot:AnalystRatingSnapshot,providerUpdatedAt:string,fetchedAt:string){
 const price=(micros:number)=>micros?`${(micros/1e6).toLocaleString(undefined,{maximumFractionDigits:2})}（报告币种）`:'—'
 return [
  {result:'共识评级',value:snapshot.recommendation,source:'Futu analyst-consensus / rating',providerUpdatedAt,fetchedAt,note:'近三个月共识；不与 Longbridge 跨来源比较变化。'},
  {result:'覆盖数与分布',value:`${snapshot.analyst_count} 位覆盖；${analystDistribution(snapshot)}`,source:'Futu / total、strong_buy、hold、sell',providerUpdatedAt,fetchedAt,note:'评级字段为百分比，未返回的档位显示 —，不推算人数。'},
  {result:'目标价',value:`平均 ${price(snapshot.target_average_micros)}；${price(snapshot.target_low_micros)} – ${price(snapshot.target_high_micros)}`,source:'Futu / average、lowest、highest',providerUpdatedAt,fetchedAt,note:`目标价分析师 ${snapshot.target_analyst_count??'—'} 位；未返回币种，不默认美元、不计算上涨空间。`}
 ]
}
