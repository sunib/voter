<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  formatMoney,
  getAdminCoffeeConfig,
  getOrdersSnapshot,
  loginAdmin,
  patchAdminCoffeeConfig,
  watchCoffeeConfig,
  watchOrders,
  type ApiError,
} from '../api/coffee'
import type { CoffeeConfig, CoffeeOrderRecord, CoffeeVoucherSpec } from '../api/coffeeTypes'

const loading = ref(true)
const saving = ref(false)
const authRequired = ref(false)
const authError = ref('')
const loadError = ref('')
const password = ref('')
const draftConfig = ref<CoffeeConfig | null>(null)
const orders = ref<CoffeeOrderRecord[]>([])
const voucherUsage = ref<Record<string, number>>({})

let configSource: EventSource | undefined
let orderSource: EventSource | undefined

const orderedEvents = computed(() => [...orders.value].reverse())
const currency = computed(() => draftConfig.value?.spec.currency ?? 'EUR')
const mailConfig = computed(() => draftConfig.value?.spec.mail ?? { apiKeySecretRef: {} })
const paymentConfig = computed(() => draftConfig.value?.spec.payments ?? { apiKeySecretRef: {} })
const mailSecretRef = computed(() => mailConfig.value.apiKeySecretRef ?? {})
const paymentSecretRef = computed(() => paymentConfig.value.apiKeySecretRef ?? {})

async function loadAdminState() {
  loading.value = true
  loadError.value = ''
  try {
    const [config, snapshot] = await Promise.all([getAdminCoffeeConfig(), getOrdersSnapshot()])
    draftConfig.value = cloneConfig(config)
    orders.value = snapshot.orders
    voucherUsage.value = snapshot.voucherUsage
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

async function handleLogin() {
  authError.value = ''
  try {
    await loginAdmin(password.value)
    password.value = ''
    await loadAdminState()
    openStreams()
  } catch (error) {
    authError.value = (error as Error).message
  }
}

async function saveConfig() {
  if (!draftConfig.value) {
    return
  }
  saving.value = true
  loadError.value = ''
  try {
    const updated = await patchAdminCoffeeConfig({
      spec: draftConfig.value.spec,
    })
    draftConfig.value = cloneConfig(updated)
  } catch (error) {
    loadError.value = (error as Error).message
  } finally {
    saving.value = false
  }
}

function openStreams() {
  configSource?.close()
  orderSource?.close()

  configSource = watchCoffeeConfig('/api/admin/coffeeconfig/watch', (event) => {
    draftConfig.value = cloneConfig(event.object)
  })

  orderSource = watchOrders((event) => {
    orders.value = [...orders.value, event]
    if (event.status === 'placed' && event.voucherCode) {
      const key = event.voucherCode.trim().toLowerCase()
      voucherUsage.value = {
        ...voucherUsage.value,
        [key]: (voucherUsage.value[key] ?? 0) + 1,
      }
    }
  })
}

function addProduct() {
  if (!draftConfig.value) {
    return
  }
  draftConfig.value.spec.products.push({
    sku: `coffee-${draftConfig.value.spec.products.length + 1}`,
    name: 'New Coffee',
    priceCents: 300,
    description: '',
    enabled: true,
  })
}

function removeProduct(index: number) {
  draftConfig.value?.spec.products.splice(index, 1)
}

function addVoucher() {
  draftConfig.value?.spec.vouchers.push({
    code: 'newvoucher',
    enabled: true,
    discountType: 'percentage',
    discountValue: 100,
    maximumUsage: 1,
    appliesToProducts: [],
    displayMessage: '',
  })
}

function removeVoucher(index: number) {
  draftConfig.value?.spec.vouchers.splice(index, 1)
}

function setVoucherProducts(voucher: CoffeeVoucherSpec, value: string) {
  voucher.appliesToProducts = value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
}

function cloneConfig(config: CoffeeConfig): CoffeeConfig {
  const cloned = JSON.parse(JSON.stringify(config)) as CoffeeConfig
  cloned.spec.products ??= []
  cloned.spec.vouchers ??= []
  cloned.spec.mail ??= {}
  cloned.spec.mail.apiKeySecretRef ??= {}
  cloned.spec.payments ??= {}
  cloned.spec.payments.apiKeySecretRef ??= {}
  return cloned
}

onMounted(async () => {
  await loadAdminState()
  if (!authRequired.value) {
    openStreams()
  }
})

onBeforeUnmount(() => {
  configSource?.close()
  orderSource?.close()
})
</script>

<template>
  <main class="page-shell page-shell--wide">
    <section class="hero-card hero-card--compact">
      <p class="eyebrow">Admin</p>
      <h1>Live coffee config</h1>
      <p class="hero-copy">
        This page reads and patches the real <code>CoffeeConfig</code> object, then watches for live changes and incoming orders.
      </p>
    </section>

    <section v-if="authRequired" class="panel admin-login">
      <div class="section-heading">
        <h2>Admin password</h2>
        <p>First cut only. The backend sets an admin session cookie after verification.</p>
      </div>
      <label class="field">
        <span>Password</span>
        <input v-model="password" type="password" placeholder="Shared password" />
      </label>
      <div class="hero-actions">
        <button class="button" @click="handleLogin">Unlock Admin</button>
        <span v-if="authError" class="error-copy">{{ authError }}</span>
      </div>
    </section>

    <section v-else-if="loading" class="panel">
      <h2>Loading admin state…</h2>
    </section>

    <template v-else-if="draftConfig">
      <section v-if="loadError" class="panel panel--danger">
        <h2>Admin request failed</h2>
        <p>{{ loadError }}</p>
      </section>

      <section class="admin-grid">
        <article class="panel">
          <div class="section-heading">
            <h2>Storefront config</h2>
            <p>Edits are saved with JSON Merge Patch against the Kubernetes object.</p>
          </div>

          <div class="form-grid">
            <label class="field">
              <span>Shop name</span>
              <input v-model="draftConfig.spec.shopName" type="text" />
            </label>
            <label class="field">
              <span>Currency</span>
              <input v-model="draftConfig.spec.currency" type="text" />
            </label>
          </div>

          <label class="field">
            <span>Banner text</span>
            <textarea v-model="draftConfig.spec.bannerText" rows="3" />
          </label>

          <div class="section-heading section-heading--tight">
            <h3>Products</h3>
            <button class="button button--secondary" @click="addProduct">Add Product</button>
          </div>
          <div class="stack-list">
            <article
              v-for="(product, index) in draftConfig.spec.products"
              :key="`${product.sku}-${index}`"
              class="embedded-card"
            >
              <div class="form-grid">
                <label class="field">
                  <span>SKU</span>
                  <input v-model="product.sku" type="text" />
                </label>
                <label class="field">
                  <span>Name</span>
                  <input v-model="product.name" type="text" />
                </label>
                <label class="field">
                  <span>Base price (cents)</span>
                  <input v-model.number="product.priceCents" type="number" min="0" />
                </label>
                <label class="field field--checkbox">
                  <input v-model="product.enabled" type="checkbox" />
                  <span>Enabled</span>
                </label>
              </div>
              <label class="field">
                <span>Description</span>
                <input v-model="product.description" type="text" />
              </label>
              <div class="row-actions">
                <span class="pill">{{ formatMoney(currency, product.priceCents) }}</span>
                <button class="button button--ghost" @click="removeProduct(index)">Remove</button>
              </div>
            </article>
          </div>

          <div class="section-heading section-heading--tight">
            <h3>Vouchers</h3>
            <button class="button button--secondary" @click="addVoucher">Add Voucher</button>
          </div>
          <div class="stack-list">
            <article
              v-for="(voucher, index) in draftConfig.spec.vouchers"
              :key="`${voucher.code}-${index}`"
              class="embedded-card"
            >
              <div class="form-grid">
                <label class="field">
                  <span>Code</span>
                  <input v-model="voucher.code" type="text" />
                </label>
                <label class="field">
                  <span>Discount type</span>
                  <select v-model="voucher.discountType">
                    <option value="percentage">percentage</option>
                    <option value="fixed">fixed</option>
                  </select>
                </label>
                <label class="field">
                  <span>Discount value</span>
                  <input v-model.number="voucher.discountValue" type="number" min="0" />
                </label>
                <label class="field">
                  <span>Maximum usage</span>
                  <input v-model.number="voucher.maximumUsage" type="number" min="0" />
                </label>
                <label class="field field--checkbox">
                  <input v-model="voucher.enabled" type="checkbox" />
                  <span>Enabled</span>
                </label>
              </div>
              <label class="field">
                <span>Eligible products</span>
                <input
                  :value="voucher.appliesToProducts.join(', ')"
                  type="text"
                  placeholder="coffee-flat-white, coffee-espresso"
                  @input="setVoucherProducts(voucher, ($event.target as HTMLInputElement).value)"
                />
              </label>
              <label class="field">
                <span>Display message</span>
                <input v-model="voucher.displayMessage" type="text" />
              </label>
              <div class="row-actions">
                <span class="pill">Used {{ voucherUsage[voucher.code.trim().toLowerCase()] ?? 0 }} / {{ voucher.maximumUsage }}</span>
                <button class="button button--ghost" @click="removeVoucher(index)">Remove</button>
              </div>
            </article>
          </div>

          <div class="section-heading section-heading--tight">
            <h3>Mail and payments</h3>
          </div>
          <div class="form-grid">
            <label class="field">
              <span>Mail provider</span>
              <input v-model="mailConfig.provider" type="text" />
            </label>
            <label class="field">
              <span>Mail template</span>
              <input v-model="mailConfig.orderConfirmationTemplate" type="text" />
            </label>
            <label class="field">
              <span>From address</span>
              <input v-model="mailConfig.fromAddress" type="text" />
            </label>
            <label class="field">
              <span>Payment provider</span>
              <input v-model="paymentConfig.provider" type="text" />
            </label>
            <label class="field">
              <span>Payment mode</span>
              <input v-model="paymentConfig.mode" type="text" />
            </label>
            <label class="field field--checkbox">
              <input v-model="paymentConfig.zeroAmountCheckoutAllowed" type="checkbox" />
              <span>Zero amount checkout allowed</span>
            </label>
          </div>

          <div class="form-grid">
            <label class="field">
              <span>Mail secret name</span>
              <input v-model="mailSecretRef.name" type="text" />
            </label>
            <label class="field">
              <span>Mail secret key</span>
              <input v-model="mailSecretRef.key" type="text" />
            </label>
            <label class="field">
              <span>Payment secret name</span>
              <input v-model="paymentSecretRef.name" type="text" />
            </label>
            <label class="field">
              <span>Payment secret key</span>
              <input v-model="paymentSecretRef.key" type="text" />
            </label>
          </div>

          <div class="row-actions">
            <span class="metadata-copy">
              Resource version {{ draftConfig.metadata?.resourceVersion ?? 'unknown' }}
            </span>
            <button class="button" :disabled="saving" @click="saveConfig">
              {{ saving ? 'Saving…' : 'Save Config' }}
            </button>
          </div>
        </article>

        <article class="panel">
          <div class="section-heading">
            <h2>Live orders</h2>
            <p>Initial state comes from <code>GET /private/orders</code>; new events arrive over SSE.</p>
          </div>
          <div v-if="orderedEvents.length === 0" class="empty-state">No coffee orders yet.</div>
          <div v-else class="stack-list">
            <article v-for="order in orderedEvents" :key="order.orderId" class="embedded-card">
              <div class="row-actions">
                <strong>{{ order.orderId }}</strong>
                <span class="pill" :class="order.status === 'placed' ? 'pill--good' : 'pill--warning'">
                  {{ order.status }}
                </span>
              </div>
              <p class="metadata-copy">{{ new Date(order.submittedAt).toLocaleString() }}</p>
              <ul class="inline-list">
                <li v-for="item in order.items" :key="`${order.orderId}-${item.sku}`">
                  {{ item.name }} × {{ item.quantity }}
                </li>
              </ul>
              <div class="row-actions">
                <span>{{ order.voucherCode || 'no voucher' }}</span>
                <strong>{{ formatMoney(order.currency, order.totalPriceCents) }}</strong>
              </div>
              <p v-if="order.failureMessage" class="error-copy">{{ order.failureCode }}: {{ order.failureMessage }}</p>
            </article>
          </div>
        </article>
      </section>
    </template>
  </main>
</template>
