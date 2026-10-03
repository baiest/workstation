// Helpers to make sense of a pile of worktrees: ticket keys, freshness, what
// needs attention, what is safe to remove, and ordering. Pure, no DOM.
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

const isLive = (w: Worktree) => w.sessions.some((s) => s.status === 'working' || s.status === 'idle')

/**
 * Hint (not a guarantee) that a worktree can go: its PR merged, nothing is
 * uncommitted, and no Claude process is alive in it. The real removal re-checks
 * everything on the server.
 */
export function mergedHint(w: Worktree, pr?: Pr): boolean {
  return !!pr && pr.state === 'merged' && !w.isMain && !w.gitError && !w.git.dirty && !isLive(w)
}

/** Higher = look at it sooner. */
export function attentionScore(w: Worktree, pr?: Pr): number {
  let score = 0
  if (w.sessions.some((s) => s.status === 'working')) score += 100
  else if (isLive(w)) score += 60
  if (w.git.dirty) score += 50
  if (pr && (pr.state === 'open')) score += pr.review === 'changes_requested' ? 40 : 30
  if (w.detached) score -= 5
  if (pr?.state === 'merged' && !w.git.dirty && !isLive(w)) score -= 50 // done: to the bottom
  return score
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
