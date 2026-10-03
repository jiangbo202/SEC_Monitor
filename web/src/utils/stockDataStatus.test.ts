import { describe, expect, it } from 'vitest'
import { stockDataStatus } from './stockDataStatus'
describe('stock data status', () => {
  it('distinguishes absence, failure, coverage and insufficient samples', () => {
    expect(stockDataStatus(null).label).toContain('未同步')
    expect(stockDataStatus({status:'failed'}).label).toContain('失败')
    expect(stockDataStatus({status:'no_coverage'}).label).toContain('覆盖')
    expect(stockDataStatus({status:'data_insufficient'}).label).toContain('样本')
  })
  it('does not confuse report dates with synchronization timestamps', () => {
    expect(stockDataStatus({as_of:'2026-06-30',created_at:'2026-10-01',fetched_at:'0001-01-01T00:00:00Z'})).toEqual({label:'本地记录',synced:'',asOf:'2026-06-30'})
    expect(stockDataStatus({fetched_at:'2026-10-01T00:00:00Z'}).synced).toBe('2026-10-01T00:00:00Z')
  })
})
