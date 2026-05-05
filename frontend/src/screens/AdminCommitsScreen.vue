<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import {
  getCoffeeConfigChangesSnapshot,
  watchCoffeeConfigChanges,
  type ApiError,
} from '../api/coffee'
import type { CoffeeConfigChangeRecord } from '../api/coffeeTypes'
import {
  formatConflictValue,
  formatEventTimestamp,
  humanizePath,
} from '../adminFormatters'
import AdminNav from '../components/admin/AdminNav.vue'

const loading = ref(true)
const loadError = ref('')
const recentChanges = ref<CoffeeConfigChangeRecord[]>([])

let changeSource: EventSource | undefined

async function loadAdminCommits() {
  loading.value = true
  loadError.value = ''
  try {
    const snapshot = await getCoffeeConfigChangesSnapshot()
    recentChanges.value = snapshot.changes
  } catch (error) {
    loadError.value = (error as ApiError).message
  } finally {
    loading.value = false
  }
}

function openChangeStream() {
  changeSource?.close()
  changeSource = watchCoffeeConfigChanges((event) => {
    recentChanges.value = [
      event,
      ...recentChanges.value.filter((entry) => entry.id !== event.id),
    ].slice(0, 48)
  })
}

onMounted(async () => {
  await loadAdminCommits()
  if (!loadError.value) {
    openChangeStream()
  }
})

onBeforeUnmount(() => {
  changeSource?.close()
})
</script>

<template>
  <main class="page-shell page-shell--wide">
    <section class="hero-card hero-card--compact">
      <p class="eyebrow">Admin</p>
      <h1>Recent history</h1>
      <p class="hero-copy">
        This tab follows saved <code>CoffeeConfig</code> patches, including the
        actor nickname and the fields changed in each save.
      </p>
      <div class="hero-actions">
        <AdminNav />
        <span class="pill pill--neutral">
          {{ recentChanges.length }} event{{
            recentChanges.length === 1 ? '' : 's'
          }}
        </span>
      </div>
    </section>

    <section v-if="loading" class="panel">
      <h2>Loading history…</h2>
    </section>

    <template v-else>
      <section v-if="loadError" class="panel panel--danger">
        <h2>Admin request failed</h2>
        <p>{{ loadError }}</p>
      </section>

      <section class="panel recent-changes">
        <div class="section-heading">
          <div>
            <p class="eyebrow">History stream</p>
            <h2>Config history</h2>
          </div>
        </div>
        <p class="metadata-copy recent-changes__intro">
          This list is kept in memory and resets when the server app restarts. Actor
          names come from the shared demo session nickname.
        </p>

        <div v-if="recentChanges.length === 0" class="empty-state">
          No config changes have been saved since app start.
        </div>

        <ul v-else class="recent-changes__list">
          <li
            v-for="entry in recentChanges"
            :key="entry.id"
            class="recent-changes__item"
          >
            <div class="recent-changes__meta">
              <span class="pill pill--neutral">
                {{ formatEventTimestamp(entry.createdAt) }}
              </span>
              <span class="pill pill--warning">{{ entry.actor }}</span>
            </div>
            <strong>{{ entry.summary }}</strong>
            <p v-if="entry.reason" class="metadata-copy">
              {{ entry.reason }}
            </p>
            <ul class="inline-list recent-changes__fields">
              <li
                v-for="change in entry.changes.slice(0, 4)"
                :key="`${entry.id}-${change.path}`"
              >
                <strong>{{ humanizePath(change.path) }}</strong>
                <span aria-hidden="true">:</span>
                <s>{{ formatConflictValue(change.previousValue) }}</s>
                <span aria-hidden="true">→</span>
                <span>{{ formatConflictValue(change.newValue) }}</span>
              </li>
            </ul>
            <p
              v-if="entry.changes.length > 4"
              class="metadata-copy recent-changes__more"
            >
              +{{ entry.changes.length - 4 }} more field{{
                entry.changes.length - 4 === 1 ? '' : 's'
              }}
            </p>
          </li>
        </ul>
      </section>
    </template>
  </main>
</template>
