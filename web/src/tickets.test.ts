import { describe, expect, it } from 'vitest'
import { buildTickets, matchTicket } from './tickets'
import type { BranchNode, Pr, Session, Worktree } from './types'

const session = (over: Partial<Session> = {}): Session => ({
  id: 's', source: 'cli', cwd: '/w', status: 'stopped', lastActivity: '2026-09-01T00:00:00Z', resumable: true, hasPlan: false, ...over,
})

const wt = (name: string, branch: string | undefined, over: Partial<Worktree> = {}): Worktree => ({
  name, path: `/repo/${name}`, branch, head: 'abc1234def', detached: !branch, isMain: false,
  git: { dirty: false, staged: 0, modified: 0, untracked: 0, conflicts: 0, hasUpstream: true, ahead: 0, behind: 0,
    lastCommit: { hash: 'h', subject: 's', date: '2026-08-01T00:00:00Z' } },
  sessions: [], ...over,
})

const pr = (branch: string, over: Partial<Pr> = {}): Pr => ({
  number: 1, title: `PR for ${branch}`, url: 'https://x/1', source: branch, dest: 'main', state: 'open', draft: false,
  approvals: 0, changesRequested: 0, updatedAt: '2026-10-01T00:00:00Z', ...over,
})

const node = (branch: string, over: Partial<BranchNode> = {}): BranchNode => ({
  branch, isDefault: false, merged: false, ahead: 1, behind: 0, tip: 't', date: '2026-08-01T00:00:00Z', ...over,
})

describe('buildTickets', () => {
  it('groups everything of one ticket: worktree, branch and PR', () => {
    const tickets = buildTickets(
      [wt('REG-5393-ace-sync-worker', 'REG-5393-ace-sync-customer-rules')],
      [node('main', { isDefault: true }), node('REG-5393-ace-sync-customer-rules', { pr: pr('REG-5393-ace-sync-customer-rules') })],
      'main',
    )
    expect(tickets).toHaveLength(1)
    const t = tickets[0]
    expect(t.key).toBe('REG-5393')
    expect(t.items).toHaveLength(1) // worktree and graph node of the same branch are one item
    expect(t.items[0].worktree?.name).toBe('REG-5393-ace-sync-worker')
    expect(t.items[0].pr?.number).toBe(1)
    expect(t.title).toBe('PR for REG-5393-ace-sync-customer-rules')
  })

  it('puts the stacked branches of one ticket together, branches with a worktree first', () => {
    const tickets = buildTickets(
      [wt('wt-b', 'REG-5393-b')],
      [node('REG-5393-a', { date: '2026-09-30T00:00:00Z' }), node('REG-5393-b'), node('REG-5393-c')],
      'main',
    )
    expect(tickets).toHaveLength(1)
    expect(tickets[0].items.map((i) => i.branch)).toEqual(['REG-5393-b', 'REG-5393-a', 'REG-5393-c'])
  })

  it('includes a worktree whose branch has no graph node, and a branch without a worktree', () => {
    const tickets = buildTickets([wt('only-wt', 'REG-1-x')], [node('REG-2-y')], 'main')
    expect(tickets.map((t) => t.key).sort()).toEqual(['REG-1', 'REG-2'])
    expect(tickets.find((t) => t.key === 'REG-2')!.items[0].worktree).toBeUndefined()
  })

  it('never lists the default branch, but lists the main worktree when it has a feature branch checked out', () => {
    const tickets = buildTickets(
      [wt('scrap-services', 'REG-5366-web-form-step-race', { isMain: true }), wt('main-checkout', 'main', { isMain: true })],
      [node('main', { isDefault: true })],
      'main',
    )
    expect(tickets).toHaveLength(1)
    expect(tickets[0].key).toBe('REG-5366')
    expect(tickets[0].items[0].worktree?.isMain).toBe(true)
  })

  it('files branches without a ticket key under one "no ticket" group, last', () => {
    const tickets = buildTickets(
      [wt('fix-typo', 'fix/typo'), wt('legacy', 'cleanup'), wt('REG-9-z', 'REG-9-z')],
      [],
      'main',
    )
    expect(tickets.map((t) => t.key)).toEqual(['REG-9', ''])
    expect(tickets[1].items.map((i) => i.branch).sort()).toEqual(['cleanup', 'fix/typo'])
    expect(tickets[1].title).toBe('No ticket')
  })

  it('falls back to the folder name for a detached worktree', () => {
    const tickets = buildTickets([wt('REG-7-baseline', undefined)], [], 'main')
    expect(tickets).toHaveLength(1)
    expect(tickets[0].key).toBe('REG-7')
    expect(tickets[0].items[0].branch).toBe('detached @ abc1234')
  })

  it('titles a ticket by its open PR first, then any PR, then the branch', () => {
    const open = buildTickets([], [node('REG-1-a', { pr: pr('REG-1-a', { state: 'merged', title: 'Old merged' }) }), node('REG-1-b', { pr: pr('REG-1-b', { title: 'Live one' }) })], 'main')
    expect(open[0].title).toBe('Live one')
    const merged = buildTickets([], [node('REG-1-a', { pr: pr('REG-1-a', { state: 'merged', title: 'Only merged' }) })], 'main')
    expect(merged[0].title).toBe('Only merged')
    const bare = buildTickets([], [node('REG-1-solo')], 'main')
    expect(bare[0].title).toBe('REG-1-solo')
  })

  it('orders tickets: needs attention first, then newest, finished ones last', () => {
    const tickets = buildTickets(
      [
        wt('w-dirty', 'REG-1-dirty', { git: { ...wt('x', 'y').git, dirty: true, modified: 1 } }),
        wt('w-open', 'REG-2-open'),
        wt('w-done', 'REG-3-done'),
        wt('w-working', 'REG-4-working', { sessions: [session({ status: 'working' })] }),
      ],
      [
        node('REG-2-open', { pr: pr('REG-2-open') }),
        node('REG-3-done', { pr: pr('REG-3-done', { state: 'merged' }) }),
      ],
      'main',
    )
    expect(tickets.map((t) => t.key)).toEqual(['REG-4', 'REG-1', 'REG-2', 'REG-3'])
    expect(tickets[3].done).toBe(true)
    expect(tickets[0].done).toBe(false)
  })

  it('is done only when every item of the ticket has a merged PR', () => {
    const t = buildTickets([], [node('REG-1-a', { pr: pr('REG-1-a', { state: 'merged' }) }), node('REG-1-b')], 'main')[0]
    expect(t.done).toBe(false)
    const all = buildTickets([], [node('REG-1-a', { pr: pr('REG-1-a', { state: 'merged' }) })], 'main')[0]
    expect(all.done).toBe(true)
  })

  it('returns nothing for nothing', () => {
    expect(buildTickets([], [], 'main')).toEqual([])
  })
})

describe('matchTicket', () => {
  const t = buildTickets(
    [wt('rate-limit-folder', 'REG-5393-ace-sync')],
    [node('REG-5393-ace-sync', { pr: pr('REG-5393-ace-sync', { title: 'Block scanner probes', number: 114 }) })],
    'main',
  )[0]

  it('matches key, title, branch, folder and PR number, ignoring case', () => {
    for (const q of ['reg-5393', 'scanner', 'ace-sync', 'RATE-LIMIT', '#114']) expect(matchTicket(t, q)).toBe(true)
  })

  it('matches everything on an empty query and nothing on a miss', () => {
    expect(matchTicket(t, '  ')).toBe(true)
    expect(matchTicket(t, 'zzz')).toBe(false)
  })
})
