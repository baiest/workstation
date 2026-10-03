<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { relativeTime, sessionHint, sessionLabel, sessionTitle, stateTone } from '../format'
import type { Session } from '../types'

const props = withDefaults(defineProps<{ sessions: Session[]; title: string; now?: Date }>(), { now: () => new Date() })
const emit = defineEmits<{ close: []; resume: [sessionId: string]; plan: [sessionId: string] }>()

const query = ref('')
const searchEl = ref<HTMLInputElement | null>(null)

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return props.sessions
  return props.sessions.filter((s) =>
    [s.title, s.prompt, s.lastMessage, s.id].some((field) => (field ?? '').toLowerCase().includes(q)),
  )
})

const ago = (iso: string) => relativeTime(iso, props.now)
// the last words are only worth showing when they add something to the name
const lastWords = (s: Session) => (s.lastMessage && s.lastMessage !== sessionTitle(s) ? s.lastMessage : '')

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}

onMounted(() => {
  window.addEventListener('keydown', onKey)
  searchEl.value?.focus()
})
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="overlay" @click.self="emit('close')">
    <section class="modal sessions-modal" role="dialog" aria-modal="true" aria-label="Claude sessions">
      <header>
        <h2>Claude sessions · {{ title }}</h2>
        <span class="muted">
          {{ query.trim() ? `${filtered.length} of ${sessions.length}` : `${sessions.length} session${sessions.length === 1 ? '' : 's'}` }}
        </span>
        <span class="spacer" />
        <button data-close @click="emit('close')">Close (Esc)</button>
      </header>

      <input
        ref="searchEl"
        v-model="query"
        data-search
        class="session-search"
        type="search"
        placeholder="Search by what you asked, the title, the last words…"
        spellcheck="false"
      />

      <p v-if="!sessions.length" class="muted">No sessions in this worktree.</p>
      <p v-else-if="!filtered.length" class="muted">No session matches “{{ query }}”.</p>

      <ul class="session-list">
        <li v-for="s in filtered" :key="s.id" class="session" :class="`tone-${stateTone(s)}`" :data-session="s.id">
          <div class="s-head">
            <span class="s-state" :title="sessionHint(s)">{{ sessionLabel(s) }}</span>
            <span class="s-title clip" :title="sessionTitle(s)">{{ sessionTitle(s) }}</span>
            <span class="s-source badge">{{ s.desktopId ? 'Desktop' : 'CLI' }}</span>
            <span class="muted s-ago">{{ ago(s.lastActivity) }}</span>
            <span class="s-actions">
              <button v-if="s.hasPlan" data-action="plan" @click="emit('plan', s.id)">Plan</button>
              <button v-if="s.resumable" data-action="resume" @click="emit('resume', s.id)">Resume</button>
            </span>
          </div>
          <p v-if="lastWords(s)" class="s-last muted clip">{{ lastWords(s) }}</p>
        </li>
      </ul>
    </section>
  </div>
</template>
