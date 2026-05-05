<script setup lang="ts">
import { onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()

onMounted(async () => {
  await router.replace({
    name: 'login',
    query: route.query,
  })
})
</script>

<template>
  <main class="page-shell page-shell--centered">
    <section class="panel">
      <h2>Moving to login…</h2>
    </section>
  </main>
</template>
