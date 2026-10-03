<script setup lang="ts">
import { computed, reactive } from 'vue'
import type { Pr, Worktree } from '../types'
import { groupByStage, stageTitle, type Stage } from '../stage'
import { WIP_LIMIT, splitFocus, wipCount, wipLabel, type Note } from '../focus'
import WorktreeCard from './WorktreeCard.vue'

const props = withDefaults(
  defineProps<{ worktrees: Worktree[]; editor: string; prFor: (branch?: string) => Pr | undefined; now?: Date; dormantDays?: number; notes?: Record<string, Note> }>(),
  { now: () => new Date(), dormantDays: 14, notes: () => ({}) },
)
const emit = defineEmits<{
  terminal: [path: string]
  editor: [path: string]
  resume: [sessionId: string]
  plan: [sessionId: string, title: string]
  sessions: [path: string]
  star: [path: string, starred: boolean]
  note: [path: string, text: string]
}>()

// Work in progress counts every worktree, focused or not; the Focus strip only changes where a card is shown.
const all = computed(() => groupByStage(props.worktrees, props.prFor, props.now, props.dormantDays))
const split = computed(() => splitFocus(props.worktrees, props.notes))
const grouped = computed(() => groupByStage(split.value.rest, props.prFor, props.now, props.dormantDays))
const wip = computed(() => wipLabel(wipCount(all.value.lanes), WIP_LIMIT))
// Dormant work is hidden until asked: the point of the board is to show what is alive.
const open = reactive<Partial<Record<Stage, boolean>>>({})
const cardProps = (w: Worktree) => ({ worktree: w, editor: props.editor, pr: props.prFor(w.branch), now: props.now, note: props.notes[w.path] })
const cardEvents = (w: Worktree) => ({
  terminal: (p: string) => emit('terminal', p),
  editor: (p: string) => emit('editor', p),
  resume: (id: string) => emit('resume', id),
  plan: (id: string) => emit('plan', id, w.name),
  sessions: (p: string) => emit('sessions', p),
  star: (p: string, on: boolean) => emit('star', p, on),
  note: (p: string, text: string) => emit('note', p, text),
})
const isOpen = (s: Stage) => (s === 'dormant' ? !!open[s] : open[s] !== false)
const toggle = (s: Stage) => (open[s] = !isOpen(s))
</script>

<template>
  <div class="board">
    <p v-if="wip" data-wip class="wip" title="Finish or park something before starting more">⚠ {{ wip }}</p>

    <div v-if="grouped.main" data-main class="grid">
      <WorktreeCard v-bind="cardProps(grouped.main)" v-on="cardEvents(grouped.main)" />
    </div>

    <section v-if="split.focus.length" data-focus class="lane lane-focus">
      <h3 class="lane-title">★ Focus <span class="count">{{ split.focus.length }}</span></h3>
      <div class="grid">
        <WorktreeCard v-for="w in split.focus" :key="w.path" v-bind="cardProps(w)" v-on="cardEvents(w)" />
      </div>
    </section>

    <section v-for="lane in grouped.lanes" :key="lane.stage" class="lane" :class="`lane-${lane.stage}`" :data-lane="lane.stage">
      <h3 class="lane-title">
        <button class="link" data-toggle-lane :aria-expanded="isOpen(lane.stage)" @click="toggle(lane.stage)">
          {{ isOpen(lane.stage) ? '▾' : '▸' }} {{ stageTitle[lane.stage] }}
        </button>
        <span class="count">{{ lane.items.length }}</span>
        <span v-if="lane.stage === 'dormant'" class="muted small-note">no activity in {{ dormantDays }} days</span>
        <span v-if="lane.stage === 'done'" class="muted small-note">PR merged or closed: safe to clean up</span>
      </h3>
      <div v-if="isOpen(lane.stage)" class="grid">
        <WorktreeCard v-for="w in lane.items" :key="w.path" v-bind="cardProps(w)" v-on="cardEvents(w)" />
      </div>
    </section>
  </div>
</template>
