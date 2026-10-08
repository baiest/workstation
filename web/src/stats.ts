// What the history of ticket states says about how the work went: where time was
// spent, how much was carried at once, how many tickets were finished, what
// became of the discoveries. Read-only and computed here from /api/tickets; the
// server only records the transitions. Pure, no DOM.
import { ticketLabel } from './board'
import type { Ticket } from './tickets'
import type { StoredTicket, TicketState } from './types'

const DAY = 24 * 3600 * 1000
const WEEK = 7 * DAY

export interface Span {
  state: TicketState
  from: number
  to: number
}

/** The stretches of time a ticket spent in each state, from its creation until now. */
export function spansOf(t: StoredTicket, now: Date): Span[] {
  const end = now.getTime()
  const created = Date.parse(t.created)
  const history = [...(t.history ?? [])].sort((a, b) => Date.parse(a.at) - Date.parse(b.at))
  if (Number.isNaN(created)) return []
  if (!history.length) return [{ state: t.state ?? '', from: created, to: Math.max(created, end) }]

  const spans: Span[] = []
  let state: TicketState = history[0].from
  let from = created
  for (const h of history) {
    const at = Date.parse(h.at)
    if (!Number.isNaN(at)) {
      spans.push({ state, from, to: Math.max(from, at) })
      state = h.to
      from = Math.max(from, at)
    }
  }
  spans.push({ state, from, to: Math.max(from, end) })
  return spans
}

const timeIn = (spans: Span[], state: TicketState, since = -Infinity) =>
  spans.filter((s) => s.state === state).reduce((n, s) => n + Math.max(0, s.to - Math.max(s.from, since)), 0)

export interface TicketTime {
  id: string
  label: string
  activeMs: number
  blockedMs: number
  sessions: number
}

export interface Stats {
  perTicket: TicketTime[] // tickets that were ever active or blocked, most time first
  doneByWeek: { start: string; count: number }[] // oldest first, the last `weeks` weeks
  averageActive: number // tickets carried at once, on average over the last `days` days
  discoveries: { total: number; open: number; dropped: number; inScope: number; promoted: number }
}

export interface StatsOptions {
  weeks?: number
  days?: number
}

function sessionCount(t?: Ticket): number {
  const ids = new Set<string>()
  for (const i of t?.items ?? []) for (const s of i.sessions) ids.add(s.id)
  return ids.size
}

export function computeStats(stored: StoredTicket[], tickets: Ticket[], now: Date = new Date(), opts: StatsOptions = {}): Stats {
  const weeks = opts.weeks ?? 8
  const days = opts.days ?? 14
  const end = now.getTime()
  const byId = new Map(tickets.map((t) => [t.id, t]))

  const perTicket: TicketTime[] = []
  const doneByWeek = Array.from({ length: weeks }, (_, i) => ({
    start: new Date(end - (weeks - i) * WEEK).toISOString().slice(0, 10),
    count: 0,
  }))
  const windowStart = end - days * DAY
  let activeMs = 0
  const discoveries = { total: 0, open: 0, dropped: 0, inScope: 0, promoted: 0 }

  for (const t of stored) {
    const spans = spansOf(t, now)
    const active = timeIn(spans, 'active')
    const blocked = timeIn(spans, 'blocked')
    activeMs += timeIn(spans, 'active', windowStart)
    if (active > 0 || blocked > 0) {
      const live = byId.get(t.id)
      perTicket.push({ id: t.id, label: live ? ticketLabel(live) : t.title || t.ref, activeMs: active, blockedMs: blocked, sessions: sessionCount(live) })
    }

    const finished = [...(t.history ?? [])].filter((h) => h.to === 'done').map((h) => Date.parse(h.at)).filter((x) => !Number.isNaN(x)).pop()
    if (finished !== undefined && finished <= end && finished > end - weeks * WEEK) {
      const idx = Math.min(weeks - 1, Math.floor((finished - (end - weeks * WEEK)) / WEEK))
      doneByWeek[idx].count++
    }

    for (const d of t.discoveries ?? []) {
      discoveries.total++
      if (d.status === 'open') discoveries.open++
      else if (d.status === 'dropped') discoveries.dropped++
      else if (d.status === 'in-scope') discoveries.inScope++
      else if (d.status === 'promoted') discoveries.promoted++
    }
  }

  perTicket.sort((a, b) => b.activeMs + b.blockedMs - (a.activeMs + a.blockedMs) || a.label.localeCompare(b.label))
  return { perTicket, doneByWeek, averageActive: activeMs / (days * DAY), discoveries }
}

/** "3 d 4 h", "5 h", "20 min", "under a minute". */
export function formatDuration(ms: number): string {
  const mins = Math.floor(ms / 60000)
  if (mins < 1) return 'under a minute'
  if (mins < 60) return `${mins} min`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours} h`
  const d = Math.floor(hours / 24)
  const h = hours % 24
  return h ? `${d} d ${h} h` : `${d} d`
}
