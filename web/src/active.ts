// "Active now": what is being worked on across all worktrees, one row per
// Claude session. Pure, no DOM.
import { attentionRank, sessionAttention, type Attention } from './attention'
import { ticketKey } from './worktrees'
import type { Repo, Session, Unlinked } from './types'

export interface ActiveRow {
  session: Session
  attention: Attention
  repo?: string
  worktree?: string
  worktreePath?: string
  branch?: string
  ticket?: string
}

const HOUR = 3600 * 1000

function isActive(s: Session, now: Date, recentHours: number): boolean {
  if (s.status === 'working' || s.status === 'idle') return true
  const t = Date.parse(s.lastActivity)
  return !Number.isNaN(t) && now.getTime() - t < recentHours * HOUR
}

/** Live sessions plus those touched in the last `recentHours`, most urgent first. */
export function activeSessions(
  repos: Repo[],
  unlinked: Unlinked[],
  now: Date = new Date(),
  recentHours = 24,
  isHidden: (r: Repo) => boolean = () => false,
): ActiveRow[] {
  const rows: ActiveRow[] = []
  for (const r of repos) {
    if (isHidden(r)) continue
    for (const w of r.worktrees) {
      for (const [i, s] of w.sessions.entries()) {
        if (!isActive(s, now, recentHours)) continue
        rows.push({ attention: sessionAttention(s, now, i === 0), session: s, repo: r.name, worktree: w.name, worktreePath: w.path, branch: w.branch, ticket: ticketKey(w.branch) ?? ticketKey(w.name) })
      }
    }
  }
  for (const u of unlinked) {
    if (isActive(u.session, now, recentHours)) rows.push({ session: u.session, attention: sessionAttention(u.session, now) })
  }
  return rows.sort((a, b) => attentionRank[a.attention] - attentionRank[b.attention] || Date.parse(b.session.lastActivity) - Date.parse(a.session.lastActivity))
}
