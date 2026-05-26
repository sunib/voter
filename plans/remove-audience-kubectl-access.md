# Remove Audience kubectl Access

## Goal

Delete every code path that hands a Kubernetes bearer token to a client, or that
accepts one back. After this change there is exactly one way for an audience
member to reach the cluster: a browser session, authenticated by cookie, with
impersonation headers attached server-side by auth-service.

## End state

- No public endpoint returns a kubeconfig or any Kubernetes token.
- The public Kubernetes API ingress (`/api`, `/apis`, `/openapi`) accepts only
  browser traffic. Any inbound `Authorization` header is stripped at the edge
  *and* rejected at the auth-service.
- `auth-service` cannot create `TokenReview` objects.
- `auth-service` can mint tokens only for `voter-audience-impersonator`.
- The `quiz-access` ServiceAccount, Role, and RoleBinding no longer exist.
- `voter-audience-impersonator` and `FORWARD_SA` keep their current purpose.
- `TokenRequest` support stays — it's how the impersonator token is minted.

## Security invariants this preserves

These are the properties the resulting code must satisfy. Every step below
maps back to one of these.

1. **No user-supplied bearer token ever reaches the API server.**
2. **Identity always comes from the session cookie**, never from request
   headers. `Authorization` and `Impersonate-*` on inbound requests are
   adversarial input.
3. **Fail closed.** If identity derivation fails, return 401 — never a 200
   with `Authorization` but no `Impersonate-User`, which would cause Traefik
   to act as the raw impersonator SA.
4. **Defense in depth.** Traefik strips the dangerous headers *and*
   auth-service refuses to honor them, in case middleware order is ever
   silently changed.

## What's being removed (inventory)

Code:
- `GET /public/kubeconfig` handler in `auth-service/http_handlers.go`
  (reachable externally as `/auth/kubeconfig` via the `auth-to-public`
  rewrite middleware).
- `kubeconfigData`, `kubeconfigTemplateStr`, `kubeconfigTmpl`, the
  `//go:embed kubeconfig.tmpl` directive.
- The `kubeconfig.tmpl` file.
- The Path-1 bearer-passthrough branch at the top of
  `/private/forward-auth-decision`.
- `reviewToken` from the `kubeHandler` interface and `kubeClient`.
- The endpoint-listing string in the `/` handler (currently advertises
  `/public/kubeconfig?code=XXXX`).

Config:
- `KubeconfigServiceAccount` / `KubeconfigServiceAccountNS` fields in
  `auth-service/config.go`.
- The `kubeconfigSa` / `kubeconfigSaNamespace` fallback block in
  `auth-service/main.go`.
- `kubeconfigSaName` / `kubeconfigSaNS` fields on `handlerDeps`.
- `KUBECONFIG_SA` and `KUBECONFIG_SA_NAMESPACE` env vars in `k8s/app.yaml`
  (and any GitOps overlays that override them).

RBAC:
- `ClusterRole/auth-service` rule granting `tokenreviews/create`.
- `quiz-access` entry in the namespaced `Role/auth-service`'s
  `serviceaccounts/token` `resourceNames` list.
- `ServiceAccount/quiz-access`, `Role/quiz-access`,
  `RoleBinding/quiz-access` in `k8s/quiz-rbac.yaml`. **Do not delete the
  file** — `voter-audience-impersonator` lives there too. Consider renaming
  the file to `audience-impersonator-rbac.yaml` afterwards.

Tests:
- All `/public/kubeconfig` cases in `auth-service/main_test.go`.
- `stubKubeClient.reviewToken` method and `reviewUsername` field.
- Any test that exercises the Path-1 bearer branch.

Docs:
- `auth-service/README.md`, `ARCHITECTURE.md`, `plans/audience-kubectl-access.md`,
  `plans/auth-service-as-oidc-issuer.md`, talk notes — anywhere a
  `curl .../auth/kubeconfig` example appears.

## Removal plan

Steps are written so each one is independently reviewable and the build/tests
stay green between them where practical. Steps 4 and 5 are the security
critical edits; the rest is cleanup that the type checker will guide.

### 1. Delete the kubeconfig HTTP surface

In `auth-service/http_handlers.go`:

- Remove the `mux.HandleFunc("/public/kubeconfig", …)` registration.
- Remove `kubeconfigData`, `kubeconfigTemplateStr`, `kubeconfigTmpl`, and
  the `//go:embed kubeconfig.tmpl` directive.
- Update the help string in the `/` handler so it no longer lists
  `/public/kubeconfig?code=XXXX`.
- Drop the now-unused `bytes`, `embed`, and `text/template` imports — the
  compiler will tell you exactly which ones.

Delete `auth-service/kubeconfig.tmpl`. The build will fail if anything still
references it.

Expected behavior after this step:

- `GET /auth/kubeconfig?code=XXXX` resolves through the
  `auth-to-public` rewrite to `/public/kubeconfig`, which is no longer
  registered. The catch-all `"/"` handler returns 404. No ingress changes
  are required.

### 2. Tighten `/private/forward-auth-decision`

The handler currently has two branches: a bearer-passthrough fast path and a
browser-session path. Remove the fast path entirely. The handler must:

1. **First**, check for an inbound `Authorization` header. If present (even
   if malformed), log it as a rejected external bearer and return 401. Do
   this before any session lookup so a request that combines a stolen
   bearer with a valid session still fails.
2. Otherwise, require a valid session cookie, derive identity, mint the
   `FORWARD_SA` token, and set the server-derived `Impersonate-*` headers
   exactly as today.

Reasoning: this is the spot where invariant 1 actually lives. Make the
rejection unconditional and the first thing the handler does — that way no
future edit that adds an `Authorization` read for an unrelated purpose can
accidentally re-open a passthrough.

### 3. Strip `Authorization` at the Traefik edge

In `k8s/ingress-kubeapi.yaml`, extend the existing `strip-impersonate`
middleware to also drop inbound `Authorization`:

```yaml
spec:
  headers:
    customRequestHeaders:
      Authorization: ""
      Impersonate-User: ""
      Impersonate-Group: ""
      Impersonate-Uid: ""
      Impersonate-Extra-configbutler.ai%2Fclaims%2Fdisplay-name: ""
      Impersonate-Extra-configbutler.ai%2Fclaims%2Femail: ""
```

Rename the middleware to `strip-client-auth` to reflect its broader purpose,
and update both the metadata `name:` and the reference in the IngressRoute's
`middlewares:` list. Keep the existing ordering: the strip middleware runs
before `auth-forwarder`, so forwardAuth's `authResponseHeaders` remain the
only source of `Authorization` and `Impersonate-*` reaching the upstream
API.

This is defense in depth for invariant 1. Step 2 makes auth-service refuse
bearer input even if Traefik forwards it; this step makes sure Traefik
doesn't forward it in the first place.

### 4. Remove auth-service RBAC that the new code can't use

In `k8s/auth-service-rbac.yaml`:

- In `ClusterRole/auth-service`, delete the `tokenreviews` rule.
- In the namespaced `Role/auth-service`, remove `quiz-access` from the
  `serviceaccounts/token` `resourceNames` list. The list becomes a single
  entry, `voter-audience-impersonator`. Update the adjacent comment to drop
  the "legacy browser-flow SA" wording.

Leave everything else — impersonation rules, CRD reads, the
`voter-audience` Role — alone.

### 5. Remove the `quiz-access` objects

In `k8s/quiz-rbac.yaml`, delete only:

- `ServiceAccount/quiz-access`
- `Role/quiz-access`
- `RoleBinding/quiz-access`
- The trailing operator comment that says
  `kubectl -n voter create token quiz-access`.

Keep `voter-audience-impersonator` and everything attached to it. After this
step the file no longer contains any "quiz" objects — rename it to
`audience-impersonator-rbac.yaml` and update `k8s/kustomization.yaml`.

Sanity check before deleting:

```sh
rg -n "quiz-access" .
```

Anything remaining should be in historical plans or talk notes only.

### 6. Remove the kubeconfig config plumbing

In `auth-service/config.go`, delete the `KubeconfigServiceAccount` and
`KubeconfigServiceAccountNS` fields.

In `auth-service/main.go`, delete the fallback block that resolves
`kubeconfigSa` / `kubeconfigSaNamespace` to `forwardSa` when unset, and drop
both values from the `registerHandlers(handlerDeps{…})` call.

In `auth-service/http_handlers.go`, remove `kubeconfigSaName` and
`kubeconfigSaNS` from `handlerDeps`.

The old fallback was actively dangerous: if `KUBECONFIG_SA` was unset the
endpoint would have minted the impersonator-SA token and returned it to a
client, which is exactly the situation this plan exists to prevent. Removing
the fallback eliminates the foot-gun along with the endpoint.

### 7. Remove the TokenReview client surface

After step 2, `reviewToken` has no callers. Remove:

- `reviewToken` from the `kubeHandler` interface in
  `auth-service/kube_client.go`.
- The `kubeClient.reviewToken` method.
- `stubKubeClient.reviewToken` and `reviewUsername` in `main_test.go`.
- The `k8s.io/api/authentication/v1` import in `kube_client.go` if and only
  if `go build` confirms it has no other users (`TokenRequest` is in a
  different package — verify, don't assume).

### 8. Remove deployment env vars

In `k8s/app.yaml`, remove the `KUBECONFIG_SA` and `KUBECONFIG_SA_NAMESPACE`
env entries from the `auth-service` container, and the comment block above
them. The container env should retain only `FORWARD_SA`,
`FORWARD_SA_NAMESPACE`, and the existing non-auth knobs.

If there are GitOps overlays that override these env vars for any
environment, remove them there too. Production may differ from the in-repo
manifest.

### 9. Tests

Remove:

- All `/public/kubeconfig` table-driven cases in
  `auth-service/main_test.go`.
- The `stubKubeClient.reviewToken` / `reviewUsername` plumbing.
- Any test fixture that set `kubeconfigSaName` / `kubeconfigSaNS`.

Add:

- `GET /public/kubeconfig` returns 404 (route is gone).
- `forward-auth-decision` with `Authorization: Bearer …` and **no** session
  cookie returns 401 and does not call any kube client method.
- `forward-auth-decision` with `Authorization: Bearer …` **and** a valid
  session cookie still returns 401 — this is the regression test for
  invariant 1. The stub must record that no token was minted.
- Browser flow with no `Authorization` continues to mint the `FORWARD_SA`
  token and set `Impersonate-User`, `Impersonate-Group`, and (when enabled)
  the two `Impersonate-Extra-*` headers.

### 10. Docs

Replace any "download a kubeconfig" instructions with the browser-only
story.

- `auth-service/README.md`: remove the kubeconfig section; describe the
  session-cookie + impersonation flow as the only path.
- `ARCHITECTURE.md`: state that Kubernetes tokens are server-side only.
- `plans/audience-kubectl-access.md`: mark as superseded by this plan;
  retain as historical context.
- `plans/auth-service-as-oidc-issuer.md`: remove or rewrite the kubectl
  framing.
- Talk notes / examples: drop any `curl .../auth/kubeconfig` one-liners.

## Verification

### Local

```sh
cd auth-service
go build ./...
go test ./...
```

Final repo-wide sweep — every match should be in historical docs or an
unrelated Kubernetes term:

```sh
rg -n "kubeconfig|KUBECONFIG_SA|quiz-access|TokenReview|tokenreviews|reviewToken" .
```

### In a deployed cluster

Replace `<ns>` with the actual deployment namespace (`voter` in this repo;
check the live cluster if it differs) and `<host>` with the public host
(`voter.z65.nl` for the auth surface, `coffee.z65.nl` for the API surface).

Kubeconfig endpoint is gone:

```sh
curl -i "https://<host>/auth/kubeconfig?code=XXXX"
# Expected: 404, no YAML, no token in body.
```

External bearer tokens are rejected at the API surface:

```sh
curl -i \
  -H "Authorization: Bearer fake" \
  "https://<host>/apis/examples.configbutler.ai/v1alpha1/namespaces/<ns>/quizsessions"
# Expected: 401/403 from auth-service, not a Kubernetes API response.
```

Browser path still works:

```sh
curl -i "https://<host>/public/session-info?session=<name>" \
  -b "auth_session=<cookie>"
# Expected: 200 with session JSON.
```

auth-service no longer has the removed privileges (run from a cluster-admin
context):

```sh
kubectl auth can-i --as=system:serviceaccount:<ns>:auth-service \
  create tokenreviews.authentication.k8s.io
# Expected: no

kubectl auth can-i --as=system:serviceaccount:<ns>:auth-service \
  create serviceaccounts/token --resource-name=quiz-access -n <ns>
# Expected: no

kubectl auth can-i --as=system:serviceaccount:<ns>:auth-service \
  create serviceaccounts/token --resource-name=voter-audience-impersonator -n <ns>
# Expected: yes (this one must still work)
```

Live deployment env reflects the new contract:

```sh
kubectl -n <ns> get deploy voter-auth-service \
  -o jsonpath='{range .spec.template.spec.containers[?(@.name=="auth-service")].env[*]}{.name}{"\n"}{end}' \
  | rg "KUBECONFIG|FORWARD"
# Expected: only FORWARD_SA and FORWARD_SA_NAMESPACE.
```

## Rollout order

1. Merge to a non-production environment.
2. Confirm `/auth/kubeconfig` returns 404 and the browser flow still works
   end-to-end (vote, coffee order, save-now commit).
3. Confirm an inbound `Authorization: Bearer …` against `/apis/...` is
   rejected.
4. Delete any `quiz-access` objects left over in the live cluster from
   before the manifest change.
5. Repeat in production.
6. Archive the historical kubeconfig plan once talk material no longer
   references it.

## Non-goals

- Do not remove browser access to the Kubernetes API through Traefik.
- Do not remove or weaken `voter-audience-impersonator` or its RBAC.
- Do not remove the server-side `TokenRequest` flow that mints the
  `FORWARD_SA` token.
- Do not remove `Impersonate-*` headers — they *are* the replacement
  security model for user attribution.
