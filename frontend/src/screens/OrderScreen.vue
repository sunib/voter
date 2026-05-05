<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  buildStorefrontFromConfig,
  formatMoney,
  getStorefront,
  submitOrder,
  watchCoffeeConfig,
  type ApiError,
} from '../api/coffee'
import { useCartStore } from '../stores/cart'

const cart = useCartStore()
const route = useRoute()
const router = useRouter()

const loading = ref(false)
const loadError = ref('')
const submitError = ref('')
const submitting = ref(false)

let configSource: EventSource | undefined

const voucherCode = computed(() => {
  const raw = route.query.voucher
  return typeof raw === 'string' ? raw.trim() : ''
})

const storefront = computed(() => cart.storefront)
const products = computed(() => storefront.value?.products ?? [])
const currency = computed(() => storefront.value?.shop.currency ?? cart.activeConfig?.spec.currency ?? 'EUR')

const cartRows = computed(() =>
  products.value
    .map((product) => ({
      product,
      quantity: cart.items[product.sku] ?? 0,
    }))
    .filter((row) => row.quantity > 0),
)

const invalidItems = computed(() => {
  const configProducts = new Map(
    (cart.activeConfig?.spec.products ?? []).map((product) => [product.sku, product]),
  )

  return Object.entries(cart.items)
    .filter(([, quantity]) => quantity > 0)
    .map(([sku, quantity]) => {
      const product = configProducts.get(sku)
      if (!product || !product.enabled) {
        return { sku, quantity }
      }
      return undefined
    })
    .filter((value): value is { sku: string; quantity: number } => value !== undefined)
})

const orderTotalCents = computed(() =>
  cartRows.value.reduce((sum, row) => sum + row.quantity * row.product.displayPriceCents, 0),
)

const canSubmit = computed(
  () => !loading.value && !submitting.value && cart.totalItems > 0 && invalidItems.value.length === 0,
)

const voucherTone = computed(() => {
  switch (storefront.value?.voucher.state) {
    case 'assumed-applied':
      return 'good'
    case 'invalid':
    case 'not-applicable':
      return 'warning'
    default:
      return 'neutral'
  }
})

const voucherHeadline = computed(() => {
  switch (storefront.value?.voucher.state) {
    case 'assumed-applied':
      return 'Voucher looks applied'
    case 'invalid':
      return 'Voucher could not be applied'
    case 'not-applicable':
      return 'Voucher does not match this cart'
    default:
      return 'Standard pricing'
  }
})

async function refreshStorefront() {
  loading.value = true
  loadError.value = ''
  try {
    const next = await getStorefront(voucherCode.value || undefined)
    cart.setVoucherCode(voucherCode.value)
    cart.setStorefront(next)
  } catch (error) {
    loadError.value = (error as Error).message
  } finally {
    loading.value = false
  }
}

function updateQuantity(sku: string, nextQuantity: number) {
  submitError.value = ''
  cart.setQuantity(sku, nextQuantity)
}

async function placeOrder() {
  if (!canSubmit.value) {
    return
  }

  submitting.value = true
  submitError.value = ''
  try {
    const order = await submitOrder({
      voucherCode: voucherCode.value || undefined,
      source: { channel: voucherCode.value ? 'qr' : 'direct' },
      items: Object.entries(cart.items)
        .filter(([, quantity]) => quantity > 0)
        .map(([sku, quantity]) => ({ sku, quantity })),
    })
    cart.setLastOrder(order)
    cart.clearCart()
    await router.push('/thanks')
  } catch (error) {
    const apiError = error as ApiError
    const body = apiError.body as { failure?: { message?: string } } | undefined
    submitError.value = body?.failure?.message ?? apiError.message
  } finally {
    submitting.value = false
  }
}

function openConfigWatch() {
  configSource?.close()
  configSource = watchCoffeeConfig('/public/coffeeconfig/watch', (event) => {
    cart.setActiveConfig(event.object)
    cart.setStorefront(buildStorefrontFromConfig(event.object, voucherCode.value || undefined))
  })
}

onMounted(() => {
  cart.ensureLoaded()
  openConfigWatch()
})

onBeforeUnmount(() => {
  configSource?.close()
})

watch(
  voucherCode,
  async (nextVoucherCode) => {
    cart.setVoucherCode(nextVoucherCode)
    if (cart.activeConfig) {
      cart.setStorefront(buildStorefrontFromConfig(cart.activeConfig, nextVoucherCode || undefined))
    } else {
      await refreshStorefront()
    }
  },
  { immediate: true },
)
</script>

<template>
  <main class="page-shell">
    <section class="hero-card">
      <p class="eyebrow">TestNet Coffee</p>
      <h1>{{ storefront?.shop.name ?? 'Coffee Loading…' }}</h1>
      <p class="hero-copy">
        {{ storefront?.shop.bannerText ?? 'Scan the QR code, pick a coffee, and see what the voucher really does at submit time.' }}
      </p>
      <div class="hero-actions">
        <RouterLink class="button button--secondary" to="/admin">Admin</RouterLink>
        <span class="pill" :class="`pill--${voucherTone}`">{{ voucherHeadline }}</span>
      </div>
      <p v-if="storefront?.voucher.displayMessage" class="voucher-copy">
        {{ storefront.voucher.displayMessage }}
      </p>
    </section>

    <section v-if="loadError" class="panel panel--danger">
      <h2>Storefront unavailable</h2>
      <p>{{ loadError }}</p>
    </section>

    <section v-if="invalidItems.length > 0" class="panel panel--danger">
      <h2>Cart needs attention</h2>
      <p>One or more coffees in your cart disappeared or were disabled after a live config change.</p>
      <ul class="inline-list">
        <li v-for="item in invalidItems" :key="item.sku">{{ item.sku }} × {{ item.quantity }}</li>
      </ul>
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>Choose your coffee</h2>
        <p>{{ loading ? 'Refreshing live config…' : 'Prices update live. Voucher depletion is only checked when you order.' }}</p>
      </div>
      <div class="product-grid">
        <article v-for="product in products" :key="product.sku" class="product-card">
          <div>
            <p class="product-kicker">{{ product.sku }}</p>
            <h3>{{ product.name }}</h3>
            <p class="product-description">{{ product.description }}</p>
          </div>
          <div class="product-footer">
            <div>
              <strong class="price">{{ formatMoney(currency, product.displayPriceCents) }}</strong>
              <span
                v-if="product.displayPriceCents !== product.basePriceCents"
                class="price price--muted"
              >
                {{ formatMoney(currency, product.basePriceCents) }}
              </span>
            </div>
            <div class="stepper">
              <button class="stepper__button" @click="updateQuantity(product.sku, (cart.items[product.sku] ?? 0) - 1)">
                -
              </button>
              <span class="stepper__value">{{ cart.items[product.sku] ?? 0 }}</span>
              <button class="stepper__button" @click="updateQuantity(product.sku, (cart.items[product.sku] ?? 0) + 1)">
                +
              </button>
            </div>
          </div>
        </article>
      </div>
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>Your cart</h2>
        <p>{{ cart.totalItems === 0 ? 'Nothing selected yet.' : `${cart.totalItems} item(s) ready to order.` }}</p>
      </div>
      <div v-if="cartRows.length === 0" class="empty-state">Pick one or more drinks to continue.</div>
      <div v-else class="cart-list">
        <article v-for="row in cartRows" :key="row.product.sku" class="cart-row">
          <div>
            <strong>{{ row.product.name }}</strong>
            <p>{{ row.quantity }} × {{ formatMoney(currency, row.product.displayPriceCents) }}</p>
          </div>
          <strong>{{ formatMoney(currency, row.quantity * row.product.displayPriceCents) }}</strong>
        </article>
      </div>
      <div class="cart-summary">
        <div>
          <span>Total</span>
          <strong>{{ formatMoney(currency, orderTotalCents) }}</strong>
        </div>
        <button class="button" :disabled="!canSubmit" @click="placeOrder">
          {{ submitting ? 'Placing Order…' : 'Order' }}
        </button>
      </div>
      <p v-if="submitError" class="error-copy">{{ submitError }}</p>
    </section>
  </main>
</template>
