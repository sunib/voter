# Frontend

UI design notes for the Vue app in [`frontend/`](frontend/README.md), and the rules it
keeps when it talks to Kubernetes.

Since 2.0.0 (2026-10-06) the browser talks to Kubernetes through
[krm-foyer](https://github.com/ConfigButler/krm-foyer) on the same origin: krm-foyer
owns login and the session cookie, forwards `/k8s` with the person's own token, and
serves live streams on `/stream/v1`. Kubernetes tokens never reach the browser, and
Voter has no session of its own. [docs/kubernetes-as-a-bff.md](docs/kubernetes-as-a-bff.md)
explains the model; [ARCHITECTURE.md](ARCHITECTURE.md) the deployment.

## 1. Goals

- Mobile-first, fast load on conference Wi-Fi/cellular.
- Scan QR → choose a display name → answer questions with minimal friction.
- Presenter view shows live results.
- No personal data by default: a display name, never an email the person typed.

## 2. Stack

- Vue 3
- Vite
- TypeScript
- PrimeVue (Aura preset) and Tailwind
- Pinia for state
- Native `fetch`, plus `@configbutler/krm-stream` 0.10.0 for live resources

## 2.1 General setup

### Project skeleton

- `src/main.ts`
  - Loads `/config.json` (namespace, object names, connectors, GitTargets) before
    mounting: every screen reads it synchronously. If it cannot load, the page says so.
- `src/router.ts`
  - Route table and the one guard: a page needs a session.
- `src/api/session.ts`
  - krm-foyer's `/auth/session`, `loginURL` and `logout`. Caches the CSRF token.
- `src/api/http.ts`
  - Typed `fetch` with `credentials: 'include'`, the CSRF header on every mutation, and
    errors that keep the API server's status and message.
- `src/api/kube.ts`
  - Addresses objects under `/k8s` and shapes requests (create, merge patch, delete,
    the conditional patch an editor saves with). It decides nothing.
- `src/api/liveResources.ts`, `liveEditableResource.ts`
  - Streams over `/stream/v1` with `connectResourceStream`.
- `src/stores/`
  - `draftSubmission` (answers in progress) and `cart` (the coffee order).
- `src/screens/`, `src/components/`
  - Screens per route; shared shell, top bar, banners, question widgets.

### Router + guards

- Every route except `/login` asks `/auth/session` first. No session → `/login?next=...`,
  which sends the browser to krm-foyer's `/auth/login` with `return_to` (and
  `oidc.connector_id` when `?connector=` is given, e.g. `github` for the operator).
- There is deliberately **no `/join` route**: Traefik sends `/join` to Room Pass on the
  same host. The QR code points at Voter's `/join-room?code=`, which hands the code to
  Room Pass and starts the login.
- There is no role guard. What a person may see on `/room` or `/admin` is Kubernetes'
  answer (a SelfSubjectRulesReview through `/k8s`), and the page renders a refusal.

### Visual direction (attendee)

The attendee UI should feel like a **conference lanyard + signage system**: high-contrast, instantly legible at arm’s length, with a single sharp accent color for “primary action”.

- Typography
  - Headings: a characterful serif display font (e.g. Fraunces).
  - Body/UI: an accessibility-forward sans (e.g. Atkinson Hyperlegible).
  - Principle: big type, short lines, minimal copy.
- Color
  - Base: near-black text on warm off-white.
  - Accent: one “electric” highlight for buttons and selected choices.
  - Status: draft/live/closed uses semantic colors, but never competes with the accent.
- Motion
  - One cohesive pattern: cards slide/fade upward on navigation; button press feedback is immediate.
  - No continuous animations (battery + distraction on mobile).

## 2.2 State + persistence rules (attendee)

- Answers-in-progress
  - Persist to `localStorage` keyed by round, so an accidental refresh doesn’t lose work.
  - Clear the draft on a successful vote.
- Join code
  - Never seen by this app. It travels from the QR URL into Room Pass's join cookie.
- “Double submit” protection
  - The submit button is disabled while the create is in flight.
  - A second ballot cannot exist anyway: its name is derived from the person, so the
    API server answers `409 AlreadyExists`, shown as "You have already voted".

## 3. Routes

### Attendee

- `/` — the home page: open rounds first, then the coffee entry points.
- `/answer/<round>`
  - Render the round's questions and cast a ballot.

  **Screen spec (Answer)**

  Layout:
  - Sticky top: round title + [`SessionStateBanner`](frontend/src/components/session/SessionStateBanner.vue) (only when relevant).
  - Main: the question cards.
  - Bottom: [`SubmitBar`](frontend/src/components/submission/SubmitBar.vue) (sticky), containing the single primary action.

  Question UX:
  - Single choice: large pill buttons; selection is visually unambiguous.
  - Multi choice: checkable rows with large hit areas.
  - Scale 0–10: segmented control with clear selected state.
  - Number: numeric keyboard via `inputmode="numeric"`.
  - Free text: single text area, with character hint only if a limit exists.

  Validation / completion:
  - Don’t block progress on optional questions.
  - If required questions exist, show inline per-question error only after the user tries to submit.

  Submission flow ([`api/ballot.ts`](frontend/src/api/ballot.ts), [`api/quiz.ts`](frontend/src/api/quiz.ts)):
  - Build a `QuizSubmission` named `<round>-<display name, lower case>`, labelled with
    the round and the submitter, and pinned to the round's `metadata.uid` and
    `status.questionsDigest`. On a brand-new round the page waits up to 8 s for Voter's
    reconciler to publish that digest.
  - `POST` it through `/k8s` as the participant. Admission (`voter-ballot`) checks
    the name, the labels, the pins and that the round is live.
  - On `2xx`: clear draft, show the results. On `409 AlreadyExists`: "You have already
    voted". On an admission refusal: show the API server's message, keep the draft.

- `/answer/<round>/results`
  - The round's `status`, written by Voter's tally reconciler and delivered over the
    stream. Just after a vote it reads "Counting your vote…" for the second the tally takes.
- `/thanks`
  - Confirmation. Do not show answers.
- `/coffee`, `/admin`, `/admin/orders` — the coffee story: order, edit the menu, the
  live order feed.
- `/databases`, `/databases/new`, `/databases/<name>` — the platform-team page.
- `/me` — who krm-foyer and the API server say you are (`/auth/session`, `/auth/whoami`),
  and sign-out.

### Presenter

- `/room` — the operator page: the QR code, open/close a round, and the audience's
  menu-editing grant.

## 3.1 Attendee error screens (shared)

To keep behavior consistent, implement these as variants of a single `CardContainer` screen pattern:

- **Not live yet** (round is draft)
  - One primary action: “Refresh”.
- **Closed**
  - One primary action: “Back”.
- **Not found** (invalid round)
  - One primary action: “Back”.
- **Network issue**
  - One primary action: “Try again”.

All attendee error screens should:

- Preserve accessibility (no color-only meaning; large tap targets).
- Avoid technical jargon (“401”, “forbidden”, “CRD”) in attendee copy. Editor and
  operator pages are different: there the API server's own refusal is the point.

## 4. UI building blocks

- Layout
  - `AppShell`
  - `TopBar`
  - `SessionIdentityBadge` (your name; links to `/me`)
  - `StreamDiagnostics`

- Session
  - `SessionStateBanner` (draft/live/closed)

- Questions
  - `QuestionRenderer`

- Submission
  - `SubmitBar`

- Authorization
  - `AuthorizationTable`, `PermissionRequirements` (what Kubernetes says you may do)

## 5. Client-side data model

Treat Kubernetes resources as typed objects.

- `QuizSession`, `QuizSubmission`
- `CoffeeConfig`, `Database`, `CommitRequest`
- `Room` (Room Pass)

Avoid coupling UI logic to Kubernetes metadata except where required: the ballot's
name and labels, and the `uid`/`resourceVersion` an editor saves against.

## 6. Auth and session behavior

Key invariant: **Kubernetes tokens never reach the browser**.

Frontend implications:

- All API calls include cookies (`credentials: include`). The cookie is krm-foyer's,
  sealed and `HttpOnly`.
- Every mutation carries krm-foyer's CSRF token, in the header `/auth/session` names.
- No session → `/login`, which goes to krm-foyer's `/auth/login`. Login links are built
  by `loginURL`, never by hand.
- Sign-out posts krm-foyer's `/auth/logout`. A Room Pass participant is then sent to
  `/join`, where Room Pass's own form ends the enrolment. Neither revokes a token.

## 7. Kubernetes API interaction patterns

All Kubernetes requests go to `/k8s/...`, and krm-foyer forwards them as the person.

Implementation choice (explicit): build a **small typed wrapper** around `fetch` for the few Kubernetes endpoints we need, rather than using a full Kubernetes client library in the browser.

Why:

- Keeps the SPA bundle small and understandable.
- Avoids Node-oriented Kubernetes client dependencies in the browser.
- Matches the security model: auth is a cookie krm-foyer holds, not a kubeconfig.

- Vote
  - `POST` a `QuizSubmission` with a derived name (above).
- Save an editor (CoffeeConfig, Database)
  - Merge patch of `spec` only, with `metadata.uid` and `metadata.resourceVersion` as
    preconditions. A `409` on non-overlapping edits is re-sent, up to 3 times;
    overlapping edits ask the person.
  - Then create a `CommitRequest` as the person. If that fails, the save is reported
    as a partial success.
- Writes carry `?fieldManager=voter`.
- "What may I do" is a SelfSubjectRulesReview, flattened in the browser.

## 8. Live updates strategy (frontend)

- Rounds, the menu, databases and commit requests stream over krm-foyer's `/stream/v1`.
  krm-foyer shares one API-server watch per scope across the room and checks every
  subscriber with a SubjectAccessReview.
- Results are the round's `status`, so they arrive on the same stream.
- The order feed is Voter's own `/public/orders/stream`: orders are not Kubernetes
  resources.

## 9. Error states

- Not signed in
- Round not live yet
- Round closed
- Already voted
- Refused by Kubernetes (RBAC or admission), with its message
- Network error on submit

Principle: one primary action per screen; clear confirmation after submit.

## 10. Mobile ergonomics and accessibility

- Large tap targets.
- Numeric keyboards for numeric questions.
- High contrast.
