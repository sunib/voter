package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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
	now := time.Now()
	rec := httptest.NewRecorder()
	if err := setParticipantSession(rec, cfg, sessionCookieCodec, participantSession{
		IDToken: token, Subject: token, TokenExpiry: now.Add(time.Hour).Unix(),
	}, now); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, "/public/coffeeconfig", strings.NewReader(`{"uid":"coffee-uid","resourceVersion":"42","patch":{"spec":{"shopName":"Changed"}}}`))
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

func TestCoffeeAuthorizationGate(t *testing.T) {
	cfg := authorizationFixture(t)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"examples.configbutler.ai/v1alpha1","kind":"CoffeeConfig","metadata":{"name":"testnet","uid":"coffee-uid","resourceVersion":"42"},"spec":{}}`))
	}))
	defer upstream.Close()
	cfg.KubernetesAPIServer = upstream.URL
	mux := http.NewServeMux()
	registerParticipantCoffeeHandlers(mux, handlerDeps{cfg: cfg, defaultNS: "voter"})
	for _, tc := range []struct {
		name, method, origin, csrf string
		anonymous                  bool
		want                       int
	}{
		{"anonymous read", "GET", cfg.AppOrigin, "valid", true, 401},
		{"forged headers do not authenticate", "PATCH", cfg.AppOrigin, "valid", true, 401},
		{"signed in read", "GET", "", "", false, 200},
		{"signed in write", "PATCH", cfg.AppOrigin, "valid", false, 200},
		{"write without csrf", "PATCH", cfg.AppOrigin, "", false, 403},
		{"write with wrong csrf", "PATCH", cfg.AppOrigin, "wrong", false, 403},
		{"foreign origin even with csrf", "PATCH", "https://evil.example", "valid", false, 403},
		{"absent origin with csrf", "PATCH", "", "valid", false, 200},
		{"null origin rejected by voter", "PATCH", "null", "valid", false, 403},
		{"unsupported delete", "DELETE", cfg.AppOrigin, "valid", false, 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := authorizedRequest(t, cfg, tc.method, "participant-token")
			if tc.anonymous {
				req.Header.Del("Cookie")
			}
			req.Header.Set("Origin", tc.origin)
			if tc.csrf != "valid" {
				req.Header.Set("X-CSRF-Token", tc.csrf)
			}
			req.Header.Set("Authorization", "Bearer attacker-token")
			req.Header.Set("Impersonate-User", "system:admin")
			req.Header.Set("X-Remote-Group", "system:masters")
			before := calls
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d: %s; want %d", rec.Code, rec.Body.String(), tc.want)
			}
			wantCalls := 0
			if tc.want == 200 {
				wantCalls = 1
				if tc.method == "PATCH" {
					wantCalls = 2
				}
			}
			if calls-before != wantCalls {
				t.Fatalf("upstream calls = %d, want %d", calls-before, wantCalls)
			}
		})
	}
}

func TestCoffeePreservesKubernetesDenialAndPartialSave(t *testing.T) {
	for _, tc := range []struct {
		name                                             string
		patchStatus, commitStatus, wantStatus, wantCalls int
	}{
		{"authenticated but forbidden", 403, 201, 403, 2},
		{"token rejected", 401, 201, 401, 2},
		{"stale resource version", 409, 201, 409, 2},
		{"commit forbidden after saved patch", 200, 403, 200, 3},
		{"token expires between writes", 200, 401, 200, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := authorizationFixture(t)
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer participant-token" {
					t.Errorf("participant credential was replaced")
				}
				for key := range r.Header {
					if strings.HasPrefix(strings.ToLower(key), "impersonate-") || strings.HasPrefix(strings.ToLower(key), "x-remote-") {
						t.Errorf("untrusted identity header forwarded: %s", key)
					}
				}
				status := tc.patchStatus
				wantPath := "/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/coffeeconfigs/testnet"
				wantMethod := "PATCH"
				if calls == 1 {
					status = 200
					wantMethod = "GET"
				}
				if calls == 3 {
					status = tc.commitStatus
					wantPath = "/apis/configbutler.ai/v1alpha3/namespaces/voter/commitrequests"
					wantMethod = "POST"
				}
				if r.URL.Path != wantPath || r.Method != wantMethod {
					t.Errorf("unexpected upstream operation: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if status >= 400 {
					_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Failure", "code": status, "reason": http.StatusText(status), "message": "denied by API"})
				} else {
					_, _ = w.Write([]byte(`{"apiVersion":"examples.configbutler.ai/v1alpha1","kind":"CoffeeConfig","metadata":{"name":"testnet","uid":"coffee-uid","resourceVersion":"42"},"spec":{}}`))
				}
			}))
			defer upstream.Close()
			cfg.KubernetesAPIServer = upstream.URL
			cfg.ConfigButlerGitTargetName = "demo-coffeeconfig"
			mux := http.NewServeMux()
			registerParticipantCoffeeHandlers(mux, handlerDeps{cfg: cfg, defaultNS: "voter"})
			req := authorizedRequest(t, cfg, "PATCH", "participant-token")
			req.Header.Set("Authorization", "Bearer attacker-token")
			req.Header.Set("Impersonate-User", "system:admin")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus || calls != tc.wantCalls {
				t.Fatalf("status=%d calls=%d body=%s", rec.Code, calls, rec.Body.String())
			}
			if tc.wantCalls == 3 {
				var body struct {
					Saved, CommitRequested bool
					CommitError            string
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if !body.Saved || body.CommitRequested || body.CommitError == "" {
					t.Fatalf("partial success misreported: %s", rec.Body.String())
				}
			}
		})
	}
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

// The editor needs the same lossless projected resource on REST reads and
// stream. Saves return receipts only.
func TestCoffeeEditorPreservesProjectedResource(t *testing.T) {
	cfg := authorizationFixture(t)
	const resource = `{
		"apiVersion":"examples.configbutler.ai/v1alpha1", "kind":"CoffeeConfig",
		"metadata":{"name":"testnet","namespace":"voter","uid":"coffee-uid","resourceVersion":"42",
			"annotations":{"example.com/note":"keep","kubectl.kubernetes.io/last-applied-configuration":"remove"},
			"managedFields":[{"manager":"test"}]},
		"spec":{"products":[{"sku":"free","enabled":false,"priceCents":0}],"vouchers":[],
			"bannerText":"","extension":{"nullable":null,"empty":{},"enabled":false},
			"mail":{"apiKeySecretRef":{"name":"mail-key","key":"token"}}},
		"status":{"ready":false}
	}`
	const expected = `{
		"apiVersion":"examples.configbutler.ai/v1alpha1", "kind":"CoffeeConfig",
		"metadata":{"name":"testnet","namespace":"voter","uid":"coffee-uid","resourceVersion":"42",
			"annotations":{"example.com/note":"keep"}},
		"spec":{"products":[{"sku":"free","enabled":false,"priceCents":0}],"vouchers":[],
			"bannerText":"","extension":{"nullable":null,"empty":{},"enabled":false},
			"mail":{"apiKeySecretRef":{"name":"mail-key","key":"token"}}},
		"status":{"ready":false}
	}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer participant-token" {
			t.Error("resource read/write did not use the participant token")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resource))
	}))
	defer upstream.Close()
	cfg.KubernetesAPIServer = upstream.URL
	cfg.ConfigButlerGitTargetName = ""
	mux := http.NewServeMux()
	registerParticipantCoffeeHandlers(mux, handlerDeps{cfg: cfg, defaultNS: "voter"})
	var want any
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, authorizedRequest(t, cfg, method, "participant-token"))
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			var object any = got
			if method == http.MethodPatch {
				if _, exists := got["config"]; exists || got["saved"] != true {
					t.Fatalf("save should return only a receipt: %s", rec.Body.String())
				}
				return
			}
			if !reflect.DeepEqual(object, want) {
				t.Fatalf("editor resource lost data or exposed removed metadata:\n%s", rec.Body.String())
			}
		})
	}
}

func TestCoffeeConditionalPatchBoundary(t *testing.T) {
	cfg := authorizationFixture(t)
	var writes int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "PATCH" {
			writes++
			var patch map[string]any
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Fatal(err)
			}
			metadata := patch["metadata"].(map[string]any)
			if metadata["uid"] != "coffee-uid" || metadata["resourceVersion"] != "41" {
				t.Errorf("captured precondition changed: %v", metadata)
			}
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","code":409,"reason":"Conflict","message":"stale version"}`))
			return
		}
		_, _ = w.Write([]byte(`{"apiVersion":"examples.configbutler.ai/v1alpha1","kind":"CoffeeConfig","metadata":{"uid":"coffee-uid","name":"testnet","resourceVersion":"42"},"spec":{}}`))
	}))
	defer upstream.Close()
	cfg.KubernetesAPIServer = upstream.URL
	mux := http.NewServeMux()
	registerParticipantCoffeeHandlers(mux, handlerDeps{cfg: cfg, defaultNS: "voter"})
	for _, tc := range []struct {
		name, body     string
		status, writes int
	}{
		{"old unconditional body", `{"spec":{"shopName":"bad"}}`, 400, 0},
		{"missing version", `{"uid":"coffee-uid","patch":{"spec":{}}}`, 400, 0},
		{"missing identity", `{"resourceVersion":"42","patch":{"spec":{}}}`, 400, 0},
		{"root replacement", `null`, 400, 0},
		{"metadata injection", `{"uid":"coffee-uid","resourceVersion":"42","patch":{"metadata":{"resourceVersion":"43"}}}`, 400, 0},
		{"status injection", `{"uid":"coffee-uid","resourceVersion":"42","patch":{"status":{"ready":true}}}`, 400, 0},
		{"unknown envelope", `{"uid":"coffee-uid","resourceVersion":"42","patch":{},"target":"other"}`, 400, 0},
		{"replacement identity", `{"uid":"old-uid","resourceVersion":"42","patch":{"spec":{}}}`, 409, 0},
		{"captured version forwarded", `{"uid":"coffee-uid","resourceVersion":"41","patch":{"spec":{"shopName":"mine"}}}`, 409, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := writes
			req := authorizedRequest(t, cfg, "PATCH", "participant-token")
			req.Body = io.NopCloser(strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.status || writes-before != tc.writes {
				t.Fatalf("status=%d writes=%d body=%s", rec.Code, writes-before, rec.Body.String())
			}
		})
	}
}
