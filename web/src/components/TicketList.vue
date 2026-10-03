<script setup lang="ts">
import { gitSummary, relativeTime, statusLabel } from '../format'
import type { Ticket, TicketItem } from '../tickets'
import { isFresh } from '../worktrees'
import PrChip from './PrChip.vue'

const props = withDefaults(defineProps<{ tickets: Ticket[]; editor: string; now?: Date }>(), { now: () => new Date() })
const emit = defineEmits<{
  terminal: [path: string]
  editor: [path: string]
  resume: [sessionId: string]
  plan: [sessionId: string]
}>()

const ago = (iso: string) => relativeTime(iso, props.now)
const isNew = (i: TicketItem) => isFresh(new Date(i.activity).toISOString(), props.now)
const resumable = (i: TicketItem) => i.sessions.find((s) => s.resumable)
const planned = (i: TicketItem) => i.sessions.find((s) => s.hasPlan)

function summary(t: Ticket): string {
  const wts = t.items.filter((i) => i.worktree).length
  return `${wts} worktree${wts === 1 ? '' : 's'} · ${t.items.length} branch${t.items.length === 1 ? '' : 'es'}`
}
</script>

<template>
  <div class="tickets">
    <p v-if="!tickets.length" class="muted">No tickets: no branch or worktree matches.</p>

    <section v-for="t in tickets" :key="t.key || 'none'" class="ticket" :class="{ done: t.done }" :data-ticket="t.key || 'none'">
      <header>
        <span class="badge ticket-key">{{ t.key || '—' }}</span>
        <h3 class="clip" :title="t.title">{{ t.title }}</h3>
        <span v-if="t.done" class="badge merged-hint">all merged</span>
        <span class="summary muted">{{ summary(t) }}</span>
      </header>

      <ul class="items">
        <li v-for="i in t.items" :key="i.branch + (i.worktree?.path ?? '')" class="item" :data-branch="i.branch">
          <div class="cell branch">
            <span class="mono clip" :title="i.branch">{{ i.branch }}</span>
            <span v-if="isNew(i)" class="new-badge" title="Activity in the last 3 days">new</span>
          </div>
          <div class="cell folder">
            <span v-if="i.worktree" class="mono clip" :title="i.worktree.path">{{ i.worktree.name }}</span>
            <span v-else class="muted">no worktree</span>
          </div>
          <div class="cell pr">
            <PrChip v-if="i.pr" :pr="i.pr" />
            <span v-else class="muted">no PR</span>
          </div>
          <div class="cell git">
            <template v-if="i.worktree">
              <span v-if="i.worktree.gitError" class="err">{{ i.worktree.gitError }}</span>
              <span v-else :class="{ changed: i.worktree.git.dirty }">{{ gitSummary(i.worktree.git) }}</span>
            </template>
            <span v-else class="muted">—</span>
          </div>
          <div class="cell claude">
            <template v-if="i.worktree">
              <span v-if="i.sessions[0]">{{ statusLabel(i.sessions[0].status) }} · {{ ago(i.sessions[0].lastActivity) }}</span>
              <span v-else class="muted">no Claude session</span>
            </template>
            <span v-else class="muted">—</span>
          </div>
          <div class="cell actions">
            <template v-if="i.worktree">
              <button data-action="terminal" @click="emit('terminal', i.worktree!.path)">Terminal</button>
              <button v-if="editor" data-action="editor" @click="emit('editor', i.worktree!.path)">
                {{ editor === 'cursor' ? 'Cursor' : 'VS Code' }}
              </button>
              <button v-if="resumable(i)" data-action="resume" @click="emit('resume', resumable(i)!.id)">Resume</button>
              <button v-if="planned(i)" data-action="plan" @click="emit('plan', planned(i)!.id)">Plan</button>
            </template>
          </div>
        </li>
      </ul>
    </section>
  </div>
</template>
