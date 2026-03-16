<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import AppShell from '../components/layout/AppShell.vue'
import Card from 'primevue/card'
import InputOtp from 'primevue/inputotp'
import Message from 'primevue/message'
import ProgressSpinner from 'primevue/progressspinner'
import { useSessionStore } from '../stores/session'
import { useDeviceSessionStore } from '../stores/deviceSession'

const route = useRoute()
const router = useRouter()

const sessionStore = useSessionStore()
const deviceSession = useDeviceSessionStore()

const initialCode = computed(() => {
  const c = route.query.code
  return typeof c === 'string' ? c.trim() : ''
})

const joinCode = ref(initialCode.value)
const busy = ref(false)
const error = ref<string | null>(null)
const lastAttemptedCode = ref<string | null>(null)

const normalizedJoinCode = computed(() => joinCode.value.trim())
const canAttemptJoin = computed(() => normalizedJoinCode.value.length === 4)

async function attemptJoin() {
  error.value = null
  const code = normalizedJoinCode.value
  if (!code) {
    error.value = 'Missing join code.'
    return
  }

  busy.value = true
  try {
    // This request both:
    // - validates/bootstraps the device session (forwardAuth)
    // - loads the session definition for the next screen
    const info = await sessionStore.fetchSessionInfo({ joinCode: code })
    await sessionStore.ensureLoaded(info.name, { joinCode: code })
    deviceSession.markAuthedOnce()

    // URL hygiene: remove join code as soon as we have a successful authed request.
    // Keep the user on /join (no navigation) only long enough to remove the sensitive value.
    const url = new URL(window.location.href)
    url.searchParams.delete('code')
    window.history.replaceState({}, '', url.toString())

    await router.replace({ name: 'answer', params: { session: info.name } })
  } catch (e: any) {
    error.value = e?.message ?? 'Join failed'
  } finally {
    busy.value = false
  }
}

watch(
  normalizedJoinCode,
  (next, prev) => {
    if (next !== prev) {
      error.value = null
    }

    if (!canAttemptJoin.value) {
      lastAttemptedCode.value = null
      return
    }

    if (busy.value) return
    if (lastAttemptedCode.value === next) return

    lastAttemptedCode.value = next
    void attemptJoin()
  },
  { immediate: true },
)
</script>

<template>
  <AppShell title="Join">
    <Card class="rounded-[var(--radius)]">
      <template #content>
        <div class="p-5">
          <div class="space-y-4">
            <div>
              <h1 class="text-2xl font-extrabold">Join session</h1>
              <p class="mt-1 text-sm text-black/60">
                Scan → join → answer. No account.
              </p>
            </div>

            <Message v-if="error" severity="error">
              {{ error }}
            </Message>

            <label class="grid gap-2" for="join_code">
              <span class="text-xs font-bold tracking-[0.18em] text-[rgb(var(--muted))]">JOIN CODE</span>
              <InputOtp
                id="join_code"
                v-model="joinCode"
                :length="4"
                :disabled="busy"
                class="w-full"
                input-class="w-full"
              />
            </label>

            <Message v-if="busy" severity="info" :closable="false">
              <div class="flex items-center gap-2">
                <ProgressSpinner
                  style="width: 1rem; height: 1rem"
                  stroke-width="6"
                  animation-duration=".8s"
                  aria-label="Joining session"
                />
                <span>Trying code {{ normalizedJoinCode }}…</span>
              </div>
            </Message>

            <p v-else class="text-xs text-black/55">Enter 4 digits and we’ll join automatically.</p>

            <p class="text-xs text-black/55">
              Your device session is stored as a secure cookie. The join code is never persisted.
            </p>
          </div>
        </div>
      </template>
    </Card>
  </AppShell>
</template>
