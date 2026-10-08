import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import StatsPanel from './StatsPanel.vue'
import type { StoredTicket } from '../types'

const now = new Date('2026-10-15T12:00:00Z')
const ago = (days: number) => new Date(now.getTime() - days * 86400_000).toISOString()

const t = (ref: string, over: Partial<StoredTicket> = {}): StoredTicket => ({ id: `/r|${ref}`, repo: '/r', ref, created: ago(20), updated: ago(1), ...over })

describe('StatsPanel', () => {
  it('explains itself when there is nothing yet', () => {
    expect(mount(StatsPanel, { props: { stored: [], tickets: [], now } }).text()).toContain('Nothing to count yet')
  })

  it('shows the average work in progress against the limit', () => {
    const many = ['A', 'B', 'C', 'D', 'E'].map((r) => t(r, { state: 'active', created: ago(30) }))
    const w = mount(StatsPanel, { props: { stored: many, tickets: [], wipLimit: 3, now } })
    expect(w.get('[data-stats-wip]').text()).toContain('5.0')
    expect(w.get('[data-stats-wip]').text()).toContain('above your limit of 3')
    const calm = mount(StatsPanel, { props: { stored: many.slice(0, 2), tickets: [], wipLimit: 3, now } })
    expect(calm.get('[data-stats-wip]').text()).toContain('within your limit')
  })

  it('lists the time per ticket and the finished tickets per week', () => {
    const done = t('A', { state: 'done', history: [{ from: '', to: 'active', at: ago(6) }, { from: 'active', to: 'done', at: ago(1) }] })
    const w = mount(StatsPanel, { props: { stored: [done], tickets: [], now } })
    expect(w.get('[data-stat-row="/r|A"]').text()).toContain('5 d')
    expect(w.findAll('[data-stats-done] li').length).toBe(8)
    expect(w.findAll('[data-stats-done] .n').map((n) => n.text()).filter((x) => x === '1')).toHaveLength(1)
  })

  it('tells what became of the discoveries', () => {
    const d = (status: 'open' | 'dropped') => ({ id: status, text: 'x', source: 'cli' as const, status, at: ago(1) })
    const w = mount(StatsPanel, { props: { stored: [t('A', { discoveries: [d('open'), d('dropped')] })], tickets: [], now } })
    expect(w.get('[data-stats-discoveries]').text()).toContain('2 noted: 1 still open')
  })
})
