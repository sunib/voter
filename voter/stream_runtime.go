package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ConfigButler/krm-stream/gateway"
	"github.com/ConfigButler/krm-stream/gateway/kube"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// streamRuntime owns the process-wide backend. Participant REST clients never
// use this service-account client or its cache.
type streamRuntime struct {
	backend                 gateway.Backend
	authorizer              gateway.Authorizer
	metrics                 *streamMetrics
	reauthorizationInterval time.Duration
	// writeTimeout bounds each downstream write plus flush. A field rather than a
	// literal so tests can shorten it, exactly as reauthorizationInterval is.
	writeTimeout time.Duration
}

// Configure a single cluster for both shared and participant clients. Only TLS
// trust crosses into the participant configuration; credentials remain separate.
func streamRESTConfig(appConfig *config) (*rest.Config, error) {
	var cfg *rest.Config
	var err error
	if appConfig.StreamKubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", appConfig.StreamKubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("stream credentials (use STREAM_KUBECONFIG outside a cluster): %w", err)
	}
	if cfg.Insecure {
		return nil, fmt.Errorf("stream cluster must verify TLS")
	}
	if host := strings.TrimSpace(appConfig.KubernetesAPIServer); host != "" {
		if appConfig.StreamKubeconfig != "" && host != cfg.Host {
			return nil, fmt.Errorf("KUBERNETES_API_SERVER must match STREAM_KUBECONFIG server")
		}
		cfg.Host = host
	}
	appConfig.KubernetesAPIServer = cfg.Host
	appConfig.participantTLS = &rest.TLSClientConfig{CAFile: cfg.CAFile, CAData: append([]byte(nil), cfg.CAData...), ServerName: cfg.ServerName}
	return cfg, nil
}

func newStreamRuntime(appConfig *config) (*streamRuntime, error) {
	cfg, err := streamRESTConfig(appConfig)
	if err != nil {
		return nil, err
	}
	// Periodic checks average 13.3 SARs/sec. A simultaneous 200-viewer
	// opening needs 400 SARs within five seconds, without client-side throttling
	// turning a valid cohort into terminal authorization timeouts.
	cfg.QPS, cfg.Burst = 100, 400
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return makeStreamRuntime(kube.NewBackend(dyn), typed), nil
}

func makeStreamRuntime(backend gateway.Backend, typed kubernetes.Interface) *streamRuntime {
	metrics := &streamMetrics{}
	return &streamRuntime{
		backend: gateway.NewSharedBackendWithOptions(backend, gateway.SharedOptions{Observer: metrics}),
		authorizer: kube.SubjectAccessReviewAuthorizer(typed, func(p gateway.Principal) (kube.Subject, error) {
			subject, ok := p.(kube.Subject)
			if !ok {
				return kube.Subject{}, fmt.Errorf("missing Kubernetes identity")
			}
			return subject, nil
		}),
		metrics:                 metrics,
		reauthorizationInterval: 30 * time.Second,
		writeTimeout:            5 * time.Second,
	}
}

type streamMetrics struct {
	// subscribers is Voter's own: HTTP handler occupancy, which includes the
	// identity resolution the library never sees. Upstream observations start
	// later, so this stays host instrumentation.
	subscribers atomic.Int64
	// streams and subscriptions come from balanced library observations. They
	// count logical lifetimes, never physical API-server watches -- nothing
	// here does. Voter used to wrap the backend to count watch openings itself,
	// which is a claim about the library made by its caller; the library's own
	// tests already make it, and the rehearsal checks it against the truth,
	// `apiserver_longrunning_requests` on the API server. So the wrapper went.
	streams           atomic.Int64
	subscriptions     atomic.Int64
	resyncs           atomic.Int64
	overflows         atomic.Int64
	transportRejected atomic.Int64
	reviewFailures    atomic.Int64
}

// Observe runs synchronously on the stream or shared-watch goroutine, sometimes
// under shared locks: counters only, and unknown kinds are ignored on purpose.
func (m *streamMetrics) Observe(o gateway.Observation) {
	switch o.Kind {
	case gateway.ObservationStreamOpened:
		m.streams.Add(1)
	case gateway.ObservationStreamClosed:
		m.streams.Add(-1)
	case gateway.ObservationSharedSubscriptionOpened:
		m.subscriptions.Add(1)
	case gateway.ObservationSharedSubscriptionClosed:
		m.subscriptions.Add(-1)
	case gateway.ObservationHTTPTransportRejected:
		m.transportRejected.Add(1)
	case gateway.ObservationConsumerResync:
		m.resyncs.Add(1)
	case gateway.ObservationSharedOverflow:
		m.overflows.Add(1)
	}
}

// No identity, scope or resource names become labels.
func (m *streamMetrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, `voter_stream_subscribers %d
voter_stream_logical_streams %d
voter_stream_shared_subscriptions %d
voter_stream_resyncs_total %d
voter_stream_overflows_total %d
voter_stream_transport_rejected_total %d
voter_stream_access_review_failures_total %d
`,
		m.subscribers.Load(), m.streams.Load(), m.subscriptions.Load(),
		m.resyncs.Load(), m.overflows.Load(), m.transportRejected.Load(), m.reviewFailures.Load())
}
