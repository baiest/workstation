import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import TicketCard from './TicketCard.vue'
import { buildTickets, type Ticket } from '../tickets'
import type { Pr, Session, StoredTicket, Worktree } from '../types'

const now = new Date('2026-10-02T12:00:00Z')

const session = (over: Partial<Session> = {}): Session => ({
  id: 's1', source: 'cli', cwd: '/w', status: 'working', state: 'thinking', lastActivity: '2026-10-02T11:58:00Z', resumable: true, hasPlan: false, ...over,
})
const wt = (over: Partial<Worktree> = {}): Worktree => ({
  name: 'REG-1-wt', path: '/r/REG-1-wt', branch: 'REG-1-thing', head: 'abc1234', detached: false, isMain: false, ticket: 'REG-1',
  git: { dirty: false, staged: 0, modified: 0, untracked: 0, conflicts: 0, hasUpstream: true, ahead: 0, behind: 0,
    lastCommit: { hash: 'h', subject: 's', date: '2026-10-02T09:00:00Z' } },
  sessions: [session()], ...over,
})
const merged: Pr = { number: 9, title: 'Done thing', url: 'https://x/9', source: 'REG-1-thing', dest: 'main', state: 'merged', draft: false, approvals: 0, changesRequested: 0, updatedAt: '2026-10-01T00:00:00Z' }

function ticket(stored: Partial<StoredTicket> = {}, w: Worktree = wt()): Ticket {
  const st: StoredTicket = { id: '/r|REG-1', repo: '/r', ref: 'REG-1', created: '2026-10-01T00:00:00Z', updated: '2026-10-01T00:00:00Z', ...stored }
  return buildTickets([w], [], 'main', { repo: '/r', stored: Object.keys(stored).length ? { [st.id]: st } : {}, now })[0]
}
const render = (t: Ticket, props: Record<string, unknown> = {}) => mount(TicketCard, { props: { ticket: t, editor: 'cursor', now, ...props } })

describe('TicketCard', () => {
  it('shows the key, the title and what Claude asks of you', () => {
    const w = render(ticket({}, wt({ sessions: [session({ status: 'idle', state: 'waiting', lastActivity: '2026-10-02T11:50:00Z', lastMessage: 'Which one?' })] })))
    expect(w.get('.ticket-key').text()).toBe('REG-1')
    expect(w.get('h3').text()).toBe('REG-1-thing')
    expect(w.get('.signal').text()).toBe('asks you')
  })

  it('names a branch ticket by its branch and shows no key badge', () => {
    const w = render(ticket({}, wt({ branch: 'fix/typo', ticket: 'branch:fix/typo', name: 'typo' })))
    expect(w.find('.ticket-key').exists()).toBe(false)
    expect(w.get('h3').text()).toBe('fix/typo')
  })

  it('offers the moves of the state it is in, and emits the chosen one', async () => {
    const w = render(ticket())
    expect(w.findAll('.ticket-actions [data-state]').map((b) => b.text())).toEqual(['Activate', 'Park', 'Backlog', 'Ignore'])
    await w.get('[data-state=parked]').trigger('click')
    expect(w.emitted('state')![0]).toEqual([expect.objectContaining({ ref: 'REG-1' }), 'parked'])
  })

  it('asks why before blocking, and sends the reason with the state', async () => {
    const w = render(ticket({ state: 'active' }))
    await w.get('[data-state=blocked]').trigger('click')
    expect(w.emitted('state')).toBeUndefined() // not yet
    await w.get('[data-blocked-input]').setValue('waits for Ana')
    await w.get('[data-blocked-save]').trigger('click')
    expect(w.emitted('state')![0]).toEqual([expect.objectContaining({ ref: 'REG-1' }), 'blocked', 'waits for Ana'])
  })

  it('shows why a blocked ticket is blocked', () => {
    expect(render(ticket({ state: 'blocked', blockedReason: 'waits for Ana' })).get('[data-blocked]').text()).toContain('waits for Ana')
  })

  it('edits the next step: Ctrl+Enter saves, Escape drops the edit', async () => {
    const w = render(ticket({ nextStep: 'run the tests' }))
    expect(w.get('[data-next]').text()).toContain('run the tests')
    await w.get('[data-next]').trigger('click')
    await w.get('[data-next-input]').setValue('write the migration')
    await w.get('[data-next-input]').trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(w.emitted('next')![0]).toEqual([expect.objectContaining({ ref: 'REG-1' }), 'write the migration'])

    await w.get('[data-next]').trigger('click')
    await w.get('[data-next-input]').setValue('discard me')
    await w.get('[data-next-input]').trigger('keydown', { key: 'Escape' })
    expect(w.emitted('next')).toHaveLength(1)
  })

  it('offers an add link when there is no next step', () => {
    expect(render(ticket()).get('[data-add-next]').text()).toContain('next step')
  })

  it('lists the open discoveries with the three ways out', async () => {
    const d = (id: string, status: 'open' | 'dropped') => ({ id, text: `text ${id}`, source: 'cli' as const, status, at: '2026-10-02T10:00:00Z' })
    const w = render(ticket({ discoveries: [d('a', 'open'), d('b', 'dropped')] }))
    expect(w.findAll('[data-discovery]').map((e) => e.attributes('data-discovery'))).toEqual(['a'])
    expect(w.get('[data-discovery=a]').text()).toContain('from Claude')
    await w.get('[data-discovery=a] [data-triage=promote]').trigger('click')
    await w.get('[data-discovery=a] [data-triage=in-scope]').trigger('click')
    await w.get('[data-discovery=a] [data-triage=drop]').trigger('click')
    expect(w.emitted('triage')!.map((e) => [e[1], e[2]])).toEqual([['a', 'promote'], ['a', 'in-scope'], ['a', 'drop']])
  })

  it('warns, without blocking, before finishing a ticket with undecided discoveries', async () => {
    const d = { id: 'a', text: 'later', source: 'cli' as const, status: 'open' as const, at: '2026-10-02T10:00:00Z' }
    const w = render(ticket({ state: 'active', discoveries: [d] }))
    await w.get('[data-state=done]').trigger('click')
    expect(w.emitted('state')).toBeUndefined()
    expect(w.get('[data-done-warning]').text()).toContain('1 discovery still undecided')
    await w.get('[data-done-anyway]').trigger('click')
    expect(w.emitted('state')![0]).toEqual([expect.objectContaining({ ref: 'REG-1' }), 'done'])
    expect(w.find('[data-done-warning]').exists()).toBe(false)

    const clean = render(ticket({ state: 'active' }))
    await clean.get('[data-state=done]').trigger('click')
    expect(clean.emitted('state')![0][1]).toBe('done') // nothing pending: no warning
  })

  it('adds a discovery with Enter, ignoring an empty one', async () => {
    const w = render(ticket())
    await w.get('[data-add-discovery]').trigger('click')
    await w.get('[data-discover-input]').setValue('  refresh tokens never expire ')
    await w.get('[data-discover-input]').trigger('keydown', { key: 'Enter' })
    expect(w.emitted('discover')![0]).toEqual([expect.objectContaining({ ref: 'REG-1' }), 'refresh tokens never expire'])

    await w.get('[data-add-discovery]').trigger('click')
    await w.get('[data-discover-input]').setValue('   ')
    await w.get('[data-discover-input]').trigger('keydown', { key: 'Enter' })
    expect(w.emitted('discover')).toHaveLength(1)
  })

  it('shows the work (worktrees, PRs, sessions) and can hide it', async () => {
    const w = render(ticket())
    expect(w.find('[data-branch="REG-1-thing"]').exists()).toBe(true)
    await w.get('[data-toggle-rows]').trigger('click')
    expect(w.find('[data-branch="REG-1-thing"]').exists()).toBe(false)
    expect(render(ticket(), { expanded: false }).find('[data-branch]').exists()).toBe(false)
  })

  it('offers "Mark done" first when every PR is merged', () => {
    const w = render(ticket({ state: 'active' }, wt({ pr: merged, sessions: [] })))
    expect(w.get('.merged-hint').text()).toContain('merged')
    expect(w.get('.ticket-actions button').text()).toBe('Mark done')
  })

  it('names the project when asked to', () => {
    expect(render(ticket(), { repoName: 'Loyall' }).get('.repo-badge').text()).toBe('Loyall')
    expect(render(ticket()).find('.repo-badge').exists()).toBe(false)
  })

  it('passes the work actions up', async () => {
    const w = render(ticket())
    await w.get('[data-action=terminal]').trigger('click')
    await w.get('[data-action=resume]').trigger('click')
    expect(w.emitted('terminal')![0]).toEqual(['/r/REG-1-wt'])
    expect(w.emitted('resume')![0]).toEqual(['s1'])
  })
})
