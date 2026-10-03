<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { fetchBranches, fetchWorkspace, runAction, type Action } from './api'
import BranchGraph from './components/BranchGraph.vue'
import CleanupModal from './components/CleanupModal.vue'
import PlanModal from './components/PlanModal.vue'
import SessionsModal from './components/SessionsModal.vue'
import TicketList from './components/TicketList.vue'
import WorktreeCard from './components/WorktreeCard.vue'
import WorktreeCleanupModal from './components/WorktreeCleanupModal.vue'
import ActiveSessions from './components/ActiveSessions.vue'
import { activeSessions } from './active'
import { loadBranchData, newBranchState, type BranchState } from './branchLoader'
import { matchesFilter, relativeTime, statusLabel } from './format'
import { resumeDecision, type ResumeWith } from './resume'
import { buildTickets, matchTicket } from './tickets'
import { mergedHint, sortWorktrees, summarizeSessions } from './worktrees'
import { browserStorage, loadHidden, saveHidden, toggleHidden } from './hidden'
import type { Pr, Repo, WorkspaceData, Worktree } from './types'
import { FONT_DEFAULT, clampFontSize, nextFontSize } from './zoom'

type Tab = 'worktrees' | 'branches' | 'tickets'

const TAB_KEY = 'workstation.tabs'
const UNLINKED_LIMIT = 50

const data = ref<WorkspaceData | null>(null)
const error = ref('')
const toast = ref('')
const loading = ref(false)
const query = ref('')
const filterEl = ref<HTMLInputElement | null>(null)
const planFor = ref<{ id: string; title: string } | null>(null)
const cleanupFor = ref<Repo | null>(null)
const wtCleanupFor = ref<Repo | null>(null)
const sessionsFor = ref<Worktree | null>(null) // the worktree whose full session list is open

function openSessions(repo: Repo, path: string) {
  sessionsFor.value = repo.worktrees.find((w) => w.path === path) ?? null
}
const tabs = reactive<Record<string, Tab>>(loadTabs())
const branchState = reactive<Record<string, BranchState>>({})

// "Resume Claude" in a terminal (CLI) or in the Claude Desktop app. Remembered per browser.
const RESUME_KEY = 'workstation.resume-with'
const resumeWith = ref<ResumeWith>(loadResumeWith())
const notice = ref('')

function loadResumeWith(): ResumeWith {
  try {
    return localStorage.getItem(RESUME_KEY) === 'desktop' ? 'desktop' : 'cli'
  } catch {
    return 'cli'
  }
}

function toggleResumeWith() {
  resumeWith.value = resumeWith.value === 'cli' ? 'desktop' : 'cli'
  try {
    localStorage.setItem(RESUME_KEY, resumeWith.value)
  } catch {
    // ignore: remembering the choice is a convenience
  }
}

function findSession(id: string) {
  for (const repo of data.value?.repos ?? []) {
    for (const wt of repo.worktrees) {
      const s = wt.sessions.find((x) => x.id === id)
      if (s) return s
    }
  }
  return data.value?.unlinked.find((u) => u.session.id === id)?.session
}

async function resumeSession(id: string) {
  const decision = resumeDecision(findSession(id), resumeWith.value)
  notice.value = ''
  await act('resume', { sessionId: id, mode: decision.mode })
  if (!toast.value && decision.note) notice.value = decision.note // only if it did not fail
}

// Order of the worktree cards: by what needs attention (default) or by name.
type SortMode = 'activity' | 'name'
const SORT_KEY = 'workstation.sort'
const sortMode = ref<SortMode>(loadSort())

function loadSort(): SortMode {
  try {
    return localStorage.getItem(SORT_KEY) === 'name' ? 'name' : 'activity'
  } catch {
    return 'activity'
  }
}

function toggleSort() {
  sortMode.value = sortMode.value === 'activity' ? 'name' : 'activity'
  try {
    localStorage.setItem(SORT_KEY, sortMode.value)
  } catch {
    // ignore: remembering the order is a convenience
  }
}

// Projects the user hid, remembered per browser.
const store = browserStorage()
const hidden = ref<string[]>(loadHidden(store))
const isHidden = (r: Repo) => hidden.value.includes(r.path)
const hiddenRepos = computed(() => (data.value?.repos ?? []).filter(isHidden))
// "Who needs me": Claude sessions waiting for you / working / failed, across the visible projects
const sessionSummary = computed(() => summarizeSessions((data.value?.repos ?? []).filter((r) => !isHidden(r))))
const activeRows = computed(() => activeSessions(data.value?.repos ?? [], data.value?.unlinked ?? [], new Date(), 24, isHidden))

function toggleRepo(repo: Repo) {
  hidden.value = toggleHidden(hidden.value, repo.path)
  saveHidden(store, hidden.value)
  if (!isHidden(repo) && !branchState[repo.path]?.data) loadBranches(repo) // just shown again
}

// UI size: the root font size in px, remembered per browser. Everything is in rem.
const UI_KEY = 'workstation.ui-px'
const uiPx = ref(loadUiPx())

function loadUiPx(): number {
  try {
    return clampFontSize(parseInt(localStorage.getItem(UI_KEY) ?? '', 10))
  } catch {
    return FONT_DEFAULT
  }
}

function applyUiPx() {
  document.documentElement.style.fontSize = `${uiPx.value}px`
}

function bumpUi(dir: 1 | -1) {
  uiPx.value = nextFontSize(uiPx.value, dir)
  applyUiPx()
  try {
    localStorage.setItem(UI_KEY, String(uiPx.value))
  } catch {
    // ignore: remembering the size is a convenience
  }
}

function loadTabs(): Record<string, Tab> {
  try {
    return JSON.parse(localStorage.getItem(TAB_KEY) ?? '{}')
  } catch {
    return {} // storage unavailable: tabs just reset on reload
  }
}

function setTab(repo: Repo, tab: Tab) {
  tabs[repo.path] = tab
  try {
    localStorage.setItem(TAB_KEY, JSON.stringify(tabs))
  } catch {
    // ignore: remembering the tab is a convenience
  }
}

const tabOf = (repo: Repo): Tab => tabs[repo.path] ?? 'worktrees'

// The graph from git appears first; pull requests fill in when they arrive.
function loadBranches(repo: Repo, refresh = false) {
  if (!branchState[repo.path]) branchState[repo.path] = newBranchState()
  // go through the reactive proxy: the object returned by an assignment is the raw one, and mutating it would not update the page
  return loadBranchData(branchState[repo.path], repo.path, refresh, fetchBranches)
}

function toggleMerged(repo: Repo) {
  const st = branchState[repo.path]
  if (!st) return
  st.merged = !st.merged
  loadBranches(repo)
}

async function refresh() {
  if (loading.value) return
  loading.value = true
  try {
    data.value = await fetchWorkspace()
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
  // PRs load in the background; the worktree view never waits for them. This does NOT force a
  // lookup: the server serves its cache (5 min, kept on disk) and refreshes old data by itself, so
  // pressing Refresh costs git, not API calls. "Refresh PRs" in the Branches tab forces one.
  // Hidden projects are skipped: no point spending API calls on what is not shown.
  for (const repo of data.value?.repos ?? []) {
    if (!isHidden(repo)) loadBranches(repo, false)
  }
}

async function act(action: Action, body: { path?: string; sessionId?: string; mode?: ResumeWith }) {
  try {
    await runAction(action, body)
    toast.value = ''
  } catch (e) {
    toast.value = `${action} failed: ${e instanceof Error ? e.message : e}`
  }
}

// Everything of a repo grouped by ticket. PRs come from the branch graph, so the
// groups gain their PR info as soon as the second loading step arrives.
function ticketsOf(repo: Repo) {
  const graph = branchState[repo.path]?.data?.graph
  const fallbackDefault = repo.worktrees.some((w) => w.branch === 'master') ? 'master' : 'main'
  return buildTickets(repo.worktrees, graph?.nodes ?? [], graph?.default ?? fallbackDefault).filter((t) => matchTicket(t, query.value))
}

/** How many worktrees of the repo look removable (merged PR, nothing pending): a hint for the button. */
const removableCount = (repo: Repo) => repo.worktrees.filter((w) => mergedHint(w, prFor(repo, w.branch))).length

const prFor = (repo: Repo, branch?: string): Pr | undefined =>
  branch ? branchState[repo.path]?.data?.graph.nodes.find((n) => n.branch === branch && !n.isDefault)?.pr : undefined

const repos = computed(() =>
  (data.value?.repos ?? [])
    .filter((r) => !isHidden(r))
    .map((r) => ({
      ...r,
      all: r.worktrees,
      worktrees: sortWorktrees(
        r.worktrees.filter((w) => matchesFilter(w, r.name, query.value)),
        (branch) => prFor(r, branch),
        sortMode.value,
      ),
    }))
    .filter((r) => r.worktrees.length > 0 || tabOf(r) !== 'worktrees'),
)

function onKey(e: KeyboardEvent) {
  if (e.metaKey || e.ctrlKey || e.altKey) return
  const typing = document.activeElement instanceof HTMLInputElement
  if (e.key === '/' && !typing) {
    e.preventDefault()
    filterEl.value?.focus()
  } else if (e.key === 'Escape' && !planFor.value && !cleanupFor.value && !wtCleanupFor.value) {
    query.value = ''
    filterEl.value?.blur()
  } else if (e.key === 'r' && !typing && !planFor.value && !cleanupFor.value && !wtCleanupFor.value) {
    refresh()
  }
}

onMounted(() => {
  applyUiPx()
  refresh()
  window.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <header class="bar">
    <h1>workstation</h1>
    <input ref="filterEl" v-model="query" class="filter" placeholder="Filter worktrees   ( / )" spellcheck="false" />
    <span v-if="data" class="session-summary" aria-label="Claude sessions">
      <span v-if="sessionSummary.waiting" class="chip attention" title="Claude finished its turn: it is waiting for your next message">
        {{ sessionSummary.waiting }} waiting for you
      </span>
      <span v-if="sessionSummary.approval" class="chip attention" title="A tool request has had no result for a while (a guess)">
        {{ sessionSummary.approval }} may need approval
      </span>
      <span v-if="sessionSummary.working" class="chip working" title="Thinking or running a tool">{{ sessionSummary.working }} working</span>
      <span v-if="sessionSummary.failed" class="chip danger" title="The last thing recorded was an API error">{{ sessionSummary.failed }} failed</span>
    </span>
    <span v-if="data" class="muted stamp">updated {{ relativeTime(data.generatedAt) }}</span>
    <button
      data-resume-with
      :title="resumeWith === 'cli' ? 'Resume Claude opens a terminal running claude --resume. Click to use Claude Desktop' : 'Resume Claude opens the Claude Desktop app (sessions started in the CLI still use a terminal). Click to use the CLI'"
      @click="toggleResumeWith"
    >
      Resume: {{ resumeWith === 'cli' ? 'CLI' : 'Desktop' }}
    </button>
    <button :title="sortMode === 'activity' ? 'Sorted by what needs attention. Click to sort by name' : 'Sorted by name. Click to sort by activity'" @click="toggleSort">
      Sort: {{ sortMode }}
    </button>
    <span class="size-controls" role="group" aria-label="UI size">
      <button title="Smaller UI" @click="bumpUi(-1)">A−</button>
      <button title="Larger UI" @click="bumpUi(1)">A+</button>
    </span>
    <button class="primary" :disabled="loading" @click="refresh">{{ loading ? 'Refreshing…' : 'Refresh' }}</button>
  </header>

  <main>
    <p v-if="error" class="banner err">Could not load workspace: {{ error }}</p>
    <p v-for="w in data?.warnings ?? []" :key="w" class="banner warn">{{ w }}</p>
    <p v-if="toast" class="banner err" @click="toast = ''">{{ toast }}</p>
    <p v-if="notice" class="banner info" @click="notice = ''">{{ notice }}</p>
    <p v-if="!data && !error" class="muted">Loading…</p>

    <ActiveSessions :rows="activeRows" @resume="(id) => resumeSession(id)" @plan="(id, title) => (planFor = { id, title })" />

    <section v-for="repo in repos" :key="repo.path">
      <h2>
        {{ repo.name }}
        <span class="muted mono path"><bdi>{{ repo.path }}</bdi></span>
        <span class="tabs">
          <button :class="{ active: tabOf(repo) === 'worktrees' }" @click="setTab(repo, 'worktrees')">Worktrees</button>
          <button :class="{ active: tabOf(repo) === 'tickets' }" data-tab="tickets" @click="setTab(repo, 'tickets'); loadBranches(repo)">
            Tickets
          </button>
          <button :class="{ active: tabOf(repo) === 'branches' }" @click="setTab(repo, 'branches'); loadBranches(repo)">
            Branches
            <span v-if="branchState[repo.path]?.data" class="count">{{ branchState[repo.path].data!.graph.nodes.length }}</span>
          </button>
        </span>
        <button class="link hide-btn" title="Hide this project (you can show it again below)" @click="toggleRepo(repo)">Hide</button>
      </h2>

      <div v-if="tabOf(repo) === 'worktrees'" class="branch-actions">
        <span class="muted small-note">Ordered by what needs attention · folders and branches differ? the title is the PR or branch.</span>
        <button
          data-wt-cleanup
          title="Remove worktree folders whose PR was merged and that hold nothing else (you review the list first)"
          @click="wtCleanupFor = repo"
        >
          Clean up worktrees…<span v-if="removableCount(repo)" class="count"> ({{ removableCount(repo) }} merged)</span>
        </button>
      </div>
      <div v-if="tabOf(repo) === 'worktrees'" class="grid">
        <WorktreeCard
          v-for="wt in repo.worktrees"
          :key="wt.path"
          :worktree="wt"
          :editor="data!.capabilities.editor"
          :pr="prFor(repo, wt.branch)"
          @terminal="(p) => act('terminal', { path: p })"
          @editor="(p) => act('editor', { path: p })"
          @resume="(id) => resumeSession(id)"
          @plan="(id) => (planFor = { id, title: wt.name })"
          @sessions="(p) => openSessions(repo, p)"
        />
      </div>

      <div v-else-if="tabOf(repo) === 'tickets'" class="tickets-tab">
        <p v-if="branchState[repo.path]?.prsLoading" class="muted loading-line"><i class="spinner" /> Loading pull requests…</p>
        <p v-if="branchState[repo.path]?.prsError" class="banner warn">Could not load pull requests: {{ branchState[repo.path].prsError }}</p>
        <TicketList
          :tickets="ticketsOf(repo)"
          :editor="data!.capabilities.editor"
          @terminal="(p) => act('terminal', { path: p })"
          @editor="(p) => act('editor', { path: p })"
          @resume="(id) => resumeSession(id)"
          @plan="(id) => (planFor = { id, title: repo.name })"
          @sessions="(p) => openSessions(repo, p)"
        />
      </div>

      <div v-else class="branches">
        <p v-for="w in branchState[repo.path]?.data?.warnings ?? []" :key="w" class="banner warn">{{ w }}</p>
        <p v-if="branchState[repo.path]?.error" class="banner err">Could not load branches: {{ branchState[repo.path].error }}</p>
        <p v-if="branchState[repo.path]?.prsError" class="banner warn">
          Could not load pull requests (the graph is from git only): {{ branchState[repo.path].prsError }}
        </p>
        <p v-if="!branchState[repo.path]?.data && !branchState[repo.path]?.error" class="muted loading-line">
          <i class="spinner" /> Loading branches…
        </p>
        <template v-if="branchState[repo.path]?.data">
          <div class="branch-actions">
            <label class="toggle muted">
              <input type="checkbox" :checked="branchState[repo.path].merged" @change="toggleMerged(repo)" />
              Show recently merged (30 days)
            </label>
            <span v-if="branchState[repo.path].prsLoading" class="muted loading-line" data-prs-loading>
              <i class="spinner" />
              {{
                branchState[repo.path].data?.prsFetchedAt
                  ? `Refreshing pull requests (showing data from ${relativeTime(branchState[repo.path].data!.prsFetchedAt!)})…`
                  : 'Loading pull requests…'
              }}
            </span>
            <span v-else-if="branchState[repo.path].data?.prsFetchedAt" class="muted small-note" data-prs-age>
              Pull requests from {{ relativeTime(branchState[repo.path].data!.prsFetchedAt!) }}
            </span>
            <button
              data-refresh-prs
              :disabled="branchState[repo.path].loading"
              title="Read the pull requests from GitHub / Bitbucket again now (they are cached for 5 minutes)"
              @click="loadBranches(repo, true)"
            >
              ↻ PRs
            </button>
            <button data-cleanup title="Delete local branches whose PR was merged long ago (you review the list first)" @click="cleanupFor = repo">
              Clean up branches…
            </button>
          </div>
          <BranchGraph
            :graph="branchState[repo.path].data!.graph"
            :worktrees="repo.all"
            :editor="data!.capabilities.editor"
            :rem="uiPx"
            :prs-loading="branchState[repo.path].prsLoading"
            @terminal="(p) => act('terminal', { path: p })"
            @editor="(p) => act('editor', { path: p })"
            @resume="(id) => resumeSession(id)"
            @plan="(id) => (planFor = { id, title: repo.name })"
          />
        </template>
      </div>
    </section>

    <p v-if="data && !repos.length" class="muted">
      {{
        query
          ? 'No worktrees match the filter.'
          : hiddenRepos.length
            ? 'All projects are hidden. Show them again from the list below.'
            : 'No repositories found. Add some to ~/.workstation.json.'
      }}
    </p>

    <details v-if="hiddenRepos.length" class="unlinked hidden-repos">
      <summary>Hidden projects ({{ hiddenRepos.length }})</summary>
      <ul>
        <li v-for="r in hiddenRepos" :key="r.path">
          <span class="clip">{{ r.name }}</span>
          <span class="muted mono clip" :title="r.path"><bdi>{{ r.path }}</bdi></span>
          <button class="link" @click="toggleRepo(r)">Show</button>
        </li>
      </ul>
    </details>

    <details v-if="data?.unlinked.length" class="unlinked">
      <summary>Unlinked sessions ({{ data.unlinked.length }})</summary>
      <p class="muted">Claude sessions that could not be matched to a Git worktree.</p>
      <ul>
        <li v-for="u in data.unlinked.slice(0, UNLINKED_LIMIT)" :key="u.session.id">
          <span class="mini" :class="`status-${u.session.status}`">{{ statusLabel(u.session.status) }}</span>
          <span class="clip">{{ u.session.title || u.session.lastMessage || u.session.id }}</span>
          <span class="muted mono clip" :title="u.session.cwd"><bdi>{{ u.session.cwd }}</bdi></span>
          <span class="muted">{{ u.reason }}</span>
          <span class="muted">{{ relativeTime(u.session.lastActivity) }}</span>
          <button v-if="u.session.hasPlan" class="link" @click="planFor = { id: u.session.id, title: u.session.title || u.session.id }">Plan</button>
          <button v-if="u.session.resumable" class="link" @click="resumeSession(u.session.id)">Resume</button>
        </li>
      </ul>
      <p v-if="data.unlinked.length > UNLINKED_LIMIT" class="muted">
        Showing the {{ UNLINKED_LIMIT }} most recent of {{ data.unlinked.length }}.
      </p>
    </details>
  </main>

  <SessionsModal
    v-if="sessionsFor"
    :sessions="sessionsFor.sessions"
    :title="sessionsFor.branch || sessionsFor.name"
    @close="sessionsFor = null"
    @resume="(id) => resumeSession(id)"
    @plan="(id) => (planFor = { id, title: sessionsFor?.branch || sessionsFor?.name || '' })"
  />
  <PlanModal v-if="planFor" :session-id="planFor.id" :title="planFor.title" @close="planFor = null" />
  <WorktreeCleanupModal
    v-if="wtCleanupFor"
    :repo="wtCleanupFor.path"
    :repo-name="wtCleanupFor.name"
    @close="wtCleanupFor = null"
    @removed="refresh()"
  />
  <CleanupModal
    v-if="cleanupFor"
    :repo="cleanupFor.path"
    :repo-name="cleanupFor.name"
    @close="cleanupFor = null"
    @deleted="loadBranches(cleanupFor!, true)"
  />
</template>
