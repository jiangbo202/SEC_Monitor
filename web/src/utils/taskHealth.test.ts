import { describe, expect, it } from 'vitest'
import { summarizeTaskHealth } from './taskHealth'

describe('task health summary', () => {
  it('never reports partial or unverified work as healthy', () => {
    expect(summarizeTaskHealth([{ enabled: true, last_status: 'partial', consecutive_failures: 0 }])).toEqual({ attention: true, summary: '1 项部分完成' })
    expect(summarizeTaskHealth([{ enabled: true, last_status: '', consecutive_failures: 0 }]).summary).toContain('未验证')
    expect(summarizeTaskHealth([{ enabled: false, last_status: 'failed', consecutive_failures: 3 }]).summary).toBe('未启用')
  })
})
