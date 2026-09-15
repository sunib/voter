<script setup lang="ts">
// What a page needs, what you hold, and therefore what you are missing.
//
// Every screen that can be refused was explaining its own refusal in prose,
// which meant each one restated a verb list that could drift from the Role it
// described. A page declares what it NEEDS instead, and the missing half is
// worked out against the API server's own answer about the viewer's token.
//
// The rule from api/authz.ts holds here too: this EXPLAINS, it never gates. The
// buttons a page offers stay live whatever this says, so the refusal a room
// sees is a real 403 and not a disabled control. If the two ever disagree, the
// API server is right and this component is the thing that is wrong.
import { computed } from 'vue'

import AuthorizationTable from './AuthorizationTable.vue'
import { allows, type Authorization, type Requirement } from '../api/authz'

const props = withDefaults(
  defineProps<{
    authz: Authorization | null
    requirements: Requirement[]
    loading?: boolean
    error?: string
    /** Headline for the refusal. The component supplies the rest. */
    title?: string
    /** Resource to mark in the full grid, usually the page's own. */
    highlight?: string
    /** Open the disclosure by default. Off: the missing lines are the answer,
     *  and the full grid is for the person who wants to check the working. */
    expanded?: boolean
  }>(),
  {
    loading: false,
    error: '',
    title: 'You are missing permissions for this page',
    highlight: '',
    expanded: false,
  },
)

type Checked = Requirement & { held: boolean }

const checked = computed<Checked[]>(() =>
  props.requirements.map((requirement) => ({
    ...requirement,
    held: allows(
      props.authz,
      requirement.apiGroup,
      requirement.resource,
      requirement.verb,
    ),
  })),
)

const missing = computed(() => checked.value.filter((row) => !row.held))
const held = computed(() => checked.value.filter((row) => row.held))

/** Nothing is missing, or nothing has been asked yet. A page binds its own
 *  v-if to this rather than reimplementing the comparison. */
const satisfied = computed(() => missing.value.length === 0)
</script>

<template>
  <!-- Deliberately silent until the first answer arrives. Announcing a refusal
       while still asking would put "you cannot do this" in front of someone who
       can, for as long as the round trip takes. -->
  <section
    v-if="!loading && !satisfied"
    class="panel panel--danger permission-gap"
    role="alert"
  >
    <h2 class="panel-title">{{ title }}</h2>

    <p class="hero-copy">
      Kubernetes grants your identity
      <strong>{{ held.length }} of {{ checked.length }}</strong>
      of the permissions this page uses. The missing ones are not enforced by
      this application — the API server refuses them, and it will refuse them
      again if you press the button anyway.
    </p>

    <ul class="permission-gap__list">
      <li
        v-for="row in missing"
        :key="`${row.apiGroup}/${row.resource}/${row.verb}`"
        class="permission-gap__row permission-gap__row--missing"
      >
        <span class="permission-gap__mark" aria-hidden="true">✗</span>
        <span class="permission-gap__what">
          <code class="inline-code">{{ row.verb }} {{ row.resource }}</code>
          <span class="permission-gap__group">{{
            row.apiGroup === '' ? 'core' : row.apiGroup
          }}</span>
        </span>
        <span class="permission-gap__purpose">
          <span class="visually-hidden">You cannot: </span>{{ row.purpose }}
        </span>
      </li>
      <li
        v-for="row in held"
        :key="`${row.apiGroup}/${row.resource}/${row.verb}`"
        class="permission-gap__row permission-gap__row--held"
      >
        <span class="permission-gap__mark" aria-hidden="true">✓</span>
        <span class="permission-gap__what">
          <code class="inline-code">{{ row.verb }} {{ row.resource }}</code>
          <span class="permission-gap__group">{{
            row.apiGroup === '' ? 'core' : row.apiGroup
          }}</span>
        </span>
        <span class="permission-gap__purpose">
          <span class="visually-hidden">You can: </span>{{ row.purpose }}
        </span>
      </li>
    </ul>

    <!-- Collapsed by default. The lines above answer "what am I missing"; this
         answers "and what DO I have", which is a different question and a much
         longer one. Putting it inline made the panel bury the page. -->
    <details class="permission-gap__details" :open="expanded">
      <summary>Everything Kubernetes says you may do here</summary>
      <AuthorizationTable
        :authz="authz"
        :loading="loading"
        :error="error"
        :highlight="highlight"
      />
      <p class="tech-facts__links">
        <a href="/auth/rules?as=yaml" target="_blank" rel="noopener">
          The same answer as YAML
          <i class="pi pi-external-link" aria-hidden="true" />
        </a>
        <RouterLink class="text-link" to="/me"
          >Your identity and full permissions</RouterLink
        >
      </p>
    </details>

    <p class="hero-copy">
      Nothing here needs a new sign-in. If someone grants your group what is
      missing while you are on this page, these lines turn green and the page
      starts working — within a few seconds, without a reload.
    </p>

    <slot />
  </section>
</template>
