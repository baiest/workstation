<script setup lang="ts">
import { computed, ref } from 'vue'
import { gitSummary, relativeTime, statusLabel } from '../format'
import type { Pr, Worktree } from '../types'
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
}>()

const expanded = ref(false)

const latest = computed(() => props.worktree.sessions[0])
const state = computed(() => latest.value?.status ?? 'none')
const extra = computed(() => props.worktree.sessions.length - 1)
const resumable = computed(() => props.worktree.sessions.find((s) => s.resumable))
const planned = computed(() => props.worktree.sessions.find((s) => s.hasPlan))
const branchText = computed(() =>
  props.worktree.detached ? `detached @ ${props.worktree.head.slice(0, 7)}` : props.worktree.branch,
)
const ahead = computed(() => props.worktree.git.ahead)
const behind = computed(() => props.worktree.git.behind)
const ago = (iso: string) => relativeTime(iso, props.now)
</script>

<template>
  <article class="card" :class="[`status-${state}`, { dirty: worktree.git.dirty }]">
    <header>
      <span class="dot" />
      <h3>{{ worktree.name }}</h3>
      <span v-if="worktree.isMain" class="badge">main</span>
      <span class="state">{{ latest ? statusLabel(latest.status) : '' }}</span>
    </header>

    <div class="branch mono">
      {{ branchText }}
      <span v-if="ahead" class="sync">↑{{ ahead }}</span>
      <span v-if="behind" class="sync">↓{{ behind }}</span>
    </div>
    <div class="path mono" :title="worktree.path"><bdi>{{ worktree.path }}</bdi></div>

    <div class="row">
      <span class="k">Git</span>
      <span v-if="worktree.gitError" class="err" :title="worktree.gitError">{{ worktree.gitError }}</span>
      <span v-else :class="{ changed: worktree.git.dirty }">{{ gitSummary(worktree.git) }}</span>
    </div>
    <div v-if="pr" class="row">
      <span class="k">PR</span>
      <PrChip :pr="pr" />
    </div>
    <div v-if="worktree.git.lastCommit" class="row">
      <span class="k">Commit</span>
      <span class="clip">
        <span class="mono">{{ worktree.git.lastCommit.hash }}</span>
        {{ worktree.git.lastCommit.subject }} · {{ ago(worktree.git.lastCommit.date) }}
      </span>
    </div>

    <div class="row">
      <span class="k">Claude</span>
      <span v-if="!latest" class="muted">No Claude session</span>
      <span v-else :title="latest.rawStatus ? `raw status: ${latest.rawStatus}` : ''">
        {{ statusLabel(latest.status) }} · {{ ago(latest.lastActivity) }}
        <button v-if="extra > 0" class="link" @click="expanded = !expanded">+{{ extra }} more</button>
      </span>
    </div>
    <p v-if="latest?.title || latest?.lastMessage" class="message">
      <strong v-if="latest.title">{{ latest.title }}</strong>
      {{ latest.lastMessage }}
    </p>

    <ul v-if="expanded" class="sessions">
      <li v-for="s in worktree.sessions.slice(1)" :key="s.id">
        <span class="mini" :class="`status-${s.status}`">{{ statusLabel(s.status) }}</span>
        <span class="clip">{{ s.title || s.lastMessage || s.id }}</span>
        <span class="muted">{{ ago(s.lastActivity) }}</span>
        <button v-if="s.hasPlan" class="link" data-action="plan-row" @click="emit('plan', s.id)">Plan</button>
        <button v-if="s.resumable" class="link" data-action="resume-row" @click="emit('resume', s.id)">Resume</button>
      </li>
    </ul>

    <footer>
      <button data-action="terminal" @click="emit('terminal', worktree.path)">Terminal</button>
      <button v-if="editor" data-action="editor" @click="emit('editor', worktree.path)">
        {{ editor === 'cursor' ? 'Cursor' : 'VS Code' }}
      </button>
      <button v-if="resumable" data-action="resume" @click="emit('resume', resumable.id)">Resume Claude</button>
      <button v-if="planned" data-action="plan" @click="emit('plan', planned.id)">Plan</button>
    </footer>
  </article>
</template>
