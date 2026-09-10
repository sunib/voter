package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestMetricsReflectOutcomesWithoutIdentityLabels(t *testing.T) {
	s, _ := fixture(t, "http://127.0.0.1:1")
	reg := prometheus.NewRegistry()
	handler, err := s.Instrument(reg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.enrollParticipant(context.Background(), "WRONG", "private-name"); err == nil {
		t.Fatal("invalid code accepted")
	}
	if _, err := s.enrollParticipant(context.Background(), "BCDFGH", "private-name"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/healthz", "/unknown-private-name?code=SECRET", "/another-random-path"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "https://demo.test"+path, nil))
	}
	// A real handler records a finite CSRF reason, not the supplied secret.
	req := httptest.NewRequest("POST", "https://demo.test/join", strings.NewReader("csrf=SECRET"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	now := time.Now()
	s.transactions["secret-handoff"] = &transaction{Expires: now.Add(time.Minute)}
	s.transactions["expired-handoff"] = &transaction{Expires: now.Add(-time.Minute)}
	scrape := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Body.String())
		}
		return rec.Body.String()
	}
	body := scrape()
	for _, want := range []string{
		`room_pass_enrollments_total{result="enrolled"} 1`,
		`room_pass_enrollments_total{result="code_or_room_rejected"} 1`,
		`room_pass_rejections_total{reason="csrf-cookie-absent"} 1`,
		`room_pass_http_requests_total{code="404",method="get",route="other"} 2`,
		`room_pass_handoffs_active 1`, `room_pass_handoff_slots_used 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in scrape:\n%s", want, body)
		}
	}
	for _, secret := range []string{"private-name", "SECRET", "secret-handoff", "BCDFGH", "demo.invalid"} {
		if strings.Contains(body, secret) {
			t.Errorf("metrics leaked %q", secret)
		}
	}
	s.now = func() time.Time { return now.Add(2 * time.Minute) }
	if !strings.Contains(scrape(), "room_pass_handoffs_active 0") {
		t.Fatal("active gauge latched stale state")
	}
}

func TestMetricsRecordDexFailure(t *testing.T) {
	s, _ := fixture(t, "http://127.0.0.1:1")
	reg := prometheus.NewRegistry()
	h, err := s.Instrument(reg)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "https://login.test/.well-known/openid-configuration", nil))
	if rec.Code != 503 {
		t.Fatalf("got %d", rec.Code)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == "room_pass_dex_transport_errors_total" && f.Metric[0].Counter.GetValue() == 1 {
			return
		}
	}
	t.Fatal("Dex transport error was not recorded")
}

func assertCounter(t *testing.T, reg *prometheus.Registry, name string, want float64) {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == name {
			if len(f.Metric) != 1 || f.Metric[0].Counter.GetValue() != want {
				t.Fatalf("unexpected %s: %v", name, f)
			}
			return
		}
	}
	t.Fatalf("missing metric %s", name)
}
