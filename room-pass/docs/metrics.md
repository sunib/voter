# Operating Room Pass with metrics

Room Pass exposes Prometheus metrics at `:9090/metrics`, separately from public
HTTP on `:8080`. Set `METRICS_ADDR=0` to disable the listener. The component Service
has a named `metrics` port; do not route it through the public ingress. Restrict
scrape access to trusted monitoring workloads using platform network policy.
The external platform deployment must adopt this port/configuration separately.
An optional [ServiceMonitor](../deploy/monitoring/servicemonitor.yaml) selects the
component Service. Adapt its namespace and any labels required by your Prometheus
discovery configuration; it is not applied by the base or local fixture.

This follows GitOps Reverser's outcome-at-the-write-boundary and scrape-time gauge
conventions, using the Prometheus client already provided by controller-runtime.
The same registry also exposes controller reconcile/workqueue and Go/process
metrics. There is no separate telemetry pipeline or per-request Kubernetes scrape.

| Metric | Operational question |
| --- | --- |
| `room_pass_http_requests_total{route,method,code}` | Are users getting 403, 429 or 503, and on which stage? |
| `room_pass_http_request_duration_seconds{route}` | Which stage is slow, including storage/proxy time? |
| `room_pass_enrollments_total{result}` | Are new enrollments confirmed, refused or failing in storage? |
| `room_pass_rejections_total{reason}` | Which detailed browser-binding/CSRF/join check rejected a request? |
| `room_pass_dex_transport_errors_total` | Is the gateway unable to get an HTTP response from Dex? |
| `room_pass_identity_handoffs_total` | How many validated identities were forwarded to Dex? |
| `room_pass_handoffs_active` | How many pending transactions are still unexpired now? |
| `room_pass_handoff_slots_used` | How much of the in-memory transaction map is allocated? |
| `room_pass_handoff_capacity` | What is the configured transaction limit? |

Enrollment result values are `enrolled`, `code_or_room_rejected`, `full` and
`storage_error` (which includes inability to generate/confirm enrollment).
`enrolled` is recorded only after a Participant create is confirmed, including
recovery of a lost create response. Returning participants do not inflate it.
It is a process-local counter, not the current number of Participants in Kubernetes.

Handoffs count forwarding, not successful OIDC token issuance or app callback
completion. A Dex HTTP 4xx/5xx is visible in the HTTP counter; only transport
failures increment `dex_transport_errors_total`. Detailed rejection reasons cover
the sites that log structured rejection events; the HTTP counter covers all
responses, including rate limiting. Use it for the total rejection rate.

Slots include expired transactions until the next login-start sweep; active
handoffs are calculated at scrape time. Neither gauge queries Kubernetes or Dex.
The scrape acquires only the short-lived in-memory transaction mutex. The gauges
will change as time passes even without new requests. Process restarts reset
counters and pending handoffs; they do not remove enrollment records.

## Example queries

Requests rejected or failing by stage:

```promql
sum by (route, code) (rate(room_pass_http_requests_total{code=~"403|429|5.."}[5m]))
```

Join p95 latency:

```promql
histogram_quantile(0.95, sum by (le) (rate(room_pass_http_request_duration_seconds_bucket{route="join"}[5m])))
```

Active handoffs relative to capacity (per scrape target):

```promql
room_pass_handoffs_active / room_pass_handoff_capacity
```

Investigate sustained Dex transport errors, readiness 503s, storage failures, or
active handoffs approaching capacity. A short burst of invalid codes/CSRF failures
is diagnostic evidence, not automatically an outage. Pair app metrics with `up`
and controller-runtime reconcile errors to detect a dead process or controller.

## Privacy and verification

Routes and result/reason labels are finite code-defined categories. No participant
name, email, Room code, transaction ID, URL query, IP address or cookie value is
exported. Unknown paths collapse into `other`. These restrictions prevent both
credential disclosure and attacker-created label cardinality.

`task room-pass:test` covers actual allowed/denied enrollment, CSRF rejection,
transport failure, route aggregation and scrape-time expiry. Browser tests exercise
the flow these metrics describe. For a local scrape after `task room-pass:e2e-up`:

```bash
kubectl --kubeconfig room-pass/.local/kubeconfig -n room-pass port-forward service/room-pass 9090:9090
# In another terminal:
curl http://localhost:9090/metrics
```
