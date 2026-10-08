import { describe, expect, it } from 'vitest'
import { groupTickets, sectionOf, stateActions, wipCount, wipLabel } from './board'
import { buildTickets, type Ticket } from './tickets'
import type { Pr, Session, StoredTicket, TicketState, Worktree } from './types'

const now = new Date('2026-10-02T12:00:00Z')
const ago = (days: number) => new Date(now.getTime() - days * 86400_000).toISOString()

const wt = (name: string, over: Partial<Worktree> = {}, commitDays = 1): Worktree => ({
  name, path: `/r/${name}`, branch: name, head: 'abc1234', detached: false, isMain: false,
  git: { dirty: false, staged: 0, modified: 0, untracked: 0, conflicts: 0, hasUpstream: true, ahead: 0, behind: 0,
    lastCommit: { hash: 'h', subject: 's', date: ago(commitDays) } },
  sessions: [], ...over,
})
const waiting: Session = { id: 's', source: 'cli', cwd: '/w', status: 'idle', state: 'waiting', lastActivity: ago(0.02), resumable: true, hasPlan: false, lastMessage: 'Which one?' }
const merged: Pr = { number: 1, title: 'T', url: 'https://x/1', source: 'a', dest: 'main', state: 'merged', draft: false, approvals: 0, changesRequested: 0, updatedAt: ago(1) }

const ticket = (w: Worktree, state?: TicketState): Ticket => {
  const stored: Record<string, StoredTicket> = {}
  if (state) {
    const ref = w.ticket ?? `branch:${w.branch}`
    stored[`/r|${ref}`] = { id: `/r|${ref}`, repo: '/r', ref, state, created: ago(9), updated: ago(9) }
  }
  return buildTickets([{ ...w, ticket: w.ticket ?? `branch:${w.branch}` }], [], 'main', { repo: '/r', stored, now })[0]
}

describe('sectionOf', () => {
  it('follows the human state', () => {
    for (const [state, want] of [['active', 'active'], ['blocked', 'blocked'], ['ready', 'ready'], ['parked', 'parked'], ['backlog', 'backlog'], ['done', 'done'], ['ignored', 'ignored']] as const) {
      expect(sectionOf(ticket(wt('a'), state), now)).toBe(want)
    }
    expect(sectionOf(ticket(wt('a')), now)).toBe('inbox')
  })

  it('shows what calls for you as such, whatever the state', () => {
    const asking = wt('a', { sessions: [waiting] })
    expect(sectionOf(ticket(asking, 'active'), now)).toBe('needs-you')
    expect(sectionOf(ticket(asking, 'parked'), now)).toBe('needs-you')
    expect(sectionOf(ticket(asking), now)).toBe('needs-you')
  })

  it('what you finished or ignored stays out of Needs you', () => {
    const asking = wt('a', { sessions: [waiting] })
    expect(sectionOf(ticket(asking, 'done'), now)).toBe('done')
    expect(sectionOf(ticket(asking, 'ignored'), now)).toBe('ignored')
  })

  it('offers to close a ticket whose PRs are all merged', () => {
    expect(sectionOf(ticket(wt('a', { pr: merged }), 'active'), now)).toBe('closable')
    expect(sectionOf(ticket(wt('a', { pr: merged }), 'done'), now)).toBe('done')
  })

  it('moves an undecided ticket to "old" only when nothing is happening in it', () => {
    expect(sectionOf(ticket(wt('a', {}, 40)), now)).toBe('dormant')
    expect(sectionOf(ticket(wt('a', {}, 3)), now)).toBe('inbox')
    expect(sectionOf(ticket(wt('a', { git: { ...wt('x').git, dirty: true, lastCommit: { hash: 'h', subject: 's', date: ago(40) } } })), now)).toBe('inbox')
    expect(sectionOf(ticket(wt('a', {}, 40), 'active'), now)).toBe('active') // a decision is never "old"
  })
})

describe('groupTickets', () => {
  it('lists only non-empty sections, in board order', () => {
    const list = [ticket(wt('p'), 'parked'), ticket(wt('a'), 'active'), ticket(wt('q', { sessions: [waiting] }), 'active')]
    expect(groupTickets(list, now).map((s) => s.id)).toEqual(['needs-you', 'active', 'parked'])
  })

  it('puts a question before a plain finished turn inside Needs you', () => {
    const asks = wt('asks', { sessions: [waiting] })
    const done = wt('done', { sessions: [{ ...waiting, id: 't', lastMessage: 'All done.' }] })
    const [section] = groupTickets([ticket(done), ticket(asks)], now)
    expect(section.tickets.map((t) => t.ref)).toEqual(['branch:asks', 'branch:done'])
  })

  it('orders the rest by latest activity', () => {
    const [section] = groupTickets([ticket(wt('old', {}, 5), 'active'), ticket(wt('new', {}, 1), 'active')], now)
    expect(section.tickets.map((t) => t.ref)).toEqual(['branch:new', 'branch:old'])
  })
})

describe('work in progress', () => {
  it('counts active tickets only, and warns softly above the limit', () => {
    const list = ['a', 'b', 'c', 'd'].map((n) => ticket(wt(n), 'active')).concat(ticket(wt('p'), 'parked'))
    expect(wipCount(list)).toBe(4)
    expect(wipLabel(4, 3)).toContain('4 active tickets, limit 3')
    expect(wipLabel(3, 3)).toBe('')
  })
})

describe('stateActions', () => {
  const labels = (t: Ticket) => stateActions(t).map((a) => a.label)

  it('offers a triage from the inbox and a way back from every end state', () => {
    expect(labels(ticket(wt('a')))).toEqual(['Activate', 'Park', 'Backlog', 'Ignore'])
    expect(labels(ticket(wt('a'), 'done'))).toEqual(['Reopen'])
    expect(stateActions(ticket(wt('a'), 'ignored'))[0]).toMatchObject({ label: 'Track again', state: '' })
  })

  it('offers "Mark done" first once every PR is merged', () => {
    expect(stateActions(ticket(wt('a', { pr: merged }), 'active'))[0]).toMatchObject({ label: 'Mark done', state: 'done', primary: true })
    expect(labels(ticket(wt('a'), 'active'))).not.toContain('Mark done')
  })

  it('a blocked ticket can be unblocked or parked', () => {
    expect(labels(ticket(wt('a'), 'blocked'))).toEqual(['Unblock', 'Park'])
  })
})
