import { describe, expect, it } from 'vitest'
import { attentionScore, isFresh, mergedHint, sortWorktrees, summarizeSessions, ticketKey, worktreeActivity } from './worktrees'
import type { Pr, Session, Worktree } from './types'

const now = new Date('2026-10-02T12:00:00Z')

const session = (over: Partial<Session> = {}): Session => ({
  id: 's', source: 'cli', cwd: '/w', status: 'stopped', lastActivity: '2026-09-01T00:00:00Z', resumable: true, hasPlan: false, ...over,
})

const wt = (over: Partial<Worktree> = {}): Worktree => ({
  name: 'wt', path: '/w/wt', branch: 'REG-1-x', head: 'abc1234', detached: false, isMain: false,
  git: { dirty: false, staged: 0, modified: 0, untracked: 0, conflicts: 0, hasUpstream: true, ahead: 0, behind: 0,
    lastCommit: { hash: 'h', subject: 's', date: '2026-08-01T00:00:00Z' } },
  sessions: [], ...over,
})

const pr = (over: Partial<Pr> = {}): Pr => ({
  number: 1, title: 'T', url: 'https://x/1', source: 'REG-1-x', dest: 'main', state: 'open', draft: false,
  approvals: 0, changesRequested: 0, updatedAt: '2026-10-01T00:00:00Z', ...over,
})

describe('ticketKey', () => {
  it.each([
    ['REG-5393-ace-sync-customer-rules', 'REG-5393'],
    ['feature/REG-5413-cycle-warning', 'REG-5413'],
    ['LOY-96-block-scanner-probes', 'LOY-96'],
    ['reg-12-lowercase', 'REG-12'],
    ['hotfix/AR-6591-poc', 'AR-6591'],
  ])('%s -> %s', (branch, key) => expect(ticketKey(branch)).toBe(key))

  it.each(['main', 'feat/max-bid-increment', 'scrap-services-legacy-end', '', undefined, 'v2'])('no key in %s', (b) => {
    expect(ticketKey(b)).toBeUndefined()
  })
})

describe('isFresh', () => {
  it('is true within the window and false after, unparseable is not fresh', () => {
    expect(isFresh('2026-10-01T12:00:00Z', now)).toBe(true) // 1 day
    expect(isFresh('2026-09-29T13:00:00Z', now)).toBe(true) // just under 3 days
    expect(isFresh('2026-09-28T00:00:00Z', now)).toBe(false)
    expect(isFresh('2026-09-20T00:00:00Z', now, 30)).toBe(true)
    expect(isFresh('nope', now)).toBe(false)
    expect(isFresh(undefined, now)).toBe(false)
  })
})

describe('worktreeActivity', () => {
  it('is the latest of the last commit and the newest session', () => {
    const a = wt({ sessions: [session({ lastActivity: '2026-09-15T00:00:00Z' })] })
    expect(worktreeActivity(a)).toBe(Date.parse('2026-09-15T00:00:00Z'))
    expect(worktreeActivity(wt())).toBe(Date.parse('2026-08-01T00:00:00Z'))
    expect(worktreeActivity(wt({ git: { ...wt().git, lastCommit: undefined } }))).toBe(0)
  })
})

describe('mergedHint', () => {
  it('flags a clean, idle worktree whose PR merged', () => {
    expect(mergedHint(wt(), pr({ state: 'merged' }))).toBe(true)
  })

  it.each([
    ['no PR', wt(), undefined],
    ['PR still open', wt(), pr()],
    ['PR declined', wt(), pr({ state: 'declined' })],
    ['uncommitted changes', wt({ git: { ...wt().git, dirty: true, modified: 2 } }), pr({ state: 'merged' })],
    ['main worktree', wt({ isMain: true }), pr({ state: 'merged' })],
    ['git error', wt({ gitError: 'boom' }), pr({ state: 'merged' })],
    ['Claude working', wt({ sessions: [session({ status: 'working' })] }), pr({ state: 'merged' })],
    ['Claude idle but alive', wt({ sessions: [session({ status: 'idle' })] }), pr({ state: 'merged' })],
  ])('not for %s', (_name, w, p) => {
    expect(mergedHint(w, p)).toBe(false)
  })

  it('a stopped session does not block it', () => {
    expect(mergedHint(wt({ sessions: [session({ status: 'stopped' })] }), pr({ state: 'merged' }))).toBe(true)
  })
})

describe('attentionScore', () => {
  it('ranks live work > uncommitted changes > open PR > nothing > merged', () => {
    const working = attentionScore(wt({ sessions: [session({ status: 'working' })] }), undefined)
    const dirty = attentionScore(wt({ git: { ...wt().git, dirty: true } }), undefined)
    const open = attentionScore(wt(), pr())
    const nothing = attentionScore(wt(), undefined)
    const merged = attentionScore(wt(), pr({ state: 'merged' }))
    expect(working).toBeGreaterThan(dirty)
    expect(dirty).toBeGreaterThan(open)
    expect(open).toBeGreaterThan(nothing)
    expect(nothing).toBeGreaterThan(merged)
  })

  it('does not bury a merged PR that still has work in it', () => {
    const dirtyMerged = attentionScore(wt({ git: { ...wt().git, dirty: true } }), pr({ state: 'merged' }))
    expect(dirtyMerged).toBeGreaterThan(attentionScore(wt(), pr({ state: 'merged' })))
  })
})

describe('summarizeSessions', () => {
  const repo = (...worktrees: Worktree[]) => ({ name: 'r', path: '/r', worktrees })
  const w = (state: Session['state'], heuristic = false) => wt({ sessions: [session({ state, stateHeuristic: heuristic })] })

  it('counts the newest session of each worktree by what the user must do', () => {
    const got = summarizeSessions([
      repo(w('waiting'), w('waiting'), w('thinking'), w('running-tool'), w('failed')),
      repo(w('needs-approval', true), w('finished'), w('stopped'), wt({ name: 'no-session' })),
    ])
    expect(got).toEqual({ waiting: 2, working: 2, failed: 1, approval: 1 })
  })

  it('only looks at the newest session of a worktree, not older ones', () => {
    const two = wt({ sessions: [session({ state: 'finished' }), session({ id: 'old', state: 'failed' })] })
    expect(summarizeSessions([repo(two)])).toEqual({ waiting: 0, working: 0, failed: 0, approval: 0 })
  })

  it('is all zeros for nothing', () => {
    expect(summarizeSessions([])).toEqual({ waiting: 0, working: 0, failed: 0, approval: 0 })
  })
})

describe('sortWorktrees', () => {
  const a = wt({ name: 'a', branch: 'REG-1-a' })
  const b = wt({ name: 'b', branch: 'REG-2-b', git: { ...wt().git, dirty: true } })
  const c = wt({ name: 'c', branch: 'REG-3-c' })
  const d = wt({ name: 'd', branch: 'REG-4-d', git: { ...wt().git, lastCommit: { hash: 'h', subject: 's', date: '2026-09-30T00:00:00Z' } } })
  const main = wt({ name: 'main-wt', branch: 'main', isMain: true })
  const prs: Record<string, Pr> = { 'REG-1-a': pr({ state: 'merged' }), 'REG-3-c': pr({ state: 'open' }) }
  const prFor = (branch?: string) => (branch ? prs[branch] : undefined)

  it('puts the main worktree first, then what needs attention, newest first, merged last', () => {
    const sorted = sortWorktrees([a, c, d, b, main], prFor, 'activity').map((w) => w.name)
    expect(sorted).toEqual(['main-wt', 'b', 'c', 'd', 'a'])
  })

  it('can sort by name', () => {
    expect(sortWorktrees([d, c, main, b, a], prFor, 'name').map((w) => w.name)).toEqual(['main-wt', 'a', 'b', 'c', 'd'])
  })

  it('does not mutate its input', () => {
    const input = [c, a, b]
    sortWorktrees(input, prFor, 'activity')
    expect(input.map((w) => w.name)).toEqual(['c', 'a', 'b'])
  })
})
