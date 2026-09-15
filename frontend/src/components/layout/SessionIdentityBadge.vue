<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { currentSession, getSession } from '../../api/session'

const route = useRoute()

// The router guard calls getSession() before every protected navigation, so
// the answer is already in hand by the time this renders. Starting from the
// cached session keeps the top bar from flickering blank on each screen change
// and saves a round trip per navigation; the fetch below is only the fallback
// for the first paint after a reload.
const displayName = ref(currentSession()?.displayName.trim() ?? '')

const visible = computed(
  () => route.name !== 'login' && displayName.value.trim().length > 0,
)

async function loadSession() {
  // getSession returns null when signed out rather than throwing: "not logged
  // in" is an expected answer for a badge, not an error.
  const session = await getSession()
  displayName.value = session === null ? '' : session.displayName.trim()
}

watch(
  () => route.fullPath,
  () => {
    if (route.name === 'login') {
      displayName.value = ''
      return
    }
    const cached = currentSession()?.displayName.trim() ?? ''
    if (cached !== '') {
      displayName.value = cached
      return
    }
    if (displayName.value.trim() !== '') {
      return
    }
    void loadSession()
  },
  { immediate: true },
)
</script>

<template>
  <!-- A link, not a label: this is the one element carrying your name on every
       screen, so it is where somebody looks to ask what "signed in" means here.
       It leads to /me, which answers in full. -->
  <RouterLink
    v-if="visible"
    to="/me"
    class="session-badge session-badge--link"
    :title="`Signed in as ${displayName} — open your identity`"
    aria-label="Your identity"
  >
    <span class="session-badge__icon-wrap" aria-hidden="true">
      <i class="pi pi-user session-badge__icon" />
    </span>
    <span class="session-badge__label">signed in</span>
    <strong class="session-badge__name">{{ displayName }}</strong>
  </RouterLink>
</template>
