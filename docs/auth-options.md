# Kubernetes auth options for this project

This doc summarizes Kubernetes authentication options relevant to the **forward-auth** pattern and how well each option lets you **recognize individual actions** in audit logs.

## 1. TokenRequest (BoundServiceAccountToken) — **recommended**

**What it is:** A Kubernetes API call that mints a **short-lived** service account token on demand.

- Endpoint: `POST /api/v1/namespaces/<ns>/serviceaccounts/<sa>/token`
- The token’s `sub`/user identity is the ServiceAccount.

**Audit identity:**
- `user.username` will appear as `system:serviceaccount:<ns>:<sa>`.
- **Per-device identity is not encoded** in the user fields by default.

**How to recognize individual devices/users:**
- Add a stable correlation header (e.g., `X-Device-Session` or `X-Join-Code-Hash`) from the auth-service and **configure audit policy** to log that header.
- Alternatively, issue **one ServiceAccount per device** (heavyweight) and mint tokens for that SA so each device has a unique `system:serviceaccount:<ns>:<sa>` identity.

**Pros:** short-lived, rotated, least privilege, no long-lived secret to leak.
**Cons:** user identity is SA-scoped unless you add correlation headers or per-device SAs.

---

## 2. Projected ServiceAccount token (in-cluster)

**What it is:** Pods get a **rotating** token via projected volumes.

**Audit identity:**
- Same as TokenRequest: `system:serviceaccount:<ns>:<sa>`.

**How to recognize individuals:**
- Same options: correlation header or per-device SAs.

**Pros:** automatic rotation, built-in.
**Cons:** only applies to in-cluster workloads, not browsers.

---

## 3. Legacy ServiceAccount token Secret (long-lived)

**What it is:** A static secret token associated with a ServiceAccount.

**Audit identity:**
- `system:serviceaccount:<ns>:<sa>`.

**Pros:** simple.
**Cons:** **long-lived**, discouraged, higher blast radius if leaked.

---

## 4. OIDC (external JWT) authentication

**What it is:** API server validates tokens from an external OIDC provider (IdP).

**Audit identity:**
- `user.username` and `user.groups` derived from OIDC claims.
- **Best built-in identity for per-user audits** if you can authenticate users.

**Pros:** strong per-user identity, enterprise-friendly.
**Cons:** requires IdP integration; harder for anonymous attendee flows.

---

## 5. Client TLS certificates (mTLS)

**What it is:** API server authenticates by x509 client certs.

**Audit identity:**
- `user.username` maps to the cert’s CN; groups from O fields.

**Pros:** strong identity for trusted automation.
**Cons:** not suitable for browsers at scale.

---

## 6. API server impersonation (proxy)

**What it is:** A trusted proxy authenticates users and sets `Impersonate-User` headers when calling the API server.

**Audit identity:**
- `user.username` becomes whatever the proxy sets.

**Pros:** excellent per-user identity without OIDC; flexible.
**Cons:** proxy must be **highly trusted** and secured; adds complexity.

---

## 7. Webhook token authentication

**What it is:** API server calls your webhook to validate bearer tokens and return user info.

**Audit identity:**
- You can **define the user identity** returned by the webhook.

**Pros:** maximum control over identity.
**Cons:** operational complexity; requires highly available auth webhook.

---

## Practical guidance for this repo

- **Use TokenRequest** for short-lived tokens.
- **To recognize individual actions**, add a **device/session correlation header** in the auth-service and configure audit policy to log it.
- If you truly need unique Kubernetes identities per device, consider **one ServiceAccount per device** or **impersonation** (proxy sets `Impersonate-User`)—both add complexity but give per-identity audit trails.

## Audit policy note

To log request headers in Kubernetes audit logs, configure an **Audit Policy** to include request headers (or selected headers) in `requestObject` or `requestURI`/`annotations`. This is cluster-level configuration; scope it carefully to avoid logging sensitive data in cleartext.
