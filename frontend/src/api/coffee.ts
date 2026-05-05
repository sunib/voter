import type {
  CoffeeConfig,
  CoffeeConfigChangeRecord,
  CoffeeConfigChangesSnapshot,
  CoffeeConfigWatchEvent,
  CoffeeOrderRecord,
  CoffeeOrderRequest,
  CoffeeOrderResponse,
  CoffeeOrdersSnapshot,
  CoffeeProductSpec,
  CoffeeVoucherSpec,
  StorefrontResponse,
} from './coffeeTypes'

export type PublicBuildInfoResponse = {
  gitCommit: string
  isDirty: boolean
  buildDate: string
  commitWithDirty: string
}

export type PublicSessionResponse = {
  nickname: string
}

export type ApiError = Error & {
  status: number
  body?: unknown
}

function createApiError(status: number, body: unknown): ApiError {
  const message =
    typeof body === 'string'
      ? body
      : ((body as { message?: string } | undefined)?.message ??
        `Request failed (${status})`)
  const err = new Error(message) as ApiError
  err.status = status
  err.body = body
  return err
}

async function readJsonOrText(res: Response): Promise<unknown> {
  const contentType = res.headers.get('content-type') ?? ''
  if (contentType.includes('application/json')) {
    return await res.json()
  }
  return await res.text()
}

async function requestJson<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  if (init?.body !== undefined && !headers.has('content-type')) {
    headers.set('content-type', 'application/json')
  }

  const res = await fetch(path, {
    ...init,
    headers,
    credentials: 'include',
  })

  if (!res.ok) {
    throw createApiError(res.status, await readJsonOrText(res))
  }
  return (await res.json()) as T
}

export async function getStorefront(
  voucherCode?: string,
): Promise<StorefrontResponse> {
  const url = new URL('/public/storefront', window.location.origin)
  if (voucherCode) {
    url.searchParams.set('voucher', voucherCode)
  }
  return await requestJson<StorefrontResponse>(url.pathname + url.search)
}

export async function getPublicBuildInfo(): Promise<PublicBuildInfoResponse> {
  return await requestJson<PublicBuildInfoResponse>('/public/build-info')
}

export async function submitOrder(
  input: CoffeeOrderRequest,
): Promise<CoffeeOrderResponse> {
  return await requestJson<CoffeeOrderResponse>('/public/orders', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export async function loginPublic(
  nickname: string,
  code: string,
): Promise<void> {
  const res = await fetch('/public/login', {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
    },
    credentials: 'include',
    body: JSON.stringify({ nickname, code }),
  })

  if (!res.ok) {
    throw createApiError(res.status, await readJsonOrText(res))
  }
}

export async function getPublicSession(): Promise<PublicSessionResponse> {
  return await requestJson<PublicSessionResponse>('/public/session')
}

export async function logoutPublic(): Promise<void> {
  const res = await fetch('/public/logout', {
    method: 'POST',
    credentials: 'include',
  })

  if (!res.ok) {
    throw createApiError(res.status, await readJsonOrText(res))
  }
}

export async function getAdminCoffeeConfig(): Promise<CoffeeConfig> {
  return await requestJson<CoffeeConfig>('/public/admin/coffeeconfig')
}

export type PatchAdminCoffeeConfigOptions = {
  reason?: string
}

export async function patchAdminCoffeeConfig(
  patch: unknown,
  options?: PatchAdminCoffeeConfigOptions,
): Promise<CoffeeConfig> {
  const headers = new Headers({
    'content-type': 'application/merge-patch+json',
  })
  if (options?.reason?.trim()) {
    headers.set('x-change-reason', options.reason.trim())
  }
  return await requestJson<CoffeeConfig>('/public/admin/coffeeconfig', {
    method: 'PATCH',
    headers,
    body: JSON.stringify(patch),
  })
}

export async function getCoffeeConfigChangesSnapshot(): Promise<CoffeeConfigChangesSnapshot> {
  return await requestJson<CoffeeConfigChangesSnapshot>(
    '/public/admin/coffeeconfig/changes',
  )
}

export async function getOrdersSnapshot(): Promise<CoffeeOrdersSnapshot> {
  return await requestJson<CoffeeOrdersSnapshot>('/public/admin/orders')
}

function openEventStream<T>(
  path: string,
  onMessage: (payload: T) => void,
  onError?: (event: Event) => void,
): EventSource {
  const source = new EventSource(path)
  source.onmessage = (event) => {
    onMessage(JSON.parse(event.data) as T)
  }
  if (onError) {
    source.onerror = onError
  }
  return source
}

export function watchCoffeeConfig(
  path: string,
  onMessage: (event: CoffeeConfigWatchEvent) => void,
  onError?: (event: Event) => void,
): EventSource {
  return openEventStream(path, onMessage, onError)
}

export function watchOrders(
  onMessage: (event: CoffeeOrderRecord) => void,
  onError?: (event: Event) => void,
): EventSource {
  return openEventStream('/public/admin/orders/stream', onMessage, onError)
}

export function watchCoffeeConfigChanges(
  onMessage: (event: CoffeeConfigChangeRecord) => void,
  onError?: (event: Event) => void,
): EventSource {
  return openEventStream(
    '/public/admin/coffeeconfig/changes/stream',
    onMessage,
    onError,
  )
}

export function buildStorefrontFromConfig(
  config: CoffeeConfig,
  voucherCode?: string,
): StorefrontResponse {
  const normalizedVoucherCode = (voucherCode ?? '').trim()
  const voucher = findVoucher(config.spec.vouchers, normalizedVoucherCode)
  const voucherState = resolveVoucherState(
    config.spec.products,
    voucher,
    normalizedVoucherCode,
  )

  return {
    shop: {
      name: config.spec.shopName ?? 'TestNet Coffee',
      bannerText: config.spec.bannerText ?? '',
      currency: config.spec.currency ?? 'EUR',
    },
    voucher: {
      code: normalizedVoucherCode,
      presentInUrl: normalizedVoucherCode.length > 0,
      displayMessage: voucher?.displayMessage ?? '',
      state: voucherState,
    },
    products: config.spec.products
      .filter((product) => product.enabled)
      .map((product) => ({
        sku: product.sku,
        name: product.name,
        description: product.description,
        enabled: product.enabled,
        basePriceCents: product.priceCents,
        displayPriceCents:
          voucher &&
          voucherState === 'assumed-applied' &&
          voucherAppliesToProduct(voucher, product.sku)
            ? discountedUnitPrice(product.priceCents, voucher)
            : product.priceCents,
        voucherState:
          voucher &&
          voucherState === 'assumed-applied' &&
          voucherAppliesToProduct(voucher, product.sku)
            ? 'assumed-applied'
            : 'not-applied',
      })),
  }
}

function findVoucher(
  vouchers: CoffeeVoucherSpec[],
  code: string,
): CoffeeVoucherSpec | undefined {
  const normalizedCode = code.toLowerCase()
  return vouchers.find(
    (voucher) => voucher.code.trim().toLowerCase() === normalizedCode,
  )
}

function resolveVoucherState(
  products: CoffeeProductSpec[],
  voucher: CoffeeVoucherSpec | undefined,
  voucherCode: string,
): string {
  if (voucherCode === '') {
    return 'not-present'
  }
  if (!voucher || !voucher.enabled) {
    return 'invalid'
  }
  if (
    !products.some(
      (product) =>
        product.enabled && voucherAppliesToProduct(voucher, product.sku),
    )
  ) {
    return 'not-applicable'
  }
  return 'assumed-applied'
}

function voucherAppliesToProduct(
  voucher: CoffeeVoucherSpec,
  sku: string,
): boolean {
  if (voucher.appliesToProducts.length === 0) {
    return true
  }
  return voucher.appliesToProducts.some(
    (productSku) => productSku.trim() === sku,
  )
}

function discountedUnitPrice(
  priceCents: number,
  voucher: CoffeeVoucherSpec,
): number {
  if (voucher.discountType === 'fixed') {
    return Math.max(priceCents - voucher.discountValue, 0)
  }
  return Math.max(
    priceCents - Math.floor((priceCents * voucher.discountValue) / 100),
    0,
  )
}

export function formatMoney(currency: string, cents: number): string {
  try {
    return new Intl.NumberFormat(undefined, {
      style: 'currency',
      currency,
    }).format(cents / 100)
  } catch {
    return `${currency || '???'} ${(cents / 100).toFixed(2)}`
  }
}
