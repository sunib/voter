<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import AdminNav from '../components/admin/AdminNav.vue'
import {
  formatMoney,
  getOrdersSnapshot,
  loginAdmin,
  watchOrders,
  type ApiError,
} from '../api/coffee'
import type { CoffeeOrderRecord } from '../api/coffeeTypes'

const loading = ref(true)
const authRequired = ref(false)
const authError = ref('')
const loadError = ref('')
const password = ref('')
const orders = ref<CoffeeOrderRecord[]>([])

let orderSource: EventSource | undefined

const orderedEvents = computed(() => [...orders.value].reverse())

async function loadAdminOrders() {
  loading.value = true
  loadError.value = ''
  try {
    const snapshot = await getOrdersSnapshot()
    orders.value = snapshot.orders
    authRequired.value = false
  } catch (error) {
    const apiError = error as ApiError
    if (apiError.status === 401) {
      authRequired.value = true
    } else {
      loadError.value = apiError.message
    }
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

async function handleLogin() {
  authError.value = ''
  try {
    await loginAdmin(password.value)
    password.value = ''
    await loadAdminOrders()
    openOrdersStream()
  } catch (error) {
    authError.value = (error as Error).message
  }
}

onMounted(async () => {
  await loadAdminOrders()
  if (!authRequired.value) {
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

    <section v-if="authRequired" class="panel admin-login">
      <div class="section-heading">
        <h2>Admin password</h2>
        <p>
          First cut only. The backend sets an admin session cookie after
          verification.
        </p>
      </div>
      <label class="field">
        <span>Password</span>
        <input
          v-model="password"
          type="password"
          placeholder="Shared password"
        />
      </label>
      <div class="hero-actions">
        <button class="button" @click="handleLogin">Unlock Admin</button>
        <span v-if="authError" class="error-copy">{{ authError }}</span>
      </div>
    </section>

    <section v-else-if="loading" class="panel">
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
            Initial state comes from <code>GET /public/admin/orders</code>;
            new events arrive over SSE.
          </p>
        </div>
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
