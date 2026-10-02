<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { fetchBranches, fetchWorkspace, runAction, type Action } from './api'
import BranchGraph from './components/BranchGraph.vue'
import PlanModal from './components/PlanModal.vue'
import WorktreeCard from './components/WorktreeCard.vue'
import { matchesFilter, relativeTime, statusLabel } from './format'
import type { BranchesResponse, Pr, Repo, WorkspaceData } from './types'

type Tab = 'worktrees' | 'branches'
interface BranchState {
  loading: boolean
  data?: BranchesResponse
  error?: string
  merged: boolean
}

const TAB_KEY = 'workstation.tabs'
const UNLINKED_LIMIT = 50

const data = ref<WorkspaceData | null>(null)
const error = ref('')
const toast = ref('')
const loading = ref(false)
const query = ref('')
const filterEl = ref<HTMLInputElement | null>(null)
const planFor = ref<{ id: string; title: string } | null>(null)
const tabs = reactive<Record<string, Tab>>(loadTabs())
const branchState = reactive<Record<string, BranchState>>({})

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

async function loadBranches(repo: Repo, refresh = false) {
  const st = (branchState[repo.path] ??= { loading: false, merged: false })
  if (st.loading) return
  st.loading = true
  try {
    st.data = await fetchBranches(repo.path, { merged: st.merged, refresh })
    st.error = ''
  } catch (e) {
    st.error = e instanceof Error ? e.message : String(e)
  } finally {
    st.loading = false
  }
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
  const hadBranches = Object.keys(branchState)
  try {
    data.value = await fetchWorkspace()
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
  // PRs load in the background (network); the worktree view never waits for them
  for (const repo of data.value?.repos ?? []) loadBranches(repo, hadBranches.includes(repo.path))
}

async function act(action: Action, body: { path?: string; sessionId?: string }) {
  try {
    await runAction(action, body)
    toast.value = ''
  } catch (e) {
    toast.value = `${action} failed: ${e instanceof Error ? e.message : e}`
  }
}

const prFor = (repo: Repo, branch?: string): Pr | undefined =>
  branch ? branchState[repo.path]?.data?.graph.nodes.find((n) => n.branch === branch && !n.isDefault)?.pr : undefined

const repos = computed(() =>
  (data.value?.repos ?? [])
    .map((r) => ({ ...r, all: r.worktrees, worktrees: r.worktrees.filter((w) => matchesFilter(w, r.name, query.value)) }))
    .filter((r) => r.worktrees.length > 0 || tabOf(r) === 'branches'),
)

function onKey(e: KeyboardEvent) {
  if (e.metaKey || e.ctrlKey || e.altKey) return
  const typing = document.activeElement instanceof HTMLInputElement
  if (e.key === '/' && !typing) {
    e.preventDefault()
    filterEl.value?.focus()
  } else if (e.key === 'Escape' && !planFor.value) {
    query.value = ''
    filterEl.value?.blur()
  } else if (e.key === 'r' && !typing && !planFor.value) {
    refresh()
  }
}

onMounted(() => {
  refresh()
  window.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <header class="bar">
    <h1>workstation</h1>
    <input ref="filterEl" v-model="query" class="filter" placeholder="Filter worktrees   ( / )" spellcheck="false" />
    <span v-if="data" class="muted stamp">updated {{ relativeTime(data.generatedAt) }}</span>
    <button class="primary" :disabled="loading" @click="refresh">{{ loading ? 'Refreshing…' : 'Refresh' }}</button>
  </header>

  <main>
    <p v-if="error" class="banner err">Could not load workspace: {{ error }}</p>
    <p v-for="w in data?.warnings ?? []" :key="w" class="banner warn">{{ w }}</p>
    <p v-if="toast" class="banner err" @click="toast = ''">{{ toast }}</p>
    <p v-if="!data && !error" class="muted">Loading…</p>

    <section v-for="repo in repos" :key="repo.path">
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
      </h2>

      <div v-if="tabOf(repo) === 'worktrees'" class="grid">
        <WorktreeCard
          v-for="wt in repo.worktrees"
          :key="wt.path"
          :worktree="wt"
          :editor="data!.capabilities.editor"
          :pr="prFor(repo, wt.branch)"
          @terminal="(p) => act('terminal', { path: p })"
          @editor="(p) => act('editor', { path: p })"
          @resume="(id) => act('resume', { sessionId: id })"
          @plan="(id) => (planFor = { id, title: wt.name })"
        />
      </div>

      <div v-else class="branches">
        <p v-for="w in branchState[repo.path]?.data?.warnings ?? []" :key="w" class="banner warn">{{ w }}</p>
        <p v-if="branchState[repo.path]?.error" class="banner err">Could not load branches: {{ branchState[repo.path].error }}</p>
        <p v-if="!branchState[repo.path]?.data && !branchState[repo.path]?.error" class="muted">Loading branches and pull requests…</p>
        <template v-if="branchState[repo.path]?.data">
          <label class="toggle muted">
            <input type="checkbox" :checked="branchState[repo.path].merged" @change="toggleMerged(repo)" />
            Show recently merged (30 days)
          </label>
          <BranchGraph
            :graph="branchState[repo.path].data!.graph"
            :worktrees="repo.all"
            :editor="data!.capabilities.editor"
            @terminal="(p) => act('terminal', { path: p })"
            @editor="(p) => act('editor', { path: p })"
            @resume="(id) => act('resume', { sessionId: id })"
            @plan="(id) => (planFor = { id, title: repo.name })"
          />
        </template>
      </div>
    </section>

    <p v-if="data && !repos.length" class="muted">
      {{ query ? 'No worktrees match the filter.' : 'No repositories found. Add some to ~/.workstation.json.' }}
    </p>

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
          <button v-if="u.session.resumable" class="link" @click="act('resume', { sessionId: u.session.id })">Resume</button>
        </li>
      </ul>
      <p v-if="data.unlinked.length > UNLINKED_LIMIT" class="muted">
        Showing the {{ UNLINKED_LIMIT }} most recent of {{ data.unlinked.length }}.
      </p>
    </details>
  </main>

  <PlanModal v-if="planFor" :session-id="planFor.id" :title="planFor.title" @close="planFor = null" />
</template>
