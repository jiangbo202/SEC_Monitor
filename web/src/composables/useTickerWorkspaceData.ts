import { onUnmounted, ref, type Ref } from 'vue'
import { apiClient } from '@/api/client'

// Every module updates as soon as its local request finishes. Same-symbol
// refreshes retain the last successful value; a symbol change clears it.
export function useTickerWorkspaceData(symbol: Ref<string>) {
  const loading=ref(false), errors=ref<string[]>([]), pending=ref<string[]>([])
  const evaluation=ref<any>(null), filings=ref<any[]>([]), insiders=ref<any[]>([]), analyses=ref<any[]>([])
  const holdings=ref<any>({institutional_holders:[],fund_holders:[]}), watchTarget=ref<any>(null)
  const localTechnicalHistory=ref<any>(null), insiderSummary=ref<any>({transactions:0})
  let generation=0, loadedSymbol='', controller:AbortController|undefined
  const items=(response:any)=>response?.data?.data?.items || response?.data?.data || []
  async function load() {
    const current=++generation, ticker=symbol.value
    controller?.abort();controller=new AbortController()
    if(ticker!==loadedSymbol) {
      evaluation.value=null;filings.value=[];insiders.value=[];analyses.value=[]
      holdings.value={institutional_holders:[],fund_holders:[]};watchTarget.value=null;localTechnicalHistory.value=null;insiderSummary.value={transactions:0}
      loadedSymbol=ticker
    }
    errors.value=[];pending.value=[];loading.value=!!ticker
    if(!ticker)return
    const options={signal:controller.signal}
    const jobs:Array<[string,()=>Promise<any>,(response:any)=>void]>=[
      ['评估',()=>apiClient.get('/ticker-evaluations',{...options,params:{ticker,page:1,page_size:1,view:'summary'}}),r=>{evaluation.value=items(r)[0]||null}],
      ['SEC',()=>apiClient.get('/filings',{...options,params:{ticker,page:1,page_size:8}}),r=>{filings.value=items(r).map((row:any)=>({...row,filing_date:row.filing_date?.slice(0,10)}))}],
      ['内幕交易',()=>apiClient.get('/insider-transactions',{...options,params:{ticker,page:1,page_size:8}}),r=>{insiders.value=items(r).map((row:any)=>({...row,transaction_date:row.transaction_date?.slice(0,10)}));insiderSummary.value=r?.data?.data?.summary||{transactions:r?.data?.data?.total||0}}],
      ['机构持仓',()=>apiClient.get(`/discovery/institutional-holdings/${encodeURIComponent(ticker)}`,options),r=>{holdings.value=r?.data?.data||{institutional_holders:[],fund_holders:[]}}],
      ['AI',()=>apiClient.get('/ai/analyses',{...options,params:{ticker,page:1,page_size:5,view:'summary'}}),r=>{analyses.value=items(r)}],
      ['监控标的',()=>apiClient.get('/watch-targets',{...options,params:{ticker,page:1,page_size:20}}),r=>{watchTarget.value=items(r)[0]||null}],
    ]
    pending.value=jobs.map(([name])=>name)
    await Promise.all(jobs.map(async([name,request,apply])=>{
      try{const response=await request();if(current===generation)apply(response)}
      catch{if(current===generation)errors.value.push(name)}
      finally{if(current===generation)pending.value=pending.value.filter(item=>item!==name)}
    }))
    if(current!==generation)return
    if(watchTarget.value?.id && !evaluation.value?.candidate_score?.technical?.trade_date) {
      pending.value.push('技术历史')
      try {const r=await apiClient.get(`/watch-targets/${watchTarget.value.id}/technical-history`,options);if(current===generation)localTechnicalHistory.value=r?.data?.data||null}
      catch{if(current===generation)errors.value.push('技术历史')}
      finally{if(current===generation)pending.value=pending.value.filter(item=>item!=='技术历史')}
    }
    if(current===generation)loading.value=false
  }
  onUnmounted(()=>{generation++;controller?.abort()})
  return {loading,errors,pending,evaluation,filings,insiders,analyses,holdings,watchTarget,localTechnicalHistory,insiderSummary,load}
}
