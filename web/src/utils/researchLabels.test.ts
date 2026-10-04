import { describe, expect, it } from 'vitest'
import { aiStatusLabel, aiStatusType, insiderDirectionLabel, insiderDirectionType } from './researchLabels'
describe('shared research meanings', () => {
  it('distinguishes market trades from acquisitions, dispositions and unknown codes', () => {
    expect(insiderDirectionLabel({ direction: 'buy', transaction_code: 'P' })).toBe('公开市场买入')
    expect(insiderDirectionLabel({ direction: 'sell', transaction_code: 'S' })).toBe('公开市场卖出')
    for (const code of ['A', 'M', 'G']) expect(insiderDirectionLabel({ direction: 'buy', transaction_code: code })).toBe('取得')
    expect(insiderDirectionLabel({ direction: 'sell', transaction_code: 'F' })).toBe('处置')
    expect(insiderDirectionType({ transaction_code: 'J' })).toBe('info')
    expect(insiderDirectionLabel({ transaction_code: 'J' })).toBe('J')
  })
  it('does not report pending AI jobs as failures', () => {
    expect(aiStatusLabel('queued')).toBe('排队中')
    expect(aiStatusLabel('running')).toBe('处理中')
    expect(aiStatusType('running')).toBe('warning')
    expect(aiStatusType('failed')).toBe('danger')
    expect(aiStatusType('unknown')).toBe('info')
  })
})
