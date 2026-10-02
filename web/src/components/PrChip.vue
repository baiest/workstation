<script setup lang="ts">
import { prLabel, prTone, safeHref } from '../format'
import type { Pr } from '../types'

defineProps<{ pr: Pr }>()
</script>

<template>
  <a
    class="pr"
    :class="`pr-${prTone(pr)}`"
    :href="safeHref(pr.url)"
    target="_blank"
    rel="noopener noreferrer"
    :title="pr.title"
    @click.stop
  >
    {{ prLabel(pr) }}
  </a>
</template>
