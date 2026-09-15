<script setup lang="ts">
// The quiz half of the demo, and only that.
//
// This page used to open with a badge repeating the name already in the top
// bar, and close with an invitation to the coffee bar that is already a tab up
// there. Both were there when the page was the front door and coffee was hidden
// behind a link. Neither is true any more, and a room looking at a projector
// should see the rounds, not a second copy of the navigation.
import { computed, onMounted, ref } from 'vue'

import AppShell from '../components/layout/AppShell.vue'
import { useLiveResources } from '../api/liveResources'
import { listRounds } from '../api/quiz'
import { currentSession } from '../api/session'
import type { QuizSession } from '../api/types'

const rounds = ref<QuizSession[]>([])
const error = ref('')
const busy = ref(false)

// The list arrives twice on purpose. The REST read is the first paint, and the
// fallback for an identity the stream cannot serve; the watch is what makes a
// round opening on the operator's screen reach this phone without a refresh.
const live = useLiveResources({
  group: 'examples.configbutler.ai',
  version: 'v1alpha1',
  resource: 'quizsessions',
  namespace: currentSession()?.namespace ?? '',
})

// Drafts are the presenter's scratch space: a round only exists for the room
// once it is live, and stays listed once closed so results survive.
const allRounds = computed<QuizSession[]>(() => {
  const streamed = live.items.value as unknown as QuizSession[]
  // Once synced, an EMPTY collection is the truth, not a reason to distrust the
  // stream: falling back on length would resurrect a round that had just been
  // deleted, using the REST list read minutes earlier.
  const source = live.synced() ? streamed : rounds.value
  return source
    .filter((round) => round.spec.state !== 'draft')
    .sort((a, b) =>
      (a.metadata.name ?? '').localeCompare(b.metadata.name ?? ''),
    )
})

const openRounds = computed(() =>
  allRounds.value.filter((round) => round.spec.state === 'live'),
)
const closedRounds = computed(() =>
  allRounds.value.filter((round) => round.spec.state === 'closed'),
)

async function refresh() {
  busy.value = true
  error.value = ''
  try {
    rounds.value = (await listRounds()).items
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load quizzes.'
  } finally {
    busy.value = false
  }
}

onMounted(refresh)
</script>

<template>
  <AppShell title="Quizzes">
    <section class="panel">
      <div class="section-heading">
        <h1>Quizzes</h1>
        <button class="text-link" :disabled="busy" @click="refresh">
          {{ busy ? 'Loading…' : 'Refresh' }}
        </button>
      </div>
      <p class="hero-copy">
        One vote per participant in each round. Answers and results are shared
        with the room.
      </p>

      <p v-if="error" role="alert" class="error-copy">{{ error }}</p>

      <div v-if="openRounds.length" class="round-list">
        <article
          v-for="round in openRounds"
          :key="round.metadata.uid"
          class="round-card round-card--open"
        >
          <div class="round-card__body">
            <h3>{{ round.spec.title }}</h3>
            <span class="pill pill--good">Voting is open</span>
          </div>
          <div class="round-card__actions">
            <RouterLink
              class="button"
              :to="{ name: 'answer', params: { session: round.metadata.name } }"
            >
              Answer questions
            </RouterLink>
            <RouterLink
              class="text-link"
              :to="{
                name: 'vote-results',
                params: { session: round.metadata.name },
              }"
            >
              View results
            </RouterLink>
          </div>
        </article>
      </div>

      <div v-else-if="!busy && !error" class="empty-state">
        No quiz is open right now. The presenter will open one shortly.
      </div>

      <template v-if="closedRounds.length">
        <p class="subsection-label">Closed rounds</p>
        <div class="round-list">
          <article
            v-for="round in closedRounds"
            :key="round.metadata.uid"
            class="round-card"
          >
            <div class="round-card__body">
              <h3>{{ round.spec.title }}</h3>
              <span class="pill">Voting is closed</span>
            </div>
            <div class="round-card__actions">
              <RouterLink
                class="button button--secondary"
                :to="{
                  name: 'vote-results',
                  params: { session: round.metadata.name },
                }"
              >
                View results
              </RouterLink>
            </div>
          </article>
        </div>
      </template>
    </section>
  </AppShell>
</template>
