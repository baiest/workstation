import type { Session } from './types'

export type ResumeWith = 'cli' | 'desktop'

export interface ResumeDecision {
  mode: ResumeWith
  /** Something to tell the user: what was opened and what to do next. */
  note?: string
}

/**
 * Decides how "Resume" runs. Claude Desktop can only be brought up (no link to a
 * single session is known), and only holds sessions started in Desktop; a
 * CLI-only session is always resumed in a terminal, whatever the preference.
 */
export function resumeDecision(session: Session | undefined, preference: ResumeWith): ResumeDecision {
  if (preference !== 'desktop' || !session) return { mode: 'cli' }
  if (!session.desktopId) {
    return { mode: 'cli', note: 'This session was started in the CLI, so it was resumed in a terminal.' }
  }
  const what = session.title ? `“${session.title}”` : `the session in ${session.cwd}`
  return { mode: 'desktop', note: `Claude Desktop opened. Look for ${what} in ${session.cwd}.` }
}
