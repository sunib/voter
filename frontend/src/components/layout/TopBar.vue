<script setup lang="ts">
defineProps<{ title?: string }>()
</script>

<template>
  <header class="sticky-surface sticky top-0 z-10">
    <div class="mx-auto flex w-full max-w-md items-center justify-between gap-3 px-4 py-3">
      <div class="min-w-0">
        <div class="top-bar__eyebrow text-xs font-bold tracking-[0.18em]">YAML Voter</div>
        <div class="truncate text-base font-semibold">{{ title ?? 'Attendee' }}</div>
      </div>

      <div class="flex items-center gap-2">
        <RouterLink to="/" class="text-sm underline">Coffee</RouterLink>
        <RouterLink to="/vote" class="text-sm underline">Vote</RouterLink>
        <div class="h-2.5 w-2.5 rounded-full bg-[rgb(var(--accent))] shadow-[0_0_0_4px_rgba(0,214,255,0.12)]" />
      </div>
    </div>
  </header>
</template>
