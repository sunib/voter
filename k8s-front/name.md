# Product naming

Current decision: keep **k8s-front** as the product and folder name. Use lowercase
throughout. Prior-use checks found collisions; **kube-foyer** remains an alternative
for later consideration. No rename is planned at this stage.
See the [service proposal](README.md) for the architecture.

## Naming criteria

**k8s-front — browser login, Kubernetes API access and live resource streams.**

The technical description remains **a Kubernetes BFF for browser applications**.
The name identifies Kubernetes as the destination and the browser-facing role of
the service, without suggesting application-specific quiz or coffee endpoints.

We initially used `kube-front`, then considered `krm-front` to strengthen the
relationship with `krm-stream`. That pairing describes complementary responsibilities:
the service owns authentication and API access; the library owns live resource views
and reconciliation. However, KRM (Kubernetes Resource Model) needs explanation for
readers who do not already use that terminology. We should not assume a frontend
developer recognizes it just because they work with Kubernetes.

`k8s` is also shorthand, but makes the Kubernetes connection more apparent. We chose
that clarity over matching the library prefix. This is a naming judgment, not a
measured claim about recognition across developer audiences.

## Candidate interpretations

These are likely first impressions, not findings from user research. The table
preserves the alternatives considered during the discussion.

| Candidate | Frontend developer might read it as… | Backend engineer might interpret it as… | Tradeoff |
| --- | --- | --- | --- |
| `k8s-front` (current) | “A service that connects my frontend to Kubernetes; perhaps a UI tool until described.” | “A browser-facing Kubernetes proxy with authentication and session handling.” | Recognizable Kubernetes shorthand and a natural pairing with `/k8s/`; describe it as a backend service to avoid UI-library ambiguity |
| `kube-foyer` | “An entrance to Kubernetes-backed applications; the description needs to explain what it provides.” | “A browser entry service for authentication and Kubernetes access.” | More distinctive in the searches below; “foyer” is less immediately descriptive than “front” |
| `krm-front` | “The browser-facing companion to krm-stream; perhaps a UI library or SDK.” | “The edge service for KRM clients, hosting authentication and API access.” | Strong family resemblance; explain KRM and make clear that this is a deployed backend service |
| `kube-front` | “Something that helps me build a Kubernetes frontend—or a ready-made dashboard.” | “A frontend-facing proxy or gateway in front of Kubernetes.” | More recognizable independently; needs a description to distinguish it from a dashboard |
| `k8s-bff` | “A backend that handles Kubernetes access for my frontend.” | “A dedicated backend for a particular UI, probably with tailored endpoints and aggregation.” | Explicit architecture, but can suggest the application-specific behavior we want to avoid |
| `krm-bff` | “A BFF for KRM applications, once I know what KRM means.” | “A resource-oriented BFF, potentially tailored to a particular client.” | Identifies a backend and fits the family, but combines two acronyms and still suggests custom endpoints |
| `krm-gateway` | “The server endpoint I connect to for resource access.” | “A routing, authentication and policy boundary, possibly krm-stream's existing gateway.” | Communicates infrastructure well; easy to confuse with the existing gateway module |
| `kube-session` | “A Kubernetes login/session helper, perhaps a frontend auth package.” | “A session and token lifecycle service; API proxying may live elsewhere.” | Makes authentication prominent but understates API access and stream hosting |
| `krm-bridge` | “An adapter connecting my app to Kubernetes resources.” | “A protocol adapter or integration service between two systems.” | Fits the family, but leaves login, deployment location and transport responsibilities unclear |

## Prior-use checks

Checked on **2026-09-11** using web search, GitHub repository search and the npm
registry. Searches included hyphenated and joined spellings. GitHub search also returns
partial matches, so result counts are not counts of exact-name projects.

| Candidate | Evidence | Naming implication |
| --- | --- | --- |
| `kube-front` / `kubefront` | [rctl/kubefront](https://github.com/rctl/kubefront) is a Kubernetes dashboard with a repository created in March 2018. [dhyanio/kubefront](https://github.com/dhyanio/kubefront) describes a cloud compliance and autotagging tool. [kubefront.net](https://kubefront.net/devops/kubelogin-openid-connect-kubernetes/) also uses the name for Kubernetes-related content. | Existing uses in the same ecosystem. Adding a hyphen would not meaningfully distinguish the name. |
| `k8s-front` | Exact repository names include [fifthl/k8s-front](https://github.com/fifthl/k8s-front), created in April 2024, and [Soyoung-Kim/k8s-front](https://github.com/Soyoung-Kim/k8s-front). | Understandable, but already used. These findings do not establish a prominent product, yet they fail the preference for a fresh name. |
| `krm-front` | [chenjunfit/krm-front](https://github.com/chenjunfit/krm-front), created in March 2025, describes a Kubernetes multi-cluster management frontend. | Close subject matter makes confusion particularly plausible. |
| `k8s-bff` | No exact-name repository appeared in the returned GitHub results. Related names include [PRO-Robotech/openapi-ui-k8s-bff](https://github.com/PRO-Robotech/openapi-ui-k8s-bff). | Useful architectural description, but a generic phrase with existing nearby uses. |
| `kubefoyer` / `kube-foyer` | No software project surfaced in the web searches. GitHub repository-name search for `kubefoyer` returned zero results. The separate `kube-foyer` GitHub query was rate-limited. Both exact npm registry lookups returned 404. | Most promising checked alternative for distinctiveness; the hyphenated GitHub check remains incomplete. |

Exact npm lookups also returned 404 for `kube-front`, `kubefront`, `k8s-front`,
`krm-front` and `k8s-bff`. An absent npm package does not outweigh an existing project
elsewhere, and a 404 does not guarantee that a registry will permit registration.
These results mean **no matching project found where reported**, not that a name has
never been used. Domain and trademark availability were not checked. The audience
interpretations above are naming judgments, not measured user preferences.

## Why kube-foyer rather than kubefoyer

A foyer is an entrance hall: a metaphor for browser entry, login and access to
Kubernetes resources. Suggested description:

**kube-foyer — browser access to Kubernetes APIs and live resource streams.**

The joined spelling `kubefoyer` was a branding suggestion, not a technical requirement.
Prefer **kube-foyer** here: the word boundary is easier to read and matches the existing
lowercase, hyphenated style of `krm-stream`. Its description should make clear that this
is a backend service, since the metaphor alone does not explain API proxying.

Treat both spellings as the same name when checking for collisions. A hyphen would not
solve a collision with an existing `kubefoyer` product, just as it does not solve the
existing `kubefront` uses. Keep one canonical spelling in documentation and distribution
names if a rename is selected. For now, continue using `k8s-front`.

## Product name and URL paths

The product name and API paths should be coherent, but need not be identical:

| Name or path | Meaning |
| --- | --- |
| `k8s-front` | The service and its folder name |
| `/k8s/...` | Native Kubernetes API access through the proxy |
| `/stream` | krm-stream access |
| `/auth/...` | Login, callbacks and sessions |

`k8s-front` pairs naturally with `/k8s/`. Keep paths based on their purpose rather
than inserting the full product name into every URL. This lets API consumers
understand the routes independently of branding and avoids requiring URL changes
if the product is renamed later. These remain proposed routes, not a deployment change.

## Relationship to krm-stream

Describe krm-stream as the underlying streaming library. Using it does not require
k8s-front, and ordinary Kubernetes API requests do not pass through the stream
protocol. k8s-front hosts both access paths and remains independent of Room Pass
and a particular frontend framework.

The name does not imply upstream endorsement, shared ownership or affiliation with
Kubernetes. The checks above assess discoverable prior use; they do not establish
exclusive rights to a name.
