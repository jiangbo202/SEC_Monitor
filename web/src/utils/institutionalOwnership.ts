import type { InstitutionalOwnershipPoint } from '@/api/types'

export function finiteOwnershipValue(value: unknown): number | null {
  if (value === null || value === undefined || value === '' || typeof value === 'boolean') return null
  const number = Number(value)
  return Number.isFinite(number) ? number : null
}

export function ownershipDate(value: string): string {
  const normalized = (value || '').replace(/[/.]/g, '-')
  if (!/^\d{4}-\d{2}-\d{2}$/.test(normalized)) return ''
  const date = new Date(`${normalized}T00:00:00Z`)
  return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === normalized ? normalized : ''
}

// Never combine holdings-as-of dates with filing dates or fetch timestamps.
export function ownershipSeries(rows: InstitutionalOwnershipPoint[], holderID: string, basis: 'holding_date' | 'provider_date', limit = 8) {
  const byDate = new Map<string, InstitutionalOwnershipPoint>()
  const holderRows = rows.filter(row => row.holder_id === holderID)
  const hasQuarter = holderRows.some(row => /^Q[1-4] \d{4}$/.test(row.period))
  for (const row of holderRows) {
    // Latest can use today's denominator and disagree with the same quarter's
    // historical ratio. Do not splice this rolling valuation into the trend.
    if (hasQuarter && /^(Latest|最新)$/i.test(row.period)) continue
    const date = ownershipDate(row[basis])
    if (!date) continue
    const quarter = /^Q([1-4]) (\d{4})$/.exec(row.period)
    // Some periods repeat an older filing_date with a newly calculated ratio.
    // Do not attribute that new period's ratio to the older date on a trend.
    if (basis === 'provider_date' && quarter && (date.slice(0,4) !== quarter[2] || Math.ceil(Number(date.slice(5,7))/3) !== Number(quarter[1]))) continue
    const prior = byDate.get(date)
    if (!prior || (row.source_kind === 'detail' && prior.source_kind !== 'detail') || (row.source_kind === prior.source_kind && row.fetched_at > prior.fetched_at)) byDate.set(date, row)
  }
  const sorted = [...byDate].sort(([a], [b]) => a.localeCompare(b)).map(([date, row]) => ({ ...row, date, ratio: finiteOwnershipValue(row.percent_of_shares) }))
  return limit > 0 ? sorted.slice(-limit) : sorted
}

export function ownershipDelta(current: unknown, previous: unknown): number | null {
  const a = finiteOwnershipValue(current), b = finiteOwnershipValue(previous)
  return a === null || b === null ? null : a - b
}
