export type ConfigurationSection = 'general' | 'maintenance' | 'connections' | 'data' | 'ai' | 'notifications'
export type ConfigurationScope = 'system' | 'integrations'

export const configurationSections = [
  { key: 'general', label: '基础与调度', hint: '界面语言与调度时区', scope: 'system' },
  { key: 'maintenance', label: '存储与维护', hint: '保留策略、清理预览与备份导出', scope: 'system' },
  { key: 'connections', label: '连接与凭据', hint: '供应商密钥、接口地址与连接测试', scope: 'integrations' },
  { key: 'data', label: '同步策略', hint: '研究预算、财报、IPO 与 SEC 同步参数', scope: 'integrations' },
  { key: 'ai', label: 'AI 模型', hint: '手动调用的模型供应商与提示词', scope: 'integrations' },
  { key: 'notifications', label: '通知与 Telegram', hint: '通知通道、事件筛选与发送边界', scope: 'integrations' }
] as const

// Keep bookmarked settings URLs working after moving integrations out of system settings.
export function integrationSection(value: unknown): ConfigurationSection | undefined {
  if (value === 'discovery') return 'data'
  return configurationSections.find(item => item.scope === 'integrations' && item.key === value)?.key
}

const connectionKeys = new Set([
  'discovery.stooq_urls', 'discovery.longbridge_app_key', 'discovery.longbridge_app_secret',
  'discovery.longbridge_access_token'
])

export function configurationOwnsKey(section: ConfigurationSection, key: string, legacyPriceRoute = false): boolean {
  if (section === 'general') return key.startsWith('ui.') || key.startsWith('scheduler.')
  if (section === 'maintenance') return key.startsWith('system.')
  // Normal price routes are saved through the API route editor with a concurrency check.
  if (key === 'discovery.price_provider') return section === 'connections' && legacyPriceRoute
  if (section === 'connections') return connectionKeys.has(key)
  if (section === 'data') return !connectionKeys.has(key) && ['sec.', 'discovery.', 'ipo.', 'earnings_preview.', 'analyst_rating.'].some(prefix => key.startsWith(prefix))
  if (section === 'notifications') return ['notification.', 'in_app_notification.', 'telegram_notification.', 'candidate_notification.', 'trade_setup_notification.'].some(prefix => key.startsWith(prefix))
  return false
}

export function isLegacyPriceRoute(value: string): boolean {
  return !value || value.split(',').some(source => !['longbridge', 'futu'].includes(source.trim()))
}
