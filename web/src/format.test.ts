import { describe, expect, it } from 'vitest'
import { gitSummary, isStale, matchesFilter, matchNode, prLabel, prTone, relativeTime, safeHref, statusLabel } from './format'
import type { BranchNode, GitInfo, Pr, Worktree } from './types'

const now = new Date('2026-10-02T12:00:00Z')

describe('relativeTime', () => {
  it.each([
    ['2026-10-02T11:59:50Z', 'just now'],
    ['2026-10-02T11:58:00Z', '2 min ago'],
    ['2026-10-02T09:00:00Z', '3 h ago'],
    ['2026-09-27T12:00:00Z', '5 d ago'],
    ['2026-08-01T12:00:00Z', '2 mo ago'],
  ])('%s -> %s', (iso, expected) => {
    expect(relativeTime(iso, now)).toBe(expected)
  })

  it('handles missing or invalid dates', () => {
    expect(relativeTime('', now)).toBe('unknown')
    expect(relativeTime('nope', now)).toBe('unknown')
  })

  it('treats future dates as just now', () => {
    expect(relativeTime('2026-10-02T12:05:00Z', now)).toBe('just now')
  })
})

describe('statusLabel', () => {
  it('maps every status', () => {
    expect(statusLabel('working')).toBe('Working')
    expect(statusLabel('idle')).toBe('Idle')
    expect(statusLabel('stopped')).toBe('Stopped')
    expect(statusLabel('unknown')).toBe('Unknown')
  })
})

const git = (over: Partial<GitInfo> = {}): GitInfo => ({
  dirty: false, staged: 0, modified: 0, untracked: 0, conflicts: 0,
  hasUpstream: false, ahead: 0, behind: 0, ...over,
})

describe('gitSummary', () => {
  it('says clean when nothing changed', () => {
    expect(gitSummary(git())).toBe('clean')
  })

  it('lists only non-zero counts', () => {
    expect(gitSummary(git({ modified: 4, untracked: 1 }))).toBe('4 modified · 1 untracked')
    expect(gitSummary(git({ staged: 2, conflicts: 1 }))).toBe('2 staged · 1 conflicts')
  })
})

const pr = (over: Partial<Pr> = {}): Pr => ({
  number: 120, title: 't', url: 'u', source: 'a', dest: 'main', state: 'open', draft: false,
  approvals: 0, changesRequested: 0, updatedAt: '2026-10-01T00:00:00Z', ...over,
})

describe('prLabel', () => {
  it.each([
    [pr(), '#120 Open'],
    [pr({ draft: true }), '#120 Draft'],
    [pr({ state: 'merged' }), '#120 Merged'],
    [pr({ state: 'declined' }), '#120 Declined'],
    [pr({ approvals: 1, review: 'approved' }), '#120 Open · 1 approval'],
    [pr({ approvals: 2, review: 'approved' }), '#120 Open · 2 approvals'],
    [pr({ changesRequested: 1, review: 'changes_requested' }), '#120 Open · changes requested'],
    [pr({ review: 'review_required' }), '#120 Open · needs review'],
    [pr({ checks: 'success' }), '#120 Open · checks ✓'],
    [pr({ checks: 'failure' }), '#120 Open · checks ✗'],
    [pr({ checks: 'pending' }), '#120 Open · checks …'],
    [pr({ approvals: 1, review: 'approved', checks: 'success' }), '#120 Open · 1 approval · checks ✓'],
  ])('%#', (p, expected) => {
    expect(prLabel(p)).toBe(expected)
  })

  it('omits review and checks once the PR is closed', () => {
    expect(prLabel(pr({ state: 'merged', approvals: 2, review: 'approved', checks: 'success' }))).toBe('#120 Merged')
  })
})

describe('prTone', () => {
  it('maps state to a style tone', () => {
    expect(prTone(pr())).toBe('open')
    expect(prTone(pr({ draft: true }))).toBe('draft')
    expect(prTone(pr({ state: 'merged' }))).toBe('merged')
    expect(prTone(pr({ state: 'declined' }))).toBe('declined')
  })
})

describe('safeHref', () => {
  it('keeps only absolute http(s) links', () => {
    expect(safeHref('https://github.com/o/r/pull/1')).toBe('https://github.com/o/r/pull/1')
    expect(safeHref('http://bb.local/pull/2')).toBe('http://bb.local/pull/2')
  })

  it.each([
    'javascript:alert(1)',
    ' JaVaScRiPt:alert(1)',
    'jav\tascript:alert(1)',
    'data:text/html,<script>alert(1)</script>',
    'vbscript:x',
    '//evil.example/x',
    '/relative',
    'not a url',
    '',
    undefined,
  ])('drops %s', (u) => {
    expect(safeHref(u)).toBeUndefined()
  })
})

describe('matchNode', () => {
  const node = {
    branch: 'LOY-67-fix-rate-limit', isDefault: false, merged: false, ahead: 1, behind: 0, tip: 'a', date: '2026-10-01T00:00:00Z',
    worktree: { name: 'rate-limit-wt', path: '/w' },
    pr: pr({ number: 114, title: 'Block scanner probes' }),
  } as BranchNode

  it('matches branch, worktree, PR title and PR number, ignoring case', () => {
    for (const q of ['loy-67', 'RATE-LIMIT', 'scanner', '#114', '114']) expect(matchNode(node, q)).toBe(true)
  })

  it('does not match unrelated text or an empty query', () => {
    expect(matchNode(node, 'nothing-here')).toBe(false)
    expect(matchNode(node, '   ')).toBe(false)
  })

  it('works for a branch without PR or worktree', () => {
    const bare = { ...node, worktree: undefined, pr: undefined } as BranchNode
    expect(matchNode(bare, 'loy')).toBe(true)
    expect(matchNode(bare, '#114')).toBe(false)
  })
})

describe('isStale', () => {
  it('is true beyond 30 days', () => {
    expect(isStale('2026-08-01T00:00:00Z', now)).toBe(true)
    expect(isStale('2026-09-20T00:00:00Z', now)).toBe(false)
    expect(isStale('nope', now)).toBe(false)
  })
})

describe('matchesFilter', () => {
  const wt = {
    name: 'LOY-67', path: 'C:\\r\\LOY-67', branch: 'feat/cycle-warning', head: 'abc',
    detached: false, isMain: false, git: git(),
    sessions: [{ id: 's1', title: 'Fix rate limit', status: 'idle', cwd: '', source: 'cli', lastActivity: '', resumable: true }],
  } as Worktree

  it('matches name, branch, path, repo and session title case-insensitively', () => {
    for (const q of ['loy-67', 'CYCLE', 'c:\\r', 'myrepo', 'rate limit']) {
      expect(matchesFilter(wt, 'myrepo', q)).toBe(true)
    }
  })

  it('matches everything on empty query and nothing on a miss', () => {
    expect(matchesFilter(wt, 'myrepo', '  ')).toBe(true)
    expect(matchesFilter(wt, 'myrepo', 'zzz')).toBe(false)
  })
})
