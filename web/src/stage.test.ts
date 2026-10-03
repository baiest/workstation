import { describe, expect, it } from 'vitest'
import { groupByStage, stageOf } from './stage'
import type { Pr, Session, Worktree } from './types'

const now = new Date('2026-10-02T12:00:00Z')
const ago = (days: number) => new Date(now.getTime() - days * 86400_000).toISOString()

const session = (over: Partial<Session> = {}): Session => ({
  id: 's', source: 'cli', cwd: '/w', status: 'stopped', lastActivity: ago(30), resumable: true, hasPlan: false, ...over,
})
const wt = (over: Partial<Worktree> = {}, commitDays = 1): Worktree => ({
  name: 'wt', path: '/r/wt', branch: 'REG-1-x', head: 'abc', detached: false, isMain: false,
  git: { dirty: false, staged: 0, modified: 0, untracked: 0, conflicts: 0, hasUpstream: true, ahead: 0, behind: 0,
    lastCommit: { hash: 'h', subject: 's', date: ago(commitDays) } },
  sessions: [], ...over,
})
const pr = (over: Partial<Pr> = {}): Pr => ({
  number: 1, title: 'T', url: 'https://x/1', source: 'a', dest: 'main', state: 'open', draft: false,
  approvals: 0, changesRequested: 0, updatedAt: ago(1), ...over,
})

describe('stageOf', () => {
  it('needs-you when Claude waits for you, or may need approval', () => {
    expect(stageOf(wt({ sessions: [session({ state: 'waiting', status: 'idle' })] }), undefined, now)).toBe('needs-you')
    expect(stageOf(wt({ sessions: [session({ state: 'needs-approval', status: 'working' })] }), undefined, now)).toBe('needs-you')
  })

  it('a failure needs you only while it is recent', () => {
    expect(stageOf(wt({ sessions: [session({ state: 'failed', lastActivity: ago(0.2) })] }), undefined, now)).toBe('needs-you')
    expect(stageOf(wt({ sessions: [session({ state: 'failed', lastActivity: ago(30) })] }, 40), undefined, now)).toBe('dormant')
  })

  it('needs-you for requested changes, failing checks and conflicts', () => {
    expect(stageOf(wt(), pr({ review: 'changes_requested' }), now)).toBe('needs-you')
    expect(stageOf(wt(), pr({ checks: 'failure' }), now)).toBe('needs-you')
    expect(stageOf(wt({ git: { ...wt().git, conflicts: 2 } }), undefined, now)).toBe('needs-you')
  })

  it('done when the PR is merged or declined', () => {
    expect(stageOf(wt(), pr({ state: 'merged' }), now)).toBe('done')
    expect(stageOf(wt(), pr({ state: 'declined' }), now)).toBe('done')
  })

  it('review for an open, non-draft PR', () => {
    expect(stageOf(wt(), pr(), now)).toBe('review')
    expect(stageOf(wt(), pr({ draft: true }), now)).toBe('in-progress')
  })

  it('dormant only when old, idle, clean and without an open PR', () => {
    expect(stageOf(wt({}, 20), undefined, now)).toBe('dormant')
    expect(stageOf(wt({}, 5), undefined, now)).toBe('in-progress')
    expect(stageOf(wt({ git: { ...wt({}, 20).git, dirty: true } }), undefined, now)).toBe('in-progress')
    expect(stageOf(wt({ sessions: [session({ status: 'idle', state: 'finished', lastActivity: ago(20) })] }, 20), undefined, now)).toBe('in-progress')
    expect(stageOf(wt({}, 20), pr(), now)).toBe('review')
  })

  it('respects the dormant threshold', () => {
    expect(stageOf(wt({}, 20), undefined, now, 30)).toBe('in-progress')
  })
})

describe('groupByStage', () => {
  it('keeps main out of the lanes and drops empty ones', () => {
    const main = wt({ name: 'main', isMain: true })
    const g = groupByStage([main, wt({ name: 'a' }, 20), wt({ name: 'b' })], () => undefined, now)
    expect(g.main).toBe(main)
    expect(g.lanes.map((l) => [l.stage, l.items.map((w) => w.name)])).toEqual([
      ['in-progress', ['b']],
      ['dormant', ['a']],
    ])
  })
})
