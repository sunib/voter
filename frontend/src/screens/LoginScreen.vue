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
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const failed = ref(false)

function loginUrl(): string {
  const params = new URLSearchParams()
  const next = typeof route.query.next === 'string' ? route.query.next : ''
  // Only same-site paths: a full URL here would turn login into an open
  // redirect. The backend validates this again.
  if (next.startsWith('/') && !next.startsWith('//')) {
    params.set('return', next)
  }
  const connector = typeof route.query.connector === 'string' ? route.query.connector : ''
  if (connector !== '') {
    params.set('connector', connector)
  }
  const query = params.toString()
  return query === '' ? '/auth/login' : `/auth/login?${query}`
}

function go() {
  window.location.assign(loginUrl())
}

onMounted(() => {
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
    <template v-if="failed">
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
  color: white;
}
</style>
