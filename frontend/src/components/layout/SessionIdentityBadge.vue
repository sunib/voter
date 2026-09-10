<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { getSession } from '../../api/session'

const route = useRoute()

const displayName = ref('')

const visible = computed(
  () => route.name !== 'login' && displayName.value.trim().length > 0,
)

async function loadSession() {
  if (route.name === 'login') {
    displayName.value = ''
    return
  }

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
    if (displayName.value.trim() !== '') {
      return
    }
    void loadSession()
  },
  { immediate: true },
)
</script>

<template>
  <div
    v-if="visible"
    class="session-badge"
    :title="`Signed in as ${displayName}`"
    aria-label="Current user"
  >
    <span class="session-badge__icon-wrap" aria-hidden="true">
      <i class="pi pi-user session-badge__icon" />
    </span>
    <span class="session-badge__label">signed in</span>
    <strong class="session-badge__name">{{ displayName }}</strong>
  </div>
</template>
