<script setup lang="ts">
// Set a new mail or payments key for the menu (docs/coffee-api-keys-design.md).
//
// Shown only to whoever may patch the Secret: the operator hands that to one
// person from /room (voter-audience-coffee-keys), and takes it back there. The
// grant is patch and nothing else, so this page never reads a key and never
// shows one, not even the one just sent. Each Save is one patch of one Secret,
// with this person's own token, and lands, encrypted, in coffee-settings as a
// commit in their name.
//
// Admission (voter-coffee-keys) holds what a patch may change: the value, to
// something new. The value itself is not checked; any length will do.
import { computed, reactive } from 'vue'

import { allowsNamed, type Authorization } from '../../api/authz'
import { SECRETS, mergePatch } from '../../api/kube'

const props = defineProps<{ authz: Authorization | null }>()

interface Key {
  secret: string
  label: string
  value: string
  busy: boolean
  done: string
  error: string
}

const keys = reactive<Key[]>([
  {
    secret: 'coffee-mail',
    label: 'Mail key',
    value: '',
    busy: false,
    done: '',
    error: '',
  },
  {
    secret: 'coffee-payments',
    label: 'Payments key',
    value: '',
    busy: false,
    done: '',
    error: '',
  },
])

const mine = computed(() =>
  keys.filter((key) =>
    allowsNamed(props.authz, '', SECRETS.resource, 'patch', key.secret),
  ),
)

async function save(key: Key) {
  key.busy = true
  key.done = ''
  key.error = ''
  try {
    await mergePatch(SECRETS, key.secret, { stringData: { apiKey: key.value } })
    key.value = ''
    key.done = `${key.label} set. It reaches the app with the next settings image.`
  } catch (e) {
    key.error = e instanceof Error ? e.message : 'Could not set the key.'
  } finally {
    key.busy = false
  }
}
</script>

<template>
  <section v-if="mine.length > 0" class="panel" data-testid="coffee-keys">
    <div class="section-heading">
      <h2>Set a new key</h2>
    </div>
    <p class="metadata-copy">
      You may replace these keys, and nobody can read them back, you included.
      Each save is a commit in coffee-settings in your name, encrypted.
    </p>
    <div class="form-grid">
      <form
        v-for="key in mine"
        :key="key.secret"
        class="field"
        @submit.prevent="save(key)"
      >
        <label class="field__heading" :for="`key-${key.secret}`">
          <span>{{ key.label }}</span>
          <code class="inline-code">{{ key.secret }}</code>
        </label>
        <input
          :id="`key-${key.secret}`"
          v-model="key.value"
          type="password"
          autocomplete="off"
          @input="key.done = ''"
        />
        <div class="row-actions">
          <button
            class="button"
            type="submit"
            :disabled="key.busy || key.value === ''"
          >
            {{
              key.busy
                ? 'Asking Kubernetes…'
                : `Save ${key.label.toLowerCase()}`
            }}
          </button>
        </div>
        <p v-if="key.done" role="status" class="metadata-copy">
          {{ key.done }}
        </p>
        <div v-if="key.error" class="error-copy" role="alert">
          <strong>Admin request failed</strong>
          <p>{{ key.error }}</p>
        </div>
      </form>
    </div>
  </section>
</template>
