export type KubeObjectMeta = {
  uid?: string
  resourceVersion?: string
  name?: string
  namespace?: string
  generation?: number
  labels?: Record<string, string>
}

export type SecretKeyRef = {
  name?: string
  key?: string
}

export type CoffeeProductSpec = {
  sku: string
  name: string
  priceCents: number
  description?: string
  enabled: boolean
}

export type CoffeeVoucherSpec = {
  code: string
  enabled: boolean
  discountType: string
  discountValue: number
  maximumUsage: number
  appliesToProducts: string[]
  displayMessage?: string
}

export type CoffeeMailSpec = {
  provider?: string
  apiKeySecretRef?: SecretKeyRef
  orderConfirmationTemplate?: string
  fromAddress?: string
}

export type CoffeePaymentsSpec = {
  provider?: string
  apiKeySecretRef?: SecretKeyRef
  mode?: string
  zeroAmountCheckoutAllowed?: boolean
}

export type CoffeeConfigSpec = {
  shopName?: string
  bannerText?: string
  qrUrl?: string
  currency?: string
  products: CoffeeProductSpec[]
  vouchers: CoffeeVoucherSpec[]
  mail?: CoffeeMailSpec
  payments?: CoffeePaymentsSpec
}

export type CoffeeConfig = {
  apiVersion?: string
  kind?: string
  metadata?: KubeObjectMeta
  spec: CoffeeConfigSpec
}

export type StorefrontProduct = {
  sku: string
  name: string
  description?: string
  enabled: boolean
  basePriceCents: number
  displayPriceCents: number
  voucherState: string
}

export type StorefrontResponse = {
  shop: {
    name: string
    bannerText: string
    currency: string
  }
  voucher: {
    code: string
    presentInUrl: boolean
    displayMessage: string
    state: string
  }
  products: StorefrontProduct[]
}

export type CoffeeOrderItemRequest = {
  sku: string
  quantity: number
}

export type CoffeeOrderRequest = {
  voucherCode?: string
  source?: Record<string, string>
  items: CoffeeOrderItemRequest[]
}

export type CoffeeOrderFailure = {
  code: string
  message: string
}

export type CoffeeOrderResponse = {
  orderId: string
  status: 'placed' | 'rejected'
  currency: string
  totalPriceCents: number
  failure?: CoffeeOrderFailure
}
