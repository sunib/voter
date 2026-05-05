<script setup lang="ts">
import { computed } from 'vue'
import SessionStateBanner from '../session/SessionStateBanner.vue'
import { useSessionStore } from '../../stores/session'

const props = defineProps<{ title?: string }>()
const sessionStore = useSessionStore()

const sessionState = computed(() => sessionStore.currentInfo?.data?.state)
const sessionTitle = computed(() => sessionStore.currentInfo?.data?.title)
const displayTitle = computed(() => sessionTitle.value ?? props.title ?? 'Attendee')
</script>

<template>
  <header class="sticky-surface sticky top-0 z-10">
    <div class="mx-auto flex w-full max-w-md items-center justify-between gap-3 px-4 py-3">
      <div class="min-w-0">
        <div class="top-bar__eyebrow text-xs font-bold tracking-[0.18em]">YAML Voter</div>
        <div class="truncate text-base font-semibold">{{ displayTitle }}</div>
      </div>

      <div class="flex items-center gap-2">
        <SessionStateBanner :state="sessionState" />
        <div class="h-2.5 w-2.5 rounded-full bg-[rgb(var(--accent))] shadow-[0_0_0_4px_rgba(0,214,255,0.12)]" />
      </div>
    </div>
  </header>
</template>
