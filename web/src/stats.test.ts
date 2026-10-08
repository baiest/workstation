import { describe, expect, it } from 'vitest'
import { computeStats, formatDuration, spansOf } from './stats'
import { buildTickets } from './tickets'
import type { StoredTicket, TicketState, Worktree } from './types'

const now = new Date('2026-10-15T12:00:00Z')
const DAY = 86400_000
const ago = (days: number) => new Date(now.getTime() - days * DAY).toISOString()

const t = (ref: string, over: Partial<StoredTicket> = {}): StoredTicket => ({ id: `/r|${ref}`, repo: '/r', ref, created: ago(20), updated: ago(1), ...over })
const step = (from: TicketState, to: TicketState, days: number) => ({ from, to, at: ago(days) })

describe('spansOf', () => {
  it('walks the history from creation until now', () => {
    const spans = spansOf(t('A', { state: 'done', history: [step('', 'active', 10), step('active', 'blocked', 6), step('blocked', 'active', 4), step('active', 'done', 1)] }), now)
    expect(spans.map((s) => [s.state, Math.round((s.to - s.from) / DAY)])).toEqual([['', 10], ['active', 4], ['blocked', 2], ['active', 3], ['done', 1]])
  })

  it('counts a ticket without history in its present state since it was created', () => {
    expect(spansOf(t('A', { state: 'active' }), now)).toEqual([{ state: 'active', from: Date.parse(ago(20)), to: now.getTime() }])
  })

  it('is empty for a ticket with no valid creation date, and tolerates unordered history', () => {
    expect(spansOf(t('A', { created: 'nope' }), now)).toEqual([])
    const shuffled = spansOf(t('A', { history: [step('active', 'done', 1), step('', 'active', 10)] }), now)
    expect(shuffled.map((s) => s.state)).toEqual(['', 'active', 'done'])
  })
})

describe('computeStats', () => {
  it('adds up the time spent active and blocked per ticket, most first', () => {
    const stats = computeStats(
      [
        t('A', { state: 'done', history: [step('', 'active', 10), step('active', 'blocked', 6), step('blocked', 'active', 4), step('active', 'done', 1)] }),
        t('B', { state: 'active', created: ago(3), history: [step('', 'active', 2)] }),
        t('C', { state: 'parked', history: [step('', 'parked', 5)] }), // never active or blocked: not listed
      ],
      [],
      now,
    )
    expect(stats.perTicket.map((x) => [x.label, Math.round(x.activeMs / DAY), Math.round(x.blockedMs / DAY)])).toEqual([['A', 7, 2], ['B', 2, 0]])
  })

  it('counts the tickets finished in each of the last weeks', () => {
    const done = (ref: string, days: number) => t(ref, { state: 'done', history: [step('active', 'done', days)] })
    const stats = computeStats([done('A', 1), done('B', 2), done('C', 9), done('D', 100)], [], now, { weeks: 4 })
    expect(stats.doneByWeek.map((w) => w.count)).toEqual([0, 0, 1, 2]) // the one from 100 days ago is out of range
    expect(stats.doneByWeek).toHaveLength(4)
  })

  it('averages how many tickets were carried at once over the window', () => {
    // A was active the whole 14 days, B for the last 7: (14 + 7) / 14 = 1.5
    const stats = computeStats([t('A', { state: 'active', created: ago(30) }), t('B', { state: 'active', created: ago(7) })], [], now, { days: 14 })
    expect(stats.averageActive).toBeCloseTo(1.5, 5)
    expect(computeStats([], [], now).averageActive).toBe(0)
  })

  it('tallies what became of the discoveries', () => {
    const d = (status: 'open' | 'dropped' | 'in-scope' | 'promoted', n: number) =>
      Array.from({ length: n }, (_, i) => ({ id: `${status}${i}`, text: 'x', source: 'cli' as const, status, at: ago(1) }))
    const stats = computeStats([t('A', { discoveries: [...d('open', 2), ...d('dropped', 3), ...d('in-scope', 1), ...d('promoted', 1)] })], [], now)
    expect(stats.discoveries).toEqual({ total: 7, open: 2, dropped: 3, inScope: 1, promoted: 1 })
  })

  it('knows how many sessions a ticket had, by its live label', () => {
    const w: Worktree = {
      name: 'REG-1-a', path: '/r/a', branch: 'REG-1-a', head: 'abc', detached: false, isMain: false, ticket: 'REG-1',
      git: { dirty: false, staged: 0, modified: 0, untracked: 0, conflicts: 0, hasUpstream: true, ahead: 0, behind: 0 },
      sessions: ['s1', 's2', 's3'].map((id) => ({ id, source: 'cli' as const, cwd: '/w', status: 'stopped' as const, lastActivity: ago(5), resumable: true, hasPlan: false })),
    }
    const stored = t('REG-1', { state: 'active', created: ago(3) })
    const live = buildTickets([w], [], 'main', { repo: '/r', stored: { [stored.id]: stored }, now })
    expect(computeStats([stored], live, now).perTicket[0]).toMatchObject({ label: 'REG-1', sessions: 3 })
  })
})

describe('formatDuration', () => {
  it.each([
    [10_000, 'under a minute'],
    [20 * 60_000, '20 min'],
    [5 * 3600_000, '5 h'],
    [3 * DAY + 4 * 3600_000, '3 d 4 h'],
    [2 * DAY, '2 d'],
  ])('%d ms is %s', (ms, text) => expect(formatDuration(ms)).toBe(text))
})
