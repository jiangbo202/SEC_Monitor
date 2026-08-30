export type QueryValue = string | string[] | null | undefined

export function normalizedTickerQuery(value: unknown): string {
  const raw = Array.isArray(value) ? value[0] : value
  return typeof raw === 'string' ? raw.trim().toUpperCase() : ''
}

export function insiderRouteState(query: Record<string, unknown>) {
  const ticker = normalizedTickerQuery(query.ticker)
  const tab = query.tab === 'plans' ? 'plans' : 'transactions'
  return {
    tab,
    transactionTicker: tab === 'transactions' ? ticker : '',
    planTicker: tab === 'plans' ? ticker : '',
  }
}

export function targetRouteState(query: Record<string, unknown>) {
  return {
    ticker: normalizedTickerQuery(query.ticker),
    status: typeof query.status === 'string' && ['enabled', 'disabled'].includes(query.status) ? query.status : '',
  }
}
