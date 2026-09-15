<script setup lang="ts">
// The live order feed, sitting deliberately next to the config editor.
//
// The Config tab edits a CoffeeConfig: a real Kubernetes object, guarded by
// RBAC and admission, and committed to Git in the editor's name. This tab shows
// what the room actually ordered, and none of that is true of it. The orders
// are an array in the Voter process. They are not reviewed, not reconciled, not
// versioned and not committed, and a pod restart forgets every one of them.
//
// That contrast is the page. Proposing Kubernetes as an application API means
// being able to say which things belong in it, and orders are the clearest
// example of something that does not.
import { computed } from 'vue'

import { formatMoney } from '../api/coffee'
import { useLiveOrders } from '../api/liveOrders'
import AdminNav from '../components/admin/AdminNav.vue'
import AppShell from '../components/layout/AppShell.vue'

const { orders, status, error, scope, capacity } = useLiveOrders()

const placed = computed(
  () => orders.value.filter((order) => order.status === 'placed').length,
)
const refused = computed(
  () => orders.value.filter((order) => order.status === 'rejected').length,
)

// Only placed orders count toward takings; a refusal was never charged. All
// orders share the config's currency, so the first one names it.
const currency = computed(() => orders.value[0]?.currency ?? 'EUR')
const takingsCents = computed(() =>
  orders.value
    .filter((order) => order.status === 'placed')
    .reduce((sum, order) => sum + order.totalPriceCents, 0),
)

// Only says something when something is wrong. A feed that is working -- or is
// merely still opening -- says so with the pill next to the heading, and a
// sentence repeating it is a line the room reads past to reach the orders.
const trouble = computed(() => {
  switch (status.value) {
    case 'retrying':
      return 'The feed dropped. Reconnecting…'
    case 'refused':
      return error.value || 'The feed is not available.'
    default:
      return ''
  }
})

function timeOf(submittedAt: string): string {
  const at = new Date(submittedAt)
  return Number.isNaN(at.getTime()) ? submittedAt : at.toLocaleTimeString()
}
</script>

<template>
  <AppShell title="Live orders" width="wide">
    <section class="hero-card hero-card--compact">
      <p class="eyebrow">Admin</p>
      <h1>Live orders</h1>
      <p class="hero-copy">
        The Config tab edits a <code>CoffeeConfig</code>, and every change to it
        becomes a Git commit. None of this does. Orders are an array in this
        pod's memory — no custom resource, no commit, nothing after a restart.
      </p>
      <div class="hero-actions">
        <AdminNav />
      </div>
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>Order stream</h2>
        <span
          class="pill"
          :class="status === 'live' ? 'pill--good' : 'pill--neutral'"
        >
          {{ status === 'live' ? 'Live' : status }}
        </span>
      </div>

      <p v-if="trouble" role="status" class="error-copy">{{ trouble }}</p>

      <div class="config-meta">
        <span class="config-meta__item"><strong>placed</strong>{{ placed }}</span>
        <span class="config-meta__item"><strong>refused</strong>{{ refused }}</span>
        <span class="config-meta__item">
          <strong>takings</strong>{{ formatMoney(currency, takingsCents) }}
        </span>
      </div>

      <div v-if="orders.length === 0" class="empty-state">
        No coffee has been ordered yet. Place one from the coffee bar and it
        shows up here without a reload.
      </div>

      <div v-else class="stack-list">
        <article
          v-for="order in orders"
          :key="order.seq"
          class="embedded-card"
        >
          <div class="row-actions">
            <strong>{{ order.who || 'Someone' }}</strong>
            <span
              class="pill"
              :class="order.status === 'placed' ? 'pill--good' : 'pill--danger'"
            >
              {{ order.status === 'placed' ? 'Placed' : 'Refused' }}
            </span>
          </div>

          <ul v-if="order.items?.length" class="inline-list">
            <li v-for="item in order.items" :key="`${order.seq}-${item.sku}`">
              {{ item.name }} × {{ item.quantity }}
              <span v-if="item.voucherApplied"> — voucher applied</span>
            </li>
          </ul>

          <!-- The refusal text is the participant's, verbatim. During the demo
               this is the line the audience is about to watch someone fix in
               the CoffeeConfig one tab over. -->
          <p v-if="order.failureMessage" class="error-copy">
            {{ order.failureMessage }}
          </p>

          <div class="row-actions">
            <span class="metadata-copy">
              {{ timeOf(order.submittedAt) }}
              <template v-if="order.voucherCode">
                · voucher {{ order.voucherCode }}
              </template>
            </span>
            <strong>{{
              formatMoney(order.currency ?? currency, order.totalPriceCents)
            }}</strong>
          </div>
        </article>
      </div>

      <p v-if="scope" class="metadata-copy feed-scope">
        This replica, since its last restart — the last {{ capacity }} orders,
        and nothing older.
      </p>
    </section>
  </AppShell>
</template>

<style scoped>
/* A footnote, not a row of the list: it needs to sit off the last card rather
   than look like the bottom of it. */
.feed-scope {
  margin-top: 1rem;
  font-size: 0.85rem;
}
</style>
