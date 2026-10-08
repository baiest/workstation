<script setup lang="ts">
import { computed, reactive } from 'vue'
import { COLLAPSED, canUnblock, groupTickets, sectionHint, sectionTitle, ticketLabel, waitingFor, wipCount, wipLabel, type SectionId } from '../board'
import type { Ticket } from '../tickets'
import type { TriageAction } from '../api'
import type { TicketState } from '../types'
import TicketCard from './TicketCard.vue'

const props = withDefaults(
  defineProps<{ tickets: Ticket[]; editor: string; wipLimit?: number; repoNames?: Record<string, string>; now?: Date }>(),
  { wipLimit: 3, repoNames: () => ({}), now: () => new Date() },
)
const emit = defineEmits<{
  state: [ticket: Ticket, state: TicketState, blockedReason?: string]
  next: [ticket: Ticket, text: string]
  discover: [ticket: Ticket, text: string]
  triage: [ticket: Ticket, discoveryId: string, action: TriageAction]
  terminal: [path: string]
  editor: [path: string]
  resume: [sessionId: string]
  plan: [sessionId: string, title: string]
  sessions: [path: string]
  start: [ticket: Ticket]
  review: [ticket: Ticket]
  depend: [ticket: Ticket, ids: string[]]
}>()

const sections = computed(() => groupTickets(props.tickets, props.now))
const wip = computed(() => wipLabel(wipCount(props.tickets), props.wipLimit))
// With more than one project, say which one a ticket belongs to.
const showRepo = computed(() => new Set(props.tickets.map((t) => t.repo)).size > 1)

const byId = computed(() => new Map(props.tickets.map((t) => [t.id, t])))
// a ticket may wait for any other that is still in play
const candidates = computed(() =>
  props.tickets
    .filter((t) => t.state !== 'done' && t.state !== 'ignored')
    .map((t) => ({ id: t.id, label: (showRepo.value ? `${props.repoNames[t.repo] ?? ''}: ` : '') + ticketLabel(t) })),
)

const open = reactive<Partial<Record<SectionId, boolean>>>({})
const isOpen = (s: SectionId) => (COLLAPSED.has(s) ? !!open[s] : open[s] !== false)
const toggle = (s: SectionId) => (open[s] = !isOpen(s))
</script>

<template>
  <div class="ticket-board">
    <p v-if="wip" data-wip class="wip" title="Finish or park something before starting more">⚠ {{ wip }}</p>
    <p v-if="!sections.length" class="muted">No tickets yet: they appear as soon as a branch or a worktree exists.</p>

    <section v-for="s in sections" :key="s.id" class="lane" :class="`lane-${s.id}`" :data-section="s.id">
      <h3 class="lane-title">
        <button class="link" data-toggle-section :aria-expanded="isOpen(s.id)" @click="toggle(s.id)">
          {{ isOpen(s.id) ? '▾' : '▸' }} {{ sectionTitle[s.id] }}
        </button>
        <span class="count">{{ s.tickets.length }}</span>
        <span class="muted small-note">{{ sectionHint[s.id] }}</span>
      </h3>
      <div v-if="isOpen(s.id)" class="tickets">
        <TicketCard
          v-for="t in s.tickets"
          :key="t.id"
          :ticket="t"
          :editor="editor"
          :now="now"
          :repo-name="showRepo ? (repoNames[t.repo] ?? '') : ''"
          :expanded="s.id === 'needs-you' || s.id === 'active' || s.id === 'blocked' || s.id === 'closable'"
          @state="(...a) => emit('state', ...a)"
          @next="(...a) => emit('next', ...a)"
          @discover="(...a) => emit('discover', ...a)"
          @triage="(...a) => emit('triage', ...a)"
          @terminal="(p) => emit('terminal', p)"
          @editor="(p) => emit('editor', p)"
          @resume="(id) => emit('resume', id)"
          @plan="(id, title) => emit('plan', id, title)"
          @sessions="(p) => emit('sessions', p)"
          :waiting="waitingFor(t, byId)"
          :unblockable="canUnblock(t, byId)"
          :candidates="candidates"
          @depend="(t2, ids) => emit('depend', t2, ids)"
          @start="(t2) => emit('start', t2)"
          @review="(t2) => emit('review', t2)"
        />
      </div>
    </section>
  </div>
</template>
