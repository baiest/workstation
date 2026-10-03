import { describe, expect, it } from 'vitest'
import { activeSessions } from './active'
import type { Repo, Session, Unlinked, Worktree } from './types'

const now = new Date('2026-10-02T12:00:00Z')
const ago = (h: number) => new Date(now.getTime() - h * 3600_000).toISOString()

const session = (over: Partial<Session> = {}): Session => ({
  id: 's', source: 'cli', cwd: '/w', status: 'stopped', lastActivity: ago(100), resumable: true, hasPlan: false, ...over,
})
const wt = (name: string, sessions: Session[], branch = `${name}-x`): Worktree => ({
  name, path: `/r/${name}`, branch, head: 'abc', detached: false, isMain: false,
  git: { dirty: false, staged: 0, modified: 0, untracked: 0, conflicts: 0, hasUpstream: true, ahead: 0, behind: 0 },
  sessions,
})
const repo = (name: string, worktrees: Worktree[]): Repo => ({ name, path: `/${name}`, worktrees })

describe('activeSessions', () => {
  it('lists live sessions with the worktree they work in', () => {
    const repos = [repo('Loyall', [wt('REG-1', [session({ id: 'a', status: 'working', state: 'thinking' })]), wt('main', [session({ id: 'b' })])])]
    const rows = activeSessions(repos, [], now)
    expect(rows.map((r) => r.session.id)).toEqual(['a'])
    expect(rows[0]).toMatchObject({ repo: 'Loyall', worktree: 'REG-1', branch: 'REG-1-x', ticket: 'REG-1' })
  })

  it('includes sessions touched within the recent window even when stopped', () => {
    const repos = [repo('R', [wt('a', [session({ id: 'recent', lastActivity: ago(3) }), session({ id: 'old', lastActivity: ago(30) })])])]
    expect(activeSessions(repos, [], now, 24).map((r) => r.session.id)).toEqual(['recent'])
  })

  it('puts what needs you first: waiting, approval, failed, then working', () => {
    const live = (id: string, state: Session['state'], status: Session['status'] = 'idle') => session({ id, state, status })
    const repos = [repo('R', [
      wt('w1', [live('work', 'thinking', 'working')]),
      wt('w2', [live('fail', 'failed')]),
      wt('w3', [live('wait', 'waiting')]),
      wt('w4', [live('appr', 'needs-approval')]),
    ])]
    expect(activeSessions(repos, [], now).map((r) => r.session.id)).toEqual(['wait', 'appr', 'fail', 'work'])
  })

  it('shows unlinked live sessions without a worktree', () => {
    const unlinked: Unlinked[] = [{ session: session({ id: 'u', status: 'idle' }), reason: 'not a git repository' }]
    const rows = activeSessions([], unlinked, now)
    expect(rows).toHaveLength(1)
    expect(rows[0].worktree).toBeUndefined()
  })

  it('skips the repos the caller hides', () => {
    const repos = [repo('R', [wt('a', [session({ id: 'x', status: 'working' })])])]
    expect(activeSessions(repos, [], now, 24, (r) => r.name === 'R')).toEqual([])
  })
})
