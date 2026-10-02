import type { BranchesResponse, PlanData, WorkspaceData } from './types'

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

export const fetchBranches = (repo: string, opts: { merged?: boolean; refresh?: boolean } = {}) =>
  getJSON<BranchesResponse>(
    `/api/branches?repo=${encodeURIComponent(repo)}${opts.merged ? '&merged=1' : ''}${opts.refresh ? '&refresh=1' : ''}`,
  )

export type Action ='terminal' | 'editor' | 'resume'

export async function runAction(action: Action, body: { path?: string; sessionId?: string }): Promise<void> {
  const res = await fetch(`/api/actions/${action}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error((await res.text()).trim() || `${res.status}`)
}
