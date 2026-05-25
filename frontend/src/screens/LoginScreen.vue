<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getPublicSession, loginPublic, type ApiError } from '../api/coffee'
import {
  defaultDisplayName,
  defaultEmail,
  getOrGenerateStableID,
} from '../lib/demoIdentity'

const route = useRoute()
const router = useRouter()

const stableId = ref('')
const displayName = ref('')
const email = ref('')
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
  () =>
    displayName.value.trim() !== '' &&
    email.value.trim() !== '' &&
    code.value.trim() !== '',
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
  stableId.value = getOrGenerateStableID()
  displayName.value = defaultDisplayName(stableId.value)
  email.value = defaultEmail(stableId.value)

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
    await loginPublic({
      code: code.value.trim(),
      stableId: stableId.value,
      displayName: displayName.value.trim(),
      email: email.value.trim(),
    })
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
        Press <strong>Join the demo</strong> to enter with the pre-filled
        defaults, or personalize your display name and email to see them in the
        commit history.
      </p>
    </section>

    <section class="panel">
      <div class="section-heading">
        <div>
          <h2>Sign in</h2>
          <p class="metadata-copy">
            {{
              codeFromQr
                ? 'The code was included from the QR.'
                : 'If you opened this page directly, also fill in the demo code.'
            }}
          </p>
        </div>
      </div>

      <p v-if="error" class="error-copy">{{ error }}</p>
      <p v-else-if="checkingSession" class="metadata-copy">Checking session…</p>

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
          This login uses the code from the link. You can now press
          <strong>Join the demo</strong> directly.
        </p>
      </div>

      <label class="field">
        <span>Display name</span>
        <input
          v-model="displayName"
          type="text"
          maxlength="64"
          :disabled="busy || checkingSession"
          @keyup.enter="submit"
        />
        <small class="metadata-copy"
          >Shown as the commit author on your changes.</small
        >
      </label>

      <label class="field">
        <span>Email</span>
        <input
          v-model="email"
          type="email"
          inputmode="email"
          autocapitalize="none"
          spellcheck="false"
          :disabled="busy || checkingSession"
          @keyup.enter="submit"
        />
        <small class="metadata-copy"
          >Used as the commit author email. Replace it with your own to see
          your real address in Git.</small
        >
      </label>

      <div class="hero-actions">
        <button
          class="button"
          :disabled="!canSubmit || busy || checkingSession"
          @click="submit"
        >
          {{ busy ? 'Entering…' : 'Join the demo' }}
        </button>
        <span class="metadata-copy">After login you'll go to {{ nextPath }}</span>
      </div>
    </section>
  </main>
</template>
