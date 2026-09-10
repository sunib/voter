package server

import (
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type metrics struct {
	enrollments    *prometheus.CounterVec
	rejections     *prometheus.CounterVec
	upstreamErrors prometheus.Counter
	handoffs       prometheus.Counter
}

// Instrument registers process-local observations before the server starts.
// Use the manager registry in production and a fresh registry in each test.
// Scrapes read in-memory state only; they never query Kubernetes or Dex.
func (s *Server) Instrument(reg prometheus.Registerer) (http.Handler, error) {
	m := &metrics{
		enrollments:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "room_pass_enrollments_total", Help: "Enrollment attempts by terminal result; enrolled means a Participant create was confirmed."}, []string{"result"}),
		rejections:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "room_pass_rejections_total", Help: "Detailed rejection reasons recorded by the browser handoff and join handlers."}, []string{"reason"}),
		upstreamErrors: prometheus.NewCounter(prometheus.CounterOpts{Name: "room_pass_dex_transport_errors_total", Help: "Requests that failed to obtain a Dex HTTP response."}),
		handoffs:       prometheus.NewCounter(prometheus.CounterOpts{Name: "room_pass_identity_handoffs_total", Help: "Validated identities forwarded to Dex; not completed OIDC logins."}),
	}
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "room_pass_http_requests_total", Help: "HTTP responses by bounded route, method and status code."}, []string{"route", "method", "code"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "room_pass_http_request_duration_seconds", Help: "HTTP request duration including storage and Dex waits.", Buckets: []float64{.005, .025, .1, .25, 1, 5, 10, 20}}, []string{"route"})
	collectors := []prometheus.Collector{m.enrollments, m.rejections, m.upstreamErrors, m.handoffs, requests, duration,
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "room_pass_handoff_slots_used", Help: "Allocated handoff slots, including expired transactions awaiting the next start sweep."}, func() float64 { s.mu.Lock(); defer s.mu.Unlock(); return float64(len(s.transactions)) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "room_pass_handoffs_active", Help: "Unexpired pending handoffs at scrape time."}, func() float64 {
			s.mu.Lock()
			defer s.mu.Unlock()
			n := 0
			now := s.now()
			for _, tx := range s.transactions {
				if now.Before(tx.Expires) {
					n++
				}
			}
			return float64(n)
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "room_pass_handoff_capacity", Help: "Configured maximum allocated handoff slots."}, func() float64 { return float64(s.cfg.MaxHandoffs) }),
	}
	for i, c := range collectors {
		if err := reg.Register(c); err != nil {
			for _, previous := range collectors[:i] {
				reg.Unregister(previous)
			}
			return nil, err
		}
	}
	s.metrics = m
	handlers := map[string]http.Handler{}
	for _, route := range []string{"health", "ready", "join", "logout", "bind", "confirm", "complete", "callback", "dex", "other"} {
		labels := prometheus.Labels{"route": route}
		handlers[route] = promhttp.InstrumentHandlerDuration(duration.MustCurryWith(labels), promhttp.InstrumentHandlerCounter(requests.MustCurryWith(labels), s))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handlers[s.metricRoute(r.URL.Path)].ServeHTTP(w, r) }), nil
}

func (s *Server) metricRoute(path string) string {
	switch path {
	case "/healthz":
		return "health"
	case "/readyz":
		return "ready"
	case "/join":
		return "join"
	case "/logout":
		return "logout"
	case "/bind":
		return "bind"
	case "/room-pass/confirm":
		return "confirm"
	case "/room-pass/complete":
		return "complete"
	case s.callbackPath():
		return "callback"
	}
	if path == "/auth" || strings.HasPrefix(path, "/auth/") || path == "/token" || path == "/keys" || strings.HasPrefix(path, "/device/") || path == "/.well-known/openid-configuration" {
		return "dex"
	}
	return "other"
}
