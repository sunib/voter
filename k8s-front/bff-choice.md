# Choosing a universal or domain-specific BFF

Status: decision aid, 2026-09-11. The name `k8s-front` is chosen; adopting it in Voter
is not. This document complements the [service proposal](README.md) and incorporates
review feedback checked against the current backend and platform manifests.

## The decision to make

Decide per capability, not once for the entire application. A Kubernetes object editor
and an anonymous quiz result screen have different contracts even if they share storage.

| Option | Contract and ownership | Best reason to choose it | Main cost |
| --- | --- | --- | --- |
| Universal BFF | k8s-front owns login and allowlisted native API/stream transport; Kubernetes extensions enforce domain invariants | Several applications need the same resource access and users can safely exercise the exposed Kubernetes permissions | Frontends absorb Kubernetes semantics; admission/controllers may become the domain backend |
| Domain-specific BFF | Application handlers express actions, validate inputs and return suitable views; responses can still be Kubernetes-shaped | Workflows, privacy and error handling differ materially from native CRUD | Per-application handlers and integration tests; shared infrastructure can be duplicated |
| Hybrid | Generic access for approved resources; domain endpoints for commands and restricted views | Resource editing and business workflows coexist | Two access paths need an explicit policy boundary, identity contract and operational ownership |

**Recommendation:** pursue k8s-front as reusable infrastructure, retain the present
Voter service for the talk, and consider a hybrid CoffeeConfig pilot afterwards.
Keep quiz domain operations until a deliberate admission/privacy redesign proves worthwhile.
This is a recommendation, not a migration approval. Universal transport can be valuable
without becoming the right public API for every feature.

## What the feedback changes

[QuizSession reads](../voter/participant_quiz.go) already return Kubernetes objects and
lists. [CoffeeConfig reads](../voter/participant_coffee.go) already return the library's
projected resource. [The stream host](../voter/participant_stream.go) already supplies
all four library integration seams and denies scopes by default. Reuse is the extraction
opportunity; claiming that these reads currently require domain DTO translation is wrong.

The current [encrypted session cookie](../voter/session_cookie.go) already keeps usable
tokens out of JavaScript. Opaque sessions add revocation and refresh management, with a
shared-store cost. Moving auth/transport files out of Voter reduces its local code but
not necessarily total maintained code. The review's approximate line counts are useful
orientation, not a measured savings estimate; include the new proxy, sessions, deployment,
policies, controllers and tests when measuring the complete system.

Most urgently, the checked-in [audience Role](../external/k8s/k8s.koudijs.dev/2-gitops/voter-demo/participant-rbac.yaml)
already grants shared QuizSubmission `get/list/watch/create`. The browser normally reaches
those permissions through narrow handlers. Exposing generic submission routes makes
handler-only constraints bypassable immediately, independently of frontend migration.
Other credential paths can already bypass them; this is not cluster-wide enforcement.
Default-deny exposure policy is required before routing the new service.

## Where native Kubernetes APIs fit poorly

These are tradeoffs for browser products, not reasons to avoid Kubernetes as infrastructure.

| Property | Consequence for a frontend/product | Possible response and its cost |
| --- | --- | --- |
| Resource/verb authorization | “May create submissions” does not express “one valid answer to this live round”; list access can disclose all readable objects | Admission for writes; separate private resources/read grants or a domain result endpoint |
| Full resource responses | Identity metadata and internal fields may not belong in a participant screen | Separate aggregate resources or a restricted domain view; hiding fields in the UI is insufficient |
| Object-oriented concurrency | UID, resourceVersion, patch types, arrays and apply ownership become client concerns | Reuse editor primitives, but still test races and translate failures into understandable choices |
| No general multi-object transaction endpoint | Saving configuration and requesting a Git commit can partially succeed; reading a round then creating a submission is not atomic | Explicit partial-success UX, command/controller protocol or domain orchestration; a BFF alone does not create a transaction either |
| List/watch rather than reporting queries | Joins, anonymous aggregation and result-specific projections require client work or another component | A domain endpoint or controller-maintained read model; account for aggregate lag and privileged reads |
| Infrastructure schema as client contract | API groups, versions, namespaces and CRD changes affect browser releases | Versioned domain API when product stability matters more than transparent resource access |
| Control-plane operations | Many browser watches, large lists and high write volume consume cluster capacity | Bound scopes, pagination, sharing and load tests; compare a database/service for high-volume records |
| Kubernetes operational lifecycle | RBAC, CRDs, admission availability and controller rollout become product dependencies | Accept this when the platform is already owned and useful; include it in support and deployment cost |

Kubernetes documents its native [API semantics](https://kubernetes.io/docs/reference/using-api/api-concepts/).
Our inference is that those semantics suit resource-management interfaces especially well;
they do not automatically make an end-user workflow simpler.

## Quiz invariants: concrete alternatives

| Requirement | Current domain handler | Native API design must supply |
| --- | --- | --- |
| Correct round and questions | Reads the round, checks live state and submitted UID/version, validates answers | Trusted round lookup or controlled policy parameters, schema/answer validation and an explicit close-race contract |
| One submission per identity per round | Server chooses SHA-256 name from round UID and OIDC subject; atomic create rejects duplicates | An enforced deterministic key, not a name suggested by frontend code |
| Trusted attribution | Session supplies identity; backend chooses namespace, labels and timestamp | Admission/controller verification or generation of trusted fields; browser metadata is untrusted |
| No participant changes | No update/patch/delete route; checked audience Role grants none of those verbs | Audit all additive grants and define admission rules and administrator exceptions as needed; prevent delete/recreate bypass |
| Private result presentation | Aggregates submissions, omitting object metadata | Narrow raw reads, provide authorized aggregates or isolate each participant's records |
| Unknown create outcome | Stable server-derived name prevents a second stored vote | Stable enforced identity key plus a safe way to inspect/reconcile outcome; no automatic POST replay |

Omitting metadata is not a promise of complete anonymity: free-text answers can identify
people and Kubernetes administrators retain broader visibility. Specify the intended
privacy boundary before selecting either implementation.

### Option A: admission webhook with a shared namespace

A webhook can implement the existing hash derivation, check identity and round content,
and reject alternate names. A mutating webhook could supply trusted fields; validating
admission must enforce the resulting contract. Kubernetes supplies authenticated request
identity, but the existing code hashes an OIDC subject while admission sees Kubernetes
user information. Define a stable mapping; do not assume the strings are interchangeable.

Keep the uniqueness guarantee in the atomic creation of one canonical object. A webhook
that merely lists existing submissions and rejects a duplicate can admit two concurrent
requests before either is persisted. Avoid side-effecting reservations during admission.
A read of the round in a webhook likewise does not atomically lock it until submission
persistence. Specify whether voting must be open at validation time or whether a stronger
close boundary needs a coordinated command-processing design. The existing BFF's read-then-
create sequence has the same cross-object race.

This is domain code moved to the Kubernetes write boundary, where it can protect all
clients. It does not eliminate domain code. Budget for webhook certificates, permissions,
latency, availability, timeout behavior and fail-closed enforcement of critical rules.
Scope it narrowly so its outage does not block unrelated resource writes. Webhooks do not
filter ordinary GET/list/watch responses, so read privacy needs a separate solution.
See [dynamic admission control](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/).

Choose this when all writers should obey these rules and the team already operates admission.
For a single quiz UI it may be more infrastructure than a small handler warrants.

### Option B: declarative admission with redesigned identity keys

CRD schema/CEL can enforce object-local constraints. ValidatingAdmissionPolicy can also
use request identity and configured parameter resources; it is not an arbitrary API
lookup facility. Round-dependent checks need a trusted, explicitly bound source and a
plan for its updates and absence. See [ValidatingAdmissionPolicy](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/).

The documented Kubernetes [CEL libraries](https://kubernetes.io/docs/reference/using-api/cel/)
do not provide a SHA-256 primitive reproducing Voter's naming algorithm. Do not call
that a mechanical policy migration. A redesigned key could combine a trusted, stable,
name-safe participant identifier and round UID, then admission validates the relationship.
Simply concatenating an arbitrary username is insufficient: length, disallowed characters,
collisions after normalization and identity changes must be addressed. Kubernetes has
[resource naming restrictions](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/).

A provisioned identifier mapping may be justified when it is already part of enrollment.
Creating it solely to avoid a small hashing webhook could make the design more elaborate.
These are design options, not tested CEL expressions or a claim of compatibility with
every cluster version.

### Option C: one namespace per person, with an enforced round name

Provision a namespace and narrow RoleBinding for each stable enrolled identity. Keep
shared rounds elsewhere. Store each person's submission under a canonical name derived
from the round UID, such as `round-<uid>`. With an enforced name, atomic create gives
one submission per person per round without hashing the person into the object name.
A literal name `submission` permits only one retained submission per personal namespace;
for multiple rounds it requires namespace-per-person-per-round or another storage design.
Deleting the previous submission sacrifices history and can reopen duplicate voting.

A namespace alone does not force that name. RBAC cannot restrict top-level create by
`resourceNames`; admission must reject other names and verify the round reference.
RBAC grants are additive, so retaining shared or cluster-wide read grants defeats the
intended isolation. Selectors and owner labels are not authorization boundaries.
These restrictions follow from [Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/).

This option offers private reads of one's own submission and simplifies identity scoping.
It still needs answer/live-round validation and trusted enrollment-to-namespace mapping.
Participants must not create extra personal namespaces, change ownership mappings or
acquire broad bindings. Provisioning and cleanup become product operations: roughly 200
participants imply roughly 200 namespaces plus bindings, with more objects for per-round
isolation. Measure that lifecycle; it is not automatically too large or automatically free.
A trusted aggregator still needs cross-namespace access and an audience-readable result
resource. Consider quotas and retention alongside privacy.

Choose this when personal workspaces already make sense for the product. Creating a
namespace, binding, identity mapping and aggregate controller solely to express one quiz
submission is a strong candidate for feeling *gekunsteld*.

### Results cutover applies to every native-write option

For a shared namespace, remove audience raw submission reads, including watch, and expose
a separately authorized aggregate. A controller may read submissions under an explicit
service identity; participants must not forge aggregate contents or status. Define lag,
round UID scoping, rebuild/deduplication and text disclosure rules.

The current result handler reads using the participant token. Narrowing grants immediately
would break it. Ship the replacement read model and client route before, or atomically with,
that RBAC change. In a personal-namespace design, allow only each person's own raw reads
and aggregate reads; audit other grants too. Admission cannot substitute for this cutover.

## When does the design become “gekunsteld”?

Moving rules into Kubernetes is natural when they protect several clients, represent
long-lived platform resources and fit existing operational ownership. It becomes forced
when most resources, namespaces and controllers exist to reconstruct one UI command and
its result, with little independent value.

Use this review before choosing:

1. State the user operation and every invariant, including privacy and concurrency.
2. Identify where each invariant holds if the browser sends arbitrary allowed API calls.
   An unspecified or browser-only enforcement point blocks raw exposure.
3. Count the full implementation: frontend glue, handlers, policies, webhook/controller
   code, manifests, session storage, test fixtures and on-call dependencies.
4. Describe failure handling: expired login, lost create response, round closure, failed
   webhook, lagging aggregate and partial Git request. Compare the user experience.
5. Name the second real consumer of the reusable layer. If none exists yet, justify the
   work as product development rather than immediate application simplification.

Prefer a universal BFF when resource operations already match intended permissions and
native semantics, and reuse outweighs the operational overhead. Prefer a domain BFF
when commands, restricted views or transaction-like workflows dominate. Prefer a hybrid
when these answers differ by feature. Do not decide by endpoint count alone.

## Decision record to complete before adoption

| Question | Proposed Voter position | Evidence still needed |
| --- | --- | --- |
| Scope | CoffeeConfig pilot; quiz, orders and Git orchestration stay domain-owned | Enumerated routes, restrictions and equivalent behavior tests |
| Reuse | Independent k8s-front module/service | An unrelated CRD and second frontend using it without backend changes |
| Enforcement | Default-deny both raw API and streams | Bypass tests, effective RBAC review and any required admission tests before routing |
| Quiz redesign | Deferred; compare webhook, declarative policy and personal namespaces if revisited | Prototype canonical naming, concurrent creates, close race and read privacy |
| Session model | Opaque sessions for the reusable product, not an urgent demo change | Store ownership, restart/replica/logout tests and acceptable availability cost |
| Delivery | Keep current talk deployment; prioritize shared-stream capacity work | 200-attendee acceptance evidence and an explicit later cutover decision |

No application, RBAC, admission or deployment changes are made by this document.
