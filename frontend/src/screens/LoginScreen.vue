<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getPublicSession, loginPublic, type ApiError } from '../api/coffee'

const route = useRoute()
const router = useRouter()

const nickname = ref('')
const code = ref('')
const checkingSession = ref(true)
const busy = ref(false)
const error = ref('')

const queryCode = computed(() => {
  const raw = route.query.code
  return typeof raw === 'string' ? raw.trim() : ''
})

const nextPath = computed(() => normalizeNextPath(route.query.next))
const codeFromQr = computed(() => queryCode.value !== '')
const canSubmit = computed(
  () => nickname.value.trim() !== '' && code.value.trim() !== '',
)

watch(
  queryCode,
  (nextCode) => {
    if (nextCode !== '') {
      code.value = nextCode
    }
  },
  { immediate: true },
)

onMounted(async () => {
  try {
    await getPublicSession()
    await router.replace(nextPath.value)
  } catch (caught) {
    const apiError = caught as ApiError
    if (apiError.status !== 401) {
      error.value = apiError.message
    }
  } finally {
    checkingSession.value = false
  }
})

async function submit() {
  if (!canSubmit.value || busy.value) {
    return
  }

  busy.value = true
  error.value = ''
  try {
    await loginPublic(nickname.value, code.value)
    await router.replace(nextPath.value)
  } catch (caught) {
    error.value = (caught as Error).message
  } finally {
    busy.value = false
  }
}

function normalizeNextPath(raw: unknown): string {
  if (typeof raw !== 'string') {
    return '/'
  }

  const trimmed = raw.trim()
  if (!trimmed.startsWith('/') || trimmed.startsWith('//')) {
    return '/'
  }

  try {
    const url = new URL(trimmed, window.location.origin)
    if (url.origin !== window.location.origin) {
      return '/'
    }
    return `${url.pathname}${url.search}${url.hash}`
  } catch {
    return '/'
  }
}
</script>

<template>
  <main class="page-shell page-shell--centered">
    <section class="hero-card hero-card--compact">
      <p class="eyebrow">Demo Access</p>
      <h1>Enter</h1>
      <p class="hero-copy">
        Kies een nickname en gebruik de gedeelde toegangscode om koffie, admin
        en verborgen questionnaire-routes te openen.
      </p>
    </section>

    <section class="panel">
      <div class="section-heading">
        <div>
          <h2>Sign in</h2>
          <p class="metadata-copy">
            {{
              codeFromQr
                ? 'De code kwam al mee uit de QR.'
                : 'Open je deze pagina direct, vul dan ook de demo-code in.'
            }}
          </p>
        </div>
      </div>

      <p v-if="error" class="error-copy">{{ error }}</p>
      <p v-else-if="checkingSession" class="metadata-copy">Checking session…</p>

      <label class="field">
        <span>Nickname</span>
        <input
          v-model="nickname"
          type="text"
          maxlength="40"
          placeholder="Simon"
          :disabled="busy || checkingSession"
          @keyup.enter="submit"
        />
      </label>

      <label v-if="!codeFromQr" class="field">
        <span>Access code</span>
        <input
          v-model="code"
          type="text"
          inputmode="text"
          autocapitalize="none"
          spellcheck="false"
          placeholder="1234"
          :disabled="busy || checkingSession"
          @keyup.enter="submit"
        />
      </label>

      <div v-else class="embedded-card">
        <strong>QR code attached</strong>
        <p class="metadata-copy">
          Deze login gebruikt de code uit de link. Jij hoeft alleen nog een
          nickname te kiezen.
        </p>
      </div>

      <div class="hero-actions">
        <button
          class="button"
          :disabled="!canSubmit || busy || checkingSession"
          @click="submit"
        >
          {{ busy ? 'Entering…' : 'Continue' }}
        </button>
        <span class="metadata-copy">Na login ga je naar {{ nextPath }}</span>
      </div>
    </section>
  </main>
</template>
