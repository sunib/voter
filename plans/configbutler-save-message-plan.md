# ConfigButler Save Message Plan

## Scope

Add support for ConfigButler's save-message flow to the coffee admin save path.

This plan is only about the save message. Identity attribution is intentionally out of scope because another agent is already working on that. The only identity-related requirement here is that the coffee config write and the save-message resource must be created with the same effective Kubernetes user.

I looked for `external-code/configbutler`, but this checkout only contains `external-code/gitops-reverser`. The relevant ConfigButler API is implemented there as `CommitRequest` in `configbutler.ai/v1alpha1`.

## What ConfigButler Exposes

The new resource is:

```yaml
apiVersion: configbutler.ai/v1alpha1
kind: CommitRequest
metadata:
  generateName: save-
  namespace: voter
spec:
  gitTargetRef:
    name: <git-target-name>
  message: "Update TestNet coffee voucher copy"
```

Relevant source:

- `external-code/gitops-reverser/api/v1alpha1/commitrequest_types.go`
- `external-code/gitops-reverser/internal/queue/commit_request.go`
- `external-code/gitops-reverser/internal/git/finalize_signal.go`
- `external-code/gitops-reverser/internal/watch/event_router.go`
- `external-code/gitops-reverser/config/samples/commitrequest.yaml`
- `external-code/gitops-reverser/docs/future/design-commit-request-api.md`

`CommitRequest` is a one-shot "save now" signal. Creating it finalizes the currently open commit window for the referenced `GitTarget`. If `spec.message` is set, ConfigButler uses it verbatim as the Git commit message.

## Important Behavior

- `spec.gitTargetRef.name` is required and must point to a `GitTarget` in the same namespace as the `CommitRequest`.
- `spec.message` is optional. If omitted, ConfigButler uses the generated grouped commit message.
- If present, `spec.message` must be 1-1024 Unicode characters.
- Newlines are allowed, so a subject plus body is valid.
- Other ASCII control characters are rejected.
- `spec` is immutable after create.
- Status progresses from `WaitingForAuditEvent` to one terminal phase:
  - `Committed`
  - `NoOpenWindow`
  - `Failed`
- On `Committed`, status includes `branch` and `sha`.

The create audit event is the trigger. ConfigButler does not finalize the commit directly from the API create call; it waits until the `CommitRequest` create appears in the audit stream. That ordering is the point: all earlier coffee config patch events should already be in the open window when the save request is processed.

## Integration Decision

Create the `CommitRequest` from the Go auth service after a successful `CoffeeConfig` patch.

Do not let the Vue admin screen post this directly to the Kubernetes API in the first pass. The admin save path already goes through:

- `frontend/src/api/coffee.ts`
- `auth-service/coffee_handlers.go`
- `auth-service/kube_client.go`

Keeping the save-message create in the same backend path makes it easier to preserve request ordering, auth/session handling, and eventual identity impersonation.

## Proposed User Flow

1. Admin edits the coffee config.
2. Admin enters a save message in the existing reason field.
3. Frontend sends the patch with the trimmed message, currently via `X-Change-Reason`.
4. Backend patches `CoffeeConfig`.
5. Backend creates a `CommitRequest` in the same namespace.
6. Backend records the in-memory admin history entry as it does today.
7. Backend returns the updated `CoffeeConfig` without blocking on Git commit status in the first cut.

The current local history can still show the save message immediately. ConfigButler status is a separate concern and can be surfaced later if needed.

## Backend Plan

Add configuration:

- `CONFIGBUTLER_GIT_TARGET_NAME`, required when save messages are enabled.
- Optional `CONFIGBUTLER_COMMITREQUEST_NAMESPACE`; default to the runtime namespace used for `CoffeeConfig`.
- Optional `CONFIGBUTLER_SAVE_MESSAGE_ENABLED`; default can be enabled only when `CONFIGBUTLER_GIT_TARGET_NAME` is set.

Add a Kubernetes client method:

```go
createCommitRequest(ctx context.Context, message string) (commitRequestStatus, error)
```

Implementation details:

- Use dynamic client GVR:
  - group: `configbutler.ai`
  - version: `v1alpha1`
  - resource: `commitrequests`
- Create with `metadata.generateName: "coffee-save-"`.
- Use the same namespace as the target `GitTarget`.
- Set `spec.gitTargetRef.name` from config.
- Set `spec.message` only when the trimmed message is non-empty.
- Do not synthesize a fallback custom message; omission is meaningful.

Patch handler sequence:

1. Read current `CoffeeConfig`.
2. Patch `CoffeeConfig`.
3. Create `CommitRequest` if configured.
4. Record local change history.
5. Return the updated config.

Error handling recommendation:

- If the `CoffeeConfig` patch fails, return the Kubernetes error as today.
- If the patch succeeds but `CommitRequest` create fails, return `200 OK` with the updated config in the first cut, but record/log the save-message failure.
- Do not fail the config save after the config was already written unless the response shape is changed to report partial success clearly.

This avoids a misleading "save failed" after the Kubernetes config object was already changed.

## Identity Contract

ConfigButler binds a `CommitRequest` to the open window by effective audit user plus `GitTarget`.

That means the final implementation must ensure:

- the coffee config patch audit event uses the intended effective user
- the `CommitRequest` create audit event uses the same effective user
- both operations target the same ConfigButler `GitTarget`

If the identity work changes `patchCoffeeConfig` to impersonate the admin nickname, the new `createCommitRequest` call must use the same impersonated REST config. If both operations remain service-account authored for now, the save-message path will be internally consistent but less useful for per-admin attribution.

## RBAC Plan

The auth-service service account currently has access to `coffeeconfigs` but not `commitrequests`.

Add namespaced access for the auth service:

```yaml
- apiGroups: ["configbutler.ai"]
  resources: ["commitrequests"]
  verbs: ["create", "get", "list", "watch"]
```

For the first cut, only `create` is required if the backend does not wait for status. Add `get/list/watch` only if the UI or backend will report `Committed`, `NoOpenWindow`, `Failed`, branch, or SHA.

ConfigButler itself also needs its existing controller permissions for `commitrequests/status`; that belongs to the ConfigButler deployment, not this app.

## Frontend Plan

Keep the first frontend change small:

- Reuse the existing `changeReason` field as the save message.
- Adjust UI copy from "reason" to "save message" where appropriate.
- Continue sending the value through `patchAdminCoffeeConfig(..., { reason })` in the first pass.
- Clear the field only after the config patch succeeds.

No new direct Kubernetes API client is needed in Vue.

Later, if commit status is surfaced:

- show a pending save entry immediately
- poll or watch the created `CommitRequest`
- update the history row with `Committed`, `NoOpenWindow`, or `Failed`
- link/display the returned SHA when available

## Response Shape Options

First cut, lowest risk:

```http
PATCH /public/admin/coffeeconfig
-> CoffeeConfig
```

The backend creates `CommitRequest` as a side effect and keeps the response compatible.

Richer later option:

```json
{
  "config": { "...": "CoffeeConfig" },
  "save": {
    "commitRequestName": "coffee-save-abcde",
    "phase": "WaitingForAuditEvent"
  }
}
```

Only use the richer shape if the admin UI is going to display commit status. Otherwise it creates churn without adding much demo value.

## Test Plan

Backend unit tests:

- save with no message omits `spec.message`
- save with message creates `CommitRequest.spec.message`
- message is trimmed before create
- patch success plus commit request failure still returns updated config if using the compatible response
- no `CONFIGBUTLER_GIT_TARGET_NAME` means no `CommitRequest` is created

Integration checks:

- create a coffee config patch and verify a `CommitRequest` appears:
  - `kubectl get commitrequests.configbutler.ai -n voter`
- verify terminal status:
  - `Committed` with `status.sha`, or
  - `NoOpenWindow` if no ConfigButler window was open
- verify the resulting Git commit message matches the admin save message
- verify the ConfigButler audit policy captures `commitrequests` create events

Manual demo check:

1. Open admin config.
2. Change a visible storefront field.
3. Enter `Update coffee banner for TestNet demo`.
4. Save.
5. Confirm storefront updates.
6. Confirm ConfigButler writes a Git commit with that message.

## Open Questions

- What is the actual `GitTarget` name for the `voter` namespace coffee config?
- Should missing save-message configuration be silent, or should admin save show a warning?
- Do we want commit SHA/status in the admin history tab now, or only the local "recent history" message?
- Should the existing `X-Change-Reason` header be renamed later, or kept as the transport to avoid frontend churn?

## Recommended First Cut

Implement the backend side effect only:

- add save-message config
- add `createCommitRequest`
- call it after a successful coffee config patch
- reuse the existing admin save message field
- keep the response as `CoffeeConfig`
- add RBAC for `commitrequests` create

That gets the new ConfigButler save-message option into the demo without colliding with the separate identity work.
