<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { get, isPrefix, type Path } from '@configbutler/krm-stream'
import { formatConflictValue, humanizePath } from '../adminFormatters'
import { formatMoney, getVoucherUsage } from '../api/coffee'
import { useLiveCoffeeConfig } from '../api/liveCoffeeConfig'
import { currentSession } from '../api/session'
import AdminNav from '../components/admin/AdminNav.vue'
import FieldStateMarker from '../components/admin/FieldStateMarker.vue'

type FieldState = 'clean' | 'dirty' | 'conflict'
type FieldConflict = { previousServer?: unknown; incomingServer: unknown }
const session = currentSession()!
const live = useLiveCoffeeConfig(
  session.namespace,
  session.coffeeConfigName,
  true,
)
const {
  draft: draftConfig,
  server: serverConfig,
  saving,
  error: loadError,
  commitNotice,
  notice,
  canSave,
  state,
  recoveryDraft,
  redactions,
} = live
const loading = computed(
  () =>
    !draftConfig.value &&
    ['connecting', 'syncing'].includes(state.value.status),
)
const changeReason = ref('')
const voucherUsage = ref<Record<string, number>>({})
const voucherUsageError = ref('')
const flashedPaths = ref<Record<string, true>>({})
const arrayFieldInputs = ref<Record<string, string>>({})
const flashTimers = new Map<string, ReturnType<typeof setTimeout>>()
// Presentation paths address only fixed CoffeeConfig fields. Arbitrary KRM
// keys never pass through this conversion; the store receives segment arrays.
const fieldPath = (path: string): Path =>
  path
    .split('.')
    .map((segment) => (/^\d+$/.test(segment) ? Number(segment) : segment))
const conflicts = computed<Record<string, FieldConflict>>(() =>
  Object.fromEntries(
    live.conflicts.value.map((conflict) => [
      conflict.path.join('.'),
      { incomingServer: conflict.theirs },
    ]),
  ),
)
const currency = computed(() => draftConfig.value?.spec.currency ?? 'EUR')
const dirtyFieldCount = computed(() => live.changes.value.length)
const conflictCount = computed(() => live.conflicts.value.length)
const cleanDirtyCount = computed(() =>
  Math.max(0, dirtyFieldCount.value - conflictCount.value),
)
const dirtySummaryEntries = computed(() =>
  live.changes.value.map((change) => {
    const path = change.path.join('.')
    return {
      path,
      label: humanizePath(path),
      state: fieldState(path),
      draftValue: change.new,
      serverValue: change.old,
      previousServer: undefined,
    }
  }),
)
const saveButtonLabel = computed(() =>
  saving.value
    ? 'Saving…'
    : live.needsRead.value
      ? 'Refresh before saving'
      : dirtyFieldCount.value
        ? `Save ${dirtyFieldCount.value} Change${dirtyFieldCount.value === 1 ? '' : 's'}`
        : 'No Changes to Save',
)
const connectionMessage = computed(() => {
  if (loadError.value) return loadError.value
  if (state.value.status === 'live')
    return draftConfig.value
      ? 'Live updates connected.'
      : 'This configuration was removed. Open a new editor for a replacement.'
  if (state.value.status === 'terminal')
    return 'Live access ended. Sign in again or check your permissions.'
  if (state.value.status === 'exhausted')
    return 'Could not reconnect. Your unsaved changes are retained; retry when ready.'
  return 'Connecting to live updates. Your unsaved changes are retained; saving is paused.'
})
async function refreshVoucherUsage() {
  try {
    voucherUsage.value = (await getVoucherUsage()).voucherUsage
    voucherUsageError.value = ''
  } catch {
    voucherUsageError.value =
      'Voucher usage could not be refreshed. Counts may be out of date.'
  }
}
async function refreshFromServer() {
  try {
    await live.refreshFromServer()
    await refreshVoucherUsage()
  } catch (cause) {
    loadError.value = (cause as Error).message
  }
}
async function saveConfig() {
  await live.save(changeReason.value)
}
watch(
  () => serverConfig.value?.metadata?.resourceVersion,
  () => {
    void refreshVoucherUsage()
  },
)
watch(live.flashed, (paths) => {
  for (const path of paths) flashField(path.join('.'))
})
onMounted(refreshVoucherUsage)
function updateField(path: string, value: unknown) {
  live.setValue(fieldPath(path), value)
}
function applyServerValue(path: string) {
  live.takeTheirs(fieldPath(path))
  clearArrayInputBranch(path)
}
function keepLocalValue(path: string) {
  live.keepMine(fieldPath(path))
  clearArrayInputBranch(path)
}
function addProduct() {
  const products = draftConfig.value?.spec.products ?? []
  let index = products.length + 1
  while (products.some((product) => product.sku === `coffee-${index}`)) index++
  updateField('spec.products', [
    ...products,
    {
      sku: `coffee-${index}`,
      name: 'New Coffee',
      priceCents: 300,
      description: '',
      enabled: true,
    },
  ])
}
function removeProduct(index: number) {
  updateField(
    'spec.products',
    (draftConfig.value?.spec.products ?? []).filter((_, i) => i !== index),
  )
}
function addVoucher() {
  const vouchers = draftConfig.value?.spec.vouchers ?? []
  let index = vouchers.length + 1
  while (vouchers.some((voucher) => voucher.code === `voucher-${index}`))
    index++
  updateField('spec.vouchers', [
    ...vouchers,
    {
      code: `voucher-${index}`,
      enabled: true,
      discountType: 'percentage',
      discountValue: 100,
      maximumUsage: 1,
      appliesToProducts: [],
      displayMessage: '',
    },
  ])
  clearArrayInputBranch('spec.vouchers')
}
function removeVoucher(index: number) {
  updateField(
    'spec.vouchers',
    (draftConfig.value?.spec.vouchers ?? []).filter((_, i) => i !== index),
  )
  clearArrayInputBranch('spec.vouchers')
}
function setVoucherProducts(path: string, value: string) {
  arrayFieldInputs.value = {
    ...arrayFieldInputs.value,
    [path]: value,
  }
  updateField(
    path,
    value
      .split(',')
      .map((item) => item.trim())
      .filter(Boolean),
  )
}

function getVoucherProductsInput(path: string): string {
  return arrayFieldInputs.value[path] ?? getArrayField(path).join(', ')
}

function finishVoucherProductsEdit(path: string) {
  if (!(path in arrayFieldInputs.value)) {
    return
  }
  const next = { ...arrayFieldInputs.value }
  delete next[path]
  arrayFieldInputs.value = next
}

function getFieldValue(path: string): unknown {
  return getPathValue(draftConfig.value, path)
}

function getTextField(path: string): string {
  const value = getFieldValue(path)
  return typeof value === 'string' ? value : ''
}

function getNumberField(path: string): number | undefined {
  const value = getFieldValue(path)
  return typeof value === 'number' ? value : undefined
}

function getBooleanField(path: string): boolean {
  return Boolean(getFieldValue(path))
}

function getArrayField(path: string): string[] {
  const value = getFieldValue(path)
  return Array.isArray(value) ? value.map((item) => String(item)) : []
}

function fieldState(path: string): FieldState {
  if (
    live.conflicts.value.some((conflict) =>
      isPrefix(conflict.path, fieldPath(path)),
    )
  ) {
    return 'conflict'
  }
  if (isDirtyPath(path)) {
    return 'dirty'
  }
  return 'clean'
}

function fieldClasses(path: string) {
  return {
    field: true,
    'field--checkbox': false,
    'field--dirty': fieldState(path) === 'dirty',
    'field--conflict': fieldState(path) === 'conflict',
    'field--flash': Boolean(flashedPaths.value[path]),
  }
}

function checkboxFieldClasses(path: string) {
  return {
    ...fieldClasses(path),
    'field--checkbox': true,
  }
}

function conflictFor(path: string): FieldConflict | undefined {
  return conflicts.value[path]
}

function serverValueFor(path: string): unknown {
  const conflict = conflictFor(path)
  if (conflict) {
    return conflict.incomingServer
  }
  return getPathValue(serverConfig.value, path)
}

function isDirtyPath(path: string): boolean {
  return live.changes.value.some((change) =>
    isPrefix(change.path, fieldPath(path)),
  )
}

function clearArrayInputBranch(path: string) {
  const next = Object.fromEntries(
    Object.entries(arrayFieldInputs.value).filter(
      ([candidate]) => candidate !== path && !candidate.startsWith(`${path}.`),
    ),
  ) as Record<string, string>
  arrayFieldInputs.value = next
}

function flashField(path: string) {
  if (!path) {
    return
  }
  flashedPaths.value = {
    ...flashedPaths.value,
    [path]: true,
  }
  const existing = flashTimers.get(path)
  if (existing) {
    clearTimeout(existing)
  }
  flashTimers.set(
    path,
    setTimeout(() => {
      const next = { ...flashedPaths.value }
      delete next[path]
      flashedPaths.value = next
      flashTimers.delete(path)
    }, 1600),
  )
}

function clearAllFlashes() {
  for (const timer of flashTimers.values()) {
    clearTimeout(timer)
  }
  flashTimers.clear()
  flashedPaths.value = {}
}

function getPathValue(target: unknown, path: string): unknown {
  return get(target, fieldPath(path))
}
onBeforeUnmount(clearAllFlashes)
</script>

<template>
  <main class="page-shell page-shell--wide">
    <section class="hero-card hero-card--compact">
      <p class="eyebrow">Admin</p>
      <h1>Live coffee config</h1>
      <p class="hero-copy">
        This page reads and patches the real <code>CoffeeConfig</code> object,
        then watches for live changes while keeping voucher usage current.
      </p>
      <div class="hero-actions">
        <AdminNav />
      </div>
    </section>

    <section class="panel" role="status">
      <p>{{ connectionMessage }}</p>
      <p v-if="notice">{{ notice }}</p>
      <a
        v-if="state.status === 'terminal'"
        class="button"
        href="/auth/login?returnTo=/admin"
        >Sign in again</a
      >
      <button
        v-if="state.status === 'exhausted'"
        class="button"
        @click="live.reconnect"
      >
        Retry connection
      </button>
      <a v-if="!draftConfig && !loading" class="button" href="/admin"
        >Open a new editor</a
      >
      <details
        v-if="recoveryDraft && (!draftConfig || state.status === 'terminal')"
      >
        <summary>Copy unsaved configuration before leaving this page</summary>
        <textarea
          readonly
          rows="12"
          :value="JSON.stringify(recoveryDraft.spec, null, 2)"
          aria-label="Unsaved configuration recovery"
        />
      </details>
      <p v-for="entry in redactions" :key="entry.path.join('.')">
        Withheld field: {{ entry.path.join('.') }}
      </p>
    </section>
    <section v-if="loading" class="panel">
      <h2>Loading admin state…</h2>
    </section>

    <!-- Outside the draftConfig branch on purpose. When the CONFIG ITSELF
         fails to load there is no draft, so an error panel nested inside that
         branch renders nothing at all and the screen just looks empty. That
         cost real time to diagnose once: a 401 from Kubernetes showed up as a
         page with a heading and no form. -->
    <section v-else-if="loadError && !draftConfig" class="panel panel--danger">
      <h2>Admin request failed</h2>
      <p>{{ loadError }}</p>
      <p class="metadata-copy">
        The coffee config could not be read with your identity. If this says
        Unauthorized or Forbidden, that is Kubernetes answering, not the app.
      </p>
    </section>

    <template v-else-if="draftConfig">
      <section v-if="loadError" class="panel panel--danger" role="alert">
        <h2>Admin request failed</h2>
        <p>{{ loadError }}</p>
      </section>
      <section
        v-if="voucherUsageError"
        class="panel panel--warning"
        role="status"
      >
        <h2>Voucher usage unavailable</h2>
        <p>{{ voucherUsageError }}</p>
      </section>

      <section v-if="commitNotice" class="panel panel--warning">
        <h2>Git commit status</h2>
        <p>{{ commitNotice }}</p>
      </section>

      <section class="admin-grid">
        <article class="panel">
          <div class="section-heading">
            <h2>Storefront config</h2>
          </div>

          <div class="config-meta">
            <span class="config-meta__item">
              <strong>Generation</strong>
              <code>{{ draftConfig.metadata?.generation ?? 'unknown' }}</code>
            </span>
          </div>

          <div class="form-grid">
            <label :class="fieldClasses('spec.shopName')">
              <div class="field__heading">
                <span>Shop name</span>
                <FieldStateMarker
                  :state="fieldState('spec.shopName')"
                  :server-value="serverValueFor('spec.shopName')"
                  :previous-server="
                    conflictFor('spec.shopName')?.previousServer
                  "
                  @apply="applyServerValue('spec.shopName')"
                />
              </div>
              <input
                :value="getTextField('spec.shopName')"
                type="text"
                @input="
                  updateField(
                    'spec.shopName',
                    ($event.target as HTMLInputElement).value,
                  )
                "
              />
            </label>
            <label :class="fieldClasses('spec.currency')">
              <div class="field__heading">
                <span>Currency</span>
                <FieldStateMarker
                  :state="fieldState('spec.currency')"
                  :server-value="serverValueFor('spec.currency')"
                  :previous-server="
                    conflictFor('spec.currency')?.previousServer
                  "
                  @apply="applyServerValue('spec.currency')"
                />
              </div>
              <input
                :value="getTextField('spec.currency')"
                type="text"
                @input="
                  updateField(
                    'spec.currency',
                    ($event.target as HTMLInputElement).value,
                  )
                "
              />
            </label>
          </div>

          <label :class="fieldClasses('spec.bannerText')">
            <div class="field__heading">
              <span>Banner text</span>
              <FieldStateMarker
                :state="fieldState('spec.bannerText')"
                :server-value="serverValueFor('spec.bannerText')"
                :previous-server="
                  conflictFor('spec.bannerText')?.previousServer
                "
                @apply="applyServerValue('spec.bannerText')"
              />
            </div>
            <textarea
              :value="getTextField('spec.bannerText')"
              rows="3"
              @input="
                updateField(
                  'spec.bannerText',
                  ($event.target as HTMLTextAreaElement).value,
                )
              "
            />
          </label>

          <div class="section-heading section-heading--tight">
            <div class="section-heading__title">
              <h3>Products</h3>
              <FieldStateMarker
                :state="fieldState('spec.products')"
                :server-value="serverValueFor('spec.products')"
                :previous-server="conflictFor('spec.products')?.previousServer"
                @apply="applyServerValue('spec.products')"
              />
            </div>
            <button class="button button--secondary" @click="addProduct">
              Add Product
            </button>
          </div>
          <div class="stack-list">
            <article
              v-for="(product, index) in draftConfig.spec.products"
              :key="index"
              class="embedded-card"
            >
              <div class="form-grid">
                <label :class="fieldClasses(`spec.products.${index}.sku`)">
                  <div class="field__heading">
                    <span>SKU</span>
                    <FieldStateMarker
                      :state="fieldState(`spec.products.${index}.sku`)"
                      :server-value="
                        serverValueFor(`spec.products.${index}.sku`)
                      "
                      :previous-server="
                        conflictFor(`spec.products.${index}.sku`)
                          ?.previousServer
                      "
                      @apply="applyServerValue(`spec.products.${index}.sku`)"
                    />
                  </div>
                  <input
                    :value="getTextField(`spec.products.${index}.sku`)"
                    :readonly="
                      !!serverConfig?.spec.products?.some(
                        (row) => row.sku === product.sku,
                      )
                    "
                    type="text"
                    @input="
                      updateField(
                        `spec.products.${index}.sku`,
                        ($event.target as HTMLInputElement).value,
                      )
                    "
                  />
                </label>
                <label :class="fieldClasses(`spec.products.${index}.name`)">
                  <div class="field__heading">
                    <span>Name</span>
                    <FieldStateMarker
                      :state="fieldState(`spec.products.${index}.name`)"
                      :server-value="
                        serverValueFor(`spec.products.${index}.name`)
                      "
                      :previous-server="
                        conflictFor(`spec.products.${index}.name`)
                          ?.previousServer
                      "
                      @apply="applyServerValue(`spec.products.${index}.name`)"
                    />
                  </div>
                  <input
                    :value="getTextField(`spec.products.${index}.name`)"
                    type="text"
                    @input="
                      updateField(
                        `spec.products.${index}.name`,
                        ($event.target as HTMLInputElement).value,
                      )
                    "
                  />
                </label>
                <label
                  :class="fieldClasses(`spec.products.${index}.priceCents`)"
                >
                  <div class="field__heading">
                    <span>Base price (cents)</span>
                    <FieldStateMarker
                      :state="fieldState(`spec.products.${index}.priceCents`)"
                      :server-value="
                        serverValueFor(`spec.products.${index}.priceCents`)
                      "
                      :previous-server="
                        conflictFor(`spec.products.${index}.priceCents`)
                          ?.previousServer
                      "
                      @apply="
                        applyServerValue(`spec.products.${index}.priceCents`)
                      "
                    />
                  </div>
                  <input
                    :value="getNumberField(`spec.products.${index}.priceCents`)"
                    type="number"
                    min="0"
                    @input="
                      updateField(
                        `spec.products.${index}.priceCents`,
                        Number(($event.target as HTMLInputElement).value),
                      )
                    "
                  />
                </label>
                <label
                  :class="
                    checkboxFieldClasses(`spec.products.${index}.enabled`)
                  "
                >
                  <input
                    :checked="getBooleanField(`spec.products.${index}.enabled`)"
                    type="checkbox"
                    @change="
                      updateField(
                        `spec.products.${index}.enabled`,
                        ($event.target as HTMLInputElement).checked,
                      )
                    "
                  />
                  <div class="field__checkbox-copy">
                    <span>Enabled</span>
                    <FieldStateMarker
                      :state="fieldState(`spec.products.${index}.enabled`)"
                      :server-value="
                        serverValueFor(`spec.products.${index}.enabled`)
                      "
                      :previous-server="
                        conflictFor(`spec.products.${index}.enabled`)
                          ?.previousServer
                      "
                      @apply="
                        applyServerValue(`spec.products.${index}.enabled`)
                      "
                    />
                  </div>
                </label>
              </div>
              <label
                :class="fieldClasses(`spec.products.${index}.description`)"
              >
                <div class="field__heading">
                  <span>Description</span>
                  <FieldStateMarker
                    :state="fieldState(`spec.products.${index}.description`)"
                    :server-value="
                      serverValueFor(`spec.products.${index}.description`)
                    "
                    :previous-server="
                      conflictFor(`spec.products.${index}.description`)
                        ?.previousServer
                    "
                    @apply="
                      applyServerValue(`spec.products.${index}.description`)
                    "
                  />
                </div>
                <input
                  :value="getTextField(`spec.products.${index}.description`)"
                  type="text"
                  @input="
                    updateField(
                      `spec.products.${index}.description`,
                      ($event.target as HTMLInputElement).value,
                    )
                  "
                />
              </label>
              <div class="row-actions">
                <span class="pill">{{
                  formatMoney(currency, product.priceCents)
                }}</span>
                <button
                  class="button button--ghost"
                  @click="removeProduct(index)"
                >
                  Remove
                </button>
              </div>
            </article>
          </div>

          <div class="section-heading section-heading--tight">
            <div class="section-heading__title">
              <h3>Vouchers</h3>
              <FieldStateMarker
                :state="fieldState('spec.vouchers')"
                :server-value="serverValueFor('spec.vouchers')"
                :previous-server="conflictFor('spec.vouchers')?.previousServer"
                @apply="applyServerValue('spec.vouchers')"
              />
            </div>
            <button class="button button--secondary" @click="addVoucher">
              Add Voucher
            </button>
          </div>
          <div class="stack-list">
            <article
              v-for="(voucher, index) in draftConfig.spec.vouchers"
              :key="index"
              class="embedded-card"
            >
              <div class="form-grid">
                <label :class="fieldClasses(`spec.vouchers.${index}.code`)">
                  <div class="field__heading">
                    <span>Code</span>
                    <FieldStateMarker
                      :state="fieldState(`spec.vouchers.${index}.code`)"
                      :server-value="
                        serverValueFor(`spec.vouchers.${index}.code`)
                      "
                      :previous-server="
                        conflictFor(`spec.vouchers.${index}.code`)
                          ?.previousServer
                      "
                      @apply="applyServerValue(`spec.vouchers.${index}.code`)"
                    />
                  </div>
                  <input
                    :value="getTextField(`spec.vouchers.${index}.code`)"
                    :readonly="
                      !!serverConfig?.spec.vouchers?.some(
                        (row) => row.code === voucher.code,
                      )
                    "
                    type="text"
                    @input="
                      updateField(
                        `spec.vouchers.${index}.code`,
                        ($event.target as HTMLInputElement).value,
                      )
                    "
                  />
                </label>
                <label
                  :class="fieldClasses(`spec.vouchers.${index}.discountType`)"
                >
                  <div class="field__heading">
                    <span>Discount type</span>
                    <FieldStateMarker
                      :state="fieldState(`spec.vouchers.${index}.discountType`)"
                      :server-value="
                        serverValueFor(`spec.vouchers.${index}.discountType`)
                      "
                      :previous-server="
                        conflictFor(`spec.vouchers.${index}.discountType`)
                          ?.previousServer
                      "
                      @apply="
                        applyServerValue(`spec.vouchers.${index}.discountType`)
                      "
                    />
                  </div>
                  <select
                    :value="getTextField(`spec.vouchers.${index}.discountType`)"
                    @change="
                      updateField(
                        `spec.vouchers.${index}.discountType`,
                        ($event.target as HTMLSelectElement).value,
                      )
                    "
                  >
                    <option value="percentage">percentage</option>
                    <option value="fixed">fixed</option>
                  </select>
                </label>
                <label
                  :class="fieldClasses(`spec.vouchers.${index}.discountValue`)"
                >
                  <div class="field__heading">
                    <span>Discount value</span>
                    <FieldStateMarker
                      :state="
                        fieldState(`spec.vouchers.${index}.discountValue`)
                      "
                      :server-value="
                        serverValueFor(`spec.vouchers.${index}.discountValue`)
                      "
                      :previous-server="
                        conflictFor(`spec.vouchers.${index}.discountValue`)
                          ?.previousServer
                      "
                      @apply="
                        applyServerValue(`spec.vouchers.${index}.discountValue`)
                      "
                    />
                  </div>
                  <input
                    :value="
                      getNumberField(`spec.vouchers.${index}.discountValue`)
                    "
                    type="number"
                    min="0"
                    @input="
                      updateField(
                        `spec.vouchers.${index}.discountValue`,
                        Number(($event.target as HTMLInputElement).value),
                      )
                    "
                  />
                </label>
                <label
                  :class="fieldClasses(`spec.vouchers.${index}.maximumUsage`)"
                >
                  <div class="field__heading">
                    <span>Maximum usage</span>
                    <FieldStateMarker
                      :state="fieldState(`spec.vouchers.${index}.maximumUsage`)"
                      :server-value="
                        serverValueFor(`spec.vouchers.${index}.maximumUsage`)
                      "
                      :previous-server="
                        conflictFor(`spec.vouchers.${index}.maximumUsage`)
                          ?.previousServer
                      "
                      @apply="
                        applyServerValue(`spec.vouchers.${index}.maximumUsage`)
                      "
                    />
                  </div>
                  <input
                    :value="
                      getNumberField(`spec.vouchers.${index}.maximumUsage`)
                    "
                    type="number"
                    min="0"
                    @input="
                      updateField(
                        `spec.vouchers.${index}.maximumUsage`,
                        Number(($event.target as HTMLInputElement).value),
                      )
                    "
                  />
                </label>
                <label
                  :class="
                    checkboxFieldClasses(`spec.vouchers.${index}.enabled`)
                  "
                >
                  <input
                    :checked="getBooleanField(`spec.vouchers.${index}.enabled`)"
                    type="checkbox"
                    @change="
                      updateField(
                        `spec.vouchers.${index}.enabled`,
                        ($event.target as HTMLInputElement).checked,
                      )
                    "
                  />
                  <div class="field__checkbox-copy">
                    <span>Enabled</span>
                    <FieldStateMarker
                      :state="fieldState(`spec.vouchers.${index}.enabled`)"
                      :server-value="
                        serverValueFor(`spec.vouchers.${index}.enabled`)
                      "
                      :previous-server="
                        conflictFor(`spec.vouchers.${index}.enabled`)
                          ?.previousServer
                      "
                      @apply="
                        applyServerValue(`spec.vouchers.${index}.enabled`)
                      "
                    />
                  </div>
                </label>
              </div>
              <label
                :class="
                  fieldClasses(`spec.vouchers.${index}.appliesToProducts`)
                "
              >
                <div class="field__heading">
                  <span>Eligible products</span>
                  <FieldStateMarker
                    :state="
                      fieldState(`spec.vouchers.${index}.appliesToProducts`)
                    "
                    :server-value="
                      serverValueFor(`spec.vouchers.${index}.appliesToProducts`)
                    "
                    :previous-server="
                      conflictFor(`spec.vouchers.${index}.appliesToProducts`)
                        ?.previousServer
                    "
                    @apply="
                      applyServerValue(
                        `spec.vouchers.${index}.appliesToProducts`,
                      )
                    "
                  />
                </div>
                <input
                  :value="
                    getVoucherProductsInput(
                      `spec.vouchers.${index}.appliesToProducts`,
                    )
                  "
                  type="text"
                  placeholder="coffee-flat-white, coffee-espresso"
                  @input="
                    setVoucherProducts(
                      `spec.vouchers.${index}.appliesToProducts`,
                      ($event.target as HTMLInputElement).value,
                    )
                  "
                  @blur="
                    finishVoucherProductsEdit(
                      `spec.vouchers.${index}.appliesToProducts`,
                    )
                  "
                />
              </label>
              <label
                :class="fieldClasses(`spec.vouchers.${index}.displayMessage`)"
              >
                <div class="field__heading">
                  <span>Display message</span>
                  <FieldStateMarker
                    :state="fieldState(`spec.vouchers.${index}.displayMessage`)"
                    :server-value="
                      serverValueFor(`spec.vouchers.${index}.displayMessage`)
                    "
                    :previous-server="
                      conflictFor(`spec.vouchers.${index}.displayMessage`)
                        ?.previousServer
                    "
                    @apply="
                      applyServerValue(`spec.vouchers.${index}.displayMessage`)
                    "
                  />
                </div>
                <input
                  :value="getTextField(`spec.vouchers.${index}.displayMessage`)"
                  type="text"
                  @input="
                    updateField(
                      `spec.vouchers.${index}.displayMessage`,
                      ($event.target as HTMLInputElement).value,
                    )
                  "
                />
              </label>
              <div class="row-actions">
                <span class="pill"
                  >Used
                  {{ voucherUsage[voucher.code.trim().toLowerCase()] ?? 0 }} /
                  {{ voucher.maximumUsage }}</span
                >
                <button
                  class="button button--ghost"
                  @click="removeVoucher(index)"
                >
                  Remove
                </button>
              </div>
            </article>
          </div>

          <div class="section-heading section-heading--tight">
            <h3>Mail and payments</h3>
          </div>
          <div class="form-grid">
            <label :class="fieldClasses('spec.mail.provider')">
              <div class="field__heading">
                <span>Mail provider</span>
                <FieldStateMarker
                  :state="fieldState('spec.mail.provider')"
                  :server-value="serverValueFor('spec.mail.provider')"
                  :previous-server="
                    conflictFor('spec.mail.provider')?.previousServer
                  "
                  @apply="applyServerValue('spec.mail.provider')"
                />
              </div>
              <input
                :value="getTextField('spec.mail.provider')"
                type="text"
                @input="
                  updateField(
                    'spec.mail.provider',
                    ($event.target as HTMLInputElement).value,
                  )
                "
              />
            </label>
            <label :class="fieldClasses('spec.mail.orderConfirmationTemplate')">
              <div class="field__heading">
                <span>Mail template</span>
                <FieldStateMarker
                  :state="fieldState('spec.mail.orderConfirmationTemplate')"
                  :server-value="
                    serverValueFor('spec.mail.orderConfirmationTemplate')
                  "
                  :previous-server="
                    conflictFor('spec.mail.orderConfirmationTemplate')
                      ?.previousServer
                  "
                  @apply="
                    applyServerValue('spec.mail.orderConfirmationTemplate')
                  "
                />
              </div>
              <input
                :value="getTextField('spec.mail.orderConfirmationTemplate')"
                type="text"
                @input="
                  updateField(
                    'spec.mail.orderConfirmationTemplate',
                    ($event.target as HTMLInputElement).value,
                  )
                "
              />
            </label>
            <label :class="fieldClasses('spec.mail.fromAddress')">
              <div class="field__heading">
                <span>From address</span>
                <FieldStateMarker
                  :state="fieldState('spec.mail.fromAddress')"
                  :server-value="serverValueFor('spec.mail.fromAddress')"
                  :previous-server="
                    conflictFor('spec.mail.fromAddress')?.previousServer
                  "
                  @apply="applyServerValue('spec.mail.fromAddress')"
                />
              </div>
              <input
                :value="getTextField('spec.mail.fromAddress')"
                type="text"
                @input="
                  updateField(
                    'spec.mail.fromAddress',
                    ($event.target as HTMLInputElement).value,
                  )
                "
              />
            </label>
            <label :class="fieldClasses('spec.payments.provider')">
              <div class="field__heading">
                <span>Payment provider</span>
                <FieldStateMarker
                  :state="fieldState('spec.payments.provider')"
                  :server-value="serverValueFor('spec.payments.provider')"
                  :previous-server="
                    conflictFor('spec.payments.provider')?.previousServer
                  "
                  @apply="applyServerValue('spec.payments.provider')"
                />
              </div>
              <input
                :value="getTextField('spec.payments.provider')"
                type="text"
                @input="
                  updateField(
                    'spec.payments.provider',
                    ($event.target as HTMLInputElement).value,
                  )
                "
              />
            </label>
            <label :class="fieldClasses('spec.payments.mode')">
              <div class="field__heading">
                <span>Payment mode</span>
                <FieldStateMarker
                  :state="fieldState('spec.payments.mode')"
                  :server-value="serverValueFor('spec.payments.mode')"
                  :previous-server="
                    conflictFor('spec.payments.mode')?.previousServer
                  "
                  @apply="applyServerValue('spec.payments.mode')"
                />
              </div>
              <input
                :value="getTextField('spec.payments.mode')"
                type="text"
                @input="
                  updateField(
                    'spec.payments.mode',
                    ($event.target as HTMLInputElement).value,
                  )
                "
              />
            </label>
            <label
              :class="
                checkboxFieldClasses('spec.payments.zeroAmountCheckoutAllowed')
              "
            >
              <input
                :checked="
                  getBooleanField('spec.payments.zeroAmountCheckoutAllowed')
                "
                type="checkbox"
                @change="
                  updateField(
                    'spec.payments.zeroAmountCheckoutAllowed',
                    ($event.target as HTMLInputElement).checked,
                  )
                "
              />
              <div class="field__checkbox-copy">
                <span>Zero amount checkout allowed</span>
                <FieldStateMarker
                  :state="fieldState('spec.payments.zeroAmountCheckoutAllowed')"
                  :server-value="
                    serverValueFor('spec.payments.zeroAmountCheckoutAllowed')
                  "
                  :previous-server="
                    conflictFor('spec.payments.zeroAmountCheckoutAllowed')
                      ?.previousServer
                  "
                  @apply="
                    applyServerValue('spec.payments.zeroAmountCheckoutAllowed')
                  "
                />
              </div>
            </label>
          </div>

          <div class="form-grid">
            <label :class="fieldClasses('spec.mail.apiKeySecretRef.name')">
              <div class="field__heading">
                <span>Mail secret name</span>
                <FieldStateMarker
                  :state="fieldState('spec.mail.apiKeySecretRef.name')"
                  :server-value="
                    serverValueFor('spec.mail.apiKeySecretRef.name')
                  "
                  :previous-server="
                    conflictFor('spec.mail.apiKeySecretRef.name')
                      ?.previousServer
                  "
                  @apply="applyServerValue('spec.mail.apiKeySecretRef.name')"
                />
              </div>
              <input
                :value="getTextField('spec.mail.apiKeySecretRef.name')"
                type="text"
                @input="
                  updateField(
                    'spec.mail.apiKeySecretRef.name',
                    ($event.target as HTMLInputElement).value,
                  )
                "
              />
            </label>
            <label :class="fieldClasses('spec.mail.apiKeySecretRef.key')">
              <div class="field__heading">
                <span>Mail secret key</span>
                <FieldStateMarker
                  :state="fieldState('spec.mail.apiKeySecretRef.key')"
                  :server-value="
                    serverValueFor('spec.mail.apiKeySecretRef.key')
                  "
                  :previous-server="
                    conflictFor('spec.mail.apiKeySecretRef.key')?.previousServer
                  "
                  @apply="applyServerValue('spec.mail.apiKeySecretRef.key')"
                />
              </div>
              <input
                :value="getTextField('spec.mail.apiKeySecretRef.key')"
                type="text"
                @input="
                  updateField(
                    'spec.mail.apiKeySecretRef.key',
                    ($event.target as HTMLInputElement).value,
                  )
                "
              />
            </label>
            <label :class="fieldClasses('spec.payments.apiKeySecretRef.name')">
              <div class="field__heading">
                <span>Payment secret name</span>
                <FieldStateMarker
                  :state="fieldState('spec.payments.apiKeySecretRef.name')"
                  :server-value="
                    serverValueFor('spec.payments.apiKeySecretRef.name')
                  "
                  :previous-server="
                    conflictFor('spec.payments.apiKeySecretRef.name')
                      ?.previousServer
                  "
                  @apply="
                    applyServerValue('spec.payments.apiKeySecretRef.name')
                  "
                />
              </div>
              <input
                :value="getTextField('spec.payments.apiKeySecretRef.name')"
                type="text"
                @input="
                  updateField(
                    'spec.payments.apiKeySecretRef.name',
                    ($event.target as HTMLInputElement).value,
                  )
                "
              />
            </label>
            <label :class="fieldClasses('spec.payments.apiKeySecretRef.key')">
              <div class="field__heading">
                <span>Payment secret key</span>
                <FieldStateMarker
                  :state="fieldState('spec.payments.apiKeySecretRef.key')"
                  :server-value="
                    serverValueFor('spec.payments.apiKeySecretRef.key')
                  "
                  :previous-server="
                    conflictFor('spec.payments.apiKeySecretRef.key')
                      ?.previousServer
                  "
                  @apply="applyServerValue('spec.payments.apiKeySecretRef.key')"
                />
              </div>
              <input
                :value="getTextField('spec.payments.apiKeySecretRef.key')"
                type="text"
                @input="
                  updateField(
                    'spec.payments.apiKeySecretRef.key',
                    ($event.target as HTMLInputElement).value,
                  )
                "
              />
            </label>
          </div>
        </article>

        <div class="admin-sidebar admin-sidebar-stack">
          <article class="panel">
            <section class="save-summary">
              <div class="save-summary__header">
                <div class="save-summary__copy">
                  <p class="eyebrow">Review and save</p>
                  <h3>
                    {{
                      dirtyFieldCount === 0
                        ? 'No pending changes'
                        : `${dirtyFieldCount} change(s) ready to save`
                    }}
                  </h3>
                  <p class="metadata-copy">
                    <template v-if="dirtyFieldCount === 0"
                      >The current form matches the latest watched
                      config.</template
                    >
                    <template v-else-if="conflictCount === 0">
                      Yellow dots are local edits. Saving writes your changes
                      into the watched config.
                    </template>
                    <template v-else>
                      Red dots indicate concurrent changes. Choose Take Theirs
                      or Keep Mine for each conflict before saving. Arrays are
                      reviewed as a whole.
                    </template>
                  </p>
                </div>
              </div>

              <ul
                v-if="dirtySummaryEntries.length > 0"
                class="save-summary__list"
              >
                <li
                  v-for="entry in dirtySummaryEntries"
                  :key="entry.path"
                  class="save-summary__item"
                  :class="
                    entry.state === 'conflict'
                      ? 'save-summary__item--conflict'
                      : 'save-summary__item--dirty'
                  "
                >
                  <div class="save-summary__item-copy">
                    <div class="save-summary__item-row">
                      <strong>{{ entry.label }}</strong>
                      <span
                        class="pill"
                        :class="
                          entry.state === 'conflict'
                            ? 'pill--danger'
                            : 'pill--warning'
                        "
                      >
                        {{
                          entry.state === 'conflict'
                            ? 'Concurrent change'
                            : 'Edited'
                        }}
                      </span>
                    </div>
                    <p v-if="entry.state === 'conflict'" class="metadata-copy">
                      <span class="save-summary__change">
                        Server
                        <s>{{ formatConflictValue(entry.previousServer) }}</s>
                        <span aria-hidden="true">→</span>
                        <span>{{
                          formatConflictValue(entry.serverValue)
                        }}</span>
                      </span>
                      <span class="save-summary__change">
                        Yours
                        <span>{{ formatConflictValue(entry.draftValue) }}</span>
                      </span>
                    </p>
                    <p v-else class="metadata-copy">
                      <span class="save-summary__change">
                        <s>{{ formatConflictValue(entry.serverValue) }}</s>
                        <span aria-hidden="true">→</span>
                        <span>{{ formatConflictValue(entry.draftValue) }}</span>
                      </span>
                    </p>
                  </div>
                  <button
                    class="button button--ghost"
                    @click="applyServerValue(entry.path)"
                  >
                    {{ entry.state === 'conflict' ? 'Take Theirs' : 'Revert' }}
                  </button>
                  <button
                    v-if="entry.state === 'conflict'"
                    class="button button--ghost"
                    @click="keepLocalValue(entry.path)"
                  >
                    Keep Mine
                  </button>
                </li>
              </ul>

              <div class="save-summary__meta metadata-copy">
                <label
                  v-if="dirtyFieldCount > 0"
                  class="field save-summary__reason"
                >
                  <span>Why are you making this change?</span>
                  <textarea
                    v-model="changeReason"
                    rows="3"
                    placeholder="Optional for now. Add a short note so people understand what changed and why."
                  />
                </label>
                <div>
                  {{ cleanDirtyCount }} edit(s) and {{ conflictCount }} missed
                  incoming change(s) in this save.
                </div>
              </div>

              <div class="save-summary__footer">
                <button
                  class="button save-summary__button"
                  :disabled="!canSave || dirtyFieldCount === 0"
                  @click="saveConfig"
                >
                  {{ saveButtonLabel }}
                </button>
                <button
                  class="button button--ghost"
                  :disabled="saving"
                  @click="refreshFromServer"
                >
                  Refresh from cluster
                </button>
              </div>
            </section>
          </article>
        </div>
      </section>
    </template>
  </main>
</template>
