<script setup lang="ts">
// There is no login form here any more.
//
// Identity comes from Dex, and who a participant is gets decided by Room Pass
// (room code + chosen name) or by GitHub — never by this browser. The old
// screen collected a display name, an email and a generated "stable ID" and
// posted them to the backend, which trusted them. That is the exact thing the
// OIDC flow exists to remove, so this screen's only job is to hand the browser
// to /auth/login and get out of the way.
//
// A full page navigation, not a fetch: /auth/login answers with a 302 to the
// issuer, and a redirect chain to a different origin cannot be followed from
// inside XHR.
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'

import { loginURL } from '../api/session'

const route = useRoute()
const failed = ref(false)
// Arriving here from the sign-out button, rather than from a guard. The
// difference matters: Dex and Room Pass still hold their own sessions, so
// bouncing to /auth/login would hand the same identity straight back and the
// sign-out would look broken. Say what happened and let them choose.
const signedOut = computed(() => route.query.signedout === '1')

function go() {
  // loginURL keeps only a same-site path, so this cannot become an open
  // redirect; krm-foyer validates it again.
  const next = typeof route.query.next === 'string' ? route.query.next : '/'
  const connector =
    typeof route.query.connector === 'string' ? route.query.connector : ''
  window.location.assign(loginURL(next, connector))
}

onMounted(() => {
  if (signedOut.value) {
    return
  }
  // Guard against a redirect loop: if we are back here immediately after being
  // sent to login, show the button instead of bouncing forever.
  const key = 'voter:login-redirected-at'
  const last = Number(sessionStorage.getItem(key) ?? '0')
  const now = Date.now()
  if (now - last < 5000) {
    failed.value = true
    return
  }
  sessionStorage.setItem(key, String(now))
  go()
})
</script>

<template>
  <main class="login">
    <template v-if="signedOut">
      <h1>Signed out</h1>
      <p>
        The application's session cookie is gone. Your Dex login is not, and
        neither is a Room Pass enrolment — this demo cannot revoke either — so signing in
        again will normally return you to the same participant without a code.
      </p>
      <p>
        If it asks for a room code and you do not have one, the presenter's
        screen has the current one.
      </p>
      <button type="button" @click="go">Sign in again</button>
    </template>
    <template v-else-if="failed">
      <h1>Sign in</h1>
      <p>The demo could not start your session automatically.</p>
      <button type="button" @click="go">Try again</button>
    </template>
    <p v-else>Taking you to the sign-in page…</p>
  </main>
</template>

<style scoped>
.login {
  margin: 3rem auto;
  padding: 0 1rem;
  max-width: 30rem;
  font:
    18px system-ui,
    sans-serif;
}
button {
  padding: 0.8rem 1.2rem;
  font: inherit;
  border: 0;
  border-radius: 0.4rem;
  background: #1749a5;
  color: white; /* not themeable: a fixed dark blue, legible under either palette */
}
</style>
