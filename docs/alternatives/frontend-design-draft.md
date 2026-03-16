# Draft: frontend design notes (superseded)

Superseded by the canonical frontend doc: [`FRONTEND.md`](../../FRONTEND.md:1).

This older draft is kept for reference.

## 1. Objectives

- Mobile-first, fast load on conference Wi-Fi/cellular.
- Simple interaction: scan QR → join session → answer questions.
- Presenter view that shows live aggregates.
- No personal data by default; collect only what the quiz asks.

## 2. Recommended stack

- Vue 3 + Vite
- TypeScript
- shadcn-vue components (Tailwind-based)
- A small state layer: Pinia
- HTTP client: fetch (native) or ofetch

Rationale:

- Vue + shadcn-vue is a good fit for high-quality UI with low custom CSS effort.
- Vite yields fast builds and good DX.

## 3. Application pages and routes

### Attendee flow

- `/join?code=...&session=...`
  - Exchanges join code for a device session
  - Loads `QuizSession` CR
  - Navigates to active question view

- `/s/<session>/answer`
  - Renders the quiz
  - Submits a `QuizSubmission` CR

- `/s/<session>/thanks`
  - Confirmation screen

### Presenter flow

- `/p/<session>`
  - Live dashboard of aggregates
  - Optional admin toggles: open/close session, next question, etc (if you choose to expose these)

## 4. UI composition

Suggested component breakdown:

- Layout
  - `AppShell`
  - `TopBar`
  - `CardContainer`

- Session
  - `JoinGate` (handles code, device session, error states)
  - `SessionStateBanner` (draft/live/closed)

- Questions
  - `QuestionRenderer` (switches by type)
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

## 5. Data model in the frontend

Frontend should treat Kubernetes resources as typed objects.

Key types:

- `QuizSession` (spec includes questions)
- `QuizSubmission` (spec includes answers)
- `DeviceSession` (frontend concept; may map to a CR or just a token cached client-side)

Important: avoid coupling the UI to raw Kubernetes metadata except where useful (resourceVersion for watches, etc).

## 6. Device session and token handling (chosen)

You want each browser to set up its own session.

Recommended frontend behavior:

- On `/join`, do:
  - Read join code from URL
  - Call the auth helper endpoint (through Traefik) to exchange join code for:
    - a short-lived BoundServiceAccountToken
    - a device session id
    - token expiry
- Store the token in memory; persist minimally (sessionStorage) if you need reload tolerance.

Security note:

- Do not store tokens in localStorage.
- Prefer header-based join code (avoid logging query params), but QR-driven flows often use query params. If query param is used, immediately exchange it, then remove it from the URL (history replaceState).

## 7. Kubernetes API interaction patterns

The frontend will call Kubernetes API endpoints via Traefik.

### Reads

- `GET` the session CR.

### Writes

- `POST` a submission CR.
- Use `generateName` and let the server pick the name.

### Live updates

Two possible patterns:

1. Polling (simplest)
   - Presenter polls an aggregate endpoint or lists submissions.

2. Watch (Kubernetes-native)
   - Presenter uses `watch=1` and streams events.

Recommendation for frontend MVP:

- Attendee: no watch required.
- Presenter: start with polling to reduce risk, then optionally add watch guarded behind a flag.

## 8. Error handling and UX states

Key states to design explicitly:

- Invalid join code
- Session not live yet
- Session closed
- Network error on submit
- Partial submit retry

UX guidelines:

- Keep a single primary action per screen.
- Prefer optimistic UI after submit with clear confirmation.

## 9. Accessibility and mobile ergonomics

- Large tap targets, minimal typing.
- For numeric questions, use numeric keyboards.
- Ensure high contrast and readable font sizes.
- Disable scroll-jank on charts; prefer simple visuals.

## 10. Suggestions / alternatives

Vue + shadcn-vue is a solid choice.

If you want alternatives:

- Nuxt 3 (still Vue) if you want server-side rendering, but it adds complexity you likely don’t need.
- React + shadcn-ui is the most common pairing, but not necessary if you prefer Vue.

Given this is a demo tool optimized for reliability, Vue 3 + Vite + shadcn-vue is the recommended direction.
