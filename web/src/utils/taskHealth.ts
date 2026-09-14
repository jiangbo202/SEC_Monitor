type TaskHealth = { enabled: boolean; last_status: string; consecutive_failures: number }

export function summarizeTaskHealth(rows: TaskHealth[]) {
  const enabled = rows.filter(row => row.enabled)
  const failed = enabled.filter(row => ['failed', 'interrupted'].includes(row.last_status)).length
  const partial = enabled.filter(row => row.last_status === 'partial').length
  const degraded = enabled.filter(row => row.last_status === 'degraded').length
  const skipped = enabled.filter(row => row.last_status === 'skipped').length
  const running = enabled.filter(row => row.last_status === 'running').length
  const idle = enabled.filter(row => !row.last_status || row.last_status === 'idle').length
  const knownStatuses = ['success', 'failed', 'interrupted', 'partial', 'degraded', 'skipped', 'running', 'idle']
  const unknown = enabled.filter(row => row.last_status && !knownStatuses.includes(row.last_status)).length
  const parts = [
    failed && `${failed} 项失败/中断`,
    partial && `${partial} 项部分完成`,
    degraded && `${degraded} 项降级完成`,
    skipped && `${skipped} 项已跳过`,
    running && `${running} 项运行中`,
    idle && `${idle} 项未运行`,
    unknown && `${unknown} 项状态未知`,
  ].filter(Boolean)
  return { attention: failed + partial > 0, summary: !enabled.length ? '未启用' : parts.join(' · ') || '最近执行成功' }
}
