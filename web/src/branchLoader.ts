import type { BranchesResponse } from './types'

/** Load state of one repo's Branches tab. Mutated in place (it is a reactive object in the app). */
export interface BranchState {
  loading: boolean // any step running
  prsLoading: boolean // the graph is on screen, pull requests still on their way
  merged: boolean // include recently merged branches
  data?: BranchesResponse
  error: string // the graph itself could not be loaded
  prsError: string // the graph is fine but pull requests could not be loaded
}

export type BranchFetcher = (
  repo: string,
  opts: { merged?: boolean; refresh?: boolean; prs?: boolean },
) => Promise<BranchesResponse>

export const newBranchState = (): BranchState => ({ loading: false, prsLoading: false, merged: false, error: '', prsError: '' })

const message = (e: unknown) => (e instanceof Error ? e.message : String(e))

/**
 * Loads a repo's branches in two steps so the page never waits for the forge:
 *
 *  1. the graph from git alone (fast) is shown immediately, unless a graph is
 *     already on screen, which is kept instead of blanked;
 *  2. the full graph with pull requests (slow, network) replaces it.
 *
 * If only step 2 fails the graph stays and `prsError` explains why.
 */
export async function loadBranchData(
  st: BranchState,
  repo: string,
  refresh: boolean,
  fetcher: BranchFetcher,
): Promise<void> {
  if (st.loading) return
  st.loading = true
  try {
    if (!st.data) {
      try {
        st.data = await fetcher(repo, { merged: st.merged, prs: false })
        st.error = ''
      } catch (e) {
        st.error = message(e)
        return
      }
    }

    st.prsLoading = true
    st.prsError = ''
    try {
      st.data = await fetcher(repo, { merged: st.merged, refresh })
      st.error = ''
    } catch (e) {
      st.prsError = message(e)
    }
  } finally {
    st.loading = false
    st.prsLoading = false
  }
}
