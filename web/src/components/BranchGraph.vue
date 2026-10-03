<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { edgePath, layoutTree } from '../layout'
import { isStale, matchNode, prTone, relativeTime, statusLabel } from '../format'
import { isFresh } from '../worktrees'
import { ZOOM_STEP, clampZoom, fitZoom } from '../zoom'
import type { BranchNode, Worktree } from '../types'
import PrChip from './PrChip.vue'

const props = withDefaults(
  defineProps<{
    graph: { default: string; nodes: BranchNode[]; hidden: number }
    worktrees: Worktree[]
    editor: string
    now?: Date
    rem?: number // root font size in px; node geometry follows it
    prsLoading?: boolean // pull requests are still loading: "no PR" is not known yet
  }>(),
  { now: () => new Date(), rem: undefined, prsLoading: false },
)
const emit = defineEmits<{
  terminal: [path: string]
  editor: [path: string]
  resume: [sessionId: string]
  plan: [sessionId: string]
}>()

// Node geometry is in rem so it follows the UI size.
const remPx = computed(() => props.rem ?? (parseFloat(getComputedStyle(document.documentElement).fontSize) || 16))
const nodeW = computed(() => 19.5 * remPx.value)
const nodeH = computed(() => 6.2 * remPx.value)
const dims = computed(() => ({ nodeW: nodeW.value, nodeH: nodeH.value, gapX: 3.5 * remPx.value, gapY: remPx.value }))

const layout = computed(() => layoutTree(props.graph.nodes, dims.value))
const byBranch = computed(() => new Map(props.graph.nodes.map((n) => [n.branch, n])))
const selected = ref('')

// --- zoom -------------------------------------------------------------------
const zoom = ref(1)
const scrollEl = ref<HTMLElement | null>(null)
const zoomPct = computed(() => Math.round(zoom.value * 100))
const zoomBy = (delta: number) => (zoom.value = clampZoom(zoom.value + delta))

function fit() {
  const available = (scrollEl.value?.clientWidth ?? 0) - 2 * remPx.value
  zoom.value = fitZoom(available, layout.value.width)
}

function onWheel(e: WheelEvent) {
  if (!e.ctrlKey) return // a plain wheel scrolls the graph
  e.preventDefault()
  zoomBy(e.deltaY < 0 ? ZOOM_STEP : -ZOOM_STEP)
}

// --- search -----------------------------------------------------------------
const query = ref('')
const cursor = ref(-1)
const searching = computed(() => query.value.trim() !== '')
const matches = computed(() => (searching.value ? props.graph.nodes.filter((n) => matchNode(n, query.value)).map((n) => n.branch) : []))
const countLabel = computed(() =>
  matches.value.length === 0 ? 'no matches' : `${matches.value.length} match${matches.value.length === 1 ? '' : 'es'}`,
)
watch(query, () => (cursor.value = -1))

function clearSearch() {
  query.value = ''
  cursor.value = -1
}

function nextMatch() {
  if (matches.value.length === 0) return
  cursor.value = (cursor.value + 1) % matches.value.length
  const branch = matches.value[cursor.value]
  selected.value = branch
  focusNode(branch)
}

/** Scrolls so the node is centred in the viewport. */
function focusNode(branch: string) {
  const el = scrollEl.value
  const p = layout.value.positions.get(branch)
  if (!el || !p || typeof el.scrollTo !== 'function') return
  el.scrollTo({
    left: (p.x + nodeW.value / 2) * zoom.value - el.clientWidth / 2,
    top: (p.y + nodeH.value / 2) * zoom.value - el.clientHeight / 2,
    behavior: 'smooth',
  })
}

// --- nodes ------------------------------------------------------------------
function worktreeOf(n: BranchNode): Worktree | undefined {
  return n.worktree ? props.worktrees.find((w) => w.path === n.worktree!.path) : undefined
}
const stateOf = (n: BranchNode) => worktreeOf(n)?.sessions[0]?.status ?? 'none'
const stale = (n: BranchNode) => !n.isDefault && !n.worktree && !n.pr && isStale(n.date, props.now)
const fresh = (n: BranchNode) => !n.isDefault && isFresh(n.date, props.now) // a commit in the last 3 days
const pos = (branch: string) => layout.value.positions.get(branch)!
const ago = (iso: string) => relativeTime(iso, props.now)

const edges = computed(() =>
  layout.value.edges.map((e) => ({
    key: `${e.from}>${e.to}`,
    d: edgePath(pos(e.from), pos(e.to), nodeW.value, nodeH.value),
    inferred: byBranch.value.get(e.to)?.via !== 'pr',
  })),
)

const detail = computed(() => {
  const n = byBranch.value.get(selected.value)
  if (!n) return null
  const wt = worktreeOf(n)
  return {
    n,
    wt,
    resumable: wt?.sessions.find((s) => s.resumable),
    planned: wt?.sessions.find((s) => s.hasPlan),
    basis: n.via === 'pr' ? 'PR base' : 'inferred from git history',
  }
})

function toggle(branch: string) {
  selected.value = selected.value === branch ? '' : branch
}
</script>

<template>
  <div class="branch-graph">
    <div class="toolbar">
      <div class="search">
        <input
          v-model="query"
          data-search
          type="search"
          placeholder="Search branches, PRs, worktrees…"
          spellcheck="false"
          @keydown.enter.prevent="nextMatch"
          @keydown.esc="clearSearch"
        />
        <span v-if="searching" data-search-count class="muted">{{ countLabel }}</span>
        <button v-if="query" data-search-clear title="Clear search (Esc)" @click="clearSearch">✕</button>
      </div>
      <div class="zoom">
        <button data-zoom="out" title="Zoom out (Ctrl + wheel)" @click="zoomBy(-ZOOM_STEP)">−</button>
        <span data-zoom="label" class="zoom-label">{{ zoomPct }}%</span>
        <button data-zoom="in" title="Zoom in (Ctrl + wheel)" @click="zoomBy(ZOOM_STEP)">+</button>
        <button data-zoom="fit" title="Fit the whole graph in view" @click="fit">Fit</button>
        <button data-zoom="reset" title="Back to 100%" @click="zoom = 1">100%</button>
      </div>
    </div>

    <div class="legend muted">
      <span><i class="swatch solid" /> PR base</span>
      <span><i class="swatch dashed" /> inferred from git history</span>
      <span>faded: merged or stale (&gt; 30 days)</span>
      <span><i class="swatch box solid-box" /> with PR (title, branch under it)</span>
      <span><i class="swatch box dashed-box" /> no PR (branch name)</span>
      <span><span class="new-badge">new</span> commit in the last 3 days</span>
      <span v-if="graph.hidden > 0">{{ graph.hidden }} more branches hidden</span>
    </div>

    <div ref="scrollEl" class="graph-scroll" @wheel="onWheel">
      <div class="graph-zoom" :style="{ width: `${layout.width * zoom}px`, height: `${layout.height * zoom}px` }">
        <div
          class="graph"
          :style="{ width: `${layout.width}px`, height: `${layout.height}px`, transform: `scale(${zoom})`, transformOrigin: '0 0' }"
        >
          <svg class="edges" :width="layout.width" :height="layout.height" aria-hidden="true">
            <path v-for="e in edges" :key="e.key" :d="e.d" class="edge" :class="{ inferred: e.inferred }" />
          </svg>

          <div
            v-for="n in graph.nodes"
            :key="n.branch"
            class="gnode"
            :class="[
              `status-${stateOf(n)}`,
              {
                merged: n.merged,
                stale: stale(n),
                selected: selected === n.branch,
                root: n.isDefault,
                fresh: fresh(n),
                'has-pr': !!n.pr && !n.isDefault,
                'no-pr': !n.pr && !n.isDefault,
                [`pr-${n.pr ? prTone(n.pr) : 'none'}`]: !!n.pr && !n.isDefault,
                'pr-loading': prsLoading && !n.pr && !n.isDefault,
                match: searching && matches.includes(n.branch),
                dim: searching && !matches.includes(n.branch),
              },
            ]"
            :data-branch="n.branch"
            :style="{ left: `${pos(n.branch).x}px`, top: `${pos(n.branch).y}px`, width: `${nodeW}px`, height: `${nodeH}px` }"
            role="button"
            tabindex="0"
            @click="toggle(n.branch)"
            @keydown.enter.prevent="toggle(n.branch)"
          >
            <!-- a PR node reads "PR title / branch / status"; a branch without PR reads "branch / no PR" -->
            <div class="line">
              <span class="dot" />
              <strong class="title clip" :class="{ mono: !n.pr }" :title="n.pr ? n.pr.title : n.branch">{{ n.pr ? n.pr.title : n.branch }}</strong>
              <span v-if="n.isDefault" class="badge">default</span>
              <span v-if="fresh(n)" class="new-badge" title="Commit in the last 3 days">new</span>
            </div>
            <div v-if="n.pr" class="line small">
              <span class="branch-name mono clip" :title="n.branch">{{ n.branch }}</span>
              <span class="muted">· {{ ago(n.date) }}</span>
            </div>
            <div v-else-if="!n.isDefault" class="line small muted">
              <span>{{ prsLoading ? 'PR…' : 'no PR' }}</span>
              <span>· {{ ago(n.date) }}</span>
            </div>
            <div v-if="!n.isDefault" class="line small">
              <PrChip v-if="n.pr" :pr="n.pr" />
              <span class="mono muted">↑{{ n.ahead }} ↓{{ n.behind }}</span>
              <span v-if="n.worktree" class="muted clip">· {{ n.worktree.name }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <section v-if="detail" class="detail">
      <h3 class="mono">{{ detail.n.branch }}</h3>
      <p v-if="detail.n.parent" class="muted">
        Based on <strong class="mono">{{ detail.n.parent }}</strong> ({{ detail.basis }}) · ↑{{ detail.n.ahead }} ↓{{ detail.n.behind }}
        vs {{ graph.default }} · last commit {{ ago(detail.n.date) }}
      </p>
      <p v-if="detail.n.pr">
        <PrChip :pr="detail.n.pr" /> {{ detail.n.pr.title }}
      </p>
      <p v-if="detail.wt" class="muted mono">{{ detail.wt.path }}</p>
      <p v-if="detail.wt?.sessions[0]" class="muted">
        Claude: {{ statusLabel(detail.wt.sessions[0].status) }} · {{ ago(detail.wt.sessions[0].lastActivity) }}
      </p>
      <div v-if="detail.wt" class="actions">
        <button data-action="terminal" @click="emit('terminal', detail.wt!.path)">Terminal</button>
        <button v-if="editor" data-action="editor" @click="emit('editor', detail.wt!.path)">
          {{ editor === 'cursor' ? 'Cursor' : 'VS Code' }}
        </button>
        <button v-if="detail.resumable" data-action="resume" @click="emit('resume', detail.resumable!.id)">Resume Claude</button>
        <button v-if="detail.planned" data-action="plan" @click="emit('plan', detail.planned!.id)">Plan</button>
      </div>
    </section>
  </div>
</template>
