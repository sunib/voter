# Feedback for krm-stream

Notes from using `@configbutler/krm-stream` in Voter's admin screen, kept for the
library's maintainers. Each entry says what the consumer hit, what the library
does today, and whether we think the library should change — including the
entries where we concluded it should not.

Voter is the library's first real consumer, so "Voter worked around it" is data
about the next consumer, not a complaint. Every claim below is pinned by a test
in this repository; the test names are given so a maintainer can read the
evidence rather than take our word for it.

Against **0.4.0** unless an entry says otherwise.

---

## 1. `changes()` is a save plan, and screens read it as a field list

**2026-09-14 · open question · `store.changes()`**

Editing one product price in Voter's menu editor marked every field of every
product as changed, and the save button offered to "Save 1 Change" while the
whole form was lit up.

The library is not wrong here. `#diff` stops at an array and reports it as one
value, which is exactly what RFC 7386 will send, and `merge.js` says so
deliberately: *"Arrays flash atomically, at the array's own path — a UI
highlights 'the conditions changed', not 'conditions[0].reason changed'."* For
`status.conditions` that is plainly right.

It is less right for an editable list rendered as a form. `spec.products` is
eleven inputs per element, each with its own dirty marker, and the screen asks
per input: *is this field dirty?* The obvious implementation — "is some change's
path a prefix of mine?" — answers yes for all of them, because there is exactly
one change and it sits above every field. Nothing in the docs warns that this
reading is wrong; `docs/client-state-model.md` describes arrays as atomic under
"Arrays and associative lists", which reads as a merge and patch statement.

What we noticed afterwards: **`store.isDirty(id, path)` already does the right
thing.** It compares the values at the exact path and is what a field marker
wants. It is not mentioned in the UI-integration section, which lists `draft`,
`status`, `changes`, `conflicts` and `redactions` as the render-time queries — so
we reached for `changes` and never found it.

`isDirty` only answers one path at a time, though, and the screen also needs to
*enumerate*: a count for the button, and a review list of what will be saved with
each old and new value. There is no leaf-level enumeration, so Voter added
`frontend/src/api/fieldChanges.ts` — `leafChanges()`, which walks the two values
of a container change in step and reports the leaves that differ. Display only;
the patch is still the library's and still whole-array.

Two behaviours fell out of that walk and we kept them, because the alternatives
would claim something the patch does not do:

- A newly appended element is **one** `add` entry at the element, not one per
  field. There is nothing on the old side to compare its fields against.
- Removing an element reports the ones after it as changed. Index is the only
  identity a JSON list has, and those positions really do hold new values once
  the patch lands.

**The question for the library:** is leaf-level enumeration in scope? Three
honest options, and we do not think the first is obviously right:

1. **Nothing changes.** `changes()` is the save plan, `isDirty()` is the field
   query, and a consumer wanting a field list writes the walk. Cheapest, and
   defensible — but the walk is ~40 lines that every form-shaped consumer will
   write, and writing it wrong is silent: the screen looks plausible and lies.
2. **Documentation only.** Say in the UI-integration section that `changes()` is
   container-granular by design, that `isDirty()` is the per-field query, and
   that prefix-matching `changes()` for field markers is the trap. This is the
   minimum we would have needed; it would have saved us the bug.
3. **An enumerating query** — `fieldChanges()`, `changes({ granularity: "leaf" })`,
   whatever the name — beside `changes()`, with the save path untouched. This is
   new public surface, which `CONTRIBUTING.md` asks be justified by a
   demonstrated use case. Voter is one. Whether one is enough is the
   maintainers' call.

Our preference is **2 at minimum**, and 3 if a second consumer asks. If 3
happens, `leafChanges` in this repo should be deleted rather than kept beside it.

Pinned by `frontend/src/api/liveCoffeeConfig.test.ts`:
`reports an edit inside a list at the list, and leafChanges narrows it`, and by
the `leafChanges` suite in `frontend/src/api/fieldChanges.test.ts`.

---

## 2. A remote change inside a list flashes nowhere a field can see

**2026-09-14 · open question · `flashed` · same root, other direction**

The same atomicity has a second consequence, and this one has no `isDirty`
equivalent to reach for.

When another editor changes a price, the merge pushes the flash path
`["spec","products"]`. Voter's screen keys its flash highlight by the exact field
path, so `spec.products.0.priceCents` finds nothing and **no field flashes at
all** — the "somebody else just changed this" signal is silently absent for
every field inside a list. The dirty case showed too much; this shows nothing.

A consumer can expand it the same way (the old and new values are reachable),
but unlike `isDirty` there is no per-path primitive that answers "did this field
just move on the server?", so the workaround is less obviously available.

We have **not** fixed this in Voter yet — it is a known gap, not a shipped
behaviour, and it is worth deciding alongside entry 1 rather than separately:
whatever granularity `changes()` grows, `flashed` should probably match it.

Pinned by `frontend/src/api/liveCoffeeConfig.test.ts`:
`flashes a remote change inside a list at the list, not at the field`.

---

## 3. Keyed lists merge by identity but still report as one value

**2026-09-14 · observation, no action asked**

`withOpenAPIKeyedLists` teaches the merge that a list has identity, and the merge
uses it: `mergeEditable` consults `regions.listMapKeys(path)` and merges by key.
`#diff` does not consult it — it stops at any array. So even a correctly
annotated `x-kubernetes-list-type: map` list is reported as one change.

This is consistent with the patch, which is still whole-array, and we are not
asking for it to change on its own. We note it because it bounds option 3 above:
if leaf-level enumeration is added, keyed lists are where it could be genuinely
better than positional — reporting "the item with `sku: a` changed price" rather
than "index 0 changed" — and that is a real feature rather than a convenience.

Voter's CoffeeConfig CRD does not annotate its lists today, so we have no
first-hand experience of the keyed path. Read this entry as "from the source",
not "from use".
