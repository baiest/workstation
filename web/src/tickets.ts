// Groups a repo's branches, worktrees, PRs and Claude sessions by ticket
// (REG-5393), so "which worktree is for which branch and PR" has one answer.
import type { BranchNode, Pr, Session, Worktree } from './types'
import { attentionScore, ticketKey, worktreeActivity } from './worktrees'

export interface TicketItem {
  branch: string // "detached @ abc1234" for a detached worktree
  worktree?: Worktree
  node?: BranchNode
  pr?: Pr
  sessions: Session[]
  activity: number // ms of the latest commit or Claude session
}

export interface Ticket {
  key: string // "" for the "no ticket" group
  title: string
  items: TicketItem[]
  activity: number
  attention: number
  done: boolean // every item has a merged PR
}

const NO_TICKET = 'No ticket'

function toMs(iso?: string): number {
  const t = iso ? Date.parse(iso) : NaN
  return Number.isNaN(t) ? 0 : t
}

/**
 * One item per branch: a worktree and the graph node of the same branch are
 * the same item. The default branch is never an item; the main worktree is,
 * when it has a feature branch checked out.
 */
function collectItems(worktrees: Worktree[], nodes: BranchNode[], defaultBranch: string): Map<string, TicketItem> {
  const items = new Map<string, TicketItem>()

  for (const w of worktrees) {
    const branch = w.branch ?? `detached @ ${w.head.slice(0, 7)}`
    if (w.branch === defaultBranch) continue
    items.set(`wt:${w.path}`, { branch, worktree: w, sessions: w.sessions, activity: worktreeActivity(w) })
  }

  for (const n of nodes) {
    if (n.isDefault || n.branch === defaultBranch) continue
    const existing = [...items.values()].find((i) => i.worktree?.branch === n.branch)
    if (existing) {
      existing.node = n
      existing.pr = n.pr
      existing.activity = Math.max(existing.activity, toMs(n.date))
    } else {
      items.set(`br:${n.branch}`, { branch: n.branch, node: n, pr: n.pr, sessions: [], activity: toMs(n.date) })
    }
  }
  return items
}

function keyOf(item: TicketItem): string {
  return ticketKey(item.worktree?.branch ?? item.branch) ?? ticketKey(item.worktree?.name) ?? ''
}

function itemAttention(i: TicketItem): number {
  if (i.worktree) return attentionScore(i.worktree, i.pr)
  if (i.pr?.state === 'open') return 30
  return i.pr?.state === 'merged' ? -50 : 0
}

function ticketTitle(key: string, items: TicketItem[]): string {
  if (!key) return NO_TICKET
  const byRecent = [...items].sort((a, b) => b.activity - a.activity)
  const pr = byRecent.find((i) => i.pr?.state === 'open')?.pr ?? byRecent.find((i) => i.pr)?.pr
  return pr?.title ?? byRecent[0].branch
}

/** Tickets ordered by what needs attention, then newest; "no ticket" and finished ones last. */
export function buildTickets(worktrees: Worktree[], nodes: BranchNode[], defaultBranch: string): Ticket[] {
  const groups = new Map<string, TicketItem[]>()
  for (const item of collectItems(worktrees, nodes, defaultBranch).values()) {
    const key = keyOf(item)
    groups.set(key, [...(groups.get(key) ?? []), item])
  }

  const tickets: Ticket[] = [...groups].map(([key, items]) => ({
    key,
    title: ticketTitle(key, items),
    items: items.sort((a, b) => Number(!!b.worktree) - Number(!!a.worktree) || b.activity - a.activity || a.branch.localeCompare(b.branch)),
    activity: Math.max(...items.map((i) => i.activity)),
    attention: Math.max(...items.map(itemAttention)),
    done: items.every((i) => i.pr?.state === 'merged'),
  }))

  return tickets.sort((a, b) => {
    if ((a.key === '') !== (b.key === '')) return a.key === '' ? 1 : -1
    if (a.done !== b.done) return a.done ? 1 : -1
    return b.attention - a.attention || b.activity - a.activity || a.key.localeCompare(b.key)
  })
}

/** Filter box for the Tickets tab: key, title, branches, folders and PR numbers. */
export function matchTicket(t: Ticket, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  const haystack = [
    t.key,
    t.title,
    ...t.items.flatMap((i) => [i.branch, i.worktree?.name ?? '', i.pr ? `#${i.pr.number}` : '', i.pr?.title ?? '']),
  ]
  return haystack.some((h) => h.toLowerCase().includes(q))
}
