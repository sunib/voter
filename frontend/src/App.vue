<script setup lang="ts">
// Chrome lives in AppShell now, not here: every signed-in screen renders the
// same top bar, so the identity and build badges ride along with it instead of
// floating over the page in a fixed corner.
</script>

<template>
  <RouterView />
</template>
