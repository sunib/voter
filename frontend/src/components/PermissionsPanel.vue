<script setup lang="ts">
import AuthorizationTable from './AuthorizationTable.vue'
import { useAuthorization } from '../api/useAuthorization'

// Live, not fetched once: an operator can widen the audience's grant while
// this page is open, and the row that changes should change here too.
const {
  authz,
  error: authzError,
  loading: authzLoading,
  mode: authzMode,
} = useAuthorization()
</script>

<template>
  <section class="panel">
    <div class="section-heading">
      <h2>What you may do</h2>
    </div>
    <p class="hero-copy">
      <code class="inline-code">kubectl auth can-i --list</code>, answered by
      the API server with your own token. This app decides none of it.
    </p>
    <AuthorizationTable
      :authz="authz"
      :loading="authzLoading"
      :error="authzError"
    />
    <p class="metadata-copy" data-testid="authz-mode">
      {{
        authzMode === 'watch'
          ? 'Asked again whenever a RoleBinding in this namespace changes.'
          : 'Asked again every few seconds: the RoleBindings here cannot be watched right now.'
      }}
    </p>
    <details v-if="authz" class="raw-answer">
      <summary>The raw answer</summary>
      <pre>{{ JSON.stringify(authz.review, null, 2) }}</pre>
    </details>
    <p class="hero-copy">
      It refreshes by itself. When someone grants you something, a new row
      shows up within seconds.
    </p>
  </section>
</template>
