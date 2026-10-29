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
import AuthorizationTable from '../components/AuthorizationTable.vue'
import { useAuthorization } from '../api/useAuthorization'
import {
  currentSession,
  canVote,
  getSession,
  logout,
  type Session,
} from '../api/session'
import { getWhoAmI, type WhoAmI } from '../api/whoami'

const router = useRouter()
// Polled, not fetched once: an operator can widen the audience's grant while
// this page is open, and the row that changes should change here too.
const { authz, error: authzError, loading: authzLoading } = useAuthorization()
const session = ref<Session | null>(currentSession())
const whoami = ref<WhoAmI | null>(null)
const signingOut = ref(false)
const signOutError = ref('')

const displayName = computed(() => session.value?.displayName?.trim() ?? '')
const initial = computed(() => displayName.value.slice(0, 1) || '?')
const kubeUsername = computed(
  () => whoami.value?.userInfo.username?.trim() ?? '',
)
// Where the Kubernetes name came from, said for this sign-in: Room Pass makes
// it from the subject, GitHub and LinkedIn from the email they vouch for.
const viaRoomPass = computed(() => canVote(session.value))
const provider = computed(() => {
  const connector = session.value?.connector ?? ''
  if (viaRoomPass.value) return 'Room Pass'
  if (connector === 'github') return 'GitHub'
  if (connector === 'linkedin') return 'LinkedIn'
  return connector || 'your sign-in'
})
const nameSource = computed(() => {
  if (viaRoomPass.value) return 'the subject below'
  if (provider.value === 'GitHub' || provider.value === 'LinkedIn')
    return `your ${provider.value} email`
  return 'the email your sign-in vouches for'
})

function formatExpiry(timestamp: string): string {
  const when = new Date(timestamp)
  if (timestamp === '' || Number.isNaN(when.getTime())) {
    return 'unknown'
  }
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

// Every field krm-foyer's /auth/session hands this browser, in the order the
// endpoint returns them. Built as data rather than markup so adding a field to the
// response is one line here, not a new row of template.
const technicalFacts = computed(() => {
  const s = session.value
  if (s === null) {
    return []
  }
  return [
    { label: 'authenticated', value: String(s.authenticated) },
    { label: 'issuer', value: s.issuer || '(none)' },
    { label: 'subject', value: s.subject || '(none)' },
    { label: 'email', value: s.email || '(none)' },
    { label: 'displayName', value: s.displayName },
    {
      label: 'groups',
      value: s.groups.length ? s.groups.join(', ') : '(none)',
    },
    { label: 'connector', value: s.connector || '(none)' },
    { label: 'expiresAt', value: formatExpiry(s.expiresAt) },
    { label: 'csrfToken', value: abbreviate(s.csrfToken) },
    { label: 'csrfHeader', value: s.csrfHeader || '(none)' },
  ]
})

async function signOut() {
  const fromRoomPass = canVote(session.value)
  signingOut.value = true
  signOutError.value = ''
  try {
    await logout()
    // Room Pass keeps its own session, and its sign-out is a form only its
    // /join page can post. A participant is sent there: it shows who they are
    // enrolled as, and the button that ends that too.
    if (fromRoomPass) {
      window.location.assign('/join')
      return
    }
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
  // Its own request, and allowed to fail on its own: the page is still worth
  // showing without the Kubernetes name, which then reads as missing.
  whoami.value = await getWhoAmI().catch(() => null)
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
        Your display name is just a label. Kubernetes knows you by the name next
        to it. That one is on everything you do here, and RBAC checks it. It
        came from {{ provider }} through Dex, not from this browser.
      </p>
    </section>

    <section v-if="session" class="panel">
      <div class="section-heading">
        <h2>What this browser was told</h2>
      </div>
      <p class="hero-copy">
        Everything <code class="inline-code">/auth/session</code> returned. Your
        Kubernetes name is built from {{ nameSource }}, not from your display
        name, so nobody can become you by typing your name.
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
          Who Kubernetes thinks you are
          <i class="pi pi-external-link" aria-hidden="true" />
        </a>
        <a href="/auth/session" target="_blank" rel="noopener">
          The raw JSON
          <i class="pi pi-external-link" aria-hidden="true" />
        </a>
      </p>
      <p class="hero-copy">
        The first link is a live SelfSubjectReview. Kubernetes stores no login
        anywhere; it works out who you are on every request.
      </p>
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>What you may do</h2>
      </div>
      <p class="hero-copy">
        <code class="inline-code">kubectl auth can-i --list</code>, answered by
        the API server with your own token. This app decides none of it.
      </p>
      <AuthorizationTable
        :authz="authz"
        :loading="authzLoading"
        :error="authzError"
      />
      <details v-if="authz" class="raw-answer">
        <summary>The raw answer</summary>
        <pre>{{ JSON.stringify(authz.review, null, 2) }}</pre>
      </details>
      <p class="hero-copy">
        It refreshes by itself. When someone grants you something, a new row
        shows up within seconds.
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
        This ends your session in this app only. Room Pass still knows you, so
        signing in again usually brings you straight back, no code needed. Room
        Pass then shows its own page, with a button to sign out there too.
      </p>
      <p class="sign-out__copy">
        <strong>Usually.</strong> If the room has closed or your Room Pass
        session has expired, you need a fresh code from the presenter's screen.
        During the talk: just stay signed in.
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
