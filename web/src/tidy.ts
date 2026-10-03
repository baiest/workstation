// What could be cleaned up in a repo, as counts for the "Tidy up" bar. Pure.
import { stageOf } from './stage'
import { mergedHint } from './worktrees'
import type { CleanupCandidate, Pr, Worktree } from './types'

export interface TidyCounts {
  removable: number // PR merged, nothing pending
  dormant: number // no activity for a long time
}

export function tidyCounts(list: Worktree[], prFor: (branch?: string) => Pr | undefined, now: Date = new Date()): TidyCounts {
  let removable = 0
  let dormant = 0
  for (const w of list) {
    if (w.isMain) continue
    const pr = prFor(w.branch)
    if (mergedHint(w, pr)) removable++
    else if (stageOf(w, pr, now) === 'dormant') dormant++
  }
  return { removable, dormant }
}

export interface BranchTidy {
  safe: number // PR merged, or merged long ago
  risky: number // old branches without a merged PR: each needs a look
}

/** A candidate without a kind is a branch whose PR was merged. */
export function branchTidy(candidates: CleanupCandidate[]): BranchTidy {
  const out = { safe: 0, risky: 0 }
  for (const c of candidates) {
    if (!c.kind || c.kind === 'pr-merged' || c.kind === 'stale-merged') out.safe++
    else out.risky++
  }
  return out
}

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`

export function tidyText(wt: TidyCounts, br?: BranchTidy): string {
  const parts: string[] = []
  if (wt.removable) parts.push(`${plural(wt.removable, 'worktree', 'worktrees')} merged`)
  if (wt.dormant) parts.push(`${wt.dormant} dormant`)
  if (br?.safe) parts.push(`${plural(br.safe, 'branch', 'branches')} can be deleted`)
  if (br?.risky) parts.push(`${plural(br.risky, 'old branch', 'old branches')} to review`)
  return parts.join(' · ')
}
