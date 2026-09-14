<script setup lang="ts">
// The demo's front door. It used to be the coffee storefront, with voting
// hidden behind a "Vote" link and its own list page. Both halves are peers, so
// the round list that lived at /vote is now simply the first section here and
// coffee is the second -- one page fewer to explain from the stage.
import { computed, onMounted, ref } from 'vue'

import AppShell from '../components/layout/AppShell.vue'
import { listRounds } from '../api/quiz'
import { currentSession, getSession, type Session } from '../api/session'
import type { QuizSession } from '../api/types'

// Seeded from the session the router guard just fetched, so the name is on
// screen in the first paint rather than after a round trip.
const session = ref<Session | null>(currentSession())
const rounds = ref<QuizSession[]>([])
const error = ref('')
const busy = ref(false)

const openRounds = computed(() =>
  rounds.value.filter((round) => round.spec.state === 'live'),
)
const closedRounds = computed(() =>
  rounds.value.filter((round) => round.spec.state === 'closed'),
)

const displayName = computed(() => session.value?.displayName?.trim() ?? '')
const initial = computed(() => displayName.value.slice(0, 1) || '?')
const groups = computed(() => session.value?.groups ?? [])

async function refresh() {
  busy.value = true
  error.value = ''
  try {
    // Drafts are the presenter's scratch space: a round only exists for the
    // room once it is live, and stays listed once closed so results survive.
    rounds.value = (await listRounds()).items
      .filter((round) => round.spec.state !== 'draft')
      .sort((a, b) =>
        (a.metadata.name ?? '').localeCompare(b.metadata.name ?? ''),
      )
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load quizzes.'
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  session.value = await getSession()
  await refresh()
})
</script>

<template>
  <AppShell title="Home">
    <section class="hero-card">
      <p class="eyebrow">Signed in</p>
      <div class="identity-card">
        <span class="identity-card__avatar" aria-hidden="true">{{
          initial
        }}</span>
        <h1 class="identity-card__name">{{ displayName || 'Loading…' }}</h1>
        <div v-if="session" class="identity-card__facts">
          <span class="pill">{{ session.username }}</span>
          <span v-if="session.email" class="pill">{{ session.email }}</span>
          <span v-for="group in groups" :key="group" class="pill pill--good">
            {{ group }}
          </span>
        </div>
      </div>
      <p class="identity-card__note">
        This is the name Kubernetes sees on every vote and every order you make
        here. It came from Room Pass through Dex — the browser never asserted
        it.
      </p>
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>Quizzes</h2>
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

    <section class="panel">
      <div class="coffee-invite">
        <div class="coffee-invite__copy">
          <h2>Coffee shop</h2>
          <p class="hero-copy">
            Order a coffee while someone edits the menu live on stage. Prices
            and vouchers come from the same cluster objects your votes are
            written to.
          </p>
        </div>
        <RouterLink class="button" to="/coffee"
          >Open the coffee shop</RouterLink
        >
      </div>
    </section>
  </AppShell>
</template>
