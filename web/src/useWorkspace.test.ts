import { describe, expect, it } from 'vitest'
import { withSessions, withoutDismissed } from './useWorkspace'
import type { Session, SessionsData, WorkspaceData, Worktree } from './types'

const s = (id: string): Session => ({ id, source: 'cli', cwd: '/w', status: 'idle', lastActivity: '2026-10-02T11:00:00Z', resumable: true, hasPlan: false })
const wt = (path: string, sessions: Session[], over: Partial<Worktree> = {}): Worktree => ({
  name: path, path, branch: 'b', head: 'abc', detached: false, isMain: false,
  git: { dirty: true, staged: 0, modified: 3, untracked: 0, conflicts: 0, hasUpstream: true, ahead: 0, behind: 0 }, sessions, ...over,
})
const ws = (): WorkspaceData => ({
  repos: [{ name: 'r', path: '/r', worktrees: [wt('/r/a', [s('old-a')]), wt('/r/b', [s('old-b')])] }],
  unlinked: [{ session: s('orphan'), reason: 'x' }],
  warnings: [],
  generatedAt: '2026-10-02T10:00:00Z',
  capabilities: { editor: 'code' },
})

describe('withSessions', () => {
  const fresh: SessionsData = {
    repos: [{ path: '/r', worktrees: [{ path: '/r/a', sessions: [s('new-a1'), s('new-a2')] }] }],
    unlinked: [],
    generatedAt: '2026-10-02T12:00:00Z',
  }

  it('replaces the sessions it knows about and keeps everything else', () => {
    const out = withSessions(ws(), fresh)
    const [a, b] = out.repos[0].worktrees
    expect(a.sessions.map((x) => x.id)).toEqual(['new-a1', 'new-a2'])
    expect(a.git.modified).toBe(3) // git state is not touched
    expect(b.sessions.map((x) => x.id)).toEqual(['old-b']) // not mentioned: kept
    expect(out.unlinked).toEqual([])
    expect(out.generatedAt).toBe('2026-10-02T12:00:00Z')
    expect(out.capabilities.editor).toBe('code')
  })

  it('does not change what it was given', () => {
    const before = ws()
    withSessions(before, fresh)
    expect(before.repos[0].worktrees[0].sessions.map((x) => x.id)).toEqual(['old-a'])
  })

  it('a session that left a worktree is gone from it', () => {
    const out = withSessions(ws(), { ...fresh, repos: [{ path: '/r', worktrees: [{ path: '/r/a', sessions: [] }] }] })
    expect(out.repos[0].worktrees[0].sessions).toEqual([])
  })
})

describe('withoutDismissed', () => {
  it('drops dismissed sessions everywhere and returns the same object when nothing is dismissed', () => {
    const base = ws()
    expect(withoutDismissed(base, new Set())).toBe(base)
    const out = withoutDismissed(base, new Set(['old-a', 'orphan']))
    expect(out.repos[0].worktrees[0].sessions).toEqual([])
    expect(out.repos[0].worktrees[1].sessions).toHaveLength(1)
    expect(out.unlinked).toEqual([])
  })
})
