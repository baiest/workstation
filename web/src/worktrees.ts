// Helpers to make sense of a pile of worktrees: ticket keys, freshness, what
// needs attention, what is safe to remove, and ordering. Pure, no DOM.
import { worktreeSessionAttention } from './attention'
import type { Pr, Worktree } from './types'

const DAY = 24 * 3600 * 1000

// Jira-style key at the start of a branch or after a slash: REG-5393, feature/LOY-96-x
const KEY = /(?:^|\/)([A-Za-z][A-Za-z0-9]{1,9}-\d{1,7})(?![A-Za-z0-9])/

/** The ticket key of a branch name, upper-cased, or undefined if it has none. */
export function ticketKey(text?: string): string | undefined {
  const m = text ? KEY.exec(text) : null
  return m ? m[1].toUpperCase() : undefined
}

/** True when the date is within the last `days` days (default 3). Unparseable is not fresh. */
export function isFresh(iso: string | undefined, now: Date = new Date(), days = 3): boolean {
  const t = iso ? Date.parse(iso) : NaN
  return !Number.isNaN(t) && now.getTime() - t < days * DAY
}

/** Latest sign of life in a worktree, in ms: its last commit or its newest Claude session. */
export function worktreeActivity(w: Worktree): number {
  const commit = w.git.lastCommit ? Date.parse(w.git.lastCommit.date) : 0
  const session = w.sessions[0] ? Date.parse(w.sessions[0].lastActivity) : 0
  return Math.max(Number.isNaN(commit) ? 0 : commit, Number.isNaN(session) ? 0 : session)
}

export const isLive = (w: Worktree) => w.sessions.some((s) => s.status === 'working' || s.status === 'idle')

/**
 * Hint (not a guarantee) that a worktree can go: its PR merged, nothing is
 * uncommitted, and no Claude process is alive in it. The real removal re-checks
 * everything on the server.
 */
export function mergedHint(w: Worktree, pr?: Pr): boolean {
  return !!pr && pr.state === 'merged' && !w.isMain && !w.gitError && !w.git.dirty && !isLive(w)
}

const sessionScore = { 'asks-you': 200, approval: 190, failed: 180, 'review-output': 150, working: 40, stale: 10, none: 0 } as const

/** Higher = look at it sooner. What waits for you comes before what runs by itself. */
export function attentionScore(w: Worktree, pr?: Pr, now: Date = new Date()): number {
  let score = sessionScore[worktreeSessionAttention(w, now)]
  if (w.git.dirty) score += 50
  if (pr && (pr.state === 'open')) score += pr.review === 'changes_requested' ? 40 : 30
  if (w.detached) score -= 5
  if (pr?.state === 'merged' && !w.git.dirty && !isLive(w)) score -= 50 // done: to the bottom
  return score
}

export interface SessionSummary {
  waiting: number // finished their turn (or asked something): your move
  working: number // thinking or running a tool
  failed: number
  approval: number // may be waiting for approval (a guess)
  stale: number // waiting for hours: probably worth closing
}

/** How many worktrees have Claude waiting for you / working / failed, by the most urgent of their sessions. */
export function summarizeSessions(repos: { worktrees: Worktree[] }[], now: Date = new Date()): SessionSummary {
  const sum: SessionSummary = { waiting: 0, working: 0, failed: 0, approval: 0, stale: 0 }
  for (const repo of repos) {
    for (const w of repo.worktrees) {
      switch (worktreeSessionAttention(w, now)) {
        case 'asks-you':
        case 'review-output':
          sum.waiting++
          break
        case 'working':
          sum.working++
          break
        case 'failed':
          sum.failed++
          break
        case 'approval':
          sum.approval++
          break
        case 'stale':
          sum.stale++
          break
      }
    }
  }
  return sum
}

/** Main worktree first, then by attention (or name), newest activity breaking ties. Returns a new array. */
export function sortWorktrees(
  list: Worktree[],
  prFor: (branch?: string) => Pr | undefined,
  mode: 'activity' | 'name',
): Worktree[] {
  return [...list].sort((a, b) => {
    if (a.isMain !== b.isMain) return a.isMain ? -1 : 1
    if (mode === 'name') return a.name.localeCompare(b.name)
    const diff = attentionScore(b, prFor(b.branch)) - attentionScore(a, prFor(a.branch))
    if (diff !== 0) return diff
    return worktreeActivity(b) - worktreeActivity(a) || a.name.localeCompare(b.name)
  })
}
