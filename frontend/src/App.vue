<script setup lang="ts">
// Chrome lives in AppShell now, not here: every signed-in screen renders the
// same top bar, so the identity and build badges ride along with it instead of
// floating over the page in a fixed corner.
</script>

<template>
  <!-- Keyed by path, for the one case Vue would otherwise reuse an instance in:
       the same component rendered for two different params. /databases/a ->
       /databases/b is that case, and the instance holds a stream pinned to the
       name it was created with, so a reused one keeps watching the first
       request. Every other pair of routes renders a different component and
       would remount regardless, so this is narrower in effect than it looks. -->
  <RouterView v-slot="{ Component, route }">
    <component :is="Component" :key="route.fullPath" />
  </RouterView>
</template>
