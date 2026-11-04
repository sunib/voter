import { describe, expect, it } from 'vitest'

import { invalidCartItems } from './invalidCartItems'

describe('invalidCartItems', () => {
  const menu = [
    { sku: 'tea', enabled: true },
    { sku: 'mocha', enabled: false },
  ]

  it('accepts what is on the menu', () => {
    expect(invalidCartItems({ tea: 2 }, menu)).toEqual([])
  })

  it('names what left the menu or was disabled', () => {
    expect(invalidCartItems({ tea: 1, mocha: 1, espresso: 3 }, menu)).toEqual([
      { sku: 'mocha', quantity: 1 },
      { sku: 'espresso', quantity: 3 },
    ])
  })

  it('ignores a quantity of zero', () => {
    expect(invalidCartItems({ espresso: 0 }, menu)).toEqual([])
  })
})
