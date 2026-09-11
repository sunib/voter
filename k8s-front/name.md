# Naming decision: k8s-front

Decision: **k8s-front**, chosen on 2026-09-11. Use lowercase throughout.
See the [service proposal](README.md) for the architecture.

## Why this name

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
| **`k8s-front` (chosen)** | “A service that connects my frontend to Kubernetes; perhaps a UI tool until described.” | “A browser-facing Kubernetes proxy with authentication and session handling.” | Recognizable Kubernetes shorthand and a natural pairing with `/k8s/`; describe it as a backend service to avoid UI-library ambiguity |
| `krm-front` | “The browser-facing companion to krm-stream; perhaps a UI library or SDK.” | “The edge service for KRM clients, hosting authentication and API access.” | Strong family resemblance; explain KRM and make clear that this is a deployed backend service |
| `kube-front` | “Something that helps me build a Kubernetes frontend—or a ready-made dashboard.” | “A frontend-facing proxy or gateway in front of Kubernetes.” | More recognizable independently; needs a description to distinguish it from a dashboard |
| `k8s-bff` | “A backend that handles Kubernetes access for my frontend.” | “A dedicated backend for a particular UI, probably with tailored endpoints and aggregation.” | Explicit architecture, but can suggest the application-specific behavior we want to avoid |
| `krm-bff` | “A BFF for KRM applications, once I know what KRM means.” | “A resource-oriented BFF, potentially tailored to a particular client.” | Identifies a backend and fits the family, but combines two acronyms and still suggests custom endpoints |
| `krm-gateway` | “The server endpoint I connect to for resource access.” | “A routing, authentication and policy boundary, possibly krm-stream's existing gateway.” | Communicates infrastructure well; easy to confuse with the existing gateway module |
| `kube-session` | “A Kubernetes login/session helper, perhaps a frontend auth package.” | “A session and token lifecycle service; API proxying may live elsewhere.” | Makes authentication prominent but understates API access and stream hosting |
| `krm-bridge` | “An adapter connecting my app to Kubernetes resources.” | “A protocol adapter or integration service between two systems.” | Fits the family, but leaves login, deployment location and transport responsibilities unclear |

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
Kubernetes. Repository, package, domain and trademark availability have not been checked.
