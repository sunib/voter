<script setup lang="ts">
import AppShell from '../components/layout/AppShell.vue'
import Card from 'primevue/card'
</script>

<template>
  <AppShell title="Thanks">
    <Card class="rounded-[var(--radius)]">
      <template #content>
        <div class="p-5">
          <div class="space-y-3 text-center">
            <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-2xl bg-[rgba(0,214,255,0.18)]">
              <div class="h-2.5 w-2.5 rounded-full bg-[rgb(var(--accent))]" />
            </div>

            <h1 class="text-2xl font-extrabold">Submitted</h1>
            <p class="text-sm text-black/60">You can close this tab.</p>
          </div>
        </div>
      </template>
    </Card>
  </AppShell>
</template>
