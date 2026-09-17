<script setup lang="ts">
// Chrome lives in AppShell now, not here: every signed-in screen renders the
// same top bar, so the identity and build badges ride along with it instead of
// floating over the page in a fixed corner.
</script>

<template>
  <!-- Keyed by path: two routes share the Database editor component, and one of
       them streams a named object. Without this, navigating from one request to
       another would reuse the instance and keep watching the first one. -->
  <RouterView v-slot="{ Component, route }">
    <component :is="Component" :key="route.fullPath" />
  </RouterView>
</template>
