<script setup lang="ts">
import { computed } from 'vue'
import { tidyText, type BranchTidy, type TidyCounts } from '../tidy'

const props = defineProps<{ counts: TidyCounts; branches?: BranchTidy }>()
const emit = defineEmits<{ worktrees: []; branches: [] }>()

const text = computed(() => tidyText(props.counts, props.branches))
const title = computed(() =>
  props.branches?.risky ? `${props.branches.risky} more old branches need a look (not counted: each is a judgement call)` : 'Nothing is removed until you review the list',
)
</script>

<template>
  <p v-if="text" data-tidy class="tidy" :title="title">
    <span class="tidy-text">Tidy up: {{ text }}</span>
    <button v-if="counts.removable" data-tidy-worktrees @click="emit('worktrees')">Clean up worktrees…</button>
    <button v-if="branches?.safe" data-tidy-branches @click="emit('branches')">Clean up branches…</button>
  </p>
</template>
