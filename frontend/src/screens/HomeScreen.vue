<script setup lang="ts">
// The demo's front door. It used to be the coffee storefront, with voting
// hidden behind a "Vote" link and its own list page. Both halves are peers, so
// the round list that lived at /vote is now simply the first section here and
// coffee is the second -- one page fewer to explain from the stage.
import { computed, onMounted, ref } from 'vue'

import AppShell from '../components/layout/AppShell.vue'
import { useLiveResources } from '../api/liveResources'
import { listRounds } from '../api/quiz'
import { currentSession, getSession, type Session } from '../api/session'
import type { QuizSession } from '../api/types'

// Seeded from the session the router guard just fetched, so the name is on
// screen in the first paint rather than after a round trip.
const session = ref<Session | null>(currentSession())
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

const displayName = computed(() => session.value?.displayName?.trim() ?? '')
const initial = computed(() => displayName.value.slice(0, 1) || '?')
const kubeUsername = computed(() => session.value?.username?.trim() ?? '')

function formatExpiry(epochSeconds: number): string {
  if (!epochSeconds) {
    return 'unknown'
  }
  const when = new Date(epochSeconds * 1000)
  const minutes = Math.round((when.getTime() - Date.now()) / 60000)
  const left =
    minutes <= 0
      ? 'expired'
      : minutes < 60
        ? `in ${minutes} min`
        : `in ${Math.round(minutes / 60)} h`
  return `${when.toLocaleString()} (${left})`
}

// The CSRF token is shown by length and first characters only. It is not a
// Kubernetes credential and it is useless without the HttpOnly cookie, so this
// is not a secrecy claim -- it keeps a 43-character random string off a
// projector. The raw JSON link below shows it in full for anyone who wants it.
function abbreviate(token: string): string {
  return token === ''
    ? '(none)'
    : `${token.slice(0, 6)}… (${token.length} chars)`
}

// Every field /auth/session hands this browser, in the order the endpoint
// returns them. Built as data rather than markup so adding a field to the
// response is one line here, not a new row of template.
const technicalFacts = computed(() => {
  const s = session.value
  if (s === null) {
    return []
  }
  return [
    { label: 'authenticated', value: String(s.authenticated) },
    { label: 'username', value: s.username || '(the review failed)' },
    { label: 'displayName', value: s.displayName },
    { label: 'email', value: s.email || '(none)' },
    {
      label: 'groups',
      value: s.groups.length ? s.groups.join(', ') : '(none)',
    },
    { label: 'csrfToken', value: abbreviate(s.csrfToken) },
    { label: 'expiresAt', value: formatExpiry(s.expiresAt) },
    { label: 'namespace', value: s.namespace },
    { label: 'coffeeConfigName', value: s.coffeeConfigName },
    { label: 'roomName', value: s.roomName },
  ]
})

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
          <span v-if="session.email" class="pill pill--fact">
            <span class="pill__label">email</span>
            {{ session.email }}
          </span>
          <!-- The Kubernetes name links to the apiserver's own answer about
               this login. It is a real object, fetched with this browser's
               token, not a page about one. -->
          <a
            v-if="kubeUsername"
            class="pill pill--fact pill--link"
            href="/auth/whoami"
            target="_blank"
            rel="noopener"
          >
            <span class="pill__label">kubernetes</span>
            {{ kubeUsername }}
            <i class="pi pi-external-link" aria-hidden="true" />
          </a>
        </div>
      </div>
      <p class="identity-card__note">
        The display name is a label. The Kubernetes name beside it is the one on
        every vote and every order you make here, and it is what RBAC matches on
        — it came from Room Pass through Dex, and the browser never asserted
        either of them.
      </p>
      <!-- Folded away because it is the whole payload, not because it is
           secret. The two facts worth reading at a glance -- the address the
           login carries and the name Kubernetes matches on -- are above; this
           is for the person who wants to see every field the endpoint
           returned. -->
      <details v-if="session" class="identity-technical">
        <summary>Technical identity</summary>
        <p>
          Everything this browser was told about itself, exactly as
          <code class="inline-code">/auth/session</code> returned it. Kubernetes
          authorizes <code class="inline-code">username</code>, not your display
          name; for a room participant that is Dex's opaque subject —
          deliberately so, since nobody can collide with it by typing your name
          into the join form.
        </p>
        <dl class="tech-facts">
          <div
            v-for="fact in technicalFacts"
            :key="fact.label"
            class="tech-facts__row"
          >
            <dt>{{ fact.label }}</dt>
            <dd>{{ fact.value }}</dd>
          </div>
        </dl>
        <p class="tech-facts__links">
          <a href="/auth/whoami" target="_blank" rel="noopener">
            Your login object as YAML
            <i class="pi pi-external-link" aria-hidden="true" />
          </a>
          <a href="/auth/session" target="_blank" rel="noopener">
            The raw JSON, CSRF token and all
            <i class="pi pi-external-link" aria-hidden="true" />
          </a>
        </p>
        <p>
          The YAML is a live SelfSubjectReview: the API server answering, with
          your token, about your token. There is no stored login object to read
          — an identity in Kubernetes is derived per request, never persisted —
          so that answer is the closest thing to one that exists.
        </p>
      </details>
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
          <h2>Coffee bar</h2>
          <p class="hero-copy">
            Order a coffee while someone edits the menu live on stage. Prices
            and vouchers come from the same cluster objects your votes are
            written to.
          </p>
        </div>
        <RouterLink class="button" to="/coffee">Open the coffee bar</RouterLink>
      </div>
    </section>
  </AppShell>
</template>
