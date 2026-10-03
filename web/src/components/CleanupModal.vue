<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { fetchCleanup, runCleanup } from '../api'
import { relativeTime } from '../format'
import type { CleanupCandidate, CleanupKind, CleanupPreview, DeleteResult } from '../types'

const props = defineProps<{ repo: string; repoName: string }>()
const emit = defineEmits<{ close: []; deleted: [] }>()

// Branches grouped by how much deleting them could lose, safest first.
const GROUPS: { kind: CleanupKind; title: string; hint: string }[] = [
  { kind: 'pr-merged', title: 'Pull request merged', hint: '' },
  { kind: 'stale-merged', title: 'No PR · already merged into the default branch', hint: 'Nothing is lost.' },
  {
    kind: 'stale-on-remote',
    title: 'No PR · a remote has the commits',
    hint: 'Backed up on a remote (as of your last fetch); the local branch can be re-created from it.',
  },
  {
    kind: 'stale-local-only',
    title: 'No PR · only on this machine',
    hint: 'These commits exist nowhere else. After deleting, only the restore command below brings them back.',
  },
]
const kindOf = (c: CleanupCandidate): CleanupKind => c.kind ?? 'pr-merged'
const SAFE: CleanupKind[] = ['pr-merged', 'stale-merged']

const days = ref(30)
const preview = ref<CleanupPreview | null>(null)
const loadError = ref('')
const loading = ref(false)
const picked = ref<Set<string>>(new Set())
const ack = ref(false) // "I understand": needed to delete branches that exist only here

const deleting = ref(false)
const results = ref<DeleteResult[] | null>(null)
const deleteError = ref('')

const candidates = computed(() => preview.value?.candidates ?? [])
const groups = computed(() =>
  GROUPS.map((g) => ({ ...g, items: candidates.value.filter((c) => kindOf(c) === g.kind) })).filter((g) => g.items.length > 0),
)
const count = computed(() => picked.value.size)
const riskyPicked = computed(() => candidates.value.filter((c) => kindOf(c) === 'stale-local-only' && picked.value.has(c.branch)))
const needsAck = computed(() => riskyPicked.value.length > 0)
const canDelete = computed(() => !deleting.value && count.value > 0 && (!needsAck.value || ack.value))
const deleteLabel = computed(() => `Delete ${count.value} local branch${count.value === 1 ? '' : 'es'}`)
const deletedCount = computed(() => results.value?.filter((r) => r.deleted).length ?? 0)
const restoreCommands = computed(() =>
  (results.value ?? []).filter((r) => r.deleted).map((r) => `git branch ${r.branch} ${r.sha}`),
)
const short = (sha: string) => sha.slice(0, 7)
const commits = (n: number) => `${n} commit${n === 1 ? '' : 's'}`

// the confirmation never outlives the selection it was given for
watch(needsAck, (needed) => {
  if (!needed) ack.value = false
})

async function load() {
  loading.value = true
  loadError.value = ''
  ack.value = false
  try {
    preview.value = await fetchCleanup(props.repo, days.value)
    // safe branches start ticked; the user chooses the riskier ones
    picked.value = new Set(preview.value.candidates.filter((c) => SAFE.includes(kindOf(c))).map((c) => c.branch))
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

/** "Select all" skips branches whose commits exist only here: those are always a deliberate choice. */
function selectAll() {
  picked.value = new Set(candidates.value.filter((c) => kindOf(c) !== 'stale-local-only').map((c) => c.branch))
}

async function confirmDelete() {
  if (!canDelete.value) return
  deleting.value = true
  deleteError.value = ''
  try {
    const chosen = candidates.value.filter((c) => picked.value.has(c.branch)).map((c) => ({ branch: c.branch, sha: c.sha }))
    results.value = await runCleanup(props.repo, days.value, chosen, needsAck.value && ack.value)
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
          days ago and that have no commits beyond what was merged, and old branches with no PR, whose last commit is older than
          that, graded by what deleting them could lose. Remote branches are not touched, and neither are the default branch,
          branches with a worktree, or branches with an open PR.
        </p>

        <p v-if="loading" class="muted">Looking for branches…</p>
        <p v-if="loadError" class="err">{{ loadError }}</p>
        <p v-for="w in preview?.warnings ?? []" :key="w" class="banner warn">{{ w }}</p>

        <template v-if="preview && !loading">
          <p v-if="!candidates.length" class="muted">Nothing to clean up.</p>

          <template v-else>
            <div class="bulk">
              <button class="link" data-select-all @click="selectAll">Select all</button>
              <button class="link" data-select-none @click="picked = new Set()">Select none</button>
            </div>

            <section v-for="g in groups" :key="g.kind" class="group" :class="`kind-${g.kind}`" :data-group="g.kind">
              <h4>
                {{ g.title }} <span class="muted">({{ g.items.length }})</span>
              </h4>
              <p v-if="g.hint" class="hint" :class="{ danger: g.kind === 'stale-local-only' }">{{ g.hint }}</p>
              <ul class="candidates">
                <li v-for="c in g.items" :key="c.branch" :data-branch-row="c.branch">
                  <label>
                    <input type="checkbox" :data-branch="c.branch" :checked="picked.has(c.branch)" @change="toggle(c.branch, ($event.target as HTMLInputElement).checked)" />
                    <span class="mono name clip" :title="c.branch">{{ c.branch }}</span>
                  </label>
                  <span class="clip">
                    <template v-if="c.kind === 'stale-local-only'">
                      <strong class="err">{{ commits(c.ahead ?? 0) }} exist only here</strong>
                    </template>
                    <template v-else-if="c.kind === 'stale-on-remote' && c.ahead">{{ commits(c.ahead) }}, also on a remote</template>
                    <template v-else-if="c.prNumber">#{{ c.prNumber }} {{ c.prTitle }}</template>
                    <template v-else>already in the default branch</template>
                    <span v-if="c.prNumber && c.kind && c.kind !== 'pr-merged'" class="muted"> · PR #{{ c.prNumber }} closed without merging</span>
                  </span>
                  <span class="muted">
                    {{ c.mergedAt ? `merged ${relativeTime(c.mergedAt)}` : c.lastCommit ? `last commit ${relativeTime(c.lastCommit)}` : '' }}
                  </span>
                  <span class="muted mono">{{ short(c.sha) }}</span>
                </li>
              </ul>
            </section>
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

        <label v-if="needsAck" class="ack">
          <input v-model="ack" type="checkbox" data-ack />
          I understand that {{ riskyPicked.length === 1 ? 'this branch holds' : 'these branches hold' }} commits that exist only on this
          machine. I can bring them back with the <code>git branch</code> command shown afterwards.
        </label>

        <p v-if="deleteError" class="err">{{ deleteError }}</p>
        <footer v-if="candidates.length">
          <button data-delete class="danger" :disabled="!canDelete" @click="confirmDelete">
            {{ deleting ? 'Deleting…' : deleteLabel }}
          </button>
          <button @click="emit('close')">Cancel</button>
        </footer>
      </template>
    </section>
  </div>
</template>
