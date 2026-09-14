<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import AppShell from '../components/layout/AppShell.vue'
import { formatMoney } from '../api/coffee'
import { useCartStore } from '../stores/cart'

const cart = useCartStore()

const order = computed(() => cart.lastOrder)
</script>

<template>
  <AppShell title="Order confirmation">
    <section class="hero-card hero-card--compact">
      <p class="eyebrow">Order confirmation</p>
      <h1>{{ order?.status === 'placed' ? 'Coffee order placed' : 'Order finished' }}</h1>
      <p class="hero-copy">
        <template v-if="order">
          {{ order.orderId }} for {{ formatMoney(order.currency, order.totalPriceCents) }}.
        </template>
        <template v-else>
          Your last coffee order has finished processing.
        </template>
      </p>
      <div class="hero-actions">
        <RouterLink class="button" to="/coffee">Order another coffee</RouterLink>
        <RouterLink class="button button--secondary" to="/">Back to quizzes</RouterLink>
      </div>
    </section>
  </AppShell>
</template>
