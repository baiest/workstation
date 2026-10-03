import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import TicketList from './TicketList.vue'
import { buildTickets } from '../tickets'
import type { BranchNode, Pr, Session, Worktree } from '../types'

const now = new Date('2026-10-02T12:00:00Z')

const session = (over: Partial<Session> = {}): Session => ({
  id: 's1', source: 'cli', cwd: '/w', status: 'working', lastActivity: '2026-10-02T11:58:00Z', resumable: true, hasPlan: true, ...over,
})

const wt = (name: string, branch: string, over: Partial<Worktree> = {}): Worktree => ({
  name, path: `/repo/${name}`, branch, head: 'abc1234', detached: false, isMain: false,
  git: { dirty: true, staged: 0, modified: 2, untracked: 0, conflicts: 0, hasUpstream: true, ahead: 1, behind: 0,
    lastCommit: { hash: 'h', subject: 's', date: '2026-10-02T09:00:00Z' } },
  sessions: [session()], ...over,
})

const pr = (branch: string, over: Partial<Pr> = {}): Pr => ({
  number: 48080, title: 'Update: use the ACE field', url: 'https://x/48080', source: branch, dest: 'main', state: 'open', draft: false,
  approvals: 1, changesRequested: 0, review: 'approved', updatedAt: '2026-10-01T00:00:00Z', ...over,
})

const node = (branch: string, over: Partial<BranchNode> = {}): BranchNode => ({
  branch, isDefault: false, merged: false, ahead: 1, behind: 0, tip: 't', date: '2026-08-01T00:00:00Z', ...over,
})

const tickets = () =>
  buildTickets(
    [wt('REG-5393-ace-sync-worker', 'REG-5393-ace-sync-customer-rules'), wt('fix-typo', 'fix/typo', { sessions: [], git: { ...wt('a', 'b').git, dirty: false, modified: 0 } })],
    [
      node('REG-5393-ace-sync-customer-rules', { pr: pr('REG-5393-ace-sync-customer-rules') }),
      node('REG-5393-ace-sync-infra', { date: '2026-10-02T08:00:00Z' }),
    ],
    'main',
  )

const render = (t = tickets(), editor = 'cursor') => mount(TicketList, { props: { tickets: t, editor, now } })

describe('TicketList', () => {
  it('shows one section per ticket with its key and title', () => {
    const w = render()
    const first = w.get('[data-ticket="REG-5393"]')
    expect(first.get('.ticket-key').text()).toBe('REG-5393')
    expect(first.get('h3').text()).toBe('Update: use the ACE field')
    expect(w.get('[data-ticket="none"] h3').text()).toBe('No ticket')
  })

  it('answers "which worktree is for which branch and PR" in one row', () => {
    const row = render().get('[data-ticket="REG-5393"] [data-branch="REG-5393-ace-sync-customer-rules"]')
    expect(row.text()).toContain('REG-5393-ace-sync-customer-rules')
    expect(row.get('.cell.folder').text()).toBe('REG-5393-ace-sync-worker')
    expect(row.get('.cell.pr').text()).toContain('#48080 Open · 1 approval')
    expect(row.get('.cell.git').text()).toContain('2 modified')
    expect(row.get('.cell.claude').text()).toContain('Working')
  })

  it('says plainly when a branch has no worktree or no PR', () => {
    const row = render().get('[data-branch="REG-5393-ace-sync-infra"]')
    expect(row.get('.cell.folder').text()).toBe('no worktree')
    expect(row.get('.cell.pr').text()).toBe('no PR')
    expect(row.find('.cell.actions button').exists()).toBe(false)
  })

  it('marks recent branches', () => {
    const w = render()
    expect(w.find('[data-branch="REG-5393-ace-sync-infra"] .new-badge').exists()).toBe(true) // 4 h ago
  })

  it('summarises each ticket', () => {
    expect(render().get('[data-ticket="REG-5393"] .summary').text()).toBe('1 worktree · 2 branches')
  })

  it('flags a ticket whose PRs are all merged', () => {
    const t = buildTickets([], [node('REG-1-a', { pr: pr('REG-1-a', { state: 'merged' }) })], 'main')
    const w = render(t)
    expect(w.get('section').classes()).toContain('done')
    expect(w.get('.merged-hint').text()).toContain('merged')
  })

  it('emits the worktree actions with the right payloads', async () => {
    const w = render()
    const row = w.get('[data-branch="REG-5393-ace-sync-customer-rules"]')
    await row.get('[data-action=terminal]').trigger('click')
    await row.get('[data-action=editor]').trigger('click')
    await row.get('[data-action=resume]').trigger('click')
    await row.get('[data-action=plan]').trigger('click')
    expect(w.emitted('terminal')![0]).toEqual(['/repo/REG-5393-ace-sync-worker'])
    expect(w.emitted('editor')![0]).toEqual(['/repo/REG-5393-ace-sync-worker'])
    expect(w.emitted('resume')![0]).toEqual(['s1'])
    expect(w.emitted('plan')![0]).toEqual(['s1'])
  })

  it('offers the session list when a worktree has several', async () => {
    const many = buildTickets(
      [wt('REG-7-x', 'REG-7-x', { sessions: [session({ id: 'a' }), session({ id: 'b' }), session({ id: 'c' })] })],
      [],
      'main',
    )
    const w = render(many)
    expect(w.get('[data-action=sessions]').text()).toBe('Sessions (3)')
    await w.get('[data-action=sessions]').trigger('click')
    expect(w.emitted('sessions')![0]).toEqual(['/repo/REG-7-x'])
    expect(render().find('[data-action=sessions]').exists()).toBe(false) // one session each: no list
  })

  it('hides the editor button when none is installed and resume/plan without a session', () => {
    const w = render(tickets(), '')
    expect(w.find('[data-action=editor]').exists()).toBe(false)
    const quiet = w.get('[data-ticket="none"] [data-branch="fix/typo"]')
    expect(quiet.find('[data-action=resume]').exists()).toBe(false)
    expect(quiet.find('[data-action=plan]').exists()).toBe(false)
    expect(quiet.find('[data-action=terminal]').exists()).toBe(true)
  })

  it('has an empty state', () => {
    expect(render([]).text()).toContain('No tickets')
  })
})
