export function stockDataStatus(data: unknown) {
  const row = data && typeof data === 'object' ? data as Record<string, unknown> : null
  if (!row) return { label: '未读取 / 未同步', synced: '', asOf: '' }
  const status = String(row.status || '')
  const label = ['failed', 'error'].includes(status) ? '查询失败（已有记录仍可查看）'
    : ['no_coverage', 'no_history'].includes(status) ? '提供方暂无覆盖'
    : ['missing', 'not_synced'].includes(status) ? '未同步'
    : status === 'data_insufficient' ? '等待足够样本'
    : status === 'partial' ? '部分数据可用' : '本地记录'
  const valid = (value: unknown) => typeof value === 'string' && Number.isFinite(Date.parse(value)) && Date.parse(value) > 0 ? value : ''
  return { label, synced: valid(row.fetched_at) || valid(row.profile_fetched_at) || valid(row.metadata_as_of), asOf: valid(row.trade_date) || valid(row.as_of) }
}
