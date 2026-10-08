<script setup lang="ts">
import { computed } from 'vue'
import { computeStats, formatDuration } from '../stats'
import type { Ticket } from '../tickets'
import type { StoredTicket } from '../types'

const props = withDefaults(defineProps<{ stored: StoredTicket[]; tickets: Ticket[]; wipLimit?: number; now?: Date }>(), {
  wipLimit: 3,
  now: () => new Date(),
})

const stats = computed(() => computeStats(props.stored, props.tickets, props.now))
const topWeek = computed(() => Math.max(1, ...stats.value.doneByWeek.map((w) => w.count)))
const avg = computed(() => stats.value.averageActive.toFixed(1))
const d = computed(() => stats.value.discoveries)
</script>

<template>
  <div class="stats" data-stats>
    <p v-if="!stored.length" class="muted">
      Nothing to count yet. As you move tickets between active, blocked, parked and done, this page shows where the time went.
    </p>
    <template v-else>
      <section data-stats-wip>
        <h3>Work in progress</h3>
        <p>
          On average <strong>{{ avg }}</strong> active tickets over the last 14 days<span v-if="stats.averageActive > wipLimit" class="warn">
            , above your limit of {{ wipLimit }}</span><span v-else>, within your limit of {{ wipLimit }}</span>.
        </p>
      </section>

      <section data-stats-done>
        <h3>Finished per week</h3>
        <ul class="bars">
          <li v-for="w in stats.doneByWeek" :key="w.start" :title="`week from ${w.start}: ${w.count}`">
            <span class="bar" :style="{ height: (w.count / topWeek) * 100 + '%' }" />
            <span class="n">{{ w.count }}</span>
            <span class="muted wk">{{ w.start.slice(5) }}</span>
          </li>
        </ul>
      </section>

      <section data-stats-time>
        <h3>Where the time went</h3>
        <p v-if="!stats.perTicket.length" class="muted">No ticket has been active or blocked yet.</p>
        <table v-else>
          <thead>
            <tr><th>Ticket</th><th>Active</th><th>Blocked</th><th>Sessions</th></tr>
          </thead>
          <tbody>
            <tr v-for="t in stats.perTicket" :key="t.id" :data-stat-row="t.id">
              <td class="clip" :title="t.label">{{ t.label }}</td>
              <td>{{ formatDuration(t.activeMs) }}</td>
              <td :class="{ muted: !t.blockedMs }">{{ t.blockedMs ? formatDuration(t.blockedMs) : '—' }}</td>
              <td>{{ t.sessions }}</td>
            </tr>
          </tbody>
        </table>
      </section>

      <section data-stats-discoveries>
        <h3>Discoveries</h3>
        <p v-if="!d.total" class="muted">None noted yet.</p>
        <p v-else>
          {{ d.total }} noted: {{ d.open }} still open, {{ d.promoted }} became their own ticket, {{ d.inScope }} turned out to be in scope, {{ d.dropped }} dropped.
        </p>
      </section>
    </template>
  </div>
</template>
