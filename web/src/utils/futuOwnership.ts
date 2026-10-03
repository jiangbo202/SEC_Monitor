import type { TickerInstitutionalHoldingHistory } from '@/api/types'

// Refreshes can finish in either order; retain the newest Futu snapshot without
// replacing Longbridge rows or applying a response to another ticker.
export function mergeFutuOwnershipHistory(current: TickerInstitutionalHoldingHistory, refreshed: TickerInstitutionalHoldingHistory): TickerInstitutionalHoldingHistory {
  if (current.ticker !== refreshed.ticker) return current
  const currentTime = Date.parse(current.futu_aggregate_synced_at || '')
  const refreshedTime = Date.parse(refreshed.futu_aggregate_synced_at || '')
  if (Number.isFinite(currentTime) && (!Number.isFinite(refreshedTime) || currentTime > refreshedTime)) return current
  return {
    ...current,
    futu_aggregate_history: refreshed.futu_aggregate_history,
    futu_aggregate_status: refreshed.futu_aggregate_status,
    futu_aggregate_synced_at: refreshed.futu_aggregate_synced_at
  }
}
