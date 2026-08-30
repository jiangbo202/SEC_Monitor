import { describe, expect, it } from 'vitest'
import { insiderRouteState, normalizedTickerQuery, targetRouteState } from './researchRouteState'

describe('research route state', () => {
  it('normalizes ticker deep links from notifications', () => {
    expect(normalizedTickerQuery(' oabi ')).toBe('OABI')
    expect(normalizedTickerQuery(['rklb', 'ignored'])).toBe('RKLB')
    expect(normalizedTickerQuery({ ticker: 'bad' })).toBe('')
  })

  it('opens the 10b5-1 plan tab with its ticker filter', () => {
    expect(insiderRouteState({ tab: 'plans', ticker: ' rklb ' })).toEqual({
      tab: 'plans', transactionTicker: '', planTicker: 'RKLB',
    })
  })

  it('keeps normal insider links on the transaction filter', () => {
    expect(insiderRouteState({ ticker: 'cbrs' })).toEqual({
      tab: 'transactions', transactionTicker: 'CBRS', planTicker: '',
    })
  })

  it('accepts only supported watch-target statuses', () => {
    expect(targetRouteState({ ticker: 'tsla', status: 'enabled' })).toEqual({ ticker: 'TSLA', status: 'enabled' })
    expect(targetRouteState({ ticker: 'tsla', status: 'unknown' })).toEqual({ ticker: 'TSLA', status: '' })
  })
})
