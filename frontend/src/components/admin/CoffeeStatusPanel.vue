<script setup lang="ts">
// What Voter says about the menu: its status, read from the object every reader of
// this page already streams, so it moves on its own. Nothing here is editable, and
// it is never the draft: it says what the server holds and how Voter judged that.
import { computed } from 'vue'

import {
  formatFirstOrderTime,
  summarizeCoffeeStatus,
} from '../../api/coffeeStatus'
import type { CoffeeConfig } from '../../api/coffeeTypes'

const props = defineProps<{ config: CoffeeConfig | null | undefined }>()
const summary = computed(() => summarizeCoffeeStatus(props.config))
</script>

<template>
  <article v-if="summary" class="panel coffee-status" aria-label="Menu status">
    <div class="section-heading">
      <h2>Status</h2>
      <span v-if="summary.behind" class="pill pill--neutral">Checking</span>
      <span v-else-if="summary.ready" class="pill pill--good">Ready</span>
      <span v-else class="pill pill--danger">Not ready</span>
    </div>

    <p v-if="summary.behind" class="coffee-status__lead">
      Voter has not looked at this version of the menu yet.
    </p>
    <p v-else-if="!summary.ready" class="coffee-status__lead">
      {{ summary.message }}
    </p>

    <div class="config-meta">
      <span class="config-meta__item">
        <strong>Generation</strong>
        <code>{{ summary.generation ?? 'unknown' }}</code>
      </span>
      <span class="config-meta__item">
        <strong>Observed</strong>
        <code>{{ summary.observedGeneration ?? 'unknown' }}</code>
      </span>
      <span class="config-meta__item">
        <strong>First order</strong>
        <template v-if="summary.firstOrderTime">{{
          formatFirstOrderTime(summary.firstOrderTime)
        }}</template>
        <template v-else>None yet</template>
      </span>
    </div>

    <ul class="coffee-status__parts">
      <li v-for="part in summary.parts" :key="part.type">
        <span
          class="pill"
          :class="part.healthy ? 'pill--good' : 'pill--danger'"
          >{{ part.label }}</span
        >
        <span>
          {{ part.message }}
          <code v-if="!part.healthy">{{ part.reason }}</code>
        </span>
      </li>
    </ul>
  </article>
</template>
