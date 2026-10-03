<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { fetchWorktreeCleanup, runWorktreeCleanup } from '../api'
import { relativeTime } from '../format'
import type { WtPreview, WtResult } from '../types'

const props = defineProps<{ repo: string; repoName: string }>()
const emit = defineEmits<{ close: []; removed: [] }>()

const days = ref(7)
const includeIdle = ref(false)
const idleDays = ref(14)
const dormantDays = computed(() => (includeIdle.value ? idleDays.value : 0))
const preview = ref<WtPreview | null>(null)
const loadError = ref('')
const loading = ref(false)
const picked = ref<Set<string>>(new Set())

const removing = ref(false)
const results = ref<WtResult[] | null>(null)
const removeError = ref('')

const candidates = computed(() => preview.value?.candidates ?? [])
const count = computed(() => picked.value.size)
const removeLabel = computed(() => `Remove ${count.value} worktree${count.value === 1 ? '' : 's'}`)
const removedCount = computed(() => results.value?.filter((r) => r.removed).length ?? 0)
const short = (sha: string) => sha.slice(0, 7)
const kindText: Record<string, string> = {
  'dormant-merged': 'idle · already in the default branch',
  'dormant-on-remote': 'idle · pushed to a remote',
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    preview.value = await fetchWorktreeCleanup(props.repo, days.value, dormantDays.value)
    // merged ones start ticked (the user unticks what to keep); idle ones are the user's call, one by one
    picked.value = new Set(preview.value.candidates.filter((c) => c.kind === 'merged').map((c) => c.path))
  } catch (e) {
    preview.value = null
    loadError.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

function toggle(path: string, on: boolean) {
  const next = new Set(picked.value)
  if (on) next.add(path)
  else next.delete(path)
  picked.value = next
}

async function confirmRemove() {
  if (removing.value || count.value === 0) return
  removing.value = true
  removeError.value = ''
  try {
    const chosen = candidates.value.filter((c) => picked.value.has(c.path)).map((c) => ({ path: c.path, sha: c.sha }))
    results.value = await runWorktreeCleanup(props.repo, days.value, dormantDays.value, chosen)
    emit('removed')
  } catch (e) {
    removeError.value = e instanceof Error ? e.message : String(e)
  } finally {
    removing.value = false
  }
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}

onMounted(() => {
  window.addEventListener('keydown', onKey)
  load()
})
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="overlay" @click.self="emit('close')">
    <section class="modal cleanup" role="dialog" aria-modal="true" aria-label="Remove merged worktrees">
      <header>
        <h2>Remove merged worktrees · {{ repoName }}</h2>
        <span class="spacer" />
        <button data-close @click="emit('close')">Close (Esc)</button>
      </header>

      <!-- result -->
      <template v-if="results">
        <p>
          <strong>Removed {{ removedCount }} of {{ results.length }}.</strong>
        </p>
        <ul class="results">
          <li v-for="r in results" :key="r.path">
            <span :class="r.removed ? 'ok' : 'err'">{{ r.removed ? '✓' : '✗' }}</span>
            <span class="mono">{{ r.name || r.path }}</span>
            <span v-if="r.branch" class="muted">{{ r.branch }}</span>
            <span v-if="r.error" class="err">{{ r.error }}</span>
          </li>
        </ul>
        <p class="muted">
          The branches were kept. Delete them from the Branches tab with <strong>Clean up branches…</strong> when you no longer
          need them.
        </p>
      </template>

      <!-- preview -->
      <template v-else>
        <p class="muted">
          Linked worktrees whose pull request was merged at least
          <input v-model.number="days" data-days class="days" type="number" min="1" max="3650" @change="load" />
          days ago, with nothing uncommitted or untracked, no Claude session running in them, and no commits beyond what was
          merged. Removing a worktree <strong>deletes the folder</strong>; its branch is kept. The main worktree is never touched.
        </p>

        <p class="muted">
          <label>
            <input v-model="includeIdle" data-include-idle type="checkbox" @change="load" />
            Also worktrees idle for
          </label>
          <input v-model.number="idleDays" data-idle-days class="days" type="number" min="1" max="3650" @change="includeIdle && load()" />
          days with no pull request in flight, only if their commits are already in the default branch or on a remote (as of your
          last fetch). They start unticked.
        </p>

        <p v-if="loading" class="muted">Looking for merged worktrees…</p>
        <p v-if="loadError" class="err">{{ loadError }}</p>
        <p v-for="w in preview?.warnings ?? []" :key="w" class="banner warn">{{ w }}</p>

        <template v-if="preview && !loading">
          <p v-if="!candidates.length" class="muted">Nothing to remove.</p>

          <template v-else>
            <div class="bulk">
              <button class="link" data-select-all @click="picked = new Set(candidates.map((c) => c.path))">Select all</button>
              <button class="link" data-select-none @click="picked = new Set()">Select none</button>
            </div>
            <ul class="candidates">
              <li v-for="c in candidates" :key="c.path">
                <label>
                  <input type="checkbox" :data-path="c.path" :checked="picked.has(c.path)" @change="toggle(c.path, ($event.target as HTMLInputElement).checked)" />
                  <span class="mono name clip" :title="c.path">{{ c.name }}</span>
                </label>
                <span v-if="c.kind === 'merged'" class="clip"><span class="mono muted">{{ c.branch }}</span> · #{{ c.prNumber }} {{ c.prTitle }}</span>
                <span v-else class="clip"><span class="mono muted">{{ c.branch }}</span></span>
                <span v-if="c.kind === 'merged'" class="muted">merged {{ relativeTime(c.mergedAt) }}</span>
                <span v-else class="muted" :data-kind="c.kind">{{ kindText[c.kind] }} · {{ c.lastActivity ? relativeTime(c.lastActivity) : '' }}</span>
                <span class="muted mono">{{ short(c.sha) }}</span>
              </li>
            </ul>
          </template>

          <details v-if="preview.skipped.length" class="skipped">
            <summary>Kept ({{ preview.skipped.length }}): not eligible</summary>
            <ul>
              <li v-for="s in preview.skipped" :key="s.path">
                <span class="mono">{{ s.name }}</span> <span class="muted"><template v-if="s.prNumber">#{{ s.prNumber }} · </template>{{ s.reason }}</span>
              </li>
            </ul>
          </details>
        </template>

        <p v-if="removeError" class="err">{{ removeError }}</p>
        <footer v-if="candidates.length">
          <button data-remove class="danger" :disabled="removing || count === 0" @click="confirmRemove">
            {{ removing ? 'Removing…' : removeLabel }}
          </button>
          <button @click="emit('close')">Cancel</button>
        </footer>
      </template>
    </section>
  </div>
</template>
