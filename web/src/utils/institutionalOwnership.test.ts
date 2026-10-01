import { describe, expect, it } from 'vitest'
import type { InstitutionalOwnershipPoint } from '@/api/types'
import { finiteOwnershipValue, ownershipDate, ownershipDelta, ownershipSeries } from './institutionalOwnership'
const point=(overrides:Partial<InstitutionalOwnershipPoint>):InstitutionalOwnershipPoint=>({holder_id:'1',holder_name:'Capital',owner_type:'Institution',period:'Q1 2026',holding_date:'',provider_date:'2026-03-31',filing_date:'',percent_of_shares:2,shares_held:100,source_kind:'top',source_url:'',fetched_at:'2026-10-01',...overrides})
describe('institutional ownership semantics',()=>{
  it('preserves nulls and real zeros',()=>{for(const value of [null,undefined,'',false,NaN])expect(finiteOwnershipValue(value)).toBeNull();expect(finiteOwnershipValue(0)).toBe(0);expect(ownershipDelta(null,2)).toBeNull()})
  it('never fabricates a holding or filing date from fetch time',()=>{expect(ownershipSeries([point({})],'1','holding_date')).toEqual([]);expect(ownershipDate('2026/02/30')).toBe('')})
  it('excludes rolling latest from quarterly history and prefers detail',()=>{const series=ownershipSeries([point({period:'Latest',percent_of_shares:10}),point({}),point({source_kind:'detail',percent_of_shares:3}),point({holder_id:'2',percent_of_shares:90})],'1','provider_date');expect(series).toHaveLength(1);expect(series[0].ratio).toBe(3)})
  it('keeps gaps, compares only same holder, limits after chronological sorting',()=>{const rows=[point({provider_date:'2026-06-30',period:'Q2 2026',percent_of_shares:null}),point({}),point({provider_date:'2025-12-31',period:'Q4 2025'})];const series=ownershipSeries(rows,'1','provider_date',2);expect(series.map(row=>row.date)).toEqual(['2026-03-31','2026-06-30']);expect(series[1].ratio).toBeNull()})
  it('does not assign a new quarter ratio to an older provider date',()=>{expect(ownershipSeries([point({provider_date:'2025-12-31',period:'Q1 2026'})],'1','provider_date')).toEqual([])})
})
