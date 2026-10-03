import type { BranchesResponse, CleanupPreview, DeleteResult, PlanData, WorkspaceData, WtPreview, WtResult } from './types'

export async function fetchWorkspace(): Promise<WorkspaceData> {
  const res = await fetch('/api/workspace', { cache: 'no-store' })
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`.trim())
  return res.json()
}

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url, { cache: 'no-store' })
  if (!res.ok) throw new Error(`${res.status} ${(await res.text()).trim()}`.trim())
  return res.json()
}

export const fetchPlan = (sessionId: string) =>
  getJSON<PlanData>(`/api/plan?session=${encodeURIComponent(sessionId)}`)

/** prs: false asks for the graph from git alone (fast, no network); the default includes pull requests. */
export const fetchBranches = (repo: string, opts: { merged?: boolean; refresh?: boolean; prs?: boolean } = {}) =>
  getJSON<BranchesResponse>(
    `/api/branches?repo=${encodeURIComponent(repo)}${opts.merged ? '&merged=1' : ''}${opts.refresh ? '&refresh=1' : ''}${
      opts.prs === false ? '&prs=0' : ''
    }`,
  )

/** Branches that could be deleted (merged PR, old enough). Changes nothing. */
export const fetchCleanup = (repo: string, days: number) =>
  getJSON<CleanupPreview>(`/api/cleanup?repo=${encodeURIComponent(repo)}&days=${days}&refresh=1`)

/** Deletes the chosen local branches; the server re-checks each one before touching it. */
export async function runCleanup(
  repo: string,
  days: number,
  branches: { branch: string; sha: string }[],
  allowLocalOnly = false,
): Promise<DeleteResult[]> {
  const res = await fetch('/api/cleanup', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ repo, days, branches, allowLocalOnly }),
  })
  if (!res.ok) throw new Error((await res.text()).trim() || `${res.status}`)
  return (await res.json()).results as DeleteResult[]
}

/** Worktrees whose PR merged and that hold nothing else. Changes nothing. */
export const fetchWorktreeCleanup = (repo: string, days: number) =>
  getJSON<WtPreview>(`/api/worktree-cleanup?repo=${encodeURIComponent(repo)}&days=${days}`)

/** Removes the chosen worktree folders; the server re-checks each one (never --force). */
export async function runWorktreeCleanup(
  repo: string,
  days: number,
  worktrees: { path: string; sha: string }[],
): Promise<WtResult[]> {
  const res = await fetch('/api/worktree-cleanup', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ repo, days, worktrees }),
  })
  if (!res.ok) throw new Error((await res.text()).trim() || `${res.status}`)
  return (await res.json()).results as WtResult[]
}

export type Action = 'terminal' | 'editor' | 'resume'

export async function runAction(action: Action, body: { path?: string; sessionId?: string; mode?: 'cli' | 'desktop' }): Promise<void> {
  const res = await fetch(`/api/actions/${action}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error((await res.text()).trim() || `${res.status}`)
}
