<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { fetchPlan } from '../api'
import { relativeTime } from '../format'
import { renderPlan } from '../markdown'
import type { PlanData } from '../types'

const props = defineProps<{ sessionId: string; title?: string }>()
const emit = defineEmits<{ close: [] }>()

const plan = ref<PlanData | null>(null)
const error = ref('')
const note = ref('')
const html = computed(() => (plan.value ? renderPlan(plan.value.markdown) : ''))

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}

async function copyPath() {
  if (!plan.value) return
  try {
    await navigator.clipboard.writeText(plan.value.path)
    note.value = 'Path copied'
  } catch {
    note.value = plan.value.path // clipboard needs a secure context; show the path instead
  }
}

onMounted(async () => {
  window.addEventListener('keydown', onKey)
  try {
    plan.value = await fetchPlan(props.sessionId)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
})
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="overlay" @click.self="emit('close')">
    <section class="modal" role="dialog" aria-modal="true" aria-label="Plan">
      <header>
        <h2>{{ title || 'Plan' }}</h2>
        <span v-if="plan" class="muted mono">{{ plan.slug }} · edited {{ relativeTime(plan.modifiedAt) }}</span>
        <span class="spacer" />
        <button v-if="plan" @click="copyPath">Copy path</button>
        <button @click="emit('close')">Close (Esc)</button>
      </header>
      <p v-if="note" class="muted mono note">{{ note }}</p>
      <p v-if="error" class="err">{{ error }}</p>
      <p v-else-if="!plan" class="muted">Loading…</p>
      <article v-else class="markdown" v-html="html" />
    </section>
  </div>
</template>
