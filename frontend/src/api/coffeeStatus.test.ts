import { describe, expect, it } from 'vitest'

import { summarizeCoffeeStatus } from './coffeeStatus'
import type { CoffeeConfig, CoffeeStatusCondition } from './coffeeTypes'

const at = '2026-11-06T09:14:03Z'
const condition = (
  type: string,
  status: CoffeeStatusCondition['status'],
  reason: string,
  message = '',
): CoffeeStatusCondition => ({
  type,
  status,
  reason,
  message,
  lastTransitionTime: at,
})
const healthy = [
  condition('Ready', 'True', 'ShopReady'),
  condition('VouchersAvailable', 'True', 'NoVouchers'),
  condition('MenuAvailable', 'True', 'ProductsEnabled'),
  condition('PaymentsKeyAvailable', 'True', 'KeySet'),
  condition('MailKeyAvailable', 'True', 'NoKeyRequired'),
]
const config = (
  status: CoffeeConfig['status'],
  generation = 7,
): CoffeeConfig => ({
  metadata: { generation },
  spec: {} as CoffeeConfig['spec'],
  status,
})

describe('summarizeCoffeeStatus', () => {
  it('says nothing when no Voter has reported on the object', () => {
    expect(summarizeCoffeeStatus(null)).toBeNull()
    expect(summarizeCoffeeStatus(config(undefined))).toBeNull()
    // What Voter wrote before the conditions: only the key bits.
    expect(
      summarizeCoffeeStatus(config({ keys: { mail: { set: true } } })),
    ).toBeNull()
  })

  it('is ready when Voter judged this very version as ready', () => {
    const summary = summarizeCoffeeStatus(
      config({ observedGeneration: 7, conditions: healthy }),
    )
    expect(summary?.ready).toBe(true)
    expect(summary?.behind).toBe(false)
  })

  it('does not call a version ready that Voter has not looked at', () => {
    const summary = summarizeCoffeeStatus(
      config({ observedGeneration: 6, conditions: healthy }),
    )
    expect(summary?.ready).toBe(false)
    expect(summary?.behind).toBe(true)
  })

  it('gives the reason and the sentence of a failure', () => {
    const summary = summarizeCoffeeStatus(
      config({
        observedGeneration: 7,
        conditions: [
          condition(
            'Ready',
            'False',
            'VoucherDepleted',
            'Voucher X is used up.',
          ),
          condition(
            'VouchersAvailable',
            'False',
            'VoucherDepleted',
            'Voucher X is used up.',
          ),
        ],
      }),
    )
    expect(summary).toMatchObject({
      ready: false,
      behind: false,
      reason: 'VoucherDepleted',
      message: 'Voucher X is used up.',
    })
  })

  it('lists the parts in the order they are judged, without Ready', () => {
    const summary = summarizeCoffeeStatus(
      config({ observedGeneration: 7, conditions: healthy }),
    )
    expect(summary?.parts.map((part) => part.label)).toEqual([
      'Menu',
      'Mail',
      'Payments',
      'Vouchers',
    ])
  })

  it('carries the time of the first order, and no time without one', () => {
    const conditions = healthy
    expect(
      summarizeCoffeeStatus(
        config({ observedGeneration: 7, conditions, firstOrderTime: at }),
      )?.firstOrderTime,
    ).toBe(at)
    expect(
      summarizeCoffeeStatus(config({ observedGeneration: 7, conditions }))
        ?.firstOrderTime,
    ).toBeUndefined()
  })
})
