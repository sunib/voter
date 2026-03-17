package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"net/http"
	"net/http/httptest"
)

func TestParseSessionRef(t *testing.T) {
	cases := []struct {
		name     string
		uri      string
		wantOK   bool
		wantNS   string
		wantName string
	}{
		{
			name:     "valid session path",
			uri:      "/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/quizsessions/kubecon-2026",
			wantOK:   true,
			wantNS:   "voter",
			wantName: "kubecon-2026",
		},
		{
			name:     "valid session path with query",
			uri:      "/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/quizsessions/kubecon-2026?foo=bar",
			wantOK:   true,
			wantNS:   "voter",
			wantName: "kubecon-2026",
		},
		{
			name:   "invalid path",
			uri:    "/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/quizsubmissions",
			wantOK: false,
		},
		{
			name:   "empty",
			uri:    "",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, ok := parseSessionRef(tc.uri)
			if ok != tc.wantOK {
				t.Fatalf("ok mismatch: got %v want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if ref.namespace != tc.wantNS {
				t.Fatalf("namespace mismatch: got %q want %q", ref.namespace, tc.wantNS)
			}
			if ref.name != tc.wantName {
				t.Fatalf("name mismatch: got %q want %q", ref.name, tc.wantName)
			}
		})
	}
}

func TestJoinCodeStoreValidate(t *testing.T) {
	cfg := config{
		JoinCodeRotate: 15 * time.Second,
		JoinCodeTTL:    60 * time.Second,
		JoinCodeLength: 4,
	}
	store := newJoinCodeStore(cfg)
	now := time.Now()

	// Empty inputs should be invalid.
	if store.validate("", "123456", now) {
		t.Fatalf("expected empty session to be invalid")
	}
	if store.validate("demo", "", now) {
		t.Fatalf("expected empty code to be invalid")
	}

	// No codes exist yet, so validation should fail.
	if store.validate("demo", "0000", now) {
		t.Fatalf("expected wrong code to be invalid when no codes exist")
	}

	// Create first code via rotateAndGet.
	first, ok := store.rotateAndGet("demo", now)
	if !ok || first == "" {
		t.Fatalf("expected rotateAndGet to generate first code")
	}

	// First code should validate.
	if !store.validate("demo", first, now.Add(10*time.Second)) {
		t.Fatalf("expected first code to be valid within TTL")
	}

	// Wrong code should still fail.
	if store.validate("demo", "0000", now.Add(10*time.Second)) {
		t.Fatalf("expected wrong code to be invalid")
	}

	// Create second code via rotation.
	second, ok := store.rotateAndGet("demo", now.Add(20*time.Second))
	if !ok || second == "" {
		t.Fatalf("expected rotateAndGet to generate second code")
	}

	// Both codes should still be valid within TTL.
	if !store.validate("demo", first, now.Add(50*time.Second)) {
		t.Fatalf("expected first code still valid within TTL")
	}
	if !store.validate("demo", second, now.Add(50*time.Second)) {
		t.Fatalf("expected second code valid within TTL")
	}

	// First code should expire after TTL (60s from creation at now).
	if store.validate("demo", first, now.Add(70*time.Second)) {
		t.Fatalf("expected first code to be expired")
	}
}

func TestJoinCodeStoreResolveAndRotate(t *testing.T) {
	cfg := config{
		JoinCodeRotate: 10 * time.Second,
		JoinCodeTTL:    30 * time.Second,
		JoinCodeLength: 4,
	}
	store := newJoinCodeStore(cfg)
	now := time.Now()

	if _, ok := store.resolve("abcd", now); ok {
		t.Fatalf("expected resolve to fail for unknown code")
	}

	code, ok := store.rotateAndGet("demo", now)
	if !ok || code == "" {
		t.Fatalf("expected rotateAndGet to return a code")
	}
	if len(code) != 4 {
		t.Fatalf("expected 4-char code, got %q", code)
	}

	// Case-insensitive resolve.
	upper := strings.ToUpper(code)
	resolved, ok := store.resolve(upper, now.Add(2*time.Second))
	if !ok || resolved != "demo" {
		t.Fatalf("expected resolve to return demo, got %q", resolved)
	}

	// Rotate after interval to new code.
	code2, ok := store.rotateAndGet("demo", now.Add(12*time.Second))
	if !ok || code2 == "" || code2 == code {
		t.Fatalf("expected new code after rotation")
	}

	// Old code should expire after TTL.
	if _, ok := store.resolve(code, now.Add(40*time.Second)); ok {
		t.Fatalf("expected old code to expire")
	}
}

type stubKubeClient struct {
	calls              int
	token              string
	exp                time.Time
	tokenErr           error
	reviewAuthenticated bool
	reviewUsername     string
	reviewErr          error
}

func (s *stubKubeClient) requestToken(_ context.Context, _, _ string, _ []string, _ int64) (string, time.Time, error) {
	s.calls++
	return s.token, s.exp, s.tokenErr
}

func (s *stubKubeClient) getQuizSession(_ context.Context, _ sessionRef) (quizSessionSpec, error) {
	return quizSessionSpec{}, nil
}

func (s *stubKubeClient) reviewToken(_ context.Context, _ string) (bool, string, error) {
	return s.reviewAuthenticated, s.reviewUsername, s.reviewErr
}

func TestTokenCacheRenewAfterExpiry(t *testing.T) {
	now := time.Now()
	cache := newTokenCache()
	stub := &stubKubeClient{token: "tok-1", exp: now.Add(30 * time.Second)}

	token, err := getOrRequestToken(cache, stub, "device-1", now, 20*time.Second, "ns", "sa", nil, 300, context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "tok-1" {
		t.Fatalf("unexpected token: %q", token)
	}
	if stub.calls != 1 {
		t.Fatalf("expected 1 token request, got %d", stub.calls)
	}

	// Still valid with skew applied, so no new request.
	token, err = getOrRequestToken(cache, stub, "device-1", now.Add(5*time.Second), 20*time.Second, "ns", "sa", nil, 300, context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "tok-1" {
		t.Fatalf("unexpected token: %q", token)
	}
	if stub.calls != 1 {
		t.Fatalf("expected cached token, got %d calls", stub.calls)
	}

	// Past expiry window; should renew.
	stub.token = "tok-2"
	stub.exp = now.Add(90 * time.Second)
	token, err = getOrRequestToken(cache, stub, "device-1", now.Add(40*time.Second), 20*time.Second, "ns", "sa", nil, 300, context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "tok-2" {
		t.Fatalf("expected refreshed token, got %q", token)
	}
	if stub.calls != 2 {
		t.Fatalf("expected 2 token requests, got %d", stub.calls)
	}
}

func TestSessionCookieRoundTrip(t *testing.T) {
	cfg := config{
		SessionCookieName:       "auth_session",
		SessionCookieMaxAgeSecs: 60,
		CookieSecure:            false,
	}
	hashKey, blockKey, err := generateCookieKeys()
	if err != nil {
		t.Fatalf("unexpected key error: %v", err)
	}
	sc, err := newSessionSecureCookie(hashKey, blockKey)
	if err != nil {
		t.Fatalf("unexpected securecookie error: %v", err)
	}

	ref := sessionRef{namespace: "voter", name: "kubecon-2026"}
	now := time.Now()
	resp := httptest.NewRecorder()
	if err := setSessionCookie(resp, cfg, sc, ref, now); err != nil {
		t.Fatalf("unexpected set cookie error: %v", err)
	}

	res := resp.Result()
	cookies := res.Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected a cookie to be set")
	}
	req := httptest.NewRequest("GET", "http://example.com/private/forward-auth-decision", nil)
	req.AddCookie(cookies[0])
	resolved, ok := getSessionFromCookie(req, cfg, sc, now.Add(10*time.Second))
	if !ok {
		t.Fatalf("expected session cookie to decode")
	}
	if resolved.namespace != ref.namespace || resolved.name != ref.name {
		t.Fatalf("unexpected ref: %v/%v", resolved.namespace, resolved.name)
	}

	// Expired cookie should be rejected.
	_, ok = getSessionFromCookie(req, cfg, sc, now.Add(2*time.Minute))
	if ok {
		t.Fatalf("expected session cookie to be expired")
	}
}

func TestExtractJoinCodeFromForwardedURI(t *testing.T) {
	cfg := config{
		JoinCodeRotate:    15 * time.Second,
		JoinCodeTTL:       60 * time.Second,
		JoinCodeLength:    4,
		SessionCookieName: "auth_session",
		CookieSecure:      false,
	}
	store := newJoinCodeStore(cfg)
	hashKey, blockKey, err := generateCookieKeys()
	if err != nil {
		t.Fatalf("unexpected key error: %v", err)
	}
	sc, err := newSessionSecureCookie(hashKey, blockKey)
	if err != nil {
		t.Fatalf("unexpected securecookie error: %v", err)
	}

	// Create a valid join code for "voter/kubecon-2026"
	code, _ := store.rotateAndGet("voter/kubecon-2026", time.Now())

	cases := []struct {
		name           string
		joinCodeHeader string
		forwardedURI   string
		requestURL     string
		wantCode       int
		wantSession    bool
		wantViaCode    bool
	}{
		{
			name:           "code in X-Forwarded-Uri query param",
			joinCodeHeader: "",
			forwardedURI:   "/voter/kubecon-2026?code=" + code,
			requestURL:     "http://example.com/private/forward-auth-decision",
			wantCode:       200,
			wantSession:    true,
			wantViaCode:    true,
		},
		{
			name:           "code in X-Forwarded-Uri with full URL",
			joinCodeHeader: "",
			forwardedURI:   "https://example.com/voter/kubecon-2026?code=" + code,
			requestURL:     "http://example.com/private/forward-auth-decision",
			wantCode:       200,
			wantSession:    true,
			wantViaCode:    true,
		},
		{
			name:           "code in X-Forwarded-Uri with other params",
			joinCodeHeader: "",
			forwardedURI:   "/voter/kubecon-2026?foo=bar&code=" + code + "&baz=qux",
			requestURL:     "http://example.com/private/forward-auth-decision",
			wantCode:       200,
			wantSession:    true,
			wantViaCode:    true,
		},
		{
			name:           "X-Join-Code header takes precedence",
			joinCodeHeader: code,
			forwardedURI:   "/voter/kubecon-2026?code=WRONG",
			requestURL:     "http://example.com/private/forward-auth-decision",
			wantCode:       200,
			wantSession:    true,
			wantViaCode:    true,
		},
		{
			name:           "invalid code in X-Forwarded-Uri",
			joinCodeHeader: "",
			forwardedURI:   "/voter/kubecon-2026?code=WRONG",
			requestURL:     "http://example.com/private/forward-auth-decision",
			wantCode:       403,
			wantSession:    false,
			wantViaCode:    false,
		},
		{
			name:           "no code anywhere - missing session",
			joinCodeHeader: "",
			forwardedURI:   "/voter/kubecon-2026",
			requestURL:     "http://example.com/private/forward-auth-decision",
			wantCode:       401,
			wantSession:    false,
			wantViaCode:    false,
		},
		{
			name:           "empty X-Forwarded-Uri",
			joinCodeHeader: "",
			forwardedURI:   "",
			requestURL:     "http://example.com/private/forward-auth-decision",
			wantCode:       401,
			wantSession:    false,
			wantViaCode:    false,
		},
		{
			name:           "malformed X-Forwarded-Uri still works (no code)",
			joinCodeHeader: "",
			forwardedURI:   "://not-a-valid-url",
			requestURL:     "http://example.com/private/forward-auth-decision",
			wantCode:       401,
			wantSession:    false,
			wantViaCode:    false,
		},
		// Tests for direct request URL query parameter
		{
			name:           "code in request URL query param",
			joinCodeHeader: "",
			forwardedURI:   "",
			requestURL:     "http://example.com/private/forward-auth-decision?code=" + code,
			wantCode:       200,
			wantSession:    true,
			wantViaCode:    true,
		},
		{
			name:           "code in request URL with other params",
			joinCodeHeader: "",
			forwardedURI:   "",
			requestURL:     "http://example.com/private/forward-auth-decision?foo=bar&code=" + code + "&baz=qux",
			wantCode:       200,
			wantSession:    true,
			wantViaCode:    true,
		},
		{
			name:           "X-Forwarded-Uri takes precedence over request URL",
			joinCodeHeader: "",
			forwardedURI:   "/voter/kubecon-2026?code=" + code,
			requestURL:     "http://example.com/private/forward-auth-decision?code=WRONG",
			wantCode:       200,
			wantSession:    true,
			wantViaCode:    true,
		},
		{
			name:           "request URL code used when X-Forwarded-Uri has no code",
			joinCodeHeader: "",
			forwardedURI:   "/voter/kubecon-2026",
			requestURL:     "http://example.com/private/forward-auth-decision?code=" + code,
			wantCode:       200,
			wantSession:    true,
			wantViaCode:    true,
		},
		{
			name:           "invalid code in request URL",
			joinCodeHeader: "",
			forwardedURI:   "",
			requestURL:     "http://example.com/private/forward-auth-decision?code=WRONG",
			wantCode:       403,
			wantSession:    false,
			wantViaCode:    false,
		},
		{
			name:           "X-Join-Code header takes precedence over request URL",
			joinCodeHeader: code,
			forwardedURI:   "",
			requestURL:     "http://example.com/private/forward-auth-decision?code=WRONG",
			wantCode:       200,
			wantSession:    true,
			wantViaCode:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := handlerDeps{
				cfg:           cfg,
				codes:         store,
				sessionCookie: sc,
			}

			var gotRef sessionRef
			var gotViaCode bool
			next := func(w http.ResponseWriter, r *http.Request) {
				gotRef, _ = getSessionRef(r)
				gotViaCode = wasResolvedViaJoinCode(r)
				w.WriteHeader(http.StatusOK)
			}

			req := httptest.NewRequest("GET", tc.requestURL, nil)
			if tc.joinCodeHeader != "" {
				req.Header.Set("X-Join-Code", tc.joinCodeHeader)
			}
			if tc.forwardedURI != "" {
				req.Header.Set("X-Forwarded-Uri", tc.forwardedURI)
			}

			rec := httptest.NewRecorder()
			requireSessionMiddleware(deps, next).ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Errorf("status code: got %d, want %d", rec.Code, tc.wantCode)
			}

			if tc.wantSession {
				if gotRef.namespace != "voter" || gotRef.name != "kubecon-2026" {
					t.Errorf("session ref: got %s/%s, want voter/kubecon-2026", gotRef.namespace, gotRef.name)
				}
				if gotViaCode != tc.wantViaCode {
					t.Errorf("resolved via join code: got %v, want %v", gotViaCode, tc.wantViaCode)
				}
			}
		})
	}
}

func TestForwardAuthBearerPassthrough(t *testing.T) {
	cfg := config{
		JoinCodeRotate:    15 * time.Second,
		JoinCodeTTL:       60 * time.Second,
		JoinCodeLength:    4,
		SessionCookieName: "auth_session",
		CookieSecure:      false,
	}
	store := newJoinCodeStore(cfg)
	hashKey, blockKey, _ := generateCookieKeys()
	sc, _ := newSessionSecureCookie(hashKey, blockKey)

	cases := []struct {
		name                string
		authHeader          string
		reviewAuthenticated bool
		reviewUsername      string
		reviewErr           error
		wantCode            int
		wantAuthForwarder   string
	}{
		{
			name:                "valid token is passed through",
			authHeader:          "Bearer good-token",
			reviewAuthenticated: true,
			reviewUsername:      "system:serviceaccount:vote:quiz-access",
			wantCode:            http.StatusOK,
			wantAuthForwarder:   "passthrough",
		},
		{
			name:                "invalid token is rejected",
			authHeader:          "Bearer bad-token",
			reviewAuthenticated: false,
			wantCode:            http.StatusUnauthorized,
		},
		{
			name:      "token review error returns 500",
			authHeader: "Bearer error-token",
			reviewErr: errors.New("kube unavailable"),
			wantCode:  http.StatusInternalServerError,
		},
		{
			name:       "no bearer token falls through to session check (no session → 401)",
			authHeader: "",
			wantCode:   http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubKubeClient{
				reviewAuthenticated: tc.reviewAuthenticated,
				reviewUsername:      tc.reviewUsername,
				reviewErr:           tc.reviewErr,
			}
			deps := handlerDeps{
				cfg:           cfg,
				codes:         store,
				kube:          stub,
				sessionCookie: sc,
				forwardSaName: "quiz-access",
				forwardSaNS:   "vote",
				tokenTTL:      600,
			}

			mux := http.NewServeMux()
			registerHandlers(mux, deps)

			req := httptest.NewRequest(http.MethodGet, "http://auth-service/private/forward-auth-decision", nil)
			req.Header.Set("X-Forwarded-Uri", "/apis/examples.configbutler.ai/v1alpha1/namespaces/vote/quizsessions")
			req.Header.Set("X-Forwarded-Method", "GET")
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Errorf("status: got %d, want %d (body: %q)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantAuthForwarder != "" {
				if got := rec.Header().Get("X-Auth-Forwarder"); got != tc.wantAuthForwarder {
					t.Errorf("X-Auth-Forwarder: got %q, want %q", got, tc.wantAuthForwarder)
				}
			}
			if tc.wantCode == http.StatusOK && tc.authHeader != "" {
				if got := rec.Header().Get("Authorization"); got != tc.authHeader {
					t.Errorf("Authorization echoed back: got %q, want %q", got, tc.authHeader)
				}
			}
		})
	}
}

func TestKubeconfigHandler(t *testing.T) {
	cfg := config{
		JoinCodeRotate:          15 * time.Second,
		JoinCodeTTL:             60 * time.Second,
		JoinCodeLength:          4,
		SessionCookieName:       "auth_session",
		SessionCookieMaxAgeSecs: 3600,
		CookieSecure:            false,
	}
	store := newJoinCodeStore(cfg)
	now := time.Now()
	code, _ := store.rotateAndGet("voter/kubecon-2026", now)

	hashKey, blockKey, err := generateCookieKeys()
	if err != nil {
		t.Fatalf("unexpected key error: %v", err)
	}
	sc, err := newSessionSecureCookie(hashKey, blockKey)
	if err != nil {
		t.Fatalf("unexpected securecookie error: %v", err)
	}

	tokenExp := now.Add(10 * time.Minute)

	cases := []struct {
		name           string
		method         string
		url            string
		forwardedHost  string
		forwardedProto string
		tokenErr       error
		wantCode       int
		wantInBody     []string
		wantMissing    []string
	}{
		{
			name:       "valid code returns kubeconfig",
			method:     http.MethodGet,
			url:        "http://auth-service/public/kubeconfig?code=" + code,
			wantCode:   http.StatusOK,
			wantInBody: []string{"kind: Config", "token: stub-token", "namespace: voter", "server: https://auth-service"},
		},
		{
			name:           "server URL comes from X-Forwarded headers",
			method:         http.MethodGet,
			url:            "http://auth-service/public/kubeconfig?code=" + code,
			forwardedHost:  "voter.z65.nl",
			forwardedProto: "https",
			wantCode:       http.StatusOK,
			wantInBody:     []string{"server: https://voter.z65.nl"},
		},
		{
			name:        "no code and no cookie returns 401",
			method:      http.MethodGet,
			url:         "http://auth-service/public/kubeconfig",
			wantCode:    http.StatusUnauthorized,
			wantMissing: []string{"kind: Config"},
		},
		{
			name:        "invalid code returns 403",
			method:      http.MethodGet,
			url:         "http://auth-service/public/kubeconfig?code=ZZZZ",
			wantCode:    http.StatusForbidden,
			wantMissing: []string{"kind: Config"},
		},
		{
			name:        "POST returns 405",
			method:      http.MethodPost,
			url:         "http://auth-service/public/kubeconfig?code=" + code,
			wantCode:    http.StatusMethodNotAllowed,
			wantMissing: []string{"kind: Config"},
		},
		{
			name:        "token request failure returns 500",
			method:      http.MethodGet,
			url:         "http://auth-service/public/kubeconfig?code=" + code,
			tokenErr:    errors.New("kube unavailable"),
			wantCode:    http.StatusInternalServerError,
			wantMissing: []string{"kind: Config"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubKubeClient{token: "stub-token", exp: tokenExp, tokenErr: tc.tokenErr}
			deps := handlerDeps{
				cfg:           cfg,
				codes:         store,
				kube:          stub,
				sessionCookie: sc,
				forwardSaName: "quiz-access",
				forwardSaNS:   "voter",
				tokenTTL:      600,
			}

			mux := http.NewServeMux()
			registerHandlers(mux, deps)

			req := httptest.NewRequest(tc.method, tc.url, nil)
			if tc.forwardedHost != "" {
				req.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			}
			if tc.forwardedProto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
			}

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Errorf("status: got %d, want %d (body: %q)", rec.Code, tc.wantCode, rec.Body.String())
			}
			body := rec.Body.String()
			for _, want := range tc.wantInBody {
				if !strings.Contains(body, want) {
					t.Errorf("body missing %q\ngot:\n%s", want, body)
				}
			}
			for _, missing := range tc.wantMissing {
				if strings.Contains(body, missing) {
					t.Errorf("body should not contain %q\ngot:\n%s", missing, body)
				}
			}
		})
	}
}
