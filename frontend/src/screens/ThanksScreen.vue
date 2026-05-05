<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { formatMoney } from '../api/coffee'
import { useCartStore } from '../stores/cart'

const cart = useCartStore()

const order = computed(() => cart.lastOrder)
</script>

<template>
  <main class="page-shell page-shell--centered">
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
        <RouterLink class="button" to="/">Order another coffee</RouterLink>
        <RouterLink class="button button--secondary" to="/admin">Admin view</RouterLink>
      </div>
    </section>
  </main>
</template>
