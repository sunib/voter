import { describe, expect, it } from 'vitest'
import type { Change } from '@configbutler/krm-stream'
import { leafChanges } from './fieldChanges'

const menu = (price: number, name = 'Espresso') => [
  { sku: 'a', name, priceCents: price, enabled: true },
  { sku: 'b', name: 'Latte', priceCents: 350, enabled: true },
]

// The shape the library actually emits for an edit inside a list: one change,
// at the list, carrying both whole arrays. Every test below starts from this
// because it is what the screen is handed.
const priceEdit: Change = {
  path: ['spec', 'products'],
  kind: 'update',
  old: menu(300),
  new: menu(325),
}

describe('leafChanges', () => {
  it('reports one field for a single price edit, not the whole menu', () => {
    expect(leafChanges([priceEdit])).toEqual([
      {
        path: ['spec', 'products', 0, 'priceCents'],
        kind: 'update',
        old: 300,
        new: 325,
      },
    ])
  })

  // The bug as it was reported: every field of every product looked changed.
  // A prefix test against the raw change says yes to all of them; against the
  // expanded list it says yes only to the price that moved.
  it('leaves the other fields of the edited product alone', () => {
    const dirty = leafChanges([priceEdit]).map((change) =>
      change.path.join('.'),
    )
    expect(dirty).not.toContain('spec.products.0.name')
    expect(dirty).not.toContain('spec.products.0.sku')
    expect(dirty).not.toContain('spec.products.1.priceCents')
    expect(dirty).toHaveLength(1)
  })

  it('reports two fields when two are edited', () => {
    expect(
      leafChanges([{ ...priceEdit, new: menu(325, 'Espresso Doppio') }]).map(
        (change) => change.path.join('.'),
      ),
    ).toEqual(['spec.products.0.name', 'spec.products.0.priceCents'])
  })

  it('passes a scalar change through untouched', () => {
    const change: Change = {
      path: ['spec', 'shopName'],
      kind: 'update',
      old: 'Base',
      new: 'Renamed',
    }
    expect(leafChanges([change])).toEqual([change])
  })

  // A whole new product is one thing that happened, not four. It stops at the
  // element because there is nothing on the old side to compare its fields
  // against -- and a screen prefix-matching this still marks every field of the
  // new product, which is exactly right: all of them are new.
  it('reports an appended product as one addition', () => {
    const added = [
      ...menu(300),
      { sku: 'c', name: 'New Coffee', priceCents: 300, enabled: true },
    ]
    expect(
      leafChanges([{ ...priceEdit, old: menu(300), new: added }]).map((c) => [
        c.path.join('.'),
        c.kind,
      ]),
    ).toEqual([['spec.products.2', 'add']])
  })

  // Honest rather than flattering: a JSON list has no identity but position, so
  // removing the first of two products really does rewrite index 0 and drop
  // index 1. Pretending otherwise would mean claiming a patch this screen is
  // not sending.
  it('reports the shift when a product is removed', () => {
    expect(
      leafChanges([{ ...priceEdit, old: menu(300), new: [menu(300)[1]] }]).map(
        (c) => [c.path.join('.'), c.kind],
      ),
    ).toEqual([
      ['spec.products.0.sku', 'update'],
      ['spec.products.0.name', 'update'],
      ['spec.products.0.priceCents', 'update'],
      ['spec.products.1', 'delete'],
    ])
  })

  it('keeps a whole list that vanished as one entry', () => {
    expect(
      leafChanges([
        {
          path: ['spec', 'vouchers'],
          kind: 'delete',
          old: menu(300),
          new: undefined,
        },
      ]),
    ).toEqual([
      {
        path: ['spec', 'vouchers'],
        kind: 'delete',
        old: menu(300),
        new: undefined,
      },
    ])
  })

  it('ignores a change that is not one', () => {
    expect(
      leafChanges([{ ...priceEdit, old: menu(300), new: menu(300) }]),
    ).toEqual([])
  })
})
