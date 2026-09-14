import { deepEqual, type Change, type Path } from '@configbutler/krm-stream'

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

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function walk(
  path: Path,
  before: unknown,
  after: unknown,
  out: Change[],
): void {
  if (deepEqual(before, after)) {
    return
  }
  // Two arrays, or two objects, are compared member by member. Anything else --
  // a scalar, a value that appeared or vanished, a shape that changed from list
  // to object -- is where the walk stops and a change is reported.
  if (Array.isArray(before) && Array.isArray(after)) {
    for (let i = 0; i < Math.max(before.length, after.length); i++) {
      walk([...path, i], before[i], after[i], out)
    }
    return
  }
  if (isPlainObject(before) && isPlainObject(after)) {
    for (const key of new Set([
      ...Object.keys(before),
      ...Object.keys(after),
    ])) {
      walk([...path, key], before[key], after[key], out)
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
 *  Removing an array element still reports every field after it, because after
 *  a removal those positions really do hold different values -- indexes are the
 *  only identity a JSON list has. */
export function leafChanges(changes: readonly Change[]): Change[] {
  const out: Change[] = []
  for (const change of changes) {
    walk(change.path, change.old, change.new, out)
  }
  return out
}
