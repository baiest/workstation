<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { stateActions } from '../board'
import { relativeTime } from '../format'
import type { Ticket } from '../tickets'
import type { Attention } from '../attention'
import type { TriageAction } from '../api'
import type { TicketState } from '../types'
import TicketRows from './TicketRows.vue'

const props = withDefaults(
  defineProps<{ ticket: Ticket; editor: string; now?: Date; repoName?: string; expanded?: boolean }>(),
  { now: () => new Date(), repoName: '', expanded: true },
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
}>()

const t = computed(() => props.ticket)
const actions = computed(() => stateActions(t.value))
const rowsOpen = ref(props.expanded)

const signalText: Partial<Record<Attention, [string, string]>> = {
  'asks-you': ['asks you', 'Claude finished with a question (a guess from its last words)'],
  approval: ['may need approval', 'A tool request has had no result for a while (a guess)'],
  failed: ['failed', 'The last thing recorded was an API error'],
  'review-output': ['review', 'Claude finished its turn: there is something to look at'],
  working: ['working', 'Claude is thinking or running a tool'],
  stale: ['stale session', 'Claude has waited for hours. Take what you need and close that session'],
}
const signal = computed(() => signalText[t.value.signal])
const summary = computed(() => {
  const wts = t.value.items.filter((i) => i.worktree).length
  return `${wts} worktree${wts === 1 ? '' : 's'} · ${t.value.items.length} branch${t.value.items.length === 1 ? '' : 'es'}`
})
const closedPrs = computed(() => t.value.done)

// next step: click to edit, Ctrl/Cmd+Enter or leaving the box saves, Esc cancels
const editingNext = ref(false)
const nextDraft = ref('')
const nextInput = ref<HTMLTextAreaElement | null>(null)
async function editNext() {
  nextDraft.value = t.value.nextStep
  editingNext.value = true
  await nextTick()
  nextInput.value?.focus()
}
function saveNext() {
  if (!editingNext.value) return
  editingNext.value = false
  if (nextDraft.value.trim() !== t.value.nextStep) emit('next', t.value, nextDraft.value)
}

// a discovery: one line, Enter adds
const discovering = ref(false)
const discoverDraft = ref('')
const discoverInput = ref<HTMLInputElement | null>(null)
async function startDiscover() {
  discovering.value = true
  await nextTick()
  discoverInput.value?.focus()
}
function addDiscovery() {
  const text = discoverDraft.value.trim()
  discoverDraft.value = ''
  discovering.value = false
  if (text) emit('discover', t.value, text)
}

// blocking asks for the reason first, so "blocked" never means "I forgot why"
const blocking = ref(false)
const reasonDraft = ref('')
const reasonInput = ref<HTMLInputElement | null>(null)
// finishing a ticket with discoveries nobody decided on: a warning, never a block
const confirmingDone = ref(false)
async function choose(state: TicketState) {
  confirmingDone.value = false
  if (state === 'done' && t.value.openDiscoveries.length) {
    confirmingDone.value = true
    return
  }
  if (state === 'blocked') {
    reasonDraft.value = t.value.blockedReason
    blocking.value = true
    await nextTick()
    reasonInput.value?.focus()
    return
  }
  emit('state', t.value, state)
}
function confirmBlock() {
  blocking.value = false
  emit('state', t.value, 'blocked', reasonDraft.value)
}
</script>

<template>
  <article class="ticket" :class="[`state-${t.state}`, { done: t.done }]" :data-ticket="t.ref">
    <header>
      <span v-if="t.key" class="badge ticket-key">{{ t.key }}</span>
      <h3 class="clip" :title="t.title">{{ t.title }}</h3>
      <span v-if="repoName" class="badge repo-badge" :title="t.repo">{{ repoName }}</span>
      <span v-if="signal" class="badge signal" :class="`signal-${t.signal}`" :title="signal[1]">{{ signal[0] }}</span>
      <span v-if="closedPrs" class="badge merged-hint" title="Every PR of this ticket is merged">all merged</span>
      <span class="summary muted">{{ summary }}</span>
    </header>

    <div class="next-line">
      <textarea
        v-if="editingNext"
        ref="nextInput"
        v-model="nextDraft"
        data-next-input
        class="next-input"
        rows="2"
        maxlength="1000"
        placeholder="Next step: what the next session (or you) should do first"
        @keydown.enter.ctrl.prevent="saveNext"
        @keydown.enter.meta.prevent="saveNext"
        @keydown.esc.prevent="editingNext = false"
        @blur="saveNext"
      />
      <button v-else-if="t.nextStep" class="link next" data-next title="Click to edit" @click="editNext">➜ {{ t.nextStep }}</button>
      <button v-else class="link muted" data-add-next @click="editNext">+ next step</button>
    </div>

    <p v-if="t.state === 'blocked' && !blocking" class="blocked-line" data-blocked>
      ⛔ {{ t.blockedReason || 'blocked, no reason given' }}
    </p>
    <div v-if="blocking" class="blocked-edit">
      <input
        ref="reasonInput"
        v-model="reasonDraft"
        data-blocked-input
        maxlength="200"
        placeholder="Blocked by what? (waiting for Ana's review, needs REG-120…)"
        @keydown.enter.prevent="confirmBlock"
        @keydown.esc.prevent="blocking = false"
      />
      <button data-blocked-save @click="confirmBlock">Block</button>
      <button class="link" @click="blocking = false">Cancel</button>
    </div>

    <p v-if="confirmingDone" class="done-warning" data-done-warning>
      {{ t.openDiscoveries.length }} discover{{ t.openDiscoveries.length === 1 ? 'y' : 'ies' }} still undecided. Drop, scope or promote them, or finish anyway:
      <button data-done-anyway @click="confirmingDone = false; emit('state', t, 'done')">Done anyway</button>
      <button class="link" @click="confirmingDone = false">Not yet</button>
    </p>

    <ul v-if="t.openDiscoveries.length" class="discoveries" data-discoveries>
      <li v-for="d in t.openDiscoveries" :key="d.id" class="discovery" :data-discovery="d.id">
        <span class="d-text clip" :title="d.text">🔎 {{ d.text }}</span>
        <span class="muted d-meta">{{ d.source === 'cli' ? 'from Claude' : 'noted' }} · {{ relativeTime(d.at, now) }}</span>
        <button class="link" data-triage="drop" title="Not worth doing" @click="emit('triage', t, d.id, 'drop')">Drop</button>
        <button class="link" data-triage="in-scope" title="It is part of this ticket after all" @click="emit('triage', t, d.id, 'in-scope')">In scope</button>
        <button class="link" data-triage="promote" title="Becomes its own ticket, parked" @click="emit('triage', t, d.id, 'promote')">New ticket</button>
      </li>
    </ul>

    <div class="ticket-actions">
      <button
        v-for="a in actions"
        :key="a.label"
        :class="{ primary: a.primary }"
        :title="a.title"
        :data-state="a.state || 'inbox'"
        @click="choose(a.state)"
      >
        {{ a.label }}
      </button>
      <span class="spacer" />
      <input
        v-if="discovering"
        ref="discoverInput"
        v-model="discoverDraft"
        data-discover-input
        class="discover-input"
        maxlength="300"
        placeholder="Something that came up, not part of this ticket"
        @keydown.enter.prevent="addDiscovery"
        @keydown.esc.prevent="discovering = false"
        @blur="addDiscovery"
      />
      <button v-else class="link" data-add-discovery title="Note something that is out of scope, to decide on later" @click="startDiscover">+ discovery</button>
      <button v-if="t.items.length" class="link" data-toggle-rows :aria-expanded="rowsOpen" @click="rowsOpen = !rowsOpen">
        {{ rowsOpen ? 'Hide work' : 'Show work' }}
      </button>
    </div>

    <TicketRows
      v-if="rowsOpen && t.items.length"
      :items="t.items"
      :editor="editor"
      :now="now"
      @terminal="(p) => emit('terminal', p)"
      @editor="(p) => emit('editor', p)"
      @resume="(id) => emit('resume', id)"
      @plan="(id) => emit('plan', id, t.title)"
      @sessions="(p) => emit('sessions', p)"
    />
  </article>
</template>
