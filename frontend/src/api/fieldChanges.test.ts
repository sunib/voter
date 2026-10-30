import { describe, expect, it } from 'vitest'
import type { Change } from '@configbutler/krm-stream'
import { leafChanges, revertTarget, serverPathFor } from './fieldChanges'
import { coffeeConfigKeyedLists as keyed } from './liveCoffeeConfig'

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

  // Without a schema a JSON list has no identity but position, so removing the
  // first of two products rewrites index 0 and drops index 1.
  it('reports the shift when a product is removed from a plain list', () => {
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

// Products are a keyed list (sku), which is how the store merges them; the
// screen reopens them the same way.
describe('leafChanges on a keyed list', () => {
  const three = [
    ...menu(300),
    { sku: 'c', name: 'Mocha', priceCents: 400, enabled: true },
  ]
  const spec = (products: unknown[]) => ({ spec: { products } })

  // The reported symptom: Remove on the first product marked every product
  // after it as edited.
  it('reports a removed product as one removal, named by its sku', () => {
    expect(
      leafChanges(
        [{ ...priceEdit, old: three, new: three.slice(1) }],
        keyed,
      ).map((c) => [c.path.join('.'), c.kind]),
    ).toEqual([['spec.products.sku=a', 'delete']])
  })

  it('reports an edit after a removal at the row the draft renders', () => {
    const after = [three[1], { ...three[2], priceCents: 450 }]
    expect(
      leafChanges([{ ...priceEdit, old: three, new: after }], keyed).map(
        (c) => [c.path.join('.'), c.old, c.new],
      ),
    ).toEqual([
      ['spec.products.1.priceCents', 400, 450],
      ['spec.products.sku=a', three[0], undefined],
    ])
  })

  // Typing a new SKU changes the key under the cursor. That is an edit of the
  // sku, not a product removed and another added.
  it('reports a renamed sku as an edit of the sku', () => {
    const after = [{ ...three[0], sku: 'a2' }, three[1], three[2]]
    expect(
      leafChanges([{ ...priceEdit, old: three, new: after }], keyed).map((c) =>
        c.path.join('.'),
      ),
    ).toEqual(['spec.products.0.sku'])
  })

  it('finds the server value of a field by sku, not by row', () => {
    const draft = spec(three.slice(1))
    expect(
      serverPathFor(
        spec(three),
        draft,
        ['spec', 'products', 0, 'priceCents'],
        keyed,
      ),
    ).toEqual(['spec', 'products', 1, 'priceCents'])
    expect(
      serverPathFor(
        spec(menu(300)),
        spec(three),
        ['spec', 'products', 2, 'name'],
        keyed,
      ),
    ).toBeUndefined()
  })

  it('reverts a field to the same product on the server', () => {
    const server = spec(three)
    const draft = spec([three[1], { ...three[2], priceCents: 450 }])
    expect(
      revertTarget(server, draft, ['spec', 'products', 1, 'priceCents'], keyed),
    ).toEqual({ target: ['spec', 'products', 1, 'priceCents'], value: 400 })
  })

  it('puts a removed product back where it was', () => {
    expect(
      revertTarget(
        spec(three),
        spec([three[0], three[2]]),
        ['spec', 'products', 'sku=b'],
        keyed,
      ),
    ).toEqual({ target: ['spec', 'products'], value: three })
  })

  it('takes an added product out again', () => {
    expect(
      revertTarget(
        spec(menu(300)),
        spec(three),
        ['spec', 'products', 2],
        keyed,
      ),
    ).toEqual({ target: ['spec', 'products'], value: menu(300) })
  })
})
