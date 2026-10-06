package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// These tests exercise the real HTTP handler and client transport. The upstream
// is a controlled Kubernetes stand-in, not proof of deployed RBAC or JWT mapping.
func authorizationFixture(t *testing.T) config {
	t.Helper()
	old := sessionCookieCodec
	sessionCookieCodec = testCodec(t)
	t.Cleanup(func() { sessionCookieCodec = old })
	cfg := testConfig()
	cfg.CoffeeConfigName = "testnet"
	return cfg
}

func authorizedRequest(t *testing.T, cfg config, method, token string) *http.Request {
	t.Helper()
	return authorizedRequestAs(t, cfg, method, token, testParticipantConnector)
}

// authorizedRequestAs builds a session for a named Dex connector. Voting is
// gated on that value, so a test needs to be able to arrive as an operator
// ("github") and not only as a participant ("room").
//
// DisplayName is the token, which is also what a submission is named after, so
// two tokens are two voters exactly as two Room Pass names would be.
func authorizedRequestAs(t *testing.T, cfg config, method, token, connector string) *http.Request {
	t.Helper()
	now := time.Now()
	rec := httptest.NewRecorder()
	if err := setParticipantSession(rec, cfg, sessionCookieCodec, participantSession{
		IDToken: token, Subject: token, DisplayName: token, Connector: connector,
		TokenExpiry: now.Add(time.Hour).Unix(),
	}, now); err != nil {
		t.Fatal(err)
	}
	// The path and body are the caller's to set; a session is what this builds.
	req := httptest.NewRequest(method, "/public/probe", nil)
	for _, cookie := range rec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	s, ok := getParticipantSession(req, cfg, sessionCookieCodec, now)
	if !ok {
		t.Fatal("fixture session invalid")
	}
	req.Header.Set("X-CSRF-Token", s.CSRF)
	req.Header.Set("Origin", cfg.AppOrigin)
	return req
}

func TestConcurrentRequestsKeepTheirOwnCredentials(t *testing.T) {
	cfg := authorizationFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+r.URL.Query().Get("expected") {
			t.Error("credentials crossed requests")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	cfg.KubernetesAPIServer = upstream.URL
	var wg sync.WaitGroup
	for _, token := range []string{"room-token", "github-token", "linkedin-token"} {
		req := authorizedRequest(t, cfg, "GET", token)
		req.URL.RawQuery = "expected=" + token
		wg.Add(1)
		go func() {
			defer wg.Done()
			handler := requireParticipant(cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
				clients, err := newParticipantClients(cfg, s.IDToken)
				if err != nil {
					t.Error(err)
					return
				}
				if err := clients.typed.CoreV1().RESTClient().Get().AbsPath("/probe").Param("expected", r.URL.Query().Get("expected")).Do(r.Context()).Error(); err != nil {
					t.Error(err)
				}
			})
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("request rejected before credential check: %d", rec.Code)
			}
		}()
	}
	wg.Wait()
}
