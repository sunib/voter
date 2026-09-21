# Room Pass open-source extraction plan

Status: proposed, 2026-09-21. Documentation only; no extraction, repository
creation, publication, or hosted operation has been performed by this plan.

Read the [product vision](PRODUCT-VISION.md) for purpose and possible future
hosting models. The [README](README.md) describes current operation.
[requirements.md](requirements.md) records the initial implementation contract;
its instruction to remain inside Voter belongs to that original phase. This
proposal describes a subsequent extraction, without changing today's runtime.

## Intended first release

An independently usable, self-hosted Room Pass repository with:

- The enrollment service, Room and Participant CRDs, and presenter QR tool.
- A working Room Pass + Dex deployment example and minimal OIDC application.
- Independent build, test, and release automation.
- A speaker quickstart, application integration guide, and operating runbook.
- Explicit single-room/single-replica limitations and an experimental release
  status until an independent speaker has rehearsed it successfully.

Keep Kubernetes and Dex. Keep Voter as a consumer. Do not add hosting,
multi-tenancy, another database, or a new token issuer to this extraction.

The [component diagram and Dex dependency explanation](PRODUCT-VISION.md#how-the-current-components-fit-together)
describe the supported architecture. Room Pass currently depends specifically
on Dex's `authproxy` connector, trusted identity headers, callback paths, and
proxied protocol routes. OIDC is the application-facing contract; it does not
make the upstream issuer interchangeable. Document and test the supported Dex
version/configuration as part of the release. Alternate issuer integrations
are a future design decision, not an extraction requirement.

A bounded follow-up [OIDC integration investigation](docs/oidc-integration-proposal.md)
would retain Dex while evaluating replacement of the authproxy boundary. It can
proceed independently and is not required for the first standalone release.

## Findings from the current tree

The service has its own Go module, Docker build context, CRDs, controller,
enrollment UI, and minimal demo client. These are useful extraction boundaries.
The development and demonstration paths still depend on the parent repository:

| Finding | Consequence |
|---|---|
| `test/e2e/up.sh` builds `../voter` and installs its CRDs | Copying this directory alone does not produce a runnable fixture |
| Browser tests include voting, editor, and stream behavior | Separate product contract tests from consumer integration tests |
| CI, devcontainer, lint configuration, image release tasks, and license are rooted above this directory | The extracted repository needs its own complete contributor and release setup |
| Go module/imports use `github.com/sunib/voter/room-pass` | Choose a destination and update module paths consistently |
| Synthetic email suffix is hardcoded in Go and browser preview | Choose neutral product behavior and keep the preview consistent |
| Display names become normalized, unique participant identifiers | Decide and document collision, international-name, and cross-room behavior |
| Deployment base omits the complete Dex/routing/TLS setup | Promote a complete example rather than presenting the base as a turnkey install |
| QR prefill requires the application login endpoint on the join host | Document this requirement; remote hosting needs a separate design |

Documentation also needs reconciliation with code: the README describes random
participant names and uncertain-create recovery, while enrollment now creates
name-derived records and returns an error on an uncertain create. The README's
Dex connector ID differs from the fixture. Treat current source and verified
behavior as the basis for rewriting the contract.

## Phase 1: settle the public contract

1. Choose the repository/module name and describe the product as room-code
   sign-in through OIDC, with Kubernetes as the current deployment prerequisite.
2. Specify stable subject semantics and identity lifetime. Explicitly prohibit
   consumers from treating display names or synthetic addresses as verified
   people or globally unique authorization identifiers.
3. Decide whether to retain name-derived participant IDs for the first release
   or separate generated IDs from display names. Cover duplicate names,
   non-Latin names, browser loss, re-enrollment, and room recreation. Audit Voter
   assumptions before changing this behavior.
4. Choose a neutral synthetic-address default and explain the claims Dex emits,
   including that its email verification flag does not establish ownership of
   a real mailbox. If configurability is needed, keep server and browser output
   driven by one configuration contract.
5. Retain the existing CRD API group initially unless there is a concrete reason
   to change it. Repository independence does not require a CRD migration.
6. Define supported deployment topology, persistence, token/session lifetime,
   and the distinction between closing enrollment and ending application access.

Exit condition: the identity and integration contract is short enough to review
and contains no accidental promises inherited from the Voter demo.

## Phase 2: extract an independent repository

1. Move the component source, relevant documentation, CRDs, manifests, QR tool,
   and minimal example client. Preserve useful source history where practical.
2. Keep Voter-specific voting, editor, stream, and production rehearsal scripts
   in Voter. Keep Room Pass enrollment/browser and protocol coverage with Room
   Pass. Separate the fixtures so each suite has a clear owner.
3. Bring over the applicable license and attribution, lint configuration,
   reproducible tool setup, and CI. Add contributor instructions and a security
   reporting route that has an actual maintainer behind it.
4. Update imports, task paths, documentation links, image source labels, and
   publication configuration. Provide standalone tasks rather than requiring
   the parent Taskfile.
5. Add explicit ignore rules for local keys, kubeconfigs, binaries, browser
   recordings, and generated local fixtures. Review the selected files and any
   exported history before making the new repository public.
6. Update Voter to consume a pinned Room Pass release and retain an integration
   check. Avoid maintaining two editable copies of the service.

Exit condition: a fresh clone builds and tests without a Voter checkout or the
author's existing development environment.

## Phase 3: make it usable by another speaker

Provide two documented paths:

- **Try locally:** start an isolated fixture, open the minimal app, project a
  QR or type a code, and demonstrate an authorized action. Describe DNS and TLS
  setup explicitly, including what is needed to reach it from a real phone.
- **Run an event:** configure public hostnames, TLS, persistent Dex storage,
  cookie keys, client/return URLs, room settings, permissions, and enforced Dex
  network isolation through one worked deployment example.

Document the exact settings users must supply rather than asking them to copy
values from a test script. Supply a sample Room resource and a rehearsal-friendly
code rotation/expiry configuration. Explain immutable settings before the event.

Publish or document installation of the presenter CLI independently of a Go
source checkout. Include opening/closing enrollment, inspecting readiness,
ending access, resetting a room, and cleaning up participant data in the runbook.
Clarify that cleanup does not erase downstream audit logs or Git commits.

Keep the integration guide focused on issuer/client configuration, token
validation, claims and scopes, local sessions, and authorization. Document QR
prefill as an additional contract with its same-host constraint. Add a minimal
example action that does not call Kubernetes, to demonstrate that connected
applications need not have a Kubernetes backend.

Exit condition: an independent speaker can complete the rehearsal from the
documentation, without private configuration or verbal instructions.

## Phase 4: verify and release

Use the existing tests as a starting point, not as proof that extraction is safe.
The new repository should verify:

- Unit/race behavior and generated CRD consistency.
- API validation against a real API server, enrollment continuity, and room
  lifecycle behavior.
- Real Dex/OIDC login and browser joining, including QR prefill, typed codes,
  expired codes, duplicate names, and closed enrollment.
- Network isolation and the documented routing trust boundary.
- A clean installation and upgrade of the release artifacts.
- A rehearsed stop procedure, including the fate of already-issued tokens and
  application sessions.

Repeat an appropriately sized enrollment rehearsal and report its environment
and results. Do not treat the existing local 300-enrollment measurement as a
guarantee for other infrastructure.

Release acceptance: another speaker starts from a clean clone or published
artifacts, configures their own room, joins from two phones, performs an
authorized example action, and withdraws access using only the runbook.

Publish a versioned experimental release with pinned artifacts, known limits,
supported versions, and upgrade notes. The complete intended release checks
should gate that release. Record what was actually run and what remains untested.

## Phase 5: learn before offering hosting

Invite a few speakers to self-host and record the setup steps that need help.
Use their experience to decide whether the next investment is easier packaging,
conference-operated infrastructure, or a small free hosted pilot.

A hosting proposal must separately resolve:

- Operator authentication, speaker ownership, and room/client isolation.
- Identity collisions and storage boundaries across rooms; the current
  participant naming scheme must not be assumed to support shared namespaces.
- Cross-domain QR joining, redirect registration, and issuer trust.
- Persistent signing keys, backup/restore, upgrades, event-time support, and
  failure communication.
- Capacity, abuse limits, spending ceilings, data retention, and teardown.
- A fallback rehearsed before the event; switching issuer during an outage is
  not transparent to applications or existing participants.

Do not start a hosted service until there is an operator willing to own these
responsibilities and confidence sufficient to use it for their own talk.
Charging money is not a prerequisite or an assumed outcome. A free, bounded
community service or a conference-operated deployment are both possible.

## Future experiment: seat QR codes and a live audience grid

After the standalone release, explore the
[seat-specific QR idea](PRODUCT-VISION.md#future-idea-one-qr-code-per-seat) with
a small room layout and several phones:

1. Print one QR per seat and connect it to the active room's enrollment flow.
   Keep seat identification separate from authorization to join the session.
2. Let participants confirm their seat and chosen name, with a clear explanation
   of what will appear on the projected screen and an ordinary join alternative.
3. Prototype a grid in the example voting application showing seat occupants,
   live submission status, and either live answers or a presenter-controlled
   reveal after the round closes.
4. Exercise seat changes, conflicting claims, copied codes, reconnects, and
   clearing associations between talks. Treat seats as participant claims.
5. Use the pilot to decide the minimal Room Pass seat-association contract;
   keep ballots and visualization in the consuming application.

This experiment is not a release gate or a commitment to implement seating
management. Its success criterion is a clear, engaging live view of who has
participated and, when the activity calls for it, what they answered.

## Communication

Before release, describe this as an extraction being explored. After the
independent rehearsal, share the repository and a short recording of the join
flow in a separate LinkedIn post.

Lead with the actual feedback and the problem: participation in a short demo
should not require an unrelated personal account. Explain the room-scoped,
unverified identity honestly. Avoid claiming anonymous participation, universal
OIDC compatibility without integration work, hosted availability, or conference
scale before those claims have been demonstrated.

The initial invitation is to try it and report what a speaker needs. A promise
of free hosting can wait for a separate operational decision.
