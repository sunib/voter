package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
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
		backend: gateway.NewSharedBackendWithOptions(&observedBackend{Backend: backend, metrics: metrics}, gateway.SharedOptions{Observer: metrics}),
		authorizer: kube.SubjectAccessReviewAuthorizer(typed, func(p gateway.Principal) (kube.Subject, error) {
			principal, ok := p.(*streamPrincipal)
			if !ok {
				return kube.Subject{}, fmt.Errorf("missing Kubernetes identity")
			}
			return principal.kubernetesSubject()
		}),
		metrics:                 metrics,
		reauthorizationInterval: 30 * time.Second,
		writeTimeout:            5 * time.Second,
	}
}

type streamMetrics struct {
	subscribers    atomic.Int64
	watches        atomic.Int64
	watchStarts    atomic.Int64
	resyncs        atomic.Int64
	overflows      atomic.Int64
	reviewFailures atomic.Int64
}

func (m *streamMetrics) Observe(o gateway.Observation) {
	switch o.Kind {
	case gateway.ObservationConsumerResync:
		m.resyncs.Add(1)
	case gateway.ObservationSharedOverflow:
		m.overflows.Add(1)
	}
}

func (m *streamMetrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "voter_stream_subscribers %d\nvoter_stream_upstream_watches %d\nvoter_stream_upstream_watch_starts_total %d\nvoter_stream_resyncs_total %d\nvoter_stream_overflows_total %d\nvoter_stream_access_review_failures_total %d\n",
		m.subscribers.Load(), m.watches.Load(), m.watchStarts.Load(), m.resyncs.Load(), m.overflows.Load(), m.reviewFailures.Load())
}

type observedBackend struct {
	gateway.Backend
	metrics *streamMetrics
}

func (b *observedBackend) Watch(ctx context.Context, scope gateway.Scope) (gateway.Watcher, error) {
	w, err := b.Backend.Watch(ctx, scope)
	if err != nil {
		return nil, err
	}
	b.metrics.watches.Add(1)
	b.metrics.watchStarts.Add(1)
	return &observedWatcher{Watcher: w, metrics: b.metrics}, nil
}

type observedWatcher struct {
	gateway.Watcher
	metrics *streamMetrics
	once    sync.Once
}

// In pinned gateway 0.3.0, sharedScope.pump defers watcher.Stop on every
// exit. This wrapper is only used underneath SharedBackend. Replace it with
// upstream lifecycle observations when that API is released.
func (w *observedWatcher) Stop() {
	w.once.Do(func() { w.Watcher.Stop(); w.metrics.watches.Add(-1) })
}
