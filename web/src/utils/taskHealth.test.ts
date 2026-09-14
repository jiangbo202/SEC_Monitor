import { describe, expect, it } from 'vitest'
import { summarizeTaskHealth } from './taskHealth'

describe('task health summary', () => {
  it('never reports partial or unstarted work as healthy', () => {
    expect(summarizeTaskHealth([{ enabled: true, last_status: 'partial', consecutive_failures: 0 }])).toEqual({ attention: true, summary: '1 项部分完成' })
    expect(summarizeTaskHealth([{ enabled: true, last_status: '', consecutive_failures: 0 }]).summary).toContain('未运行')
    expect(summarizeTaskHealth([{ enabled: false, last_status: 'failed', consecutive_failures: 3 }]).summary).toBe('未启用')
  })

  it('keeps degraded, skipped, idle, and unknown states distinct', () => {
    expect(summarizeTaskHealth([
      { enabled: true, last_status: 'degraded', consecutive_failures: 0 },
      { enabled: true, last_status: 'skipped', consecutive_failures: 0 },
      { enabled: true, last_status: 'idle', consecutive_failures: 0 },
      { enabled: true, last_status: 'future_status', consecutive_failures: 0 },
    ])).toEqual({
      attention: false,
      summary: '1 项降级完成 · 1 项已跳过 · 1 项未运行 · 1 项状态未知',
    })
  })

  it('does not include disabled task states in the summary', () => {
    expect(summarizeTaskHealth([
      { enabled: true, last_status: 'success', consecutive_failures: 0 },
      { enabled: false, last_status: '', consecutive_failures: 0 },
      { enabled: false, last_status: 'failed', consecutive_failures: 3 },
    ])).toEqual({ attention: false, summary: '最近执行成功' })
  })
})
