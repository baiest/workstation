<script setup lang="ts">
import { computed } from 'vue'
import { canUnblock, ticketLabel } from '../board'
import { edgePath, layoutDag } from '../layout'
import type { Ticket } from '../tickets'

const props = withDefaults(defineProps<{ tickets: Ticket[]; repoNames?: Record<string, string> }>(), { repoNames: () => ({}) })

const NODE_W = 220
const NODE_H = 64
const dims = { nodeW: NODE_W, nodeH: NODE_H, gapX: 70, gapY: 18 }

const byId = computed(() => new Map(props.tickets.map((t) => [t.id, t])))
// only the tickets that take part in a dependency: the rest would be noise here
const involved = computed(() => {
  const ids = new Set<string>()
  for (const t of props.tickets) {
    for (const d of t.blockedBy) {
      if (!byId.value.has(d)) continue
      ids.add(t.id)
      ids.add(d)
    }
  }
  return props.tickets.filter((t) => ids.has(t.id))
})
const layout = computed(() => layoutDag(involved.value.map((t) => ({ id: t.id, deps: t.blockedBy })), dims))
const nodes = computed(() => involved.value.map((t) => ({ t, p: layout.value.positions.get(t.id)! })))
const edges = computed(() =>
  layout.value.edges.map((e) => ({
    key: `${e.from}>${e.to}`,
    d: edgePath(layout.value.positions.get(e.from)!, layout.value.positions.get(e.to)!, NODE_W, NODE_H),
    open: byId.value.get(e.from)?.state !== 'done', // an edge to a finished ticket no longer holds anything back
  })),
)
const unblockable = computed(() => involved.value.filter((t) => canUnblock(t, byId.value)))
const stateText = (t: Ticket) => (t.state === 'inbox' ? 'inbox' : t.state)
</script>

<template>
  <div class="deps">
    <p v-if="!involved.length" class="muted">
      No dependencies yet. On a ticket, “+ waits for” says what it cannot go on without; they show up here.
    </p>
    <template v-else>
      <p v-if="unblockable.length" class="unblockable" data-unblockable-list>
        Ready to unblock: {{ unblockable.map(ticketLabel).join(', ') }}
      </p>
      <div class="dag-scroll">
        <div class="dag" :style="{ width: layout.width + 'px', height: layout.height + 'px' }">
          <svg :width="layout.width" :height="layout.height" aria-hidden="true">
            <path v-for="e in edges" :key="e.key" :d="e.d" class="dag-edge" :class="{ settled: !e.open }" />
          </svg>
          <article
            v-for="n in nodes"
            :key="n.t.id"
            class="dag-node"
            :class="[`state-${n.t.state}`, { waiting: n.t.state === 'blocked' }]"
            :style="{ left: n.p.x + 'px', top: n.p.y + 'px', width: NODE_W + 'px', height: NODE_H + 'px' }"
            :data-dag-node="n.t.id"
            :title="n.t.title"
          >
            <div class="dag-top">
              <span v-if="n.t.key" class="badge ticket-key">{{ n.t.key }}</span>
              <span class="badge">{{ stateText(n.t) }}</span>
              <span v-if="repoNames[n.t.repo]" class="muted dag-repo clip">{{ repoNames[n.t.repo] }}</span>
            </div>
            <div class="clip dag-title">{{ n.t.title }}</div>
          </article>
        </div>
      </div>
      <p class="muted small-note">An arrow goes from what is waited for to what waits. Grey arrows lead from finished tickets.</p>
    </template>
  </div>
</template>
