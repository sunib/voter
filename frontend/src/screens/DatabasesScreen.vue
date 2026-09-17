<script setup lang="ts">
// What every team has asked the platform team for.
//
// The list arrives twice, for the same reason the round list does: the REST
// read is the first paint and the fallback for an identity the stream cannot
// serve, and the watch is what makes another team's request appear on this
// screen during the talk without anybody pressing anything.
//
// One action on this page: file a new request. Opening an existing one is the
// row itself.
import { computed, onMounted, ref } from 'vue'

import AppShell from '../components/layout/AppShell.vue'
import PermissionRequirements from '../components/PermissionRequirements.vue'
import { DATABASE_REQUIREMENTS } from '../api/authz'
import { useAuthorization } from '../api/useAuthorization'
import { listDatabases } from '../api/databases'
import {
  INTENT_ANNOTATION,
  intentSentence,
  type Database,
} from '../api/databaseTypes'
import { useLiveResources } from '../api/liveResources'
import { currentSession } from '../api/session'

const session = currentSession()!
const { authz, error: authzError, loading: authzLoading } = useAuthorization()

const read = ref<Database[]>([])
const error = ref('')
const busy = ref(false)

const live = useLiveResources({
  group: 'platform.configbutler.ai',
  version: 'v1alpha1',
  resource: 'databases',
  namespace: session.namespace,
})

const databases = computed<Database[]>(() => {
  const streamed = live.items.value as unknown as Database[]
  // Once synced, an EMPTY collection is the truth rather than a reason to fall
  // back: preferring the REST read on length would resurrect a request that had
  // just been deleted with kubectl.
  const source = live.synced() ? streamed : read.value
  return [...source].sort((a, b) =>
    (a.metadata?.name ?? '').localeCompare(b.metadata?.name ?? ''),
  )
})

/** By cost centre, because that is the column the finance people read and the
 *  one that makes "outstanding intent from all teams" look like a bill. */
const byCostCentre = computed(() => {
  const groups = new Map<string, Database[]>()
  for (const database of databases.value) {
    const key = database.spec?.owner?.costCentre || 'No cost centre'
    groups.set(key, [...(groups.get(key) ?? []), database])
  }
  return [...groups.entries()].sort(([a], [b]) => a.localeCompare(b))
})

function intentOf(database: Database): string {
  return database.metadata?.annotations?.[INTENT_ANNOTATION] ?? ''
}

/** Nothing reconciles a Database in this demo, so status.phase is empty for
 *  every one of them. Saying "Not provisioned" is honest; showing a green
 *  "Ready" pill that no controller wrote would not be. */
function phaseOf(database: Database): string {
  return database.status?.phase ?? 'Not provisioned'
}

async function refresh() {
  busy.value = true
  error.value = ''
  try {
    read.value = (await listDatabases()).items
  } catch (cause) {
    error.value =
      cause instanceof Error
        ? cause.message
        : 'Could not read the database requests.'
  } finally {
    busy.value = false
  }
}

onMounted(refresh)
</script>

<template>
  <AppShell title="Databases" width="wide">
    <section class="hero-card hero-card--compact">
      <p class="eyebrow">Platform</p>
      <h1>Outstanding database requests</h1>
      <p class="hero-copy">
        Every <code>Database</code> object in this namespace: what each team has
        asked the platform team for, in their own words. Nothing reconciles
        these yet — the declared intent is the whole of it, and that is the
        point.
      </p>
      <div class="hero-actions">
        <RouterLink class="button" :to="{ name: 'database-new' }">
          New database request
        </RouterLink>
        <button class="text-link" :disabled="busy" @click="refresh">
          {{ busy ? 'Loading…' : 'Refresh' }}
        </button>
      </div>
    </section>

    <!-- Explains, never gates. The New button above stays live whatever this
         says, so a refusal the room sees is a real 403 from the API server. -->
    <PermissionRequirements
      :authz="authz"
      :loading="authzLoading"
      :error="authzError"
      :requirements="DATABASE_REQUIREMENTS"
      title="You may not file or change database requests"
      highlight="databases"
    />

    <section v-if="error" class="panel panel--danger" role="alert">
      <h2>Could not read the requests</h2>
      <p>{{ error }}</p>
      <p class="metadata-copy">
        If this says Unauthorized or Forbidden, that is Kubernetes answering
        with your identity, not the application.
      </p>
    </section>

    <section
      v-else-if="!busy && databases.length === 0"
      class="panel"
      role="status"
    >
      <div class="empty-state">
        No team has asked for a database yet. Yours can be the first.
      </div>
    </section>

    <section
      v-for="[costCentre, requests] in byCostCentre"
      :key="costCentre"
      class="panel"
    >
      <div class="section-heading">
        <h2>{{ costCentre }}</h2>
        <span class="pill"
          >{{ requests.length }} request{{
            requests.length === 1 ? '' : 's'
          }}</span
        >
      </div>

      <div class="round-list">
        <RouterLink
          v-for="database in requests"
          :key="database.metadata?.uid"
          class="round-card database-card"
          :to="{
            name: 'database-edit',
            params: { name: database.metadata?.name },
          }"
        >
          <div class="round-card__body">
            <h3>{{ database.metadata?.name }}</h3>
            <p class="database-card__sentence">
              {{ intentSentence(database.spec) }}
            </p>
            <div class="database-card__pills">
              <span class="pill">{{ database.spec?.tier }}</span>
              <span class="pill">{{ database.spec?.size ?? 'small' }}</span>
              <span class="pill">{{
                database.spec?.region ?? 'eu-central-2'
              }}</span>
              <span class="pill">{{
                database.spec?.dataClassification ?? 'internal'
              }}</span>
              <span class="pill pill--neutral">{{ phaseOf(database) }}</span>
            </div>
            <p v-if="intentOf(database)" class="database-card__intent">
              “{{ intentOf(database) }}”
            </p>
          </div>
          <div class="round-card__actions">
            <span class="text-link">Open</span>
          </div>
        </RouterLink>
      </div>
    </section>
  </AppShell>
</template>
