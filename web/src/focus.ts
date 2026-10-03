// Focus (starred worktrees) and the work-in-progress limit. Pure.
import type { Lane } from './stage'
import type { Worktree } from './types'

export interface Note {
  starred?: boolean
  text?: string
}

export const WIP_LIMIT = 3

/** Starred worktrees go to the focus strip (main never does); the rest keep their order. */
export function splitFocus(list: Worktree[], notes: Record<string, Note>): { focus: Worktree[]; rest: Worktree[] } {
  const focus: Worktree[] = []
  const rest: Worktree[] = []
  for (const w of list) (!w.isMain && notes[w.path]?.starred ? focus : rest).push(w)
  return { focus, rest }
}

/** How many worktrees you are working on or that wait for you. */
export function wipCount(lanes: Lane[]): number {
  return lanes.filter((l) => l.stage === 'needs-you' || l.stage === 'in-progress').reduce((n, l) => n + l.items.length, 0)
}

/** A soft warning, or "" while within the limit. */
export const wipLabel = (count: number, limit: number): string => (count > limit ? `${count} in progress, limit ${limit}` : '')
