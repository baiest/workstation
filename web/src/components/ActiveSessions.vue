<script setup lang="ts">
import { relativeTime, sessionHint, sessionLabel, sessionTitle, stateTone } from '../format'
import type { ActiveRow } from '../active'

withDefaults(defineProps<{ rows: ActiveRow[]; now?: Date }>(), { now: () => new Date() })
const emit = defineEmits<{ resume: [sessionId: string]; plan: [sessionId: string, title: string] }>()
</script>

<template>
  <section v-if="rows.length" class="active" aria-label="Active now">
    <h2>Active now <span class="count">{{ rows.length }}</span></h2>
    <ul>
      <li v-for="r in rows" :key="r.session.id" class="active-row" :class="`tone-${stateTone(r.session)}`" :data-active="r.session.id">
        <span class="s-state" :title="sessionHint(r.session)">{{ sessionLabel(r.session) }}</span>
        <span class="clip s-title" :title="sessionTitle(r.session)">{{ sessionTitle(r.session) }}</span>
        <span v-if="r.ticket" class="badge">{{ r.ticket }}</span>
        <span class="muted clip" :title="r.worktreePath ?? r.session.cwd">
          <template v-if="r.worktree">{{ r.repo }} · {{ r.worktree }}<template v-if="r.branch"> · {{ r.branch }}</template></template>
          <template v-else>not in a worktree</template>
        </span>
        <span class="muted s-ago">{{ relativeTime(r.session.lastActivity, now) }}</span>
        <button v-if="r.session.hasPlan" class="link" @click="emit('plan', r.session.id, sessionTitle(r.session))">Plan</button>
        <button v-if="r.session.resumable" class="link" @click="emit('resume', r.session.id)">Resume</button>
      </li>
    </ul>
  </section>
</template>
