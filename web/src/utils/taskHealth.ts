type TaskHealth = { enabled: boolean; last_status: string; consecutive_failures: number }

export function summarizeTaskHealth(rows: TaskHealth[]) {
  const enabled = rows.filter(row => row.enabled)
  const failed = enabled.filter(row => ['failed', 'interrupted'].includes(row.last_status)).length
  const partial = enabled.filter(row => row.last_status === 'partial').length
  const running = enabled.filter(row => row.last_status === 'running').length
  const unknown = enabled.filter(row => !['success', 'failed', 'interrupted', 'partial', 'running'].includes(row.last_status)).length
  const parts = [failed && `${failed} 项失败/中断`, partial && `${partial} 项部分完成`, running && `${running} 项运行中`, unknown && `${unknown} 项未验证`].filter(Boolean)
  return { attention: failed + partial > 0, summary: !enabled.length ? '未启用' : parts.join(' · ') || '最近执行成功' }
}
