import {
  deepEqual,
  get,
  type Change,
  type KubernetesStructuralSchema,
  type Path,
} from '@configbutler/krm-stream'

// Turning one atomic change into the fields that actually moved.
//
// A merge patch replaces a list whole -- RFC 7386 has no way to address one
// element -- so the library reports an edit anywhere inside `spec.products` as
// a single change at `["spec","products"]`, carrying the old and new arrays.
// That is right for saving and wrong for showing: a screen that asks "is
// spec.products.2.priceCents dirty?" by testing whether some change path is a
// prefix of it gets `true` for every field of every product, and one price edit
// lights up the entire menu.
//
// So the array is reopened here, for display only. The patch is still the
// library's, unchanged and still whole-array.
//
// A plain list is reopened by position, the only identity it has. A keyed list
// (`x-kubernetes-list-type: map`, the same schema the store merges with) is
// reopened by key, so removing one product reports that product and not a
// rewrite of every product after it. Paths into a keyed list still use the
// DRAFT's index, because that is the row the screen renders; a removed element
// has no row, so it is named by its key instead (`spec.products.sku=latte`).

type Schema = KubernetesStructuralSchema | undefined

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function child(schema: Schema, segment: string | number): Schema {
  return typeof segment === 'number'
    ? schema?.items
    : schema?.properties?.[segment]
}

function schemaAt(schema: Schema, path: Path): Schema {
  return path.reduce<Schema>((node, segment) => child(node, segment), schema)
}

function listKeys(schema: Schema): string[] | undefined {
  return schema?.['x-kubernetes-list-type'] === 'map'
    ? schema['x-kubernetes-list-map-keys']
    : undefined
}

/** How a removed element is named in a path: by its key, since it has no row. */
function removedSegment(keys: string[], element: unknown): string {
  const value = element as Record<string, unknown>
  return keys.map((key) => `${key}=${String(value[key])}`).join(',')
}

type Pair = { old?: number; new?: number }

/** Lines two keyed lists up by key. Undefined when either list cannot be keyed
 *  -- an element without its key, or two sharing one, which is a moment while
 *  somebody types a SKU -- and the caller falls back to position, as the store
 *  does. An element whose key changed in place (a renamed SKU) has no partner
 *  by key; it is paired with whatever sits at the same index on the other side,
 *  so a rename reads as an edit to the key rather than a removal and an add. */
function pairElements(
  keys: string[],
  before: unknown[],
  after: unknown[],
): Pair[] | undefined {
  const index = (list: unknown[]) => {
    const byIdentity = new Map<string, number>()
    for (const [i, element] of list.entries()) {
      if (!isPlainObject(element)) return undefined
      const parts = keys.map((key) => element[key])
      if (parts.some((part) => part == null || typeof part === 'object'))
        return undefined
      const identity = JSON.stringify(parts)
      if (byIdentity.has(identity)) return undefined
      byIdentity.set(identity, i)
    }
    return byIdentity
  }
  const oldIndex = index(before)
  const newIndex = index(after)
  if (!oldIndex || !newIndex) return undefined

  const oldOf = new Map<number, number>()
  for (const [identity, j] of newIndex) {
    const i = oldIndex.get(identity)
    if (i !== undefined) oldOf.set(j, i)
  }
  const matchedOld = new Set(oldOf.values())
  for (let j = 0; j < after.length; j++) {
    if (!oldOf.has(j) && j < before.length && !matchedOld.has(j)) {
      oldOf.set(j, j)
      matchedOld.add(j)
    }
  }
  const pairs: Pair[] = after.map((_, j) => ({ old: oldOf.get(j), new: j }))
  for (let i = 0; i < before.length; i++) {
    if (!matchedOld.has(i)) pairs.push({ old: i })
  }
  return pairs
}

function walk(
  path: Path,
  before: unknown,
  after: unknown,
  schema: Schema,
  out: Change[],
): void {
  if (deepEqual(before, after)) {
    return
  }
  // Two arrays, or two objects, are compared member by member. Anything else --
  // a scalar, a value that appeared or vanished, a shape that changed from list
  // to object -- is where the walk stops and a change is reported.
  if (Array.isArray(before) && Array.isArray(after)) {
    const keys = listKeys(schema)
    const pairs = keys && pairElements(keys, before, after)
    if (keys && pairs) {
      for (const pair of pairs) {
        if (pair.new !== undefined) {
          walk(
            [...path, pair.new],
            pair.old === undefined ? undefined : before[pair.old],
            after[pair.new],
            schema?.items,
            out,
          )
        } else {
          const removed = before[pair.old!]
          out.push({
            path: [...path, removedSegment(keys, removed)],
            kind: 'delete',
            old: removed,
            new: undefined,
          })
        }
      }
      return
    }
    for (let i = 0; i < Math.max(before.length, after.length); i++) {
      walk([...path, i], before[i], after[i], schema?.items, out)
    }
    return
  }
  if (isPlainObject(before) && isPlainObject(after)) {
    for (const key of new Set([
      ...Object.keys(before),
      ...Object.keys(after),
    ])) {
      walk([...path, key], before[key], after[key], child(schema, key), out)
    }
    return
  }
  out.push({
    path,
    kind:
      before === undefined ? 'add' : after === undefined ? 'delete' : 'update',
    old: before,
    new: after,
  })
}

/** The changes a person would recognize: one entry per field whose value
 *  differs, rather than one entry per atomically-replaced container.
 *
 *  Without `keyedLists`, removing an array element reports every field after
 *  it, because by position those really do hold different values. With them, a
 *  keyed list is compared element by element, by key. */
export function leafChanges(
  changes: readonly Change[],
  keyedLists?: KubernetesStructuralSchema,
): Change[] {
  const out: Change[] = []
  for (const change of changes) {
    walk(
      change.path,
      change.old,
      change.new,
      schemaAt(keyedLists, change.path),
      out,
    )
  }
  return out
}

/** Where in `server` the thing at `path` in `draft` lives: the same path,
 *  except that an index into a keyed list is swapped for the index of the
 *  element with the same key. Undefined when the server has no such element --
 *  a product added in the draft. */
export function serverPathFor(
  server: unknown,
  draft: unknown,
  path: Path,
  keyedLists?: KubernetesStructuralSchema,
): Path | undefined {
  const out: Path = []
  let schema: Schema = keyedLists
  for (const segment of path) {
    const serverHere = get(server, out)
    const draftHere = get(draft, path.slice(0, out.length))
    const keys = listKeys(schema)
    if (
      keys &&
      typeof segment === 'number' &&
      Array.isArray(serverHere) &&
      Array.isArray(draftHere)
    ) {
      const pairs = pairElements(keys, serverHere, draftHere)
      if (pairs) {
        const old = pairs.find((pair) => pair.new === segment)?.old
        if (old === undefined) return undefined
        out.push(old)
        schema = child(schema, segment)
        continue
      }
    }
    out.push(segment)
    schema = child(schema, segment)
  }
  return out
}

/** What pressing Revert on a path from leafChanges writes into the draft:
 *  `value` at `target`, or a removal of `target` when value is undefined.
 *
 *  Reverting a removed element puts the server's element back, at its server
 *  position where it still fits. Reverting an added element takes it out. Any
 *  other path takes the server's value for the same element, found by key. */
export function revertTarget(
  server: unknown,
  draft: unknown,
  path: Path,
  keyedLists?: KubernetesStructuralSchema,
): { target: Path; value: unknown } {
  const listPath = path.slice(0, -1)
  const segment = path[path.length - 1]
  const keys = listKeys(schemaAt(keyedLists, listPath))
  const serverList = get(server, listPath)
  const draftList = get(draft, listPath)
  if (keys && Array.isArray(serverList) && Array.isArray(draftList)) {
    if (typeof segment === 'string') {
      const at = serverList.findIndex(
        (element) =>
          isPlainObject(element) && removedSegment(keys, element) === segment,
      )
      if (at >= 0) {
        const restored = [...draftList]
        restored.splice(Math.min(at, restored.length), 0, serverList[at])
        return { target: listPath, value: restored }
      }
    } else if (!serverPathFor(server, draft, path, keyedLists)) {
      return {
        target: listPath,
        value: draftList.filter((_, i) => i !== segment),
      }
    }
  }
  const from = serverPathFor(server, draft, path, keyedLists)
  return { target: path, value: from ? get(server, from) : undefined }
}
