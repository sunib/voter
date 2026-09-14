<script setup lang="ts">
// What a failing stream will not tell you from the stage.
//
// This exists because the first failure in production reached the operator's
// screen as the single word "INTERNAL" under a heading that said they lacked
// permission -- both useless and wrong. Everything needed to name the cause is
// already in the browser; it just was not being shown. Folded away so it costs
// nothing when things work.
import type { LiveScope } from '../../api/liveResources'
import type { Session } from '../../api/session'

defineProps<{
  live: {
    error: { value: string }
    errorCode: { value: string }
    state: { value: { status: string; retries: number } }
  }
  scope: LiveScope
  session: Session | null
}>()

// The codes the gateway can report, in the words an operator needs rather than
// the words the protocol uses.
const meaning: Record<string, string> = {
  FORBIDDEN: 'Kubernetes refused this identity. RBAC, working as intended.',
  UNAUTHENTICATED: 'The credential was rejected — usually an expired session.',
  SCOPE_INVALID:
    'The backend will not serve this scope at all: it is not on the stream allowlist.',
  UPSTREAM_UNAVAILABLE:
    "The API server could not be reached, or the application's own ServiceAccount cannot watch this resource.",
  RESYNC_REQUIRED: 'The stream fell too far behind and needs a fresh snapshot.',
  SLOW_CONSUMER: 'This browser could not keep up with the stream.',
  INTERNAL:
    "Something failed inside the stream. Check the application's ServiceAccount RBAC for this resource first — the shared watch runs as the application, not as you.",
}
</script>

<template>
  <details class="stream-diagnostics">
    <summary>What went wrong</summary>
    <dl>
      <dt>Code</dt>
      <dd>
        <code>{{ live.errorCode.value || 'none' }}</code>
        <span v-if="meaning[live.errorCode.value]">
          — {{ meaning[live.errorCode.value] }}</span
        >
      </dd>

      <dt>Message</dt>
      <dd>
        <code>{{ live.error.value || 'none reported' }}</code>
      </dd>

      <dt>Connection</dt>
      <dd>
        <code>{{ live.state.value.status }}</code>
        <span v-if="live.state.value.retries">
          after {{ live.state.value.retries }} retries</span
        >
      </dd>

      <dt>Watching</dt>
      <dd>
        <code
          >{{ scope.resource }}.{{ scope.group }}/{{ scope.version }} in
          {{ scope.namespace
          }}{{ scope.name ? ` named ${scope.name}` : ' (all)' }}</code
        >
      </dd>

      <dt>As</dt>
      <dd>
        <code>{{ session?.username || 'unknown' }}</code>
      </dd>

      <dt>Groups</dt>
      <dd>
        <code>{{ session?.groups?.join(', ') || 'none' }}</code>
      </dd>
    </dl>
    <p>
      To check this from a terminal:
      <code
        >kubectl auth can-i watch {{ scope.resource
        }}{{ scope.name ? '/' + scope.name : '' }} -n
        {{ scope.namespace }}</code
      >
    </p>
  </details>
</template>
