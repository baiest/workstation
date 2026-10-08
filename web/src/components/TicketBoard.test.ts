import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import TicketBoard from './TicketBoard.vue'
import { buildTickets } from '../tickets'
import type { Session, StoredTicket, TicketState, Worktree } from '../types'

const now = new Date('2026-10-02T12:00:00Z')
const ago = (days: number) => new Date(now.getTime() - days * 86400_000).toISOString()

const wt = (name: string, sessions: Session[] = [], commitDays = 1): Worktree => ({
  name, path: `/r/${name}`, branch: name, head: 'abc', detached: false, isMain: false, ticket: `branch:${name}`,
  git: { dirty: false, staged: 0, modified: 0, untracked: 0, conflicts: 0, hasUpstream: true, ahead: 0, behind: 0,
    lastCommit: { hash: 'h', subject: 's', date: ago(commitDays) } },
  sessions,
})
const asks: Session = { id: 's', source: 'cli', cwd: '/w', status: 'idle', state: 'waiting', lastActivity: ago(0.02), resumable: true, hasPlan: false, lastMessage: 'Which?' }

function board(list: [Worktree, TicketState?][], props: Record<string, unknown> = {}) {
  const stored: Record<string, StoredTicket> = {}
  for (const [w, state] of list) {
    if (state) stored[`/r|${w.ticket}`] = { id: `/r|${w.ticket}`, repo: '/r', ref: w.ticket!, state, created: ago(9), updated: ago(9) }
  }
  const tickets = buildTickets(list.map(([w]) => w), [], 'main', { repo: '/r', stored, now })
  return mount(TicketBoard, { props: { tickets, editor: 'cursor', now, ...props } })
}

describe('TicketBoard', () => {
  it('puts each ticket in the section its state and signals say', () => {
    const w = board([[wt('a'), 'active'], [wt('b'), 'parked'], [wt('c', [asks]), 'active'], [wt('d')]])
    const ids = w.findAll('[data-section]').map((s) => s.attributes('data-section'))
    expect(ids).toEqual(['needs-you', 'active', 'inbox', 'parked'])
    expect(w.get('[data-section=needs-you] [data-ticket]').attributes('data-ticket')).toBe('branch:c')
    expect(w.get('[data-section=active] .count').text()).toBe('1')
  })

  it('keeps what is not happening now folded away until asked', async () => {
    const w = board([[wt('old'), 'backlog'], [wt('x'), 'active']])
    expect(w.find('[data-section=backlog] [data-ticket]').exists()).toBe(false)
    await w.get('[data-section=backlog] [data-toggle-section]').trigger('click')
    expect(w.find('[data-section=backlog] [data-ticket]').exists()).toBe(true)
  })

  it('warns softly above the active limit', () => {
    const many = ['a', 'bb', 'ccc', 'dddd'].map((n): [Worktree, TicketState] => [wt(n), 'active'])
    expect(board(many).get('[data-wip]').text()).toContain('4 active tickets, limit 3')
    expect(board(many.slice(0, 3)).find('[data-wip]').exists()).toBe(false)
    expect(board(many, { wipLimit: 5 }).find('[data-wip]').exists()).toBe(false)
  })

  it('names the project only when there is more than one', () => {
    const a = buildTickets([wt('a')], [], 'main', { repo: '/r1' })
    const b = buildTickets([wt('b')], [], 'main', { repo: '/r2' })
    const names = { '/r1': 'One', '/r2': 'Two' }
    const two = mount(TicketBoard, { props: { tickets: [...a, ...b], editor: '', now, repoNames: names } })
    expect(two.findAll('.repo-badge').map((e) => e.text()).sort()).toEqual(['One', 'Two'])
    const one = mount(TicketBoard, { props: { tickets: a, editor: '', now, repoNames: names } })
    expect(one.find('.repo-badge').exists()).toBe(false)
  })

  it('says so when there is nothing yet', () => {
    expect(board([]).text()).toContain('No tickets yet')
  })

  it('forwards what happens on a card', async () => {
    const w = board([[wt('a'), 'active']])
    await w.get('[data-state=parked]').trigger('click')
    expect(w.emitted('state')![0]).toEqual([expect.objectContaining({ ref: 'branch:a' }), 'parked'])
    await w.get('[data-action=terminal]').trigger('click')
    expect(w.emitted('terminal')![0]).toEqual(['/r/a'])
  })
})
