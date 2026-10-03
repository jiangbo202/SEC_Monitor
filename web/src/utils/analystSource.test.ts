import {describe,it,expect} from 'vitest'
import {analystDistribution,futuAnalystProvenance} from './analystSource'
import type {AnalystRatingSnapshot} from '@/api/types'
describe('共识来源与数据口径',()=>{
 it('富途百分比不转为人数，未返回档位不显示为零',()=>{
  const snapshot={provider:'futu',analyst_count:7,target_analyst_count:5,strong_buy_pct:57.14,hold_pct:42.86,strong_buy_count:0,buy_count:0,hold_count:0,underperform_count:0,sell_count:0,target_average_micros:25000000,target_high_micros:30000000,target_low_micros:20000000} as AnalystRatingSnapshot
  expect(analystDistribution(snapshot)).toBe('强烈买入 57.14% · 买入 — · 持有 42.86% · 跑输 — · 卖出 —')
  const provenance=futuAnalystProvenance(snapshot,'2026-10-02','2026-10-03')
  expect(provenance.every(row=>row.source.startsWith('Futu'))).toBe(true)
  expect(provenance[2]!.value).toContain('报告币种')
  expect(provenance[2]!.value).not.toContain('$')
 })
 it('Longbridge 保留评级人数',()=>{const snapshot={provider:'longbridge',strong_buy_count:3,buy_count:2,hold_count:1,underperform_count:0,sell_count:0} as AnalystRatingSnapshot;expect(analystDistribution(snapshot)).toContain('强烈买入 3 · 买入 2')})
})
