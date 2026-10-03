import { describe, expect, it } from 'vitest'
import { branchTidy, tidyCounts, tidyText } from './tidy'
import type { CleanupCandidate, Pr, Worktree } from './types'

const now = new Date('2026-10-02T12:00:00Z')
const ago = (d: number) => new Date(now.getTime() - d * 86400_000).toISOString()
const wt = (name: string, days: number, over: Partial<Worktree> = {}): Worktree => ({
  name, path: `/r/${name}`, branch: name, head: 'abc', detached: false, isMain: false,
  git: { dirty: false, staged: 0, modified: 0, untracked: 0, conflicts: 0, hasUpstream: true, ahead: 0, behind: 0,
    lastCommit: { hash: 'h', subject: 's', date: ago(days) } },
  sessions: [], ...over,
})
const merged = { number: 1, state: 'merged' } as Pr

describe('tidyCounts', () => {
  it('counts merged-and-removable and dormant worktrees', () => {
    const list = [wt('a', 1), wt('b', 1), wt('old', 40), wt('main', 1, { isMain: true })]
    const c = tidyCounts(list, (b) => (b === 'b' ? merged : undefined), now)
    expect(c).toEqual({ removable: 1, dormant: 1 })
  })
})

describe('branchTidy', () => {
  const cand = (kind?: CleanupCandidate['kind']): CleanupCandidate => ({ branch: 'x', sha: 's', kind })
  it('separates safe branches from riskier ones', () => {
    expect(branchTidy([cand(), cand('pr-merged'), cand('stale-merged'), cand('stale-on-remote'), cand('stale-local-only')]))
      .toEqual({ safe: 3, risky: 2 })
  })
})

describe('tidyText', () => {
  it('lists only what there is to do', () => {
    expect(tidyText({ removable: 3, dormant: 0 }, { safe: 18, risky: 0 })).toBe('3 worktrees merged · 18 branches can be deleted')
    expect(tidyText({ removable: 1, dormant: 12 }, undefined)).toBe('1 worktree merged · 12 dormant')
  })
  it('mentions old branches that need a look, without calling them safe', () => {
    expect(tidyText({ removable: 0, dormant: 0 }, { safe: 2, risky: 5 })).toBe('2 branches can be deleted · 5 old branches to review')
    expect(tidyText({ removable: 0, dormant: 0 }, { safe: 0, risky: 1 })).toBe('1 old branch to review')
  })

  it('is empty when everything is tidy', () => {
    expect(tidyText({ removable: 0, dormant: 0 }, { safe: 0, risky: 0 })).toBe('')
  })
})
