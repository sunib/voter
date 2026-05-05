import { defineStore } from 'pinia'
import type { CoffeeConfig, CoffeeOrderResponse, StorefrontResponse } from '../api/coffeeTypes'

type CartState = {
  loaded: boolean
  voucherCode: string
  items: Record<string, number>
  storefront?: StorefrontResponse
  activeConfig?: CoffeeConfig
  lastOrder?: CoffeeOrderResponse
}

type PersistedCartState = {
  voucherCode: string
  items: Record<string, number>
}

const storageKey = 'coffee-demo:cart:v1'

export const useCartStore = defineStore('cart', {
  state: (): CartState => ({
    loaded: false,
    voucherCode: '',
    items: {},
    storefront: undefined,
    activeConfig: undefined,
    lastOrder: undefined,
  }),
  getters: {
    totalItems(state): number {
      return Object.values(state.items).reduce((sum, quantity) => sum + quantity, 0)
    },
  },
  actions: {
    ensureLoaded() {
      if (this.loaded) {
        return
      }
      this.loaded = true
      try {
        const raw = localStorage.getItem(storageKey)
        if (!raw) {
          return
        }
        const parsed = JSON.parse(raw) as PersistedCartState
        this.voucherCode = parsed.voucherCode ?? ''
        this.items = parsed.items ?? {}
      } catch {
        this.voucherCode = ''
        this.items = {}
      }
    },
    persist() {
      const payload: PersistedCartState = {
        voucherCode: this.voucherCode,
        items: this.items,
      }
      localStorage.setItem(storageKey, JSON.stringify(payload))
    },
    setVoucherCode(voucherCode: string) {
      this.voucherCode = voucherCode
      this.persist()
    },
    setStorefront(storefront: StorefrontResponse) {
      this.storefront = storefront
    },
    setActiveConfig(config: CoffeeConfig) {
      this.activeConfig = config
    },
    setQuantity(sku: string, quantity: number) {
      if (quantity <= 0) {
        delete this.items[sku]
      } else {
        this.items[sku] = quantity
      }
      this.items = { ...this.items }
      this.persist()
    },
    clearCart() {
      this.items = {}
      this.persist()
    },
    setLastOrder(order: CoffeeOrderResponse | undefined) {
      this.lastOrder = order
    },
  },
})
