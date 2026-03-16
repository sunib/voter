# Dev API contract (current frontend expectations)

This documents what the SPA currently calls (paths + headers), based on [`kubeFetch()`](../src/api/kube.ts:28).

## 1) Base path

The app talks to a Kubernetes-style API under:

  - `VITE_KUBE_API_PREFIX` (default): `/apis/examples.configbutler.ai/v1alpha1`
  - `VITE_KUBE_NAMESPACE` (default): `demo`

So the effective base is:

```
  /apis/examples.configbutler.ai/v1alpha1/namespaces/<namespace>/...
```

## 2) Auth / bootstrap (ForwardAuth model)

Key invariant: the browser never sees Kubernetes bearer tokens. Auth is expected to work like this:

1. First request after scanning QR includes a join code.
2. ForwardAuth validates it and sets a device-session cookie.
3. Subsequent requests only rely on the cookie.

### Required request behavior

- Browser requests must include cookies: `credentials: 'include'` (implemented in [`kubeFetch()`](../src/api/kube.ts:28)).

### Join code header

On the **first** request, the SPA sends:

- `X-Join-Code: <join code>`

See: [`kubeFetch()` header injection](../src/api/kube.ts:41).

### Cookie

After bootstrap, the SPA expects the ForwardAuth layer to have set a cookie (name is currently a backend concern; the dev mock uses `device_session`).

## 3) Endpoints

### 3.1 Read session

```
GET /apis/examples.configbutler.ai/v1alpha1/namespaces/<ns>/quizsessions/<session>
```

Headers:

- (optional, only for bootstrap) `X-Join-Code: <code>`
- `Cookie: ...` (browser-managed)

Response:

- `200` with a `QuizSession` JSON object.
- `401/403` if device session is missing/expired.

### 3.2 Submit answers

```
POST /apis/examples.configbutler.ai/v1alpha1/namespaces/<ns>/quizsubmissions
```

Headers:

- `Content-Type: application/json`
- `Cookie: ...` (browser-managed)

Body:

- Kubernetes-style object with `metadata.generateName` (so the API server assigns `metadata.name`).

The frontend currently sends the minimal payload constructed in [`createQuizSubmission()`](../src/api/kube.ts:96).

Response:

- `201` with the created object.
- `401/403` if device session is missing/expired.

Body schema update (after CRD changes):

```json
{
  "apiVersion": "examples.configbutler.ai/v1alpha1",
  "kind": "QuizSubmission",
  "metadata": { "generateName": "<session>-" },
  "spec": {
    "sessionRef": {
      "group": "examples.configbutler.ai",
      "kind": "QuizSession",
      "name": "<session>"
    },
    "submittedAt": "<rfc3339>",
    "answers": [
      { "questionId": "q1", "singleChoice": "Yes" },
      { "questionId": "q2", "multiChoice": ["Kubernetes"] },
      { "questionId": "q3", "number": 7 },
      { "questionId": "q5", "freeText": "Loved it." }
    ]
  }
}
```

## 4) Local dev: built-in file-based mock (recommended)

To develop without Traefik/Kubernetes, enable the Vite dev-server mock that serves these endpoints from files and persists submissions to disk:

```bash
VITE_DEV_KUBE_MOCK=1 npm run dev
```

Implementation: [`kubeMockPlugin()`](kube-mock-plugin.ts:1)

Fixtures:

- `dev/fixtures/quizsessions/<session>.json`

Submissions are appended (NDJSON) to:

- `dev/data/quizsubmissions.ndjson`

Bootstrap behavior in mock:

- First request must include `X-Join-Code` → sets `device_session` cookie → allows.
- Later requests only require cookie.
