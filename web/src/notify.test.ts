import { describe, expect, it } from 'vitest'
import { needsYouCount, transitions } from './notify'
import type { Repo, Session, Worktree } from './types'

const s = (id: string, state: Session['state'], over: Partial<Session> = {}): Session => ({
  id, source: 'cli', cwd: '/w', status: 'idle', state, lastActivity: '2026-10-02T11:00:00Z', resumable: true, hasPlan: false, title: `chat ${id}`, ...over,
})
const repos = (sessions: Session[], branch = 'LOY-96-x'): Repo[] => [
  { name: 'Loyall', path: '/r', worktrees: [{ name: 'wt', path: '/r/wt', branch, sessions } as Worktree] },
]

describe('transitions', () => {
  it('says nothing on the first load', () => {
    expect(transitions(undefined, repos([s('a', 'waiting')]), [])).toEqual([])
  })

  it('reports a session that newly waits for you, with its ticket and chat name', () => {
    const ev = transitions(repos([s('a', 'thinking', { status: 'working' })]), repos([s('a', 'waiting')]), [])
    expect(ev).toEqual([{ id: 'a', kind: 'waiting', title: 'Claude is waiting: LOY-96', body: 'chat a' }])
  })

  it('reports failures and possible approvals too', () => {
    const prev = repos([s('a', 'thinking'), s('b', 'running-tool')])
    const kinds = transitions(prev, repos([s('a', 'failed'), s('b', 'needs-approval')]), []).map((e) => e.kind)
    expect(kinds).toEqual(['failed', 'needs-approval'])
  })

  it('does not repeat while the state stays the same, or for sessions seen for the first time', () => {
    const prev = repos([s('a', 'waiting')])
    expect(transitions(prev, repos([s('a', 'waiting'), s('new', 'waiting')]), [])).toEqual([])
  })

  it('includes unlinked sessions', () => {
    const before = [{ session: s('u', 'thinking'), reason: 'x' }]
    const after = [{ session: s('u', 'waiting'), reason: 'x' }]
    expect(transitions(repos([]), repos([]), after, before)).toHaveLength(1)
  })
})

describe('needsYouCount', () => {
  it('counts sessions that wait or may need approval, for the tab title', () => {
    expect(needsYouCount(repos([s('a', 'waiting'), s('b', 'thinking'), s('c', 'needs-approval')]), [])).toBe(2)
  })
})
