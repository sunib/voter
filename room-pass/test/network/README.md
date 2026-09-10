# Dex NetworkPolicy regression suite

Run from the repository root:

```bash
task test-network
```

Docker and k3d are required. The task creates a dedicated two-node
`dex-network-e2e` cluster with k3s NetworkPolicy enforcement, deploys real Dex,
runs the probes and deletes the cluster on exit. It refuses to reuse an existing
cluster with that name. It uses a dedicated kubeconfig and never changes the
user's context. CI runs this task as part of the test job.

## What must stay true

| Source | Direct Dex access |
| --- | --- |
| `voter`, pod labeled `app: room-pass` | Allowed |
| `traefik-system`, any pod | Allowed by the current platform policy |
| Ordinary pod in `default` | Blocked |
| Preview pod in `web-preview-pr-123` | Blocked |
| Untrusted pod in the Dex namespace | Blocked |
| Other application pod in `voter` | Blocked |
| Preview pod copying the Room Pass label | Blocked |

Every row probes both Service DNS and Pod IP, for discovery and a callback carrying
forged identity headers. A denied case must be rejected or time out at the network boundary.
DNS errors, HTTP 403 and probe execution failures do not count as successful isolation.
Every source must connect during the policy-removal control, so connection refusal
from a dead destination cannot produce a pass. Allowed discovery must return HTTP 200. Dex uses changed
pod labels so a policy accidentally targeting old chart labels fails the suite.

The suite removes the policy **inside its disposable cluster**, requires every
source to connect, restores it, and requires isolation again. This checks that a
dead Dex, broken network or unrelated restriction cannot produce a green result.

This catches absent or ineffective policies, wrong destination selectors, an
accidental namespace/pod-selector OR, overly broad namespace grants, and blocked
legitimate callers. The allowed namespace is a trust boundary: the current policy
allows every pod in `traefik-system`, and a caller able to create a pod with
`app: room-pass` in `voter` can match its trusted peer selector. Admission/RBAC
must control who may create workloads there.

## Test the actual platform file

`dex-networkpolicy.yaml` is a snapshot of the platform policy as reviewed on
2026-09-10. It is test input, not a new application deployment base. To validate a
platform edit directly, supply its absolute path:

```bash
DEX_NETWORK_POLICY_FILE="$PWD/external/k8s/k8s.koudijs.dev/2-gitops/auth/dex/networkpolicy.yaml" task test-network
```

The override is required to exist and parse; it never silently falls back to the
snapshot. Keep the snapshot synchronized when the platform policy changes, and
run this command in the platform change's validation workflow. This repository's
CI cannot automatically detect edits in a separate private repository.

## Limits

This proves Kubernetes NetworkPolicy behavior in k3s, not the live Cilium
installation, deployed chart selectors, additional additive policies, hostNetwork
traffic or node compromise. It does not prove Traefik's L7 callback routing. The
existing login e2e suite covers its local gateway/header path. Neither suite
replaces a live deployment check or a browser login test.
