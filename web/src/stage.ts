// Which stage of the work a worktree is in, derived from data we already have
// (Claude state, git, PR, dates). Nothing here is typed in by the user.
import type { Pr, Worktree } from './types'
import { worktreeActivity } from './worktrees'

export type Stage = 'needs-you' | 'in-progress' | 'review' | 'done' | 'dormant'

export const STAGES: Stage[] = ['needs-you', 'in-progress', 'review', 'done', 'dormant']

export const stageTitle: Record<Stage, string> = {
  'needs-you': 'Needs you',
  'in-progress': 'In progress',
  review: 'In review',
  done: 'Done',
  dormant: 'Dormant',
}

export const DORMANT_DAYS = 14
const DAY = 24 * 3600 * 1000
const FAILURE_WINDOW = DAY // an old failure of a dead process must not stay red forever

const isLive = (w: Worktree) => w.sessions.some((s) => s.status === 'working' || s.status === 'idle')

function needsYou(w: Worktree, pr: Pr | undefined, now: Date): boolean {
  const s = w.sessions[0]
  if (s?.state === 'waiting' || s?.state === 'needs-approval') return true
  if (s?.state === 'failed' && now.getTime() - Date.parse(s.lastActivity) < FAILURE_WINDOW) return true
  if (w.git.conflicts > 0) return true
  return pr?.state === 'open' && (pr.review === 'changes_requested' || pr.checks === 'failure')
}

/** First match wins: needs-you, done, review, dormant, else in-progress. */
export function stageOf(w: Worktree, pr: Pr | undefined, now: Date = new Date(), dormantDays = DORMANT_DAYS): Stage {
  if (needsYou(w, pr, now)) return 'needs-you'
  if (pr && (pr.state === 'merged' || pr.state === 'declined')) return 'done'
  if (pr?.state === 'open' && !pr.draft) return 'review'
  const idleDays = (now.getTime() - worktreeActivity(w)) / DAY
  if (!pr && !w.git.dirty && !isLive(w) && idleDays > dormantDays) return 'dormant'
  return 'in-progress'
}

export interface Lane {
  stage: Stage
  items: Worktree[]
}

/** The main worktree stays out of the lanes; empty lanes are dropped. Item order is kept. */
export function groupByStage(
  list: Worktree[],
  prFor: (branch?: string) => Pr | undefined,
  now: Date = new Date(),
  dormantDays = DORMANT_DAYS,
): { main?: Worktree; lanes: Lane[] } {
  const by = new Map<Stage, Worktree[]>()
  let main: Worktree | undefined
  for (const w of list) {
    if (w.isMain) {
      main = w
      continue
    }
    const st = stageOf(w, prFor(w.branch), now, dormantDays)
    by.set(st, [...(by.get(st) ?? []), w])
  }
  return { main, lanes: STAGES.filter((s) => by.has(s)).map((s) => ({ stage: s, items: by.get(s)! })) }
}
