<script setup lang="ts">
// Everything the browser knows about who it is, on a page of its own.
//
// This used to be a <details> block on the home page, which put ten technical
// fields in front of somebody who came to answer a quiz. It is reached from the
// badge carrying your name in the top bar -- the place you already look to
// answer "who am I signed in as".
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import AppShell from '../components/layout/AppShell.vue'
import {
  currentSession,
  getSession,
  logout,
  type Session,
} from '../api/session'

const router = useRouter()
const session = ref<Session | null>(currentSession())
const signingOut = ref(false)
const signOutError = ref('')

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

async function signOut() {
  const token = session.value?.csrfToken ?? ''
  signingOut.value = true
  signOutError.value = ''
  try {
    await logout(token)
    // Not a router push to "/": the guard would send it to /login, which takes
    // itself straight back to /auth/login, and Dex would hand the same
    // identity back before anyone read anything. The signed-out screen is the
    // honest end of this action, and it is where the way back in lives.
    await router.push({ name: 'login', query: { signedout: '1' } })
  } catch (cause) {
    signOutError.value =
      cause instanceof Error ? cause.message : 'Signing out failed.'
  } finally {
    signingOut.value = false
  }
}

onMounted(async () => {
  session.value = await getSession()
})
</script>

<template>
  <AppShell title="Your identity">
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
    </section>

    <section v-if="session" class="panel">
      <div class="section-heading">
        <h2>What this browser was told</h2>
      </div>
      <p class="hero-copy">
        Every field <code class="inline-code">/auth/session</code> returned,
        under its own key names. Kubernetes authorizes
        <code class="inline-code">username</code>, not your display name; for a
        room participant that is Dex's opaque subject — deliberately so, since
        nobody can collide with it by typing your name into the join form.
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
      <p class="hero-copy">
        The YAML is a live SelfSubjectReview: the API server answering, with
        your token, about your token. There is no stored login object to read —
        an identity in Kubernetes is derived per request, never persisted — so
        that answer is the closest thing to one that exists.
      </p>
    </section>

    <!-- Deliberately plain, deliberately last, and the warning comes before the
         button rather than after it. Nothing here is styled to invite a press:
         during a demo there is no reason to sign out, and the cost of doing it
         by accident is paid by the person who did it, on stage. -->
    <section class="panel sign-out">
      <div class="section-heading">
        <h2 class="sign-out__heading">Signing out</h2>
      </div>
      <p class="sign-out__copy">
        This clears the application's session cookie and nothing else. Your Dex
        login and your Room Pass enrolment stay where they are, so signing in
        again normally brings you straight back as the same participant, with no
        code to type.
      </p>
      <p class="sign-out__copy">
        <strong>Normally.</strong> If the room has been stopped, enrolment has
        closed, your participant was revoked, or your Room Pass session has
        expired in the meantime, then the way back in is a fresh join code from
        the presenter's screen — and during a talk there may not be one going
        spare. There is no reason to sign out while the demo is running.
      </p>
      <p v-if="signOutError" role="alert" class="error-copy">
        {{ signOutError }}
      </p>
      <button
        type="button"
        class="sign-out__button"
        :disabled="signingOut || !session"
        @click="signOut"
      >
        {{ signingOut ? 'Signing out…' : 'Sign out anyway' }}
      </button>
    </section>
  </AppShell>
</template>
