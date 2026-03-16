<script setup lang="ts">
import TopBar from './TopBar.vue'

defineProps<{ title?: string }>()
</script>

<template>
  <div class="min-h-screen">
    <TopBar :title="title" />
    <main class="mx-auto w-full max-w-md px-4 pb-10 pt-6">
      <slot />
    </main>
  </div>
</template>

