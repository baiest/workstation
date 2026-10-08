// The one rule for "what needs me": every place that asks (header chips, tab
// title, Active now, the stage board, ordering) goes through here, so they cannot
// disagree. Pure, no DOM.
import type { Session, Worktree } from './types'

export type Attention =
  | 'asks-you' // Claude finished its turn with a question: it cannot go on without you (a guess)
  | 'approval' // a tool request has waited a while (a guess)
  | 'failed' // the last thing recorded was an API error, recently
  | 'review-output' // Claude finished its turn: there is something to look at
  | 'working'
  | 'stale' // alive and waiting for a long time: nobody is coming back, close it
  | 'none'

export const STALE_WAITING_HOURS = 2
export const FAILURE_WINDOW_HOURS = 24
const HOUR = 3600 * 1000

/** Lower = look at it sooner. */
export const attentionRank: Record<Attention, number> = {
  'asks-you': 0,
  approval: 1,
  failed: 2,
  'review-output': 3,
  working: 4,
  stale: 5,
  none: 6,
}

/** True for the kinds that call for you now (what the chips, the title and "Needs you" count). */
export const needsYou = (a: Attention): boolean => a === 'asks-you' || a === 'approval' || a === 'failed' || a === 'review-output'

/** Attention kinds that come from a guess rather than from something recorded. */
export const isGuess = (a: Attention): boolean => a === 'asks-you' || a === 'approval'

/** The last words end in a question mark: Claude asked something. A guess, labelled as one. */
export function endsWithQuestion(text?: string): boolean {
  const t = (text ?? '').trim().replace(/[\s"')\]*_`]+$/u, '')
  return t.endsWith('?') || t.endsWith('？')
}

function ageHours(iso: string, now: Date): number {
  const t = Date.parse(iso)
  return Number.isNaN(t) ? Infinity : (now.getTime() - t) / HOUR
}

/**
 * What one session asks of you. `newest` says it is the latest session of its
 * worktree: an old failure that a newer session superseded stays quiet.
 */
export function sessionAttention(s: Session, now: Date = new Date(), newest = true): Attention {
  switch (s.state) {
    case 'waiting':
    case 'needs-approval': {
      if (s.status === 'stopped') return 'none'
      if (ageHours(s.lastActivity, now) > STALE_WAITING_HOURS) return 'stale'
      if (s.state === 'needs-approval') return 'approval'
      return endsWithQuestion(s.lastMessage) ? 'asks-you' : 'review-output'
    }
    case 'failed':
      return newest && ageHours(s.lastActivity, now) < FAILURE_WINDOW_HOURS ? 'failed' : 'none'
    case 'thinking':
    case 'running-tool':
      return 'working'
  }
  return s.status === 'working' ? 'working' : 'none'
}

/** The most urgent thing any session of a worktree asks of you. */
export function worktreeSessionAttention(w: Pick<Worktree, 'sessions'>, now: Date = new Date()): Attention {
  let best: Attention = 'none'
  w.sessions.forEach((s, i) => {
    const a = sessionAttention(s, now, i === 0)
    if (attentionRank[a] < attentionRank[best]) best = a
  })
  return best
}
