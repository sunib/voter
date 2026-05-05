<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { buildInfo } from '../buildInfo'
import FieldStateMarker from '../components/admin/FieldStateMarker.vue'
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
import type { CoffeeConfig, CoffeeOrderRecord } from '../api/coffeeTypes'

type FieldConflict = {
  previousServer: unknown
  incomingServer: unknown
}

type FieldState = 'clean' | 'dirty' | 'conflict'

const loading = ref(true)
const saving = ref(false)
const authRequired = ref(false)
const authError = ref('')
const loadError = ref('')
const password = ref('')
const serverConfig = ref<CoffeeConfig | null>(null)
const draftConfig = ref<CoffeeConfig | null>(null)
const orders = ref<CoffeeOrderRecord[]>([])
const voucherUsage = ref<Record<string, number>>({})
const dirtyPaths = ref<Record<string, true>>({})
const flashedPaths = ref<Record<string, true>>({})
const conflicts = ref<Record<string, FieldConflict>>({})
const arrayFieldInputs = ref<Record<string, string>>({})

let configSource: EventSource | undefined
let orderSource: EventSource | undefined
const flashTimers = new Map<string, ReturnType<typeof setTimeout>>()

const orderedEvents = computed(() => [...orders.value].reverse())
const currency = computed(() => draftConfig.value?.spec.currency ?? 'EUR')
const dirtyFieldCount = computed(() => Object.keys(dirtyPaths.value).length)
const conflictEntries = computed(() => Object.entries(conflicts.value))
const conflictCount = computed(() => conflictEntries.value.length)
const dirtySummaryEntries = computed(() =>
  Object.keys(dirtyPaths.value)
    .sort((left, right) => {
      const leftState = fieldState(left)
      const rightState = fieldState(right)
      if (leftState !== rightState) {
        return leftState === 'conflict' ? -1 : 1
      }
      return humanizePath(left).localeCompare(humanizePath(right))
    })
    .map((path) => ({
      path,
      label: humanizePath(path),
      state: fieldState(path),
      draftValue: getPathValue(draftConfig.value, path),
      serverValue: serverValueFor(path),
      previousServer: conflictFor(path)?.previousServer,
    })),
)
const cleanDirtyCount = computed(
  () => dirtyFieldCount.value - conflictCount.value,
)
const saveButtonLabel = computed(() => {
  if (saving.value) {
    return 'Saving…'
  }
  if (dirtyFieldCount.value === 0) {
    return 'No Changes to Save'
  }
  return `Save ${dirtyFieldCount.value} Change${dirtyFieldCount.value === 1 ? '' : 's'}`
})

async function loadAdminState() {
  loading.value = true
  loadError.value = ''
  try {
    const [config, snapshot] = await Promise.all([
      getAdminCoffeeConfig(),
      getOrdersSnapshot(),
    ])
    resetConfigState(config)
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
    resetConfigState(updated)
  } catch (error) {
    loadError.value = (error as Error).message
  } finally {
    saving.value = false
  }
}

function openStreams() {
  configSource?.close()
  orderSource?.close()

  configSource = watchCoffeeConfig(
    '/public/admin/coffeeconfig/watch',
    (event) => {
      applyIncomingConfig(event.object)
    },
  )

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
  refreshFieldState('spec.products')
}

function removeProduct(index: number) {
  draftConfig.value?.spec.products.splice(index, 1)
  refreshFieldState('spec.products')
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
  clearArrayInputBranch('spec.vouchers')
  refreshFieldState('spec.vouchers')
}

function removeVoucher(index: number) {
  draftConfig.value?.spec.vouchers.splice(index, 1)
  clearArrayInputBranch('spec.vouchers')
  refreshFieldState('spec.vouchers')
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

function resetConfigState(config: CoffeeConfig) {
  const cloned = cloneConfig(config)
  serverConfig.value = cloneConfig(cloned)
  draftConfig.value = cloned
  dirtyPaths.value = {}
  conflicts.value = {}
  arrayFieldInputs.value = {}
  clearAllFlashes()
}

function applyIncomingConfig(config: CoffeeConfig) {
  const incoming = cloneConfig(config)
  if (!serverConfig.value || !draftConfig.value) {
    resetConfigState(incoming)
    return
  }

  draftConfig.value = reconcileValue(
    '',
    draftConfig.value,
    serverConfig.value,
    incoming,
  ) as CoffeeConfig
  serverConfig.value = incoming
}

function reconcileValue(
  path: string,
  draftValue: unknown,
  previousServer: unknown,
  incomingServer: unknown,
): unknown {
  if (
    Array.isArray(previousServer) ||
    Array.isArray(incomingServer) ||
    Array.isArray(draftValue)
  ) {
    const previousArray = Array.isArray(previousServer) ? previousServer : []
    const incomingArray = Array.isArray(incomingServer) ? incomingServer : []
    const draftArray = Array.isArray(draftValue) ? draftValue : []

    if (
      isDirtyPath(path) ||
      previousArray.length !== incomingArray.length ||
      draftArray.length !== previousArray.length
    ) {
      return preserveOrConflict(path, draftArray, previousArray, incomingArray)
    }

    return incomingArray.map((item, index) =>
      reconcileValue(
        joinPath(path, String(index)),
        draftArray[index],
        previousArray[index],
        item,
      ),
    )
  }

  if (
    isObjectLike(previousServer) ||
    isObjectLike(incomingServer) ||
    isObjectLike(draftValue)
  ) {
    const previousObject = isObjectLike(previousServer) ? previousServer : {}
    const incomingObject = isObjectLike(incomingServer) ? incomingServer : {}
    const draftObject = isObjectLike(draftValue) ? draftValue : {}
    const result: Record<string, unknown> = {}
    const keys = new Set([
      ...Object.keys(previousObject),
      ...Object.keys(incomingObject),
      ...Object.keys(draftObject),
    ])

    for (const key of keys) {
      result[key] = reconcileValue(
        joinPath(path, key),
        draftObject[key],
        previousObject[key],
        incomingObject[key],
      )
    }

    return result
  }

  if (deepEqual(previousServer, incomingServer)) {
    return cloneValue(draftValue)
  }

  if (!path) {
    return cloneValue(incomingServer)
  }

  if (isDirtyPath(path)) {
    if (deepEqual(draftValue, incomingServer)) {
      clearDirty(path)
      clearConflict(path)
      return cloneValue(draftValue)
    }
    setConflict(path, previousServer, incomingServer)
    return cloneValue(draftValue)
  }

  clearConflict(path)
  flashField(path)
  return cloneValue(incomingServer)
}

function preserveOrConflict(
  path: string,
  draftValue: unknown,
  previousServer: unknown,
  incomingServer: unknown,
): unknown {
  if (deepEqual(draftValue, incomingServer)) {
    clearDirty(path)
    clearConflict(path)
    return cloneValue(draftValue)
  }
  setConflict(path, previousServer, incomingServer)
  return cloneValue(draftValue)
}

function updateField(path: string, value: unknown) {
  if (!draftConfig.value) {
    return
  }
  setPathValue(draftConfig.value, path, value)
  refreshFieldState(path)
}

function refreshFieldState(path: string) {
  const draftValue = getPathValue(draftConfig.value, path)
  const serverValue = getPathValue(serverConfig.value, path)
  if (deepEqual(draftValue, serverValue)) {
    clearDirty(path)
    clearConflict(path)
    return
  }
  dirtyPaths.value = {
    ...dirtyPaths.value,
    [path]: true,
  }
}

function applyServerValue(path: string) {
  if (!draftConfig.value || !serverConfig.value) {
    return
  }
  setPathValue(
    draftConfig.value,
    path,
    cloneValue(getPathValue(serverConfig.value, path)),
  )
  clearDirtyBranch(path)
  clearConflictBranch(path)
  clearArrayInputBranch(path)
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
  if (conflicts.value[path]) {
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
  return Boolean(dirtyPaths.value[path])
}

function setConflict(
  path: string,
  previousServer: unknown,
  incomingServer: unknown,
) {
  conflicts.value = {
    ...conflicts.value,
    [path]: {
      previousServer: cloneValue(previousServer),
      incomingServer: cloneValue(incomingServer),
    },
  }
}

function clearConflict(path: string) {
  if (!conflicts.value[path]) {
    return
  }
  const next = { ...conflicts.value }
  delete next[path]
  conflicts.value = next
}

function clearConflictBranch(path: string) {
  const next = Object.fromEntries(
    Object.entries(conflicts.value).filter(
      ([candidate]) => candidate !== path && !candidate.startsWith(`${path}.`),
    ),
  ) as Record<string, FieldConflict>
  conflicts.value = next
}

function clearDirty(path: string) {
  if (!dirtyPaths.value[path]) {
    return
  }
  const next = { ...dirtyPaths.value }
  delete next[path]
  dirtyPaths.value = next
}

function clearDirtyBranch(path: string) {
  const next = Object.fromEntries(
    Object.entries(dirtyPaths.value).filter(
      ([candidate]) => candidate !== path && !candidate.startsWith(`${path}.`),
    ),
  ) as Record<string, true>
  dirtyPaths.value = next
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

function joinPath(base: string, key: string): string {
  return base ? `${base}.${key}` : key
}

function getPathValue(target: unknown, path: string): unknown {
  if (!target || !path) {
    return target
  }
  return path.split('.').reduce<unknown>((current, segment) => {
    if (current === null || current === undefined) {
      return undefined
    }
    const index = Number(segment)
    if (Array.isArray(current) && Number.isInteger(index)) {
      return current[index]
    }
    if (isObjectLike(current)) {
      return current[segment]
    }
    return undefined
  }, target)
}

function setPathValue(target: unknown, path: string, value: unknown) {
  if (!target || !path) {
    return
  }

  const segments = path.split('.')
  let current: unknown = target

  for (let index = 0; index < segments.length - 1; index += 1) {
    const segment = segments[index]
    const nextSegment = segments[index + 1]
    if (segment === undefined || nextSegment === undefined) {
      return
    }
    const nextIsIndex = Number.isInteger(Number(nextSegment))

    if (Array.isArray(current)) {
      const arrayIndex = Number(segment)
      if (current[arrayIndex] === undefined) {
        current[arrayIndex] = nextIsIndex ? [] : {}
      }
      current = current[arrayIndex]
      continue
    }

    if (!isObjectLike(current)) {
      return
    }

    if (current[segment] === undefined || current[segment] === null) {
      current[segment] = nextIsIndex ? [] : {}
    }
    current = current[segment]
  }

  const lastSegment = segments[segments.length - 1]
  if (lastSegment === undefined) {
    return
  }
  if (Array.isArray(current)) {
    current[Number(lastSegment)] = value as never
    return
  }
  if (isObjectLike(current)) {
    current[lastSegment] = value
  }
}

function isObjectLike(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function deepEqual(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right)
}

function cloneValue<T>(value: T): T {
  if (value === null || value === undefined) {
    return value
  }
  if (typeof value !== 'object') {
    return value
  }
  return JSON.parse(JSON.stringify(value)) as T
}

function formatConflictValue(value: unknown): string {
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return '(empty)'
    }
    if (value.every((item) => typeof item !== 'object' || item === null)) {
      return value.map((item) => String(item)).join(', ')
    }
    return `${value.length} item${value.length === 1 ? '' : 's'}`
  }
  if (typeof value === 'boolean') {
    return value ? 'true' : 'false'
  }
  if (isObjectLike(value)) {
    const keys = Object.keys(value)
    return keys.length === 0
      ? '(empty)'
      : `${keys.length} field${keys.length === 1 ? '' : 's'}`
  }
  if (value === null || value === undefined || value === '') {
    return '(empty)'
  }
  return String(value)
}

function humanizePath(path: string): string {
  return path
    .replace(/^spec\./, '')
    .replace(/\.apiKeySecretRef\./g, ' secret ')
    .replace(/\.orderConfirmationTemplate/g, ' mail template')
    .replace(/\.zeroAmountCheckoutAllowed/g, ' zero checkout')
    .replace(/\./g, ' / ')
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
  clearAllFlashes()
})
</script>

<template>
  <main class="page-shell page-shell--wide">
    <section class="hero-card hero-card--compact">
      <p class="eyebrow">Admin</p>
      <h1>Live coffee config</h1>
      <p class="hero-copy">
        This page reads and patches the real <code>CoffeeConfig</code> object,
        then watches for live changes and incoming orders.
      </p>
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
            <p>
              Edits are saved with JSON Merge Patch against the Kubernetes
              object.
            </p>
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

          <section class="save-summary">
            <div class="save-summary__header">
              <div class="save-summary__copy">
                <p class="eyebrow">Save summary</p>
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
                    Yellow dots are local edits. Saving writes your changes into
                    the watched config.
                  </template>
                  <template v-else>
                    Yellow dots save local edits. Red dots mean a newer server
                    value arrived; saving now keeps your changes and overwrites
                    that newer value unless you take theirs first.
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
                        entry.state === 'conflict' ? 'Missed update' : 'Edited'
                      }}
                    </span>
                  </div>
                  <p v-if="entry.state === 'conflict'" class="metadata-copy">
                    Server changed from
                    {{ formatConflictValue(entry.previousServer) }} to
                    {{ formatConflictValue(entry.serverValue) }}. Saving keeps
                    your value {{ formatConflictValue(entry.draftValue) }}.
                  </p>
                  <p v-else class="metadata-copy">
                    Saving writes {{ formatConflictValue(entry.draftValue) }}.
                    Current server value is
                    {{ formatConflictValue(entry.serverValue) }}.
                  </p>
                </div>
                <button
                  class="button button--ghost"
                  @click="applyServerValue(entry.path)"
                >
                  {{ entry.state === 'conflict' ? 'Take Theirs' : 'Revert' }}
                </button>
              </li>
            </ul>

            <div class="save-summary__meta metadata-copy">
              <div>
                Resource version
                {{ draftConfig.metadata?.resourceVersion ?? 'unknown' }}
              </div>
              <div>
                {{ cleanDirtyCount }} edit(s) and {{ conflictCount }} missed
                incoming change(s) in this save.
              </div>
              <div>
                commit {{ buildInfo.commitWithDirty }} • built
                {{ buildInfo.buildDate }}
              </div>
            </div>

            <div class="save-summary__footer">
              <button
                class="button save-summary__button"
                :disabled="saving || dirtyFieldCount === 0"
                @click="saveConfig"
              >
                {{ saveButtonLabel }}
              </button>
            </div>
          </section>
        </article>

        <article class="panel">
          <div class="section-heading">
            <h2>Live orders</h2>
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
        </article>
      </section>
    </template>
  </main>
</template>
