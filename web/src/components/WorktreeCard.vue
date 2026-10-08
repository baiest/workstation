<script setup lang="ts">
import { computed, ref } from 'vue'
import { gitSummary, relativeTime, sessionHint, sessionLabel, sessionTitle, stateTone } from '../format'
import type { Pr, Worktree } from '../types'
import { isFresh, mergedHint, ticketKey } from '../worktrees'
import PrChip from './PrChip.vue'

const props = withDefaults(defineProps<{ worktree: Worktree; editor: string; now?: Date; pr?: Pr }>(), {
  now: () => new Date(),
  pr: undefined,
})
const emit = defineEmits<{
  terminal: [path: string]
  editor: [path: string]
  resume: [sessionId: string]
  plan: [sessionId: string]
  sessions: [path: string] // open the full list of this worktree's sessions
}>()

const open = ref(false)
const latest = computed(() => props.worktree.sessions[0])
const state = computed(() => latest.value?.status ?? 'none')
const tone = computed(() => stateTone(latest.value))
const extra = computed(() => props.worktree.sessions.length - 1)
const resumable = computed(() => props.worktree.sessions.find((s) => s.resumable))
const planned = computed(() => props.worktree.sessions.find((s) => s.hasPlan))
const branchText = computed(() =>
  props.worktree.detached ? `detached @ ${props.worktree.head.slice(0, 7)}` : props.worktree.branch,
)
// The title says what the work IS (the PR title, else the branch), not the folder it happens to live in.
const ticket = computed(() => (props.worktree.isMain ? undefined : ticketKey(props.worktree.branch)))
const title = computed(() => {
  const w = props.worktree
  if (w.isMain) return w.name
  return props.pr?.title ?? w.branch ?? `detached @ ${w.head.slice(0, 7)}`
})
const showBranchLine = computed(() => props.worktree.isMain || !!props.pr) // otherwise the branch is already the title
const fresh = computed(() => !props.worktree.isMain && isFresh(props.worktree.git.lastCommit?.date, props.now))
const removable = computed(() => mergedHint(props.worktree, props.pr))
const ahead = computed(() => props.worktree.git.ahead)
const behind = computed(() => props.worktree.git.behind)
const ago = (iso: string) => relativeTime(iso, props.now)
</script>

<template>
  <article class="card" :class="[`status-${state}`, `tone-${tone}`, { dirty: worktree.git.dirty, fresh }]">
    <header>
      <span class="dot" />
      <span v-if="ticket" class="badge ticket">{{ ticket }}</span>
      <h3 :title="title">{{ title }}</h3>
      <span v-if="worktree.isMain" class="badge">main</span>
      <span v-if="removable" class="badge merged-hint" title="Its PR is merged and nothing is pending here: this worktree can be removed">
        merged · removable
      </span>
      <span class="state" :title="latest ? sessionHint(latest) : ''">{{ latest ? sessionLabel(latest) : '' }}</span>
    </header>

    <div v-if="showBranchLine" class="branch mono clip">{{ branchText }}</div>

    <div data-glance class="glance">
      <div class="row">
        <span v-if="worktree.gitError" class="err" :title="worktree.gitError">{{ worktree.gitError }}</span>
        <template v-else>
          <span :class="{ changed: worktree.git.dirty }">{{ gitSummary(worktree.git) }}</span>
          <span v-if="ahead" class="sync">↑{{ ahead }}</span>
          <span v-if="behind" class="sync">↓{{ behind }}</span>
        </template>
        <PrChip v-if="pr" :pr="pr" />
      </div>
      <div class="row claude">
        <span v-if="!latest" class="muted">No Claude session</span>
        <template v-else>
          <span :title="sessionHint(latest) || (latest.rawStatus ? `raw status: ${latest.rawStatus}` : '')">
            {{ sessionLabel(latest) }} · {{ ago(latest.lastActivity) }}
          </span>
          <button v-if="extra > 0" class="link" @click="emit('sessions', worktree.path)">+{{ extra }} more</button>
        </template>
      </div>
      <p v-if="latest?.title || latest?.prompt" class="chat clip" :title="latest.title || latest.prompt">{{ latest.title || latest.prompt }}</p>
    </div>

    <button class="link details-toggle" data-toggle-details :aria-expanded="open" @click="open = !open">
      {{ open ? 'Hide details' : 'Details' }}
    </button>
    <div v-show="open" data-details class="details">
      <div class="row">
        <span class="k">Folder</span>
        <span class="folder mono clip" :title="worktree.path">{{ worktree.name }}</span>
      </div>
      <div class="path mono" :title="worktree.path"><bdi>{{ worktree.path }}</bdi></div>
      <div v-if="worktree.git.lastCommit" class="row">
        <span class="k">Commit</span>
        <span class="clip">
          <span class="mono">{{ worktree.git.lastCommit.hash }}</span>
          {{ worktree.git.lastCommit.subject }} · {{ ago(worktree.git.lastCommit.date) }}
        </span>
      </div>
      <p v-if="latest?.lastMessage" class="message">{{ latest.lastMessage }}</p>
    </div>

    <footer>
      <button data-action="terminal" @click="emit('terminal', worktree.path)">Terminal</button>
      <button v-if="editor" data-action="editor" @click="emit('editor', worktree.path)">
        {{ editor === 'cursor' ? 'Cursor' : 'VS Code' }}
      </button>
      <button
        v-if="resumable"
        data-action="resume"
        :title="extra > 0 ? `Resumes the latest session (${sessionTitle(resumable)}). Use Sessions to pick another.` : ''"
        @click="emit('resume', resumable.id)"
      >
        {{ extra > 0 ? 'Resume latest' : 'Resume Claude' }}
      </button>
      <button v-if="extra > 0" data-action="sessions" @click="emit('sessions', worktree.path)">Sessions ({{ worktree.sessions.length }})</button>
      <button v-if="planned" data-action="plan" @click="emit('plan', planned.id)">Plan</button>
    </footer>
  </article>
</template>
