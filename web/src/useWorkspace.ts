// Everything the page loads and keeps fresh: the workspace (git + Claude), each
// repo's branch graph with its pull requests, and the ticket store. One place, so
// App.vue only has to show it.
import { computed, reactive, ref, type Ref } from 'vue'
import { fetchBranches, fetchCleanup, fetchTickets, fetchWorkspace } from './api'
import { loadBranchData, newBranchState, type BranchState } from './branchLoader'
import { deliver } from './notifier'
import { transitions } from './notify'
import { branchTidy, type BranchTidy } from './tidy'
import type { Repo, TicketsData, WorkspaceData } from './types'

/** The workspace without the sessions the user said no longer matter. */
export function withoutDismissed(ws: WorkspaceData, dismissed: ReadonlySet<string>): WorkspaceData {
  if (!dismissed.size) return ws
  const keep = <T extends { id: string }>(list: T[]) => list.filter((s) => !dismissed.has(s.id))
  return {
    ...ws,
    repos: ws.repos.map((r) => ({ ...r, worktrees: r.worktrees.map((w) => ({ ...w, sessions: keep(w.sessions) })) })),
    unlinked: ws.unlinked.filter((u) => !dismissed.has(u.session.id)),
  }
}

export interface WorkspaceOptions {
  isHidden: (r: Repo) => boolean
  notifyOn: Ref<boolean>
}

export function useWorkspace({ isHidden, notifyOn }: WorkspaceOptions) {
  const raw = ref<WorkspaceData | null>(null)
  const store = ref<TicketsData | null>(null)
  const error = ref('')
  const loading = ref(false)
  const branchState = reactive<Record<string, BranchState>>({})
  const branchTidyOf = reactive<Record<string, BranchTidy>>({})

  const dismissed = computed(() => new Set(store.value?.dismissed ?? []))
  const data = computed(() => (raw.value ? withoutDismissed(raw.value, dismissed.value) : null))

  // The graph from git appears first; pull requests fill in when they arrive.
  function loadBranches(repo: Repo, refresh = false) {
    if (!branchState[repo.path]) branchState[repo.path] = newBranchState()
    // go through the reactive proxy: the object returned by an assignment is the raw one, and mutating it would not update the page
    return loadBranchData(branchState[repo.path], repo.path, refresh, fetchBranches)
  }

  // How many branches could go, from the cleanup preview (read-only, served from the PR cache).
  async function loadTidy(repo: Repo) {
    const st = branchState[repo.path]
    if (!st?.data || st.data.prsPending) return // without PR data the count would be misleading
    try {
      branchTidyOf[repo.path] = branchTidy((await fetchCleanup(repo.path, 30)).candidates)
    } catch {
      delete branchTidyOf[repo.path] // the bar just shows less
    }
  }

  /** Re-reads the ticket store; the board works without it (everything is then in the inbox). */
  async function loadTickets(): Promise<void> {
    try {
      store.value = await fetchTickets()
    } catch {
      // keep what is on screen
    }
  }

  function announce(prev: WorkspaceData | null, next: WorkspaceData) {
    const events = transitions(prev?.repos, next.repos, next.unlinked, prev?.unlinked)
    deliver(events, {
      enabled: notifyOn.value,
      permission: typeof Notification === 'undefined' ? 'denied' : Notification.permission,
      hidden: document.hidden,
      make: (title, options) => {
        const n = new Notification(title, options)
        n.onclick = () => {
          window.focus()
          n.close()
        }
      },
    })
  }

  let busy = false
  async function refresh(quiet = false) {
    if (busy) return
    busy = true
    loading.value = !quiet // the timer refreshes without flickering the button
    try {
      const prev = data.value
      const [next] = await Promise.all([fetchWorkspace(), loadTickets()])
      raw.value = next
      if (data.value) announce(prev, data.value)
      error.value = ''
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    } finally {
      loading.value = false
      busy = false
    }
    // The branch graph (and its PRs) loads in the background; the board never waits for it. This does
    // NOT force a lookup: the server serves its cache (5 min, kept on disk) and refreshes old data by
    // itself, so pressing Refresh costs git, not API calls. "↻ PRs" in the Branches tab forces one.
    // Hidden projects are skipped: no point spending API calls on what is not shown.
    if (quiet) return // the timer only watches Claude: branches are not worth a git call every 30 s
    for (const repo of raw.value?.repos ?? []) {
      if (!isHidden(repo)) loadBranches(repo, false)?.then(() => loadTidy(repo))
    }
  }

  return { raw, data, store, dismissed, error, loading, branchState, branchTidyOf, loadBranches, loadTidy, loadTickets, refresh }
}
