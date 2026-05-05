# Admin Config Concurrency

Status: current frontend behavior

This note describes how the admin coffee config editor handles concurrent edits while a live Kubernetes-backed watch is active.

## Summary

The admin screen uses an optimistic, field-oriented concurrency model.

It keeps two versions of the config in memory:

- `serverConfig`: the latest config received from the watch stream
- `draftConfig`: the local editable draft shown in the form

This avoids the original problem where every watch event replaced the whole form and could silently wipe a user’s in-progress edits.

## Field states

Each field effectively has three possible states:

### 1. Clean

- local draft matches the latest watched server value

### 2. Dirty

- the local user changed the field
- the field no longer matches `serverConfig`

### 3. Conflict

- the local user changed the field
- a newer server value arrived for that same field before save

## Merge behavior on incoming watch updates

When a new config event arrives from the backend SSE stream:

### Clean field

- update the draft automatically
- briefly flash the field to show that a live update arrived

### Dirty field, no new server difference

- keep the local draft as-is
- do not mark a conflict

### Dirty field, newer server difference

- keep the local draft visible
- mark the field as a conflict
- show that the server changed underneath the local edit

This means watch updates never blindly overwrite in-progress edits anymore.

## Save behavior

Saving still patches the current local draft as a whole.

So the system is still effectively:

- last writer wins at save time

But the important UX improvement is:

- the editor sees remote changes before saving
- the editor is not surprised by silent live overwrites inside the form

## User experience

The UI communicates concurrency state like this:

### Dirty field

- shows a subtle yellow status dot
- hovering the dot shows the current server value
- hover action:
  - `Revert`

### Remote change on a dirty field

- shows a subtle red status dot
- hovering the dot explains that a newer server value arrived
- hover action:
  - `Take Theirs`

### Auto-updated clean field

- gets a subtle flash

### Page-level review

- a dedicated save block at the bottom lists all dirty fields
- each entry shows:
  - the field path
  - whether it is a normal local edit or a missed incoming change
  - what save will do
  - a quick action to `Revert` or `Take Theirs`
- conflicts are explained there explicitly:
  - saving keeps the local draft
  - and overwrites the newer server value unless the user takes theirs first

## Array and collection behavior

Simple scalar fields are handled at field level.

Examples:

- `spec.shopName`
- `spec.currency`
- `spec.mail.provider`
- `spec.payments.mode`

Collections are more conservative.

Examples:

- `spec.products`
- `spec.vouchers`

For those, structural changes such as add/remove/reorder are treated at the collection level rather than as a fine-grained per-row merge.

That means:

- if product or voucher structure changes remotely while a local user is editing that list
- the list can be marked as conflicted as a whole
- the user can choose server list or local list

This is deliberate in the current design because it is predictable and much easier to reason about than partial row-level merging.

## Why this design was chosen

The goal was not to implement a full CRDT or collaborative editor.

The goal was:

- keep the live watch behavior
- avoid silent form rewrites
- make remote interference visible
- stay simple enough for a demo-oriented Kubernetes config UI

This model is a good fit for:

- low-frequency admin edits
- a small number of concurrent editors
- forms backed by a watched Kubernetes resource

## Current limitations

- save is still whole-draft last-writer-wins
- list merging is coarse
- conflict tracking is client-side only
- there is no server-side compare-and-swap or resourceVersion precondition on save

So this improves operator awareness and reduces accidental stomping, but it does not guarantee strict multi-writer consistency.

## Possible future improvements

- add resourceVersion-aware save checks
- reject save when the watched base version is stale
- support per-section or per-field patching instead of whole-draft patching
- add better per-row merge behavior for products and vouchers
- show who changed a field if audit/user metadata becomes available
