import type { SaveRequest } from '@configbutler/krm-stream'
import type {
  CoffeeConfig,
  CoffeeOrderRequest,
  CoffeeOrderResponse,
  CoffeeProductSpec,
  CoffeeVoucherSpec,
  StorefrontResponse,
} from './coffeeTypes'
import { currentCsrfToken } from './session'

export type PublicBuildInfoResponse = {
  gitCommit: string
  isDirty: boolean
  buildDate: string
  commitWithDirty: string
}

export type PublicSessionResponse = {
  stableId: string
  displayName: string
  email: string
  namespace?: string
}

export type LoginRequest = {
  code: string
  stableId: string
  displayName: string
  email: string
}

export type ApiError = Error & {
  status: number
  body?: unknown
}

function createApiError(status: number, body: unknown): ApiError {
  const details = body as { message?: unknown; error?: unknown } | null
  const message =
    typeof body === 'string'
      ? body
      : typeof details?.message === 'string'
        ? details.message
        : typeof details?.error === 'string'
          ? details.error
          : `Request failed (${status})`
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

  // Every cookie-authenticated mutation needs CSRF proof, or the backend
  // answers 403. GET and HEAD are not mutations and the backend does not ask.
  const method = (init?.method ?? 'GET').toUpperCase()
  if (method !== 'GET' && method !== 'HEAD' && !headers.has('x-csrf-token')) {
    headers.set('x-csrf-token', currentCsrfToken())
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

export async function getAdminCoffeeConfig(): Promise<CoffeeConfig> {
  return await requestJson<CoffeeConfig>('/public/coffeeconfig', {
    cache: 'no-store',
  })
}

export type PatchAdminCoffeeConfigOptions = {
  reason?: string
}

/** Receipt only: commitRequested means acceptance, not an observed Git commit. */
export type PatchCoffeeConfigResult = {
  saved: boolean
  commitRequested?: boolean
  commitRequest?: string
  commitError?: string
}

export async function patchAdminCoffeeConfig(
  intent: SaveRequest,
  options?: PatchAdminCoffeeConfigOptions,
): Promise<PatchCoffeeConfigResult> {
  const headers = new Headers({
    'content-type': 'application/json',
  })
  if (options?.reason?.trim()) {
    headers.set('x-change-reason', options.reason.trim())
  }
  return await requestJson<PatchCoffeeConfigResult>('/public/coffeeconfig', {
    method: 'PATCH',
    headers,
    body: JSON.stringify(intent),
  })
}

/** How many times each voucher has been redeemed, keyed by the lower-cased
 *  code. `scope: "process"` is a warning, not decoration: the count is one
 *  replica's, since its last restart. */
export type VoucherUsageSnapshot = {
  voucherUsage: Record<string, number>
  scope: string
}

export async function getVoucherUsage(): Promise<VoucherUsageSnapshot> {
  return await requestJson<VoucherUsageSnapshot>('/public/vouchers')
}

export function buildStorefrontFromConfig(
  config: CoffeeConfig,
  voucherCode?: string,
): StorefrontResponse {
  const normalizedVoucherCode = (voucherCode ?? '').trim()
  const voucher = findVoucher(config.spec.vouchers ?? [], normalizedVoucherCode)
  const voucherState = resolveVoucherState(
    config.spec.products ?? [],
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
    products: (config.spec.products ?? [])
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
