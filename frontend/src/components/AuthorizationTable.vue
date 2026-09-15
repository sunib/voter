<script setup lang="ts">
// What Kubernetes says this identity may do, as a grid.
//
// The blanks carry the argument. Before the operator grants it, the
// coffeeconfigs row has get/list/watch and nothing else, and a reader can see
// the missing `patch` before they ever press a button and get refused. That
// order -- read the rule, then watch it come true -- is better than being
// refused and then told why.
//
// This component renders and nothing else. It never decides anything: every
// cell comes from a SelfSubjectRulesReview the API server answered with the
// viewer's own token.
import { computed } from 'vue'

import {
  DISPLAY_VERBS,
  extraVerbs,
  type Authorization,
  type AuthzRule,
} from '../api/authz'

const props = withDefaults(
  defineProps<{
    authz: Authorization | null
    loading?: boolean
    error?: string
    /** Resource to mark as the one under discussion, e.g. "coffeeconfigs" on
     *  the admin page. Purely presentational. */
    highlight?: string
  }>(),
  { loading: false, error: '', highlight: '' },
)

/** "" is the core API group, which has no name to print. Showing an empty cell
 *  there would read as missing data rather than as "core". */
function groupLabel(rule: AuthzRule): string {
  return rule.apiGroup === '' ? 'core' : rule.apiGroup
}

function holds(rule: AuthzRule, verb: string): boolean {
  return rule.verbs.includes(verb) || rule.verbs.includes('*')
}

const rows = computed(() => props.authz?.rules ?? [])

// Rules the apiserver reports that are not about a namespaced resource the
// table can lay out -- a bare "*" on everything, typically, which is what an
// operator identity gets. Worth naming rather than rendering as 40 blank rows.
const wildcardRow = computed(() =>
  rows.value.find((rule) => rule.apiGroup === '*' && rule.resource === '*'),
)
</script>

<template>
  <div class="authz">
    <p v-if="loading && !authz" class="metadata-copy">
      Asking Kubernetes what you may do…
    </p>

    <p v-else-if="error && !authz" class="error-copy" role="alert">
      {{ error }}
    </p>

    <template v-else-if="authz">
      <!-- A stale table is better than an empty one, but the reader has to
           know which they are looking at. -->
      <p v-if="error" class="error-copy" role="status">
        {{ error }} Showing the last answer that arrived.
      </p>

      <p v-if="wildcardRow" class="authz__wildcard">
        This identity holds <strong>every verb on every resource</strong> in
        <code class="inline-code">{{ authz.namespace }}</code
        >. That is an operator, not a participant.
      </p>

      <div class="authz__scroll">
        <table class="authz__table">
          <caption class="authz__caption">
            In namespace
            <code class="inline-code">{{ authz.namespace }}</code
            >, according to the API server
          </caption>
          <thead>
            <tr>
              <th scope="col" class="authz__resource-head">Resource</th>
              <th v-for="verb in DISPLAY_VERBS" :key="verb" scope="col">
                {{ verb }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="rule in rows"
              :key="`${rule.apiGroup}/${rule.resource}/${(rule.names ?? []).join(',')}`"
              :class="{ 'authz__row--highlight': rule.resource === highlight }"
            >
              <th scope="row" class="authz__resource">
                <span class="authz__resource-name">{{ rule.resource }}</span>
                <span class="authz__group">{{ groupLabel(rule) }}</span>
                <!-- A grant restricted to named objects is not the same as the
                     verb on everything, and a row that hid the restriction
                     would overstate what the holder can do. -->
                <span v-if="(rule.names ?? []).length" class="authz__names">
                  only: {{ (rule.names ?? []).join(', ') }}
                </span>
                <span v-if="extraVerbs(rule).length" class="authz__names">
                  also: {{ extraVerbs(rule).join(', ') }}
                </span>
              </th>
              <td v-for="verb in DISPLAY_VERBS" :key="verb">
                <span v-if="holds(rule, verb)" class="authz__yes">
                  <i class="pi pi-check" aria-hidden="true" />
                  <span class="visually-hidden">may {{ verb }}</span>
                </span>
                <span v-else class="authz__no">
                  <span aria-hidden="true">·</span>
                  <span class="visually-hidden">may not {{ verb }}</span>
                </span>
              </td>
            </tr>
            <tr v-if="!rows.length">
              <td :colspan="DISPLAY_VERBS.length + 1" class="authz__empty">
                Kubernetes grants this identity nothing at all in this
                namespace.
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Not decoration. An authorizer that cannot enumerate makes this list a
           floor rather than the whole answer, and presenting a short list as
           the truth in front of a room is exactly the overclaim to avoid. -->
      <p v-if="authz.incomplete" class="error-copy" role="status">
        This answer is incomplete — an authorizer could not enumerate its rules,
        so there may be permissions not listed here.
        <template v-if="authz.evaluationError">
          Kubernetes said: {{ authz.evaluationError }}
        </template>
      </p>
    </template>
  </div>
</template>
