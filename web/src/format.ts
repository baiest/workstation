import type { BranchNode, ClaudeState, ClaudeStatus, GitInfo, Pr, Session, Worktree } from './types'

export function relativeTime(iso: string, now: Date = new Date()): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return 'unknown'
  const secs = Math.max(0, (now.getTime() - t) / 1000)
  if (secs < 60) return 'just now'
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins} min ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours} h ago`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days} d ago`
  return `${Math.floor(days / 30)} mo ago`
}

const labels: Record<ClaudeStatus, string> = {
  working: 'Working',
  idle: 'Idle',
  stopped: 'Stopped',
  unknown: 'Unknown',
}

export function statusLabel(status: ClaudeStatus): string {
  return labels[status]
}

const stateLabels: Record<ClaudeState, string> = {
  thinking: 'Thinking',
  'running-tool': 'Running a tool',
  'needs-approval': 'May need approval?',
  waiting: 'Waiting for you',
  failed: 'Failed',
  interrupted: 'Interrupted',
  finished: 'Finished',
  stopped: 'Stopped',
  unknown: 'Unknown',
}

const stateHints: Record<ClaudeState, string> = {
  thinking: 'The model is working on its answer.',
  'running-tool': 'A tool was requested a moment ago and is running.',
  'needs-approval':
    'A tool was requested and there has been no result for a while: it may be waiting for your approval, or just be a slow tool. This is a guess.',
  waiting: 'The turn is finished: Claude is waiting for your next message.',
  failed: 'The last thing recorded was an API error.',
  interrupted: 'You interrupted it and it is not running.',
  finished: 'Not running; its last turn completed.',
  stopped: 'Not running (closed in the middle of a turn, or nothing recorded).',
  unknown: 'The state cannot be determined (for example a Claude Desktop session).',
}

/** What the session is doing, in words; falls back to the process status for data without a state. */
export function sessionLabel(s: Session): string {
  return s.state ? stateLabels[s.state] : statusLabel(s.status)
}

export function sessionHint(s: Session): string {
  return s.state ? stateHints[s.state] : ''
}

export type StateTone = 'working' | 'attention' | 'danger' | 'idle' | 'unknown' | 'none'

/** The colour family of a session: working, needs you, failed, quiet, unknown. */
export function stateTone(s?: Session): StateTone {
  if (!s) return 'none'
  switch (s.state) {
    case 'thinking':
    case 'running-tool':
      return 'working'
    case 'waiting':
    case 'needs-approval':
      return 'attention'
    case 'failed':
      return 'danger'
    case 'interrupted':
    case 'finished':
    case 'stopped':
      return 'idle'
    case 'unknown':
      return 'unknown'
  }
  if (s.status === 'working') return 'working'
  return s.status === 'unknown' ? 'unknown' : 'idle'
}

export function gitSummary(g: GitInfo): string {
  const parts: string[] = []
  if (g.staged) parts.push(`${g.staged} staged`)
  if (g.modified) parts.push(`${g.modified} modified`)
  if (g.untracked) parts.push(`${g.untracked} untracked`)
  if (g.conflicts) parts.push(`${g.conflicts} conflicts`)
  return parts.length ? parts.join(' · ') : 'clean'
}

const prStateWord = { open: 'Open', merged: 'Merged', declined: 'Declined' } as const
const checkMark = { success: '✓', failure: '✗', pending: '…' } as const

/** One-line PR status: "#120 Open · 1 approval · checks ✓". Review/checks only matter while open. */
export function prLabel(pr: Pr): string {
  const parts = [`#${pr.number} ${pr.draft && pr.state === 'open' ? 'Draft' : prStateWord[pr.state]}`]
  if (pr.state === 'open' && !pr.draft) {
    if (pr.review === 'changes_requested') parts.push('changes requested')
    else if (pr.approvals > 0) parts.push(`${pr.approvals} approval${pr.approvals === 1 ? '' : 's'}`)
    else if (pr.review === 'review_required') parts.push('needs review')
    if (pr.checks) parts.push(`checks ${checkMark[pr.checks]}`)
  }
  return parts.join(' · ')
}

export type PrTone = 'open' | 'draft' | 'merged' | 'declined'

export function prTone(pr: Pr): PrTone {
  if (pr.state === 'open') return pr.draft ? 'draft' : 'open'
  return pr.state
}

/**
 * Returns the URL only if it is an absolute http(s) link. PR links come from
 * remote APIs; a javascript: or data: URL must never become an href.
 */
export function safeHref(url?: string): string | undefined {
  if (!url) return undefined
  try {
    const u = new URL(url.trim())
    return u.protocol === 'https:' || u.protocol === 'http:' ? u.href : undefined
  } catch {
    return undefined
  }
}

/** True when the date is more than 30 days before now (unparseable dates are not stale). */
export function isStale(iso: string, now: Date = new Date()): boolean {
  const t = Date.parse(iso)
  return !Number.isNaN(t) && now.getTime() - t > 30 * 24 * 3600 * 1000
}

/** Graph search: branch name, worktree name, PR title or PR number ("#114" or "114"). */
export function matchNode(n: BranchNode, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return false
  const haystack = [n.branch, n.worktree?.name ?? '', n.pr?.title ?? '', n.pr ? `#${n.pr.number}` : '']
  return haystack.some((h) => h.toLowerCase().includes(q))
}

export function matchesFilter(wt: Worktree, repoName: string, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  const haystack = [wt.name, wt.branch ?? '', wt.path, repoName, ...wt.sessions.map((s) => s.title ?? '')]
  return haystack.some((h) => h.toLowerCase().includes(q))
}
