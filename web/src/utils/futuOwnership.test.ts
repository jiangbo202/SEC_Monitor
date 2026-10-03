import { expect, it } from 'vitest'
import type { TickerInstitutionalHoldingHistory } from '@/api/types'
import { mergeFutuOwnershipHistory } from './futuOwnership'
const base: TickerInstitutionalHoldingHistory = { ticker: 'TEST', institutional_holders: [], fund_holders: [], ownership_history: [], other_holders: [], message: 'Longbridge unchanged', futu_aggregate_status: 'not_synced' }
it('updates only Futu fields for the same ticker', () => {
  const refreshed = { ...base, message: 'stale Longbridge message', futu_aggregate_status: 'available', futu_aggregate_synced_at: '2026-10-03T08:00:00Z' }
  expect(mergeFutuOwnershipHistory(base, refreshed)).toEqual({ ...refreshed, message: base.message })
  expect(mergeFutuOwnershipHistory(base, { ...refreshed, ticker: 'OTHER' })).toBe(base)
})
it('retains a newer Futu snapshot when another refresh finishes later with older data', () => {
  const current = { ...base, futu_aggregate_status: 'available', futu_aggregate_synced_at: '2026-10-03T08:00:00Z' }
  expect(mergeFutuOwnershipHistory(current, { ...base, futu_aggregate_synced_at: '2026-10-02T08:00:00Z' })).toBe(current)
  expect(mergeFutuOwnershipHistory(current, base)).toBe(current)
})
