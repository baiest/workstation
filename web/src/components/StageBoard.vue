<script setup lang="ts">
import { computed, reactive } from 'vue'
import type { Pr, Worktree } from '../types'
import { groupByStage, stageTitle, type Stage } from '../stage'
import WorktreeCard from './WorktreeCard.vue'

const props = withDefaults(
  defineProps<{ worktrees: Worktree[]; editor: string; prFor: (branch?: string) => Pr | undefined; now?: Date; dormantDays?: number }>(),
  { now: () => new Date(), dormantDays: 14 },
)
const emit = defineEmits<{
  terminal: [path: string]
  editor: [path: string]
  resume: [sessionId: string]
  plan: [sessionId: string, title: string]
  sessions: [path: string]
}>()

const grouped = computed(() => groupByStage(props.worktrees, props.prFor, props.now, props.dormantDays))
// Dormant work is hidden until asked: the point of the board is to show what is alive.
const open = reactive<Partial<Record<Stage, boolean>>>({})
const isOpen = (s: Stage) => (s === 'dormant' ? !!open[s] : open[s] !== false)
const toggle = (s: Stage) => (open[s] = !isOpen(s))
</script>

<template>
  <div class="board">
    <div v-if="grouped.main" data-main class="grid">
      <WorktreeCard
        :worktree="grouped.main" :editor="editor" :pr="prFor(grouped.main.branch)" :now="now"
        @terminal="(p) => emit('terminal', p)" @editor="(p) => emit('editor', p)" @resume="(id) => emit('resume', id)"
        @plan="(id) => emit('plan', id, grouped.main!.name)" @sessions="(p) => emit('sessions', p)"
      />
    </div>

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
        <WorktreeCard
          v-for="w in lane.items" :key="w.path"
          :worktree="w" :editor="editor" :pr="prFor(w.branch)" :now="now"
          @terminal="(p) => emit('terminal', p)" @editor="(p) => emit('editor', p)" @resume="(id) => emit('resume', id)"
          @plan="(id) => emit('plan', id, w.name)" @sessions="(p) => emit('sessions', p)"
        />
      </div>
    </section>
  </div>
</template>
