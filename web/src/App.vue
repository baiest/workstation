<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watchEffect } from 'vue'
import {
  addDiscovery, dismissSession, runAction, saveTicket, triageDiscovery,
  type Action, type TriageAction,
} from './api'
import BranchGraph from './components/BranchGraph.vue'
import CleanupModal from './components/CleanupModal.vue'
import PlanModal from './components/PlanModal.vue'
import SessionsModal from './components/SessionsModal.vue'
import StageBoard from './components/StageBoard.vue'
import TidyBar from './components/TidyBar.vue'
import TicketBoard from './components/TicketBoard.vue'
import DependencyGraph from './components/DependencyGraph.vue'
import WorktreeCleanupModal from './components/WorktreeCleanupModal.vue'
import ActiveSessions from './components/ActiveSessions.vue'
import { activeSessions } from './active'
import { matchesFilter, relativeTime, statusLabel } from './format'
import { resumeDecision, type ResumeWith } from './resume'
import { buildTickets, matchTicket, type Ticket } from './tickets'
import { tabTitle } from './notifier'
import { needsYouCount } from './notify'
import { tidyCounts } from './tidy'
import { mergedHint, sortWorktrees, summarizeSessions } from './worktrees'
import { browserStorage, loadHidden, saveHidden, toggleHidden } from './hidden'
import type { Pr, Repo, TicketState, Worktree } from './types'
import { FONT_DEFAULT, clampFontSize, nextFontSize } from './zoom'
import { oneOf, pref, readPref, writePref } from './prefs'
import { useWorkspace } from './useWorkspace'

type Tab = 'worktrees' | 'branches'
type View = 'tickets' | 'deps' | 'repos'

const TAB_KEY = 'workstation.tabs'
const UNLINKED_LIMIT = 50

const toast = ref('')
const notice = ref('')
const query = ref('')
const filterEl = ref<HTMLInputElement | null>(null)
const planFor = ref<{ id: string; title: string } | null>(null)
const cleanupFor = ref<Repo | null>(null)
const wtCleanupFor = ref<Repo | null>(null)
const sessionsFor = ref<Worktree | null>(null) // the worktree whose full session list is open

// Which page is the main one: the tickets (default) or the repos with their worktrees and branches.
const viewPref = pref<View>('workstation.view', 'tickets', oneOf<View>('tickets', 'deps', 'repos'))
const view = viewPref.value

// Projects the user hid, remembered per browser.
const store = browserStorage()
const hidden = ref<string[]>(loadHidden(store))
const isHidden = (r: Repo) => hidden.value.includes(r.path)

// Browser notifications when a session starts waiting for you. Off until asked; remembered per browser.
const NOTIFY_KEY = 'workstation.notify'
const notifyOn = ref(
  readPref(NOTIFY_KEY, false, (raw) => raw === '1') && typeof Notification !== 'undefined' && Notification.permission === 'granted',
)
async function toggleNotify() {
  if (typeof Notification === 'undefined') {
    toast.value = 'this browser has no notifications'
    return
  }
  if (!notifyOn.value && Notification.permission !== 'granted') {
    if ((await Notification.requestPermission()) !== 'granted') {
      toast.value = 'notifications are blocked: allow them for this site in the browser'
      return
    }
  }
  notifyOn.value = !notifyOn.value
  writePref(NOTIFY_KEY, notifyOn.value ? '1' : '0')
}

const { data, store: ticketStore, error, loading, branchState, branchTidyOf, loadBranches, loadTickets, refresh } = useWorkspace({ isHidden, notifyOn })

function openSessions(repo: Repo, path: string) {
  sessionsFor.value = repo.worktrees.find((w) => w.path === path) ?? null
}
function openSessionsByPath(path: string) {
  sessionsFor.value = (data.value?.repos ?? []).flatMap((r) => r.worktrees).find((w) => w.path === path) ?? null
}
const tabs = reactive<Record<string, Tab>>(readPref(TAB_KEY, {}, (raw) => JSON.parse(raw)))

// "Resume Claude" in a terminal (CLI) or in the Claude Desktop app. Remembered per browser.
const RESUME_KEY = 'workstation.resume-with'
const resumeWithPref = pref<ResumeWith>(RESUME_KEY, 'cli', oneOf<ResumeWith>('cli', 'desktop'))
const resumeWith = resumeWithPref.value

function toggleResumeWith() {
  resumeWithPref.set(resumeWith.value === 'cli' ? 'desktop' : 'cli')
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
const sortPref = pref<SortMode>(SORT_KEY, 'activity', oneOf<SortMode>('activity', 'name'))
const sortMode = sortPref.value

function toggleSort() {
  sortPref.set(sortMode.value === 'activity' ? 'name' : 'activity')
}

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
const uiPref = pref<number>(UI_KEY, FONT_DEFAULT, (raw) => clampFontSize(parseInt(raw, 10)))
const uiPx = uiPref.value

function applyUiPx() {
  document.documentElement.style.fontSize = `${uiPx.value}px`
}

function bumpUi(dir: 1 | -1) {
  uiPref.set(nextFontSize(uiPx.value, dir))
  applyUiPx()
}

function setTab(repo: Repo, tab: Tab) {
  tabs[repo.path] = tab
  writePref(TAB_KEY, JSON.stringify(tabs))
}

const tabOf = (repo: Repo): Tab => (tabs[repo.path] === 'branches' ? 'branches' : 'worktrees')

function toggleMerged(repo: Repo) {
  const st = branchState[repo.path]
  if (!st) return
  st.merged = !st.merged
  loadBranches(repo)
}

async function act(action: Action, body: { path?: string; sessionId?: string; mode?: ResumeWith; repo?: string; ref?: string }) {
  try {
    await runAction(action, body)
    toast.value = ''
  } catch (e) {
    toast.value = `${action} failed: ${e instanceof Error ? e.message : e}`
  }
}

// The ticket store lives on the server; every change is saved there and the board re-reads it.
async function saved(what: string, fn: () => Promise<unknown>) {
  try {
    await fn()
    toast.value = ''
  } catch (e) {
    toast.value = `${what}: ${e instanceof Error ? e.message : e}`
  }
  await loadTickets()
}
const target = (t: Ticket) => ({ repo: t.repo, ref: t.ref })
const setState = (t: Ticket, state: TicketState, blockedReason?: string) =>
  saved('could not save', () => saveTicket(target(t), { state, ...(blockedReason !== undefined ? { blockedReason } : {}) }))
const setNextStep = (t: Ticket, text: string) => saved('could not save', () => saveTicket(target(t), { nextStep: text }))
const setDependencies = (t: Ticket, ids: string[]) => saved('could not save', () => saveTicket(target(t), { blockedBy: ids }))
const noteDiscovery = (t: Ticket, text: string) => saved('could not save', () => addDiscovery(target(t), text))
const triage = (t: Ticket, discoveryId: string, action: TriageAction) => saved('could not save', () => triageDiscovery(t.id, discoveryId, action))
const dismiss = (sessionId: string) => saved('could not dismiss', () => dismissSession(sessionId, true))

// Starting Claude on a ticket makes it active: ask first when that would go past the limit.
async function startTicket(t: Ticket) {
  const limit = ticketStore.value?.config.wipLimit ?? 3
  const active = allTickets.value.filter((x) => x.state === 'active').length
  if (t.state !== 'active' && active >= limit && !window.confirm(`You already have ${active} active tickets (limit ${limit}). Start this one anyway?`)) return
  notice.value = ''
  await act('start', { repo: t.repo, ref: t.ref })
  if (!toast.value) notice.value = `Claude opened in a terminal on ${t.title}`
  await Promise.all([refresh(true), loadTickets()]) // it may have made a worktree and the ticket is active now
}

function reviewTicket(t: Ticket) {
  const wt = t.items.find((i) => i.worktree && !i.worktree.isMain)?.worktree
  if (wt) act('review', { path: wt.path })
}

const visibleRepos = computed(() => (data.value?.repos ?? []).filter((r) => !isHidden(r)))
const repoNames = computed(() => Object.fromEntries(visibleRepos.value.map((r) => [r.path, r.name])))

// Everything grouped by ticket, across projects. The graph brings the PRs and the branches
// without a worktree; until it has loaded, the PRs the server attached to each worktree are used.
const allTickets = computed(() =>
  visibleRepos.value
    .flatMap((repo) => {
      const graph = branchState[repo.path]?.data?.graph
      const fallbackDefault = repo.worktrees.some((w) => w.branch === 'master') ? 'master' : 'main'
      return buildTickets(repo.worktrees, graph?.nodes ?? [], graph?.default ?? fallbackDefault, { repo: repo.path, stored: ticketStore.value?.tickets })
    })
    .filter((t) => matchTicket(t, query.value)),
)

/** How many worktrees of the repo look removable (merged PR, nothing pending): a hint for the button. */
const removableCount = (repo: Repo) => repo.worktrees.filter((w) => mergedHint(w, prFor(repo, w.branch))).length

// The branch graph is the freshest source once it has loaded; until then the server's cache rides on the worktree.
const prFor = (repo: Repo, branch?: string): Pr | undefined =>
  branch
    ? (branchState[repo.path]?.data?.graph.nodes.find((n) => n.branch === branch && !n.isDefault)?.pr ??
      repo.worktrees.find((w) => w.branch === branch && !w.isMain)?.pr)
    : undefined

const repos = computed(() =>
  visibleRepos.value
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
  const typing = document.activeElement instanceof HTMLInputElement || document.activeElement instanceof HTMLTextAreaElement
  const modal = planFor.value || cleanupFor.value || wtCleanupFor.value
  if (e.key === '/' && !typing) {
    e.preventDefault()
    filterEl.value?.focus()
  } else if (e.key === 'Escape' && !modal && document.activeElement === filterEl.value) {
    query.value = ''
    filterEl.value?.blur()
  } else if (e.key === 'r' && !typing && !modal) {
    refresh()
  }
}

const POLL_MS = 30_000
let timer: ReturnType<typeof setInterval> | undefined
watchEffect(() => {
  document.title = tabTitle(data.value ? needsYouCount(data.value.repos, data.value.unlinked) : 0)
})

onMounted(() => {
  applyUiPx()
  refresh()
  timer = setInterval(() => refresh(true), POLL_MS)
  window.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  clearInterval(timer)
})
</script>

<template>
  <header class="bar">
    <h1>workstation</h1>
    <span class="views" role="group" aria-label="View">
      <button data-view="tickets" :class="{ active: view === 'tickets' }" @click="viewPref.set('tickets')">Tickets</button>
      <button data-view="deps" :class="{ active: view === 'deps' }" title="Which ticket waits for which" @click="viewPref.set('deps')">Dependencies</button>
      <button data-view="repos" :class="{ active: view === 'repos' }" @click="viewPref.set('repos')">Repos</button>
    </span>
    <input ref="filterEl" v-model="query" class="filter" :placeholder="view === 'repos' ? 'Filter worktrees   ( / )' : 'Filter tickets   ( / )'" spellcheck="false" />
    <span v-if="data" class="session-summary" aria-label="Claude sessions">
      <span v-if="sessionSummary.waiting" class="chip attention" title="Claude finished its turn: it is waiting for your next message">
        {{ sessionSummary.waiting }} waiting for you
      </span>
      <span v-if="sessionSummary.approval" class="chip attention" title="A tool request has had no result for a while (a guess)">
        {{ sessionSummary.approval }} may need approval
      </span>
      <span v-if="sessionSummary.stale" class="chip" title="Waiting for hours: probably finished with. Close the claude process once you have what you need">{{ sessionSummary.stale }} stale</span>
      <span v-if="sessionSummary.working" class="chip working" title="Thinking or running a tool">{{ sessionSummary.working }} working</span>
      <span v-if="sessionSummary.failed" class="chip danger" title="The last thing recorded was an API error">{{ sessionSummary.failed }} failed</span>
    </span>
    <span v-if="data" class="muted stamp">updated {{ relativeTime(data.generatedAt) }}</span>
    <button
      data-notify
      :class="{ active: notifyOn }"
      :title="notifyOn ? 'You get a notification when Claude waits for you while this tab is in the background. Click to turn off' : 'Get a notification when Claude waits for you while this tab is in the background'"
      @click="toggleNotify"
    >
      Notify: {{ notifyOn ? 'on' : 'off' }}
    </button>
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
    <button class="primary" :disabled="loading" @click="refresh()">{{ loading ? 'Refreshing…' : 'Refresh' }}</button>
  </header>

  <main>
    <p v-if="error" class="banner err">Could not load workspace: {{ error }}</p>
    <p v-for="w in data?.warnings ?? []" :key="w" class="banner warn">{{ w }}</p>
    <p v-if="toast" class="banner err" @click="toast = ''">{{ toast }}</p>
    <p v-if="notice" class="banner info" @click="notice = ''">{{ notice }}</p>
    <p v-if="!data && !error" class="muted">Loading…</p>

    <ActiveSessions
      :rows="activeRows"
      @resume="(id) => resumeSession(id)"
      @plan="(id, title) => (planFor = { id, title })"
      @dismiss="(id) => dismiss(id)"
    />

    <DependencyGraph v-if="view === 'deps' && data" :tickets="allTickets" :repo-names="repoNames" />

    <TicketBoard
      v-if="view === 'tickets' && data"
      :tickets="allTickets"
      :editor="data?.capabilities.editor ?? ''"
      :wip-limit="ticketStore?.config.wipLimit ?? 3"
      :repo-names="repoNames"
      @state="(t, s, reason) => setState(t, s, reason)"
      @next="(t, text) => setNextStep(t, text)"
      @discover="(t, text) => noteDiscovery(t, text)"
      @triage="(t, id, action) => triage(t, id, action)"
      @terminal="(p) => act('terminal', { path: p })"
      @editor="(p) => act('editor', { path: p })"
      @resume="(id) => resumeSession(id)"
      @plan="(id, title) => (planFor = { id, title })"
      @sessions="(p) => openSessionsByPath(p)"
      @depend="(t, ids) => setDependencies(t, ids)"
      @start="(t) => startTicket(t)"
      @review="(t) => reviewTicket(t)"
    />

    <section v-for="repo in view === 'repos' ? repos : []" :key="repo.path">
      <h2>
        {{ repo.name }}
        <span class="muted mono path"><bdi>{{ repo.path }}</bdi></span>
        <span class="tabs">
          <button :class="{ active: tabOf(repo) === 'worktrees' }" @click="setTab(repo, 'worktrees')">Worktrees</button>
          <button :class="{ active: tabOf(repo) === 'branches' }" @click="setTab(repo, 'branches'); loadBranches(repo)">
            Branches
            <span v-if="branchState[repo.path]?.data" class="count">{{ branchState[repo.path].data!.graph.nodes.length }}</span>
          </button>
        </span>
        <button class="link hide-btn" title="Hide this project (you can show it again below)" @click="toggleRepo(repo)">Hide</button>
      </h2>

      <TidyBar
        v-if="tabOf(repo) === 'worktrees'"
        :counts="tidyCounts(repo.all, (b) => prFor(repo, b))"
        :branches="branchTidyOf[repo.path]"
        @worktrees="wtCleanupFor = repo"
        @branches="cleanupFor = repo"
      />
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
      <StageBoard
        v-if="tabOf(repo) === 'worktrees'"
        :worktrees="repo.worktrees"
        :editor="data!.capabilities.editor"
        :pr-for="(branch) => prFor(repo, branch)"
        @terminal="(p) => act('terminal', { path: p })"
        @editor="(p) => act('editor', { path: p })"
        @resume="(id) => resumeSession(id)"
        @plan="(id, title) => (planFor = { id, title })"
        @sessions="(p) => openSessions(repo, p)"
      />

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

    <p v-if="data && view === 'repos' && !repos.length" class="muted">
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

    <details v-if="view === 'repos' && data?.unlinked.length" class="unlinked">
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
