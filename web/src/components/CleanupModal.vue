<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { fetchCleanup, runCleanup } from '../api'
import { relativeTime } from '../format'
import type { CleanupPreview, DeleteResult } from '../types'

const props = defineProps<{ repo: string; repoName: string }>()
const emit = defineEmits<{ close: []; deleted: [] }>()

const days = ref(30)
const preview = ref<CleanupPreview | null>(null)
const loadError = ref('')
const loading = ref(false)
const picked = ref<Set<string>>(new Set())

const deleting = ref(false)
const results = ref<DeleteResult[] | null>(null)
const deleteError = ref('')

const candidates = computed(() => preview.value?.candidates ?? [])
const count = computed(() => picked.value.size)
const deleteLabel = computed(() => `Delete ${count.value} local branch${count.value === 1 ? '' : 'es'}`)
const deletedCount = computed(() => results.value?.filter((r) => r.deleted).length ?? 0)
const restoreCommands = computed(() =>
  (results.value ?? []).filter((r) => r.deleted).map((r) => `git branch ${r.branch} ${r.sha}`),
)
const short = (sha: string) => sha.slice(0, 7)

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    preview.value = await fetchCleanup(props.repo, days.value)
    picked.value = new Set(preview.value.candidates.map((c) => c.branch)) // everything ticked; the user unticks what to keep
  } catch (e) {
    preview.value = null
    loadError.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

function toggle(branch: string, on: boolean) {
  const next = new Set(picked.value)
  if (on) next.add(branch)
  else next.delete(branch)
  picked.value = next
}

async function confirmDelete() {
  if (deleting.value || count.value === 0) return
  deleting.value = true
  deleteError.value = ''
  try {
    const chosen = candidates.value.filter((c) => picked.value.has(c.branch)).map((c) => ({ branch: c.branch, sha: c.sha }))
    results.value = await runCleanup(props.repo, days.value, chosen)
    emit('deleted')
  } catch (e) {
    deleteError.value = e instanceof Error ? e.message : String(e)
  } finally {
    deleting.value = false
  }
}

const note = ref('')
async function copyRestore() {
  try {
    await navigator.clipboard.writeText(restoreCommands.value.join('\n'))
    note.value = 'Copied'
  } catch {
    note.value = 'Select the commands above and copy them' // clipboard needs a secure context
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
    <section class="modal cleanup" role="dialog" aria-modal="true" aria-label="Clean up branches">
      <header>
        <h2>Clean up branches · {{ repoName }}</h2>
        <span class="spacer" />
        <button data-close @click="emit('close')">Close (Esc)</button>
      </header>

      <!-- result of a deletion -->
      <template v-if="results">
        <p>
          <strong>Deleted {{ deletedCount }} of {{ results.length }}.</strong>
          <span class="muted"> Nothing was touched on the remote.</span>
        </p>
        <ul class="results">
          <li v-for="r in results" :key="r.branch">
            <span :class="r.deleted ? 'ok' : 'err'">{{ r.deleted ? '✓' : '✗' }}</span>
            <span class="mono">{{ r.branch }}</span>
            <span v-if="r.error" class="err">{{ r.error }}</span>
          </li>
        </ul>
        <template v-if="restoreCommands.length">
          <p class="muted">To bring a branch back (the commits are still in git until it is garbage collected):</p>
          <pre class="restore"><code>{{ restoreCommands.join('\n') }}</code></pre>
          <button @click="copyRestore">Copy restore commands</button>
          <span v-if="note" class="muted note">{{ note }}</span>
        </template>
      </template>

      <!-- preview -->
      <template v-else>
        <p class="muted">
          Local branches whose pull request was merged at least
          <input v-model.number="days" data-days class="days" type="number" min="1" max="3650" @change="load" />
          days ago and that have no commits beyond what was merged. Remote branches are not touched, and neither are the
          default branch, branches with a worktree, or branches with an open PR.
        </p>

        <p v-if="loading" class="muted">Looking for merged branches…</p>
        <p v-if="loadError" class="err">{{ loadError }}</p>
        <p v-for="w in preview?.warnings ?? []" :key="w" class="banner warn">{{ w }}</p>

        <template v-if="preview && !loading">
          <p v-if="!candidates.length" class="muted">Nothing to clean up.</p>

          <template v-else>
            <div class="bulk">
              <button class="link" data-select-all @click="picked = new Set(candidates.map((c) => c.branch))">Select all</button>
              <button class="link" data-select-none @click="picked = new Set()">Select none</button>
            </div>
            <ul class="candidates">
              <li v-for="c in candidates" :key="c.branch">
                <label>
                  <input type="checkbox" :data-branch="c.branch" :checked="picked.has(c.branch)" @change="toggle(c.branch, ($event.target as HTMLInputElement).checked)" />
                  <span class="mono name clip" :title="c.branch">{{ c.branch }}</span>
                </label>
                <span class="clip">#{{ c.prNumber }} {{ c.prTitle }}</span>
                <span class="muted">merged {{ relativeTime(c.mergedAt) }}</span>
                <span class="muted mono">{{ short(c.sha) }}</span>
              </li>
            </ul>
          </template>

          <details v-if="preview.skipped.length" class="skipped">
            <summary>Kept ({{ preview.skipped.length }}): merged PR, but not eligible</summary>
            <ul>
              <li v-for="s in preview.skipped" :key="s.branch">
                <span class="mono">{{ s.branch }}</span> <span class="muted">#{{ s.prNumber }} · {{ s.reason }}</span>
              </li>
            </ul>
          </details>
        </template>

        <p v-if="deleteError" class="err">{{ deleteError }}</p>
        <footer v-if="candidates.length">
          <button data-delete class="danger" :disabled="deleting || count === 0" @click="confirmDelete">
            {{ deleting ? 'Deleting…' : deleteLabel }}
          </button>
          <button @click="emit('close')">Cancel</button>
        </footer>
      </template>
    </section>
  </div>
</template>
