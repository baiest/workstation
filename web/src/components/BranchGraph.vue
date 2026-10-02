<script setup lang="ts">
import { computed, ref } from 'vue'
import { edgePath, layoutTree } from '../layout'
import { isStale, relativeTime, statusLabel } from '../format'
import type { BranchNode, Worktree } from '../types'
import PrChip from './PrChip.vue'

const props = withDefaults(
  defineProps<{
    graph: { default: string; nodes: BranchNode[]; hidden: number }
    worktrees: Worktree[]
    editor: string
    now?: Date
  }>(),
  { now: () => new Date() },
)
const emit = defineEmits<{
  terminal: [path: string]
  editor: [path: string]
  resume: [sessionId: string]
  plan: [sessionId: string]
}>()

// Node geometry is in rem so it follows the root font size in style.css.
const rem = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16
const NODE_W = 19.5 * rem
const NODE_H = 6.2 * rem
const dims = { nodeW: NODE_W, nodeH: NODE_H, gapX: 3.5 * rem, gapY: 1 * rem }

const layout = computed(() => layoutTree(props.graph.nodes, dims))
const byBranch = computed(() => new Map(props.graph.nodes.map((n) => [n.branch, n])))
const selected = ref('')

function worktreeOf(n: BranchNode): Worktree | undefined {
  return n.worktree ? props.worktrees.find((w) => w.path === n.worktree!.path) : undefined
}
const stateOf = (n: BranchNode) => worktreeOf(n)?.sessions[0]?.status ?? 'none'
const stale = (n: BranchNode) => !n.isDefault && !n.worktree && !n.pr && isStale(n.date, props.now)
const pos = (branch: string) => layout.value.positions.get(branch)!
const ago = (iso: string) => relativeTime(iso, props.now)

const edges = computed(() =>
  layout.value.edges.map((e) => ({
    key: `${e.from}>${e.to}`,
    d: edgePath(pos(e.from), pos(e.to), NODE_W, NODE_H),
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
    <div class="legend muted">
      <span><i class="swatch solid" /> PR base</span>
      <span><i class="swatch dashed" /> inferred from git history</span>
      <span>faded: merged or stale (&gt; 30 days)</span>
      <span v-if="graph.hidden > 0">{{ graph.hidden }} more branches hidden</span>
    </div>

    <div class="graph-scroll">
      <div class="graph" :style="{ width: `${layout.width}px`, height: `${layout.height}px` }">
        <svg class="edges" :width="layout.width" :height="layout.height" aria-hidden="true">
          <path v-for="e in edges" :key="e.key" :d="e.d" class="edge" :class="{ inferred: e.inferred }" />
        </svg>

        <div
          v-for="n in graph.nodes"
          :key="n.branch"
          class="gnode"
          :class="[`status-${stateOf(n)}`, { merged: n.merged, stale: stale(n), selected: selected === n.branch, root: n.isDefault }]"
          :data-branch="n.branch"
          :style="{ left: `${pos(n.branch).x}px`, top: `${pos(n.branch).y}px`, width: `${NODE_W}px`, height: `${NODE_H}px` }"
          role="button"
          tabindex="0"
          @click="toggle(n.branch)"
          @keydown.enter.prevent="toggle(n.branch)"
        >
          <div class="line">
            <span class="dot" />
            <strong class="mono clip" :title="n.branch">{{ n.branch }}</strong>
            <span v-if="n.isDefault" class="badge">default</span>
          </div>
          <div class="line">
            <PrChip v-if="n.pr" :pr="n.pr" />
            <span v-else-if="!n.isDefault" class="muted">no PR</span>
          </div>
          <div v-if="!n.isDefault" class="line muted small">
            <span class="mono">↑{{ n.ahead }} ↓{{ n.behind }}</span>
            <span>· {{ ago(n.date) }}</span>
            <span v-if="n.worktree" class="clip">· {{ n.worktree.name }}</span>
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
