/// <reference types="vite/client" />
import { describe, expect, it } from 'vitest'
import { parse } from '@vue/compiler-dom'
import { stockDetailSections } from './stockDetailSections'
import targetSource from '../views/TargetsView.vue?raw'
import candidateSource from '../views/DiscoveryCandidatesView.vue?raw'

describe('shared stock detail structure', () => {
  it('keeps one vocabulary and reading order for both entry points', () => {
    expect(stockDetailSections.map(section => section.label)).toEqual([
      '概览', '行情技术', '基本面', '事件公告', '机构持仓', '估值共识', '研究记录', '数据管理'
    ])
    expect(new Set(stockDetailSections.map(section => section.key)).size).toBe(8)
  })

  for (const view of ['TargetsView.vue', 'DiscoveryCandidatesView.vue']) {
    it(`${view} supplies all shared groups in DOM reading order`, () => {
      const source = view === 'TargetsView.vue' ? targetSource : candidateSource
      const start = source.indexOf('<StockDetailLayout ')
      const end = source.indexOf('</StockDetailLayout>', start) + '</StockDetailLayout>'.length
      const root = parse(source.slice(start, end))
      const layout = root.children[0] as any
      const slots = layout.children.filter((node: any) => node.type === 1 && node.tag === 'template')
        .map((node: any) => node.props.find((prop: any) => prop.type === 7 && prop.name === 'slot')?.arg?.content)
      expect(slots.filter((slot: string) => slot !== 'actions')).toEqual(stockDetailSections.map(section => section.key))
      expect(source).not.toContain('detail-order-')
      expect(source).toContain('size="min(960px, 100%)"')
      expect(source.slice(start, end)).toContain('InstitutionalOwnershipHistory')
    })
  }
})
