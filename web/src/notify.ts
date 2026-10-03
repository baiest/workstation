// Which sessions just started needing you, for browser notifications and the
// tab title. Pure: the app compares two snapshots.
import { sessionTitle } from './format'
import { ticketKey } from './worktrees'
import type { Repo, Session, Unlinked } from './types'

export type NotifyKind = 'waiting' | 'needs-approval' | 'failed'

export interface NotifyEvent {
  id: string
  kind: NotifyKind
  title: string
  body: string
}

const KINDS: NotifyKind[] = ['waiting', 'needs-approval', 'failed']
const verb: Record<NotifyKind, string> = {
  waiting: 'Claude is waiting',
  'needs-approval': 'Claude may need approval',
  failed: 'Claude hit an error',
}

interface Seen {
  session: Session
  place?: string
}

function collect(repos: Repo[], unlinked: Unlinked[]): Map<string, Seen> {
  const out = new Map<string, Seen>()
  for (const r of repos) {
    for (const w of r.worktrees) {
      const place = ticketKey(w.branch) ?? w.branch ?? w.name
      for (const session of w.sessions) out.set(session.id, { session, place })
    }
  }
  for (const u of unlinked) out.set(u.session.id, { session: u.session })
  return out
}

/** Sessions that were seen before in another state and now wait for you, may need approval or failed. */
export function transitions(
  prevRepos: Repo[] | undefined,
  nextRepos: Repo[],
  nextUnlinked: Unlinked[],
  prevUnlinked: Unlinked[] = [],
): NotifyEvent[] {
  if (!prevRepos) return [] // first load: everything would look new
  const before = collect(prevRepos, prevUnlinked)
  const events: NotifyEvent[] = []
  for (const [id, { session, place }] of collect(nextRepos, nextUnlinked)) {
    const old = before.get(id)
    const kind = KINDS.find((k) => k === session.state)
    if (!old || !kind || old.session.state === session.state) continue
    events.push({ id, kind, title: place ? `${verb[kind]}: ${place}` : verb[kind], body: sessionTitle(session) })
  }
  return events
}

/** How many live sessions wait for you now (shown in the tab title). */
export function needsYouCount(repos: Repo[], unlinked: Unlinked[]): number {
  let n = 0
  for (const { session } of collect(repos, unlinked).values()) {
    if ((session.state === 'waiting' || session.state === 'needs-approval') && session.status !== 'stopped') n++
  }
  return n
}
