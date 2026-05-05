<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { getPublicSession, type ApiError } from '../../api/coffee'

const route = useRoute()

const nickname = ref('')

const visible = computed(
  () => route.name !== 'login' && nickname.value.trim().length > 0,
)

async function loadSession() {
  if (route.name === 'login') {
    nickname.value = ''
    return
  }

  try {
    const session = await getPublicSession()
    nickname.value = session.nickname.trim()
  } catch (error) {
    if ((error as ApiError).status === 401) {
      nickname.value = ''
    }
  }
}

watch(
  () => route.fullPath,
  () => {
    if (route.name === 'login') {
      nickname.value = ''
      return
    }
    if (nickname.value.trim() !== '') {
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
    :title="`Signed in as ${nickname}`"
    aria-label="Current user"
  >
    <span class="session-badge__icon-wrap" aria-hidden="true">
      <i class="pi pi-user session-badge__icon" />
    </span>
    <span class="session-badge__label">signed in</span>
    <strong class="session-badge__name">{{ nickname }}</strong>
  </div>
</template>
