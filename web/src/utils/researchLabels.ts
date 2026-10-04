type InsiderDirection = { direction?: string; transaction_code?: string }
export function insiderDirectionLabel(row: InsiderDirection) {
  if (row.direction === 'buy') return row.transaction_code === 'P' ? '公开市场买入' : '取得'
  if (row.direction === 'sell') return row.transaction_code === 'S' ? '公开市场卖出' : '处置'
  return row.transaction_code || '其他'
}
export function insiderDirectionType(row: InsiderDirection) {
  return row.direction === 'buy' ? 'success' : row.direction === 'sell' ? 'danger' : 'info'
}
export function aiStatusLabel(value: string) {
  return ({ success: '成功', failed: '失败', queued: '排队中', running: '处理中' } as Record<string, string>)[value] || value || '未记录'
}
export function aiStatusType(value: string) {
  return value === 'success' ? 'success' : value === 'failed' ? 'danger' : ['queued', 'running'].includes(value) ? 'warning' : 'info'
}
