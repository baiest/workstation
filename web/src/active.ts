// "Active now": what is being worked on across all worktrees, one row per
// Claude session. Pure, no DOM.
import { ticketKey } from './worktrees'
import type { Repo, Session, Unlinked } from './types'

export interface ActiveRow {
  session: Session
  repo?: string
  worktree?: string
  worktreePath?: string
  branch?: string
  ticket?: string
}

const HOUR = 3600 * 1000

// Lower = needs you sooner.
function rank(s: Session): number {
  switch (s.state) {
    case 'waiting': return 0
    case 'needs-approval': return 1
    case 'failed': return 2
    case 'thinking':
    case 'running-tool': return 3
    default: return s.status === 'working' ? 3 : 4
  }
}

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
      for (const s of w.sessions) {
        if (!isActive(s, now, recentHours)) continue
        rows.push({ session: s, repo: r.name, worktree: w.name, worktreePath: w.path, branch: w.branch, ticket: ticketKey(w.branch) ?? ticketKey(w.name) })
      }
    }
  }
  for (const u of unlinked) {
    if (isActive(u.session, now, recentHours)) rows.push({ session: u.session })
  }
  return rows.sort((a, b) => rank(a.session) - rank(b.session) || Date.parse(b.session.lastActivity) - Date.parse(a.session.lastActivity))
}
