<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { getOrdersSnapshot, getPublicSession, watchOrders } from '../api/coffee'
import AdminNav from '../components/admin/AdminNav.vue'
import type { CoffeeOrderRecord } from '../api/coffeeTypes'
import { formatMoney, type ApiError } from '../api/coffee'

const loading = ref(true)
const loadError = ref('')
const adminNickname = ref('')
const orders = ref<CoffeeOrderRecord[]>([])

let orderSource: EventSource | undefined

const orderedEvents = computed(() => [...orders.value].reverse())

async function loadAdminOrders() {
  loading.value = true
  loadError.value = ''
  try {
    const [session, snapshot] = await Promise.all([
      getPublicSession(),
      getOrdersSnapshot(),
    ])
    adminNickname.value = session.nickname
    orders.value = snapshot.orders
  } catch (error) {
    loadError.value = (error as ApiError).message
  } finally {
    loading.value = false
  }
}

function openOrdersStream() {
  orderSource?.close()
  orderSource = watchOrders((event) => {
    orders.value = [...orders.value, event]
  })
}

onMounted(async () => {
  await loadAdminOrders()
  if (!loadError.value) {
    openOrdersStream()
  }
})

onBeforeUnmount(() => {
  orderSource?.close()
})
</script>

<template>
  <main class="page-shell page-shell--wide">
    <section class="hero-card hero-card--compact">
      <p class="eyebrow">Admin</p>
      <h1>Live orders</h1>
      <p class="hero-copy">
        This page follows the live order feed and keeps a running view of what
        people have submitted.
      </p>
      <div class="hero-actions">
        <AdminNav />
      </div>
    </section>

    <section v-if="loading" class="panel">
      <h2>Loading live orders…</h2>
    </section>

    <template v-else>
      <section v-if="loadError" class="panel panel--danger">
        <h2>Admin request failed</h2>
        <p>{{ loadError }}</p>
      </section>

      <section class="panel">
        <div class="section-heading">
          <h2>Order stream</h2>
          <p>
            Initial state comes from <code>GET /public/admin/orders</code>; new
            events arrive over SSE.
          </p>
        </div>
        <p class="metadata-copy">Signed in as {{ adminNickname }}</p>
        <div v-if="orderedEvents.length === 0" class="empty-state">
          No coffee orders yet.
        </div>
        <div v-else class="stack-list">
          <article
            v-for="order in orderedEvents"
            :key="order.orderId"
            class="embedded-card"
          >
            <div class="row-actions">
              <strong>{{ order.orderId }}</strong>
              <span
                class="pill"
                :class="
                  order.status === 'placed' ? 'pill--good' : 'pill--warning'
                "
              >
                {{ order.status }}
              </span>
            </div>
            <p class="metadata-copy">
              {{ new Date(order.submittedAt).toLocaleString() }}
            </p>
            <ul class="inline-list">
              <li
                v-for="item in order.items"
                :key="`${order.orderId}-${item.sku}`"
              >
                {{ item.name }} × {{ item.quantity }}
              </li>
            </ul>
            <div class="row-actions">
              <span>{{ order.voucherCode || 'no voucher' }}</span>
              <strong>{{
                formatMoney(order.currency, order.totalPriceCents)
              }}</strong>
            </div>
            <p v-if="order.failureMessage" class="error-copy">
              {{ order.failureCode }}: {{ order.failureMessage }}
            </p>
          </article>
        </div>
      </section>
    </template>
  </main>
</template>
