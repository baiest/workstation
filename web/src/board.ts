// How the ticket board lays tickets out: which section each one is in, in what
// order, and what a human can do with it next. Pure, no DOM.
import { attentionRank } from './attention'
import { isLive } from './worktrees'
import type { EffectiveState, Ticket } from './tickets'

export type SectionId =
  | 'needs-you'
  | 'active'
  | 'blocked'
  | 'ready'
  | 'closable'
  | 'inbox'
  | 'parked'
  | 'backlog'
  | 'dormant'
  | 'done'
  | 'ignored'

export const SECTIONS: SectionId[] = ['needs-you', 'active', 'blocked', 'ready', 'closable', 'inbox', 'parked', 'backlog', 'dormant', 'done', 'ignored']

export const sectionTitle: Record<SectionId, string> = {
  'needs-you': 'Needs you',
  active: 'Active',
  blocked: 'Blocked',
  ready: 'Ready',
  closable: 'Ready to close',
  inbox: 'Inbox',
  parked: 'Parked',
  backlog: 'Backlog',
  dormant: 'Old, undecided',
  done: 'Done',
  ignored: 'Ignored',
}

export const sectionHint: Record<SectionId, string> = {
  'needs-you': 'Claude waits for you, a PR needs changes or fails its checks, or git has conflicts',
  active: 'What you are carrying now',
  blocked: 'Waiting for something outside your hands',
  ready: 'Decided and ready to start',
  closable: 'Every PR is merged: mark it done, then clean up',
  inbox: 'Spotted from a branch, nobody decided anything yet. Activate, park, or ignore it',
  parked: 'Left on purpose, for later',
  backlog: 'Ideas and discoveries that are not started',
  dormant: 'No decision and no activity for weeks: ignore it or clean it up',
  done: 'Finished',
  ignored: 'Not tracked: branches you do not care about',
}

/** Sections that start collapsed: they are not what you are doing now. */
export const COLLAPSED: ReadonlySet<SectionId> = new Set(['backlog', 'dormant', 'done', 'ignored'])

export const DORMANT_DAYS = 14
const DAY = 24 * 3600 * 1000

function idle(t: Ticket, now: Date, days: number): boolean {
  if (!t.items.length) return false
  if (t.items.some((i) => i.pr?.state === 'open' || (i.worktree && (isLive(i.worktree) || i.worktree.git.dirty)))) return false
  return now.getTime() - t.activity > days * DAY
}

/** First match wins. A ticket that calls for you is shown as such whatever its state. */
export function sectionOf(t: Ticket, now: Date = new Date(), dormantDays = DORMANT_DAYS): SectionId {
  if (t.state === 'ignored') return 'ignored'
  if (t.state === 'done') return 'done'
  if (t.needsYou) return 'needs-you'
  if (t.done) return 'closable'
  switch (t.state) {
    case 'active':
      return 'active'
    case 'blocked':
      return 'blocked'
    case 'ready':
      return 'ready'
    case 'parked':
      return 'parked'
    case 'backlog':
      return 'backlog'
  }
  return idle(t, now, dormantDays) ? 'dormant' : 'inbox'
}

export interface Section {
  id: SectionId
  tickets: Ticket[]
}

function order(id: SectionId): (a: Ticket, b: Ticket) => number {
  if (id === 'needs-you') {
    return (a, b) => attentionRank[a.signal] - attentionRank[b.signal] || b.attention - a.attention || b.activity - a.activity
  }
  return (a, b) => b.activity - a.activity || a.ref.localeCompare(b.ref)
}

/** Non-empty sections in board order, tickets inside each ordered by what matters there. */
export function groupTickets(tickets: Ticket[], now: Date = new Date(), dormantDays = DORMANT_DAYS): Section[] {
  const by = new Map<SectionId, Ticket[]>()
  for (const t of tickets) {
    const id = sectionOf(t, now, dormantDays)
    by.set(id, [...(by.get(id) ?? []), t])
  }
  return SECTIONS.filter((id) => by.has(id)).map((id) => ({ id, tickets: by.get(id)!.sort(order(id)) }))
}

/** Tickets you are carrying: the ones the work-in-progress limit counts. */
export const wipCount = (tickets: Ticket[]): number => tickets.filter((t) => t.state === 'active').length

/** A soft warning, or "" while within the limit. */
export const wipLabel = (count: number, limit: number): string =>
  count > limit ? `${count} active tickets, limit ${limit}: park or finish one before starting more` : ''

export interface StateAction {
  label: string
  state: Exclude<EffectiveState, 'inbox'> | ''
  primary?: boolean
  title?: string
}

/** What a human can do with a ticket next. Moving to 'blocked' asks for a reason in the card. */
export function stateActions(t: Ticket): StateAction[] {
  const closeNow: StateAction[] = t.done && t.state !== 'done' ? [{ label: 'Mark done', state: 'done', primary: true, title: 'Every PR is merged' }] : []
  switch (t.state) {
    case 'inbox':
      return [
        ...closeNow,
        { label: 'Activate', state: 'active', primary: !closeNow.length },
        { label: 'Park', state: 'parked', title: 'Leave it for later, on purpose' },
        { label: 'Backlog', state: 'backlog' },
        { label: 'Ignore', state: 'ignored', title: 'Stop tracking this branch' },
      ]
    case 'backlog':
      return [{ label: 'Ready', state: 'ready' }, { label: 'Activate', state: 'active', primary: true }, { label: 'Ignore', state: 'ignored' }]
    case 'ready':
      return [{ label: 'Activate', state: 'active', primary: true }, { label: 'Backlog', state: 'backlog' }]
    case 'active':
      return [...closeNow, { label: 'Park', state: 'parked' }, { label: 'Block', state: 'blocked', title: 'Waiting for something outside your hands' }, { label: 'Done', state: 'done' }]
    case 'parked':
      return [...closeNow, { label: 'Activate', state: 'active', primary: true }, { label: 'Backlog', state: 'backlog' }]
    case 'blocked':
      return [{ label: 'Unblock', state: 'active', primary: true }, { label: 'Park', state: 'parked' }]
    case 'done':
      return [{ label: 'Reopen', state: 'active' }]
    case 'ignored':
      return [{ label: 'Track again', state: '' }]
  }
}
