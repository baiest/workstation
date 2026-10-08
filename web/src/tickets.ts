// A ticket is the unit of work: everything of it (branches, worktrees, PRs, Claude
// sessions) comes from Git and Claude, and what only a human can say (state, next
// step, discoveries) comes from the server's store. Pure, no DOM.
//
// A ticket is named by its ref: a tracker key (REG-5393) when the branch has one,
// otherwise the branch itself ("branch:fix/typo"). The server decides the ref
// (internal/tickets); the fallback below only serves data that lacks it.
import { worktreeSessionAttention, attentionRank, type Attention } from './attention'
import { needsYouNow } from './stage'
import type { BranchNode, Discovery, Pr, Session, StoredTicket, TicketState, Worktree } from './types'
import { attentionScore, ticketKey, worktreeActivity } from './worktrees'

export interface TicketItem {
  branch: string // "detached @ abc1234" for a detached worktree
  worktree?: Worktree
  node?: BranchNode
  pr?: Pr
  sessions: Session[]
  activity: number // ms of the latest commit or Claude session
}

export type EffectiveState = Exclude<TicketState, ''> | 'inbox'

export interface Ticket {
  id: string // repo path + "|" + ref, the identity the server stores it under
  repo: string
  ref: string
  key: string // the tracker key, "" for a ticket named after its branch
  title: string
  items: TicketItem[]
  activity: number
  attention: number
  signal: Attention // the most urgent thing its Claude sessions ask of you
  needsYou: boolean // sessions, a failing PR or conflicts call for you
  done: boolean // it has work and every item has a merged PR
  state: EffectiveState
  stored?: StoredTicket
  nextStep: string
  blockedReason: string
  blockedBy: string[] // ids of the tickets it waits for
  openDiscoveries: Discovery[]
}

export interface BuildOptions {
  repo?: string // path of the repo, to pick its stored tickets
  stored?: Record<string, StoredTicket>
  now?: Date
}

function toMs(iso?: string): number {
  const t = iso ? Date.parse(iso) : NaN
  return Number.isNaN(t) ? 0 : t
}

/** The id the server stores a ticket under. */
export const ticketId = (repo: string, ref: string) => `${repo}|${ref}`

/** Same rule as the server's Keyer.Ref, for data that arrives without a ref. */
export function fallbackRef(branch?: string, folder?: string): string {
  const key = ticketKey(branch) ?? ticketKey(folder)
  if (key) return key
  if (branch) return `branch:${branch.replaceAll('|', '_')}`
  return folder ? `folder:${folder.replaceAll('|', '_')}` : ''
}

/** A ref that is a tracker key rather than a branch, folder or note ref. */
export const isKeyRef = (ref: string) => ref !== '' && !/^(branch|folder|note):/.test(ref)

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
    items.set(`wt:${w.path}`, { branch, worktree: w, sessions: w.sessions, pr: w.pr, activity: worktreeActivity(w) })
  }

  for (const n of nodes) {
    if (n.isDefault || n.branch === defaultBranch) continue
    const existing = [...items.values()].find((i) => i.worktree?.branch === n.branch)
    if (existing) {
      existing.node = n
      existing.pr = n.pr ?? existing.pr // the graph is the fresher source once it has loaded
      existing.activity = Math.max(existing.activity, toMs(n.date))
    } else {
      items.set(`br:${n.branch}`, { branch: n.branch, node: n, pr: n.pr, sessions: [], activity: toMs(n.date) })
    }
  }
  return items
}

function refOf(item: TicketItem, owner: Map<string, string>): string {
  if (owner.has(item.branch)) return owner.get(item.branch)!
  return item.worktree?.ticket ?? item.node?.ticket ?? fallbackRef(item.worktree?.branch ?? item.branch, item.worktree?.name)
}

function itemNeedsYou(i: TicketItem, now: Date): boolean {
  return !!i.worktree && needsYouNow(i.worktree, i.pr, now)
}

function itemAttention(i: TicketItem, now: Date): number {
  if (i.worktree) return attentionScore(i.worktree, i.pr, now)
  if (i.pr?.state === 'open') return 30
  return i.pr?.state === 'merged' ? -50 : 0
}

function titleOf(ref: string, items: TicketItem[], stored?: StoredTicket): string {
  if (stored?.title) return stored.title
  if (!items.length) return ref.replace(/^[a-z]+:/, '')
  const byRecent = [...items].sort((a, b) => b.activity - a.activity)
  const pr = byRecent.find((i) => i.pr?.state === 'open')?.pr ?? byRecent.find((i) => i.pr)?.pr
  return pr?.title ?? byRecent[0].branch
}

const SECTIONLESS: EffectiveState = 'inbox'

/** Tickets ordered by what needs attention, then newest; finished ones last. */
export function buildTickets(worktrees: Worktree[], nodes: BranchNode[], defaultBranch: string, opts: BuildOptions = {}): Ticket[] {
  const now = opts.now ?? new Date()
  const repo = opts.repo ?? ''
  const prefix = repo + '|'
  const storedHere = Object.values(opts.stored ?? {}).filter((s) => s.repo === repo || s.id.startsWith(prefix))

  // a stored ticket may own a branch whose name says otherwise (a discovery promoted to work)
  const owner = new Map<string, string>()
  for (const s of storedHere) if (s.branch) owner.set(s.branch, s.ref)

  const groups = new Map<string, TicketItem[]>()
  for (const item of collectItems(worktrees, nodes, defaultBranch).values()) {
    const ref = refOf(item, owner)
    groups.set(ref, [...(groups.get(ref) ?? []), item])
  }
  for (const s of storedHere) if (!groups.has(s.ref)) groups.set(s.ref, [])

  const tickets: Ticket[] = [...groups].map(([ref, items]) => {
    const stored = opts.stored?.[ticketId(repo, ref)]
    const sorted = items.sort((a, b) => Number(!!b.worktree) - Number(!!a.worktree) || b.activity - a.activity || a.branch.localeCompare(b.branch))
    const signal = sorted.reduce<Attention>((best, i) => {
      const a = i.worktree ? worktreeSessionAttention(i.worktree, now) : 'none'
      return attentionRank[a] < attentionRank[best] ? a : best
    }, 'none')
    return {
      id: ticketId(repo, ref),
      repo,
      ref,
      key: isKeyRef(ref) ? ref : '',
      title: titleOf(ref, sorted, stored),
      items: sorted,
      activity: sorted.length ? Math.max(...sorted.map((i) => i.activity)) : toMs(stored?.updated),
      attention: sorted.length ? Math.max(...sorted.map((i) => itemAttention(i, now))) : 0,
      signal,
      needsYou: sorted.some((i) => itemNeedsYou(i, now)),
      done: sorted.length > 0 && sorted.every((i) => i.pr?.state === 'merged'),
      state: (stored?.state || SECTIONLESS) as EffectiveState,
      stored,
      nextStep: stored?.nextStep ?? '',
      blockedReason: stored?.blockedReason ?? '',
      blockedBy: stored?.blockedBy ?? [],
      openDiscoveries: (stored?.discoveries ?? []).filter((d) => d.status === 'open'),
    }
  })

  return tickets.sort((a, b) => {
    if (a.done !== b.done) return a.done ? 1 : -1
    return b.attention - a.attention || b.activity - a.activity || a.ref.localeCompare(b.ref)
  })
}

/** Filter box for the tickets: key, title, next step, branches, folders and PR numbers. */
export function matchTicket(t: Ticket, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  const haystack = [
    t.key,
    t.ref,
    t.title,
    t.nextStep,
    ...t.items.flatMap((i) => [i.branch, i.worktree?.name ?? '', i.pr ? `#${i.pr.number}` : '', i.pr?.title ?? '']),
  ]
  return haystack.some((h) => h.toLowerCase().includes(q))
}
