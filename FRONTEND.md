# Frontend (chosen path)

This document is the canonical frontend plan for this repo.

It assumes the chosen architecture described in [`ARCHITECTURE.md`](ARCHITECTURE.md:1): the browser talks to Kubernetes API paths through Traefik, with forward-auth establishing a per-browser device session, and Kubernetes bearer tokens never reaching the browser.

## 1. Goals

- Mobile-first, fast load on conference Wi-Fi/cellular.
- Scan QR → join session → answer questions with minimal friction.
- Presenter view shows live aggregates.
- No personal data by default.

## 2. Stack

- Vue 3
- Vite
- TypeScript
- shadcn-vue (Tailwind-based components)
- Pinia for state
- Native `fetch`

Related: TypeScript guidance for talking to Kubernetes endpoints from a browser is in [`docs/kubernetes-api-typescript.md`](docs/kubernetes-api-typescript.md:1).

## 2.1 General setup (first implementation pass)

This section is intentionally practical: it describes the initial SPA wiring and conventions so attendee flows can be built quickly, shipped safely, and iterated.

### Project skeleton

- `src/main.ts`
  - Create app, install Pinia + router, mount.
- `src/router.ts`
  - Route table + navigation guards (join gating / session expiry).
- `src/api/kube.ts`
  - Typed wrapper around `fetch` with `credentials: 'include'` and consistent error handling.
- `src/stores/`
  - `deviceSession` store (mostly derived: “authed?” / “lastSeen?”)
  - `session` store (current `QuizSession` + lifecycle)
  - `draftSubmission` store (answers-in-progress for `/answer`)
- `src/screens/`
  - Attendee screens first: `JoinScreen`, `AnswerScreen`, `ThanksScreen`.
- `src/components/`
  - Shared UI building blocks (shell, top bar, banners, question widgets).

### Router + guards

- Routes are the canonical “state machine” for the attendee.
- Guard strategy (MVP):
  - `/join` is always reachable.
  - `/s/<session>/answer` and `/s/<session>/thanks` require an active device session.
  - If any Kubernetes request returns `401`/`403`, assume the device session cookie is missing/expired → redirect to [`/join`](FRONTEND.md:29).
- URL hygiene:
  - `/join?code=...&session=...` is allowed only as a *landing URL*.
  - After the first successful authenticated request (any `2xx` Kubernetes API response), remove `code` from the URL via `history.replaceState` while keeping `session`.

### shadcn-vue + Tailwind conventions

- Use shadcn-vue components for consistent touch targets + accessibility.
- Keep per-screen styling minimal; prefer theme tokens + composed primitives.
- Tailwind conventions:
  - Use `@layer base` for typography + CSS variables.
  - Avoid deep component-specific Tailwind soup; keep utility usage “surface-level”.

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
  - Persist to `localStorage` keyed by `session` so accidental refresh doesn’t lose work.
  - Clear the draft on successful submission.
- Join code
  - Never persist the join code.
  - Once removed from URL (see [`/join`](FRONTEND.md:29)), it should not be recoverable from the SPA.
- “Double submit” protection
  - Submit button becomes disabled and shows progress while POST is in flight.
  - If the network fails, keep answers and show a single recovery action: “Try again”.

## 3. Routes

### Attendee

- `/join?code=...&session=...`
  - Entry route from QR.
  - First navigation includes the join code.
  - After first successful authenticated request, remove `code` from the URL using `history.replaceState`.

  **Screen spec (Join)**

  Purpose: turn a QR scan into a valid device session cookie with the least possible friction.

  UI states:
  - *Auto-join (happy path)*
    - If `code` and `session` are present: immediately attempt bootstrap, show a focused “Joining…” card.
  - *Manual join (fallback)*
    - If query params are missing or invalid: show a single input for join code and a primary action.
  - *Join error*
    - Invalid/expired code → explain briefly + show “Scan again” and “Enter code” actions.
    - Network error → show “Try again”.

  Behavior:
  - On success: navigate to [`/s/<session>/answer`](FRONTEND.md:34).
  - On “session not live” (draft): navigate to `/s/<session>/answer` but show [`SessionStateBanner`](FRONTEND.md:55) with a blocking message and a single action (“Refresh”).
  - On “closed”: show a closed screen (copy + no inputs) and offer a single exit action (“Back”).

  Notes:
  - The join screen must *not* render any presenter-only data.
  - Keep copy extremely short; large type.

- `/s/<session>/answer`
  - Render the quiz.
  - Submit a `QuizSubmission` CR.

  **Screen spec (Answer)**

  Layout:
  - Sticky top: session title + [`SessionStateBanner`](FRONTEND.md:55) (only when relevant).
  - Main: one question card at a time on mobile (pagination), with an optional “All questions” mode later.
  - Bottom: [`SubmitBar`](FRONTEND.md:66) (sticky), containing the single primary action.

  Question UX (MVP defaults):
  - Single choice: large pill buttons; selection is visually unambiguous.
  - Multi choice: checkable rows with large hit areas.
  - Scale 0–10: segmented control with clear selected state; support thumb-friendly dragging later.
  - Number: numeric keyboard via `inputmode="numeric"`.
  - Free text: single text area, with character hint only if a limit exists.

  Validation / completion:
  - Don’t block progress on optional questions.
  - If required questions exist, show inline per-question error only after the user tries to submit.

  Submission flow:
  - Build a `QuizSubmission` object with `generateName`.
  - POST via the typed wrapper described in [`docs/kubernetes-api-typescript.md`](docs/kubernetes-api-typescript.md:1).
  - On `2xx`: clear draft + navigate to [`/s/<session>/thanks`](FRONTEND.md:38).
  - On `401/403`: redirect to [`/join`](FRONTEND.md:29) (device session expired).
  - On network error: keep draft, show error summary above submit bar, and keep one action: “Try again”.

- `/s/<session>/thanks`
  - Confirmation.

  **Screen spec (Thanks)**

  Purpose: provide unambiguous completion and stop repeat submissions.

  UI:
  - A single centered card with a short confirmation (“Submitted”).
  - Secondary info (small): “You can close this tab.”
  - Optional: a subtle “Back to start” link to [`/join`](FRONTEND.md:29) (not a button).

  Behavior:
  - Do not show answers.
  - If user navigates back to [`/s/<session>/answer`](FRONTEND.md:34), show the quiz again but with an empty draft (MVP); later we can support “already submitted” detection.

### Presenter

- `/p/<session>`
  - Live dashboard.

## 3.1 Attendee error screens (shared)

To keep behavior consistent, implement these as variants of a single `CardContainer` screen pattern:

- **Not live yet** (session is draft)
  - One primary action: “Refresh”.
- **Closed**
  - One primary action: “Back”.
- **Not found** (invalid session)
  - One primary action: “Back”.
- **Network issue**
  - One primary action: “Try again”.

All attendee error screens should:

- Preserve accessibility (no color-only meaning; large tap targets).
- Avoid technical jargon (“401”, “forbidden”, “CRD”) in the UI copy.

## 4. UI building blocks

- Layout
  - `AppShell`
  - `TopBar`
  - `CardContainer`

- Session
  - `JoinGate` (handles join errors and redirects)
  - `SessionStateBanner` (draft/live/closed)

- Questions
  - `QuestionRenderer`
  - `QuestionSingleChoice`
  - `QuestionMultiChoice`
  - `QuestionScale0to10`
  - `QuestionNumber`
  - `QuestionFreeText`

- Submission
  - `SubmitBar`
  - `OfflineHint` (optional)

- Presenter
  - `PresenterDashboard`
  - `ChartBar`
  - `ChartHistogram`
  - `LiveCount`

## 5. Client-side data model

Treat Kubernetes resources as typed objects.

- `QuizSession`
- `QuizSubmission`

Avoid coupling UI logic to Kubernetes metadata except where required.

## 6. Auth and device session behavior

Key invariant: **Kubernetes tokens never reach the browser**.

Frontend implications:

- All API calls must include cookies (`credentials: include`).
- The join code is only used to bootstrap a device session; afterwards the browser relies on the Secure, HttpOnly cookie set by the forward-auth service.
- If the device session expires, redirect back to `/join`.

## 7. Kubernetes API interaction patterns

All requests go to Kubernetes API endpoints through Traefik.

Implementation choice (explicit): build a **small typed wrapper** around `fetch` for the few Kubernetes endpoints we need, rather than using a full Kubernetes client library in the browser.

Why:

- Keeps the SPA bundle small and understandable.
- Avoids Node-oriented Kubernetes client dependencies in the browser.
- Matches the security model: auth is via cookies and forwardAuth, not kubeconfig.

See rationale and options in [`docs/kubernetes-api-typescript.md`](docs/kubernetes-api-typescript.md:1).

- Read session
  - `GET` the `QuizSession` resource.

- Submit
  - `POST` a `QuizSubmission` resource.
  - Use `generateName` so the API server assigns the name.

## 8. Live updates strategy (frontend)

For MVP reliability:

- Attendee: no live updates required.
- Presenter: start with polling a lightweight view and only add watch if needed, with strict rate limits.

## 9. Error states

- Invalid join code
- Session not live yet
- Session closed
- Network error on submit

Principle: one primary action per screen; clear confirmation after submit.

## 10. Mobile ergonomics and accessibility

- Large tap targets.
- Numeric keyboards for numeric questions.
- High contrast.
