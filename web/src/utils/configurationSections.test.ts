import { describe, expect, it } from 'vitest'
import { configurationOwnsKey, integrationSection, isLegacyPriceRoute } from './configurationSections'

describe('configuration boundaries', () => {
  it('keeps system saves away from integration credentials and routes', () => {
    const keys = ['ui.default_locale', 'scheduler.timezone', 'system.backup_dir', 'discovery.longbridge_app_key', 'discovery.price_provider', 'notification.quiet_hours_enabled']
    expect(keys.filter(key => configurationOwnsKey('general', key))).toEqual(['ui.default_locale', 'scheduler.timezone'])
    expect(keys.filter(key => configurationOwnsKey('maintenance', key))).toEqual(['system.backup_dir'])
  })
  it('sync saves do not overwrite credentials or the route maintained by another editor', () => {
    const keys = ['sec.user_agent', 'discovery.longbridge_candidate_research_request_budget', 'discovery.longbridge_app_secret', 'discovery.price_provider', 'ipo.lookback_days']
    expect(keys.filter(key => configurationOwnsKey('data', key))).toEqual(['sec.user_agent', 'discovery.longbridge_candidate_research_request_budget', 'ipo.lookback_days'])
  })
  it('allows only the connection section to migrate a legacy price route', () => {
    expect(configurationOwnsKey('connections', 'discovery.price_provider')).toBe(false)
    expect(configurationOwnsKey('connections', 'discovery.price_provider', true)).toBe(true)
    expect(configurationOwnsKey('data', 'discovery.price_provider', true)).toBe(false)
    expect(isLegacyPriceRoute('stooq')).toBe(true)
    expect(isLegacyPriceRoute('')).toBe(true)
    expect(isLegacyPriceRoute('futu,longbridge')).toBe(false)
  })
  it('maps old settings bookmarks without redirecting common system settings', () => {
    expect(integrationSection('discovery')).toBe('data')
    expect(integrationSection('ai')).toBe('ai')
    expect(integrationSection('notifications')).toBe('notifications')
    expect(integrationSection('general')).toBeUndefined()
    expect(integrationSection('maintenance')).toBeUndefined()
    expect(integrationSection(['ai'])).toBeUndefined()
  })
})
