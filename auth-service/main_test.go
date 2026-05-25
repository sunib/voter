package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"net/http"
	"net/http/httptest"

	"github.com/gorilla/securecookie"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8swatch "k8s.io/apimachinery/pkg/watch"
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

func TestJoinCodeStoreEnsureActiveCodeReusesValidCode(t *testing.T) {
	cfg := config{
		JoinCodeTTL:    2 * time.Hour,
		JoinCodeLength: 4,
	}
	store := newJoinCodeStore(cfg)
	now := time.Now()

	first, ok, created := store.ensureActiveCode(globalDemoAccessCodeKey, now)
	if !ok || !created || first == "" {
		t.Fatalf("expected first ensureActiveCode call to create a code")
	}

	second, ok, created := store.ensureActiveCode(globalDemoAccessCodeKey, now.Add(10*time.Second))
	if !ok {
		t.Fatalf("expected second ensureActiveCode call to succeed")
	}
	if created {
		t.Fatalf("expected existing code to be reused while still valid")
	}
	if second != first {
		t.Fatalf("expected same code to be reused, got %q want %q", second, first)
	}

	third, ok, created := store.ensureActiveCode(globalDemoAccessCodeKey, now.Add(2*time.Hour+time.Second))
	if !ok || !created {
		t.Fatalf("expected a new code after ttl expiry")
	}
	if third == first {
		t.Fatalf("expected a fresh code after expiry")
	}
}

type stubKubeClient struct {
	mu                  sync.Mutex
	calls               int
	token               string
	exp                 time.Time
	tokenErr            error
	requestStarted      chan struct{}
	releaseRequest      chan struct{}
	reviewAuthenticated bool
	reviewUsername      string
	reviewErr           error
	coffeeConfig        coffeeConfig
	patchResult         coffeeConfig
	coffeeConfigErr     error
	patchCoffeeErr      error
	lastPatchBody       []byte
	lastPatchIdentity   audienceIdentity
	commitRequestName   string
	commitRequestErr    error
	commitRequestCalls  int
	lastCommitRequest   createCommitRequestParams
}

func (s *stubKubeClient) requestToken(_ context.Context, _, _ string, _ []string, _ int64) (string, time.Time, error) {
	s.mu.Lock()
	s.calls++
	token := s.token
	exp := s.exp
	tokenErr := s.tokenErr
	started := s.requestStarted
	release := s.releaseRequest
	s.mu.Unlock()

	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if release != nil {
		<-release
	}
	return token, exp, tokenErr
}

func (s *stubKubeClient) getQuizSession(_ context.Context, _ sessionRef) (quizSessionSpec, error) {
	return quizSessionSpec{}, nil
}

func (s *stubKubeClient) reviewToken(_ context.Context, _ string) (bool, string, error) {
	return s.reviewAuthenticated, s.reviewUsername, s.reviewErr
}

func (s *stubKubeClient) getCoffeeConfig(_ context.Context) (coffeeConfig, error) {
	return s.coffeeConfig, s.coffeeConfigErr
}

func (s *stubKubeClient) patchCoffeeConfig(_ context.Context, patch []byte, identity audienceIdentity) (coffeeConfig, error) {
	s.lastPatchBody = append([]byte(nil), patch...)
	s.lastPatchIdentity = identity
	return s.patchResult, s.patchCoffeeErr
}

// Canonical session identity used across the cookie and admin-patch tests.
// Mirrors what the frontend constants module would produce for stableID
// 521541 — the slug-from-displayName link is gone, so the displayName here is
// a regular human-readable string with no relationship to the K8s username.
const (
	testStableID    = "521541"
	testDisplayName = "Alice"
	testEmail       = "521541@demo.configbutler.ai"
)

// setTestSessionCookie validates an identity via the identity helper and
// writes it as a signed session cookie. Mirrors what the login handler does.
func setTestSessionCookie(t *testing.T, w http.ResponseWriter, cfg config, sc *securecookie.SecureCookie, stableID, displayName, email string, now time.Time) error {
	t.Helper()
	identity, err := audienceIdentityFromSession(stableID, displayName, email)
	if err != nil {
		return err
	}
	return setSessionCookie(w, cfg, sc, identity, stableID, now)
}

func (s *stubKubeClient) watchCoffeeConfig(_ context.Context) (coffeeConfig, k8swatch.Interface, error) {
	return coffeeConfig{}, nil, errors.New("not implemented")
}

func (s *stubKubeClient) createCommitRequest(_ context.Context, params createCommitRequestParams) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitRequestCalls++
	s.lastCommitRequest = params
	name := s.commitRequestName
	if name == "" {
		name = "coffee-save-stub"
	}
	return name, s.commitRequestErr
}

func TestCoffeeConfigFromWatchEventIgnoresBookmark(t *testing.T) {
	event := k8swatch.Event{
		Type: k8swatch.Bookmark,
		Object: &unstructured.Unstructured{
			Object: map[string]any{
				"metadata": map[string]any{
					"name":            "testnet-coffee",
					"namespace":       "voter",
					"resourceVersion": "123",
				},
			},
		},
	}

	if _, ok := coffeeConfigFromWatchEvent(event); ok {
		t.Fatalf("expected bookmark event to be ignored")
	}
}

func TestCoffeeConfigFromWatchEventAcceptsModifiedConfig(t *testing.T) {
	event := k8swatch.Event{
		Type: k8swatch.Modified,
		Object: &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "examples.configbutler.ai/v1alpha1",
				"kind":       "CoffeeConfig",
				"metadata": map[string]any{
					"name":            "testnet-coffee",
					"namespace":       "voter",
					"generation":      int64(7),
					"resourceVersion": "456",
				},
				"spec": map[string]any{
					"shopName": "TestNet Coffee",
					"currency": "EUR",
					"products": []any{
						map[string]any{
							"sku":        "coffee-flat-white",
							"name":       "Flat White",
							"priceCents": int64(395),
							"enabled":    true,
						},
					},
					"vouchers": []any{},
				},
			},
		},
	}
	cfg, ok := coffeeConfigFromWatchEvent(event)
	if !ok {
		t.Fatalf("expected modified event to decode")
	}
	if cfg.Metadata.ResourceVersion != "456" {
		t.Fatalf("unexpected resourceVersion: got %q want %q", cfg.Metadata.ResourceVersion, "456")
	}
	if cfg.Metadata.Generation != 7 {
		t.Fatalf("unexpected generation: got %d want %d", cfg.Metadata.Generation, 7)
	}
	if len(cfg.Spec.Products) != 1 {
		t.Fatalf("unexpected products length: got %d want %d", len(cfg.Spec.Products), 1)
	}
	if cfg.Spec.Products[0].SKU != "coffee-flat-white" {
		t.Fatalf("unexpected sku: got %q want %q", cfg.Spec.Products[0].SKU, "coffee-flat-white")
	}
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

func TestGetOrRequestTokenCoalescesConcurrentMisses(t *testing.T) {
	now := time.Now()
	cache := newTokenCache()
	stub := &stubKubeClient{
		token:          "tok-1",
		exp:            now.Add(10 * time.Minute),
		requestStarted: make(chan struct{}),
		releaseRequest: make(chan struct{}),
	}

	const callers = 64
	tokens := make(chan string, callers)
	errs := make(chan error, callers)

	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			token, err := getOrRequestToken(cache, stub, "shared", now, 20*time.Second, "vote", "quiz-access", nil, 300, context.Background())
			if err != nil {
				errs <- err
				return
			}
			tokens <- token
		}()
	}

	<-stub.requestStarted
	close(stub.releaseRequest)
	wg.Wait()
	close(tokens)
	close(errs)

	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %d", len(errs))
	}
	if stub.calls != 1 {
		t.Fatalf("expected exactly 1 token request, got %d", stub.calls)
	}
	for token := range tokens {
		if token != "tok-1" {
			t.Fatalf("unexpected token: got %q want %q", token, "tok-1")
		}
	}
}

func TestGetOrFetchQuizSessionCachesAndCoalesces(t *testing.T) {
	now := time.Now()
	cache := newQuizSessionCache()
	want := quizSessionSpec{}
	want.Spec.State = "live"
	want.Spec.Title = "KubeCon Quiz"

	started := make(chan struct{})
	release := make(chan struct{})

	var mu sync.Mutex
	calls := 0
	fetch := func(context.Context) (quizSessionSpec, error) {
		mu.Lock()
		calls++
		mu.Unlock()

		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return want, nil
	}

	const callers = 64
	sessions := make(chan quizSessionSpec, callers)
	errs := make(chan error, callers)

	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			session, err := getOrFetchQuizSession(cache, "voter/kubecon-2026", now, 30*time.Second, context.Background(), fetch)
			if err != nil {
				errs <- err
				return
			}
			sessions <- session
		}()
	}

	<-started
	close(release)
	wg.Wait()
	close(sessions)
	close(errs)

	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %d", len(errs))
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 session fetch, got %d", calls)
	}
	for session := range sessions {
		if session.Spec.State != want.Spec.State || session.Spec.Title != want.Spec.Title {
			t.Fatalf("unexpected session: got %+v want %+v", session, want)
		}
	}

	cached, err := getOrFetchQuizSession(cache, "voter/kubecon-2026", now.Add(5*time.Second), 30*time.Second, context.Background(), func(context.Context) (quizSessionSpec, error) {
		t.Fatal("expected cached session")
		return quizSessionSpec{}, nil
	})
	if err != nil {
		t.Fatalf("unexpected cache hit error: %v", err)
	}
	if cached.Spec.State != want.Spec.State || cached.Spec.Title != want.Spec.Title {
		t.Fatalf("unexpected cached session: got %+v want %+v", cached, want)
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

	now := time.Now()
	resp := httptest.NewRecorder()
	if err := setTestSessionCookie(t, resp, cfg, sc, testStableID, testDisplayName, testEmail, now); err != nil {
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
	if resolved.StableID != testStableID {
		t.Fatalf("unexpected stableID: got %q want %q", resolved.StableID, testStableID)
	}
	if resolved.DisplayName != testDisplayName {
		t.Fatalf("unexpected displayName: got %q want %q", resolved.DisplayName, testDisplayName)
	}
	if resolved.Email != testEmail {
		t.Fatalf("unexpected email: got %q want %q", resolved.Email, testEmail)
	}

	// Expired cookie should be rejected.
	_, ok = getSessionFromCookie(req, cfg, sc, now.Add(2*time.Minute))
	if ok {
		t.Fatalf("expected session cookie to be expired")
	}
}

func TestRequireSessionMiddlewareUsesSharedCookie(t *testing.T) {
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
	resp := httptest.NewRecorder()
	if err := setTestSessionCookie(t, resp, cfg, sc, testStableID, testDisplayName, testEmail, time.Now()); err != nil {
		t.Fatalf("unexpected set cookie error: %v", err)
	}

	req := httptest.NewRequest("GET", "http://example.com/private/forward-auth-decision", nil)
	for _, cookie := range resp.Result().Cookies() {
		req.AddCookie(cookie)
	}

	deps := handlerDeps{
		cfg:           cfg,
		sessionCookie: sc,
	}

	var gotSession sessionCookiePayload
	next := func(w http.ResponseWriter, r *http.Request) {
		gotSession, _ = getBrowserSession(r)
		w.WriteHeader(http.StatusOK)
	}

	rec := httptest.NewRecorder()
	requireSessionMiddleware(deps, next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code: got %d, want %d", rec.Code, http.StatusOK)
	}
	if gotSession.DisplayName != testDisplayName {
		t.Fatalf("unexpected displayName: got %q want %q", gotSession.DisplayName, testDisplayName)
	}
}

func TestPublicBuildInfoEndpoint(t *testing.T) {
	previousCommit := gitCommit
	previousDirty := gitDirty
	previousBuildDate := buildDate
	t.Cleanup(func() {
		gitCommit = previousCommit
		gitDirty = previousDirty
		buildDate = previousBuildDate
	})

	gitCommit = "abc1234"
	gitDirty = "1"
	buildDate = "2026-05-05T08:30:00Z"

	mux := http.NewServeMux()
	registerHandlers(mux, handlerDeps{
		kube: &stubKubeClient{},
	})

	req := httptest.NewRequest(http.MethodGet, "http://auth-service/public/build-info", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}

	var payload publicBuildInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if payload.GitCommit != "abc1234" {
		t.Fatalf("unexpected gitCommit: got %q want %q", payload.GitCommit, "abc1234")
	}
	if !payload.IsDirty {
		t.Fatalf("expected dirty build flag to be true")
	}
	if payload.BuildDate != "2026-05-05T08:30:00Z" {
		t.Fatalf("unexpected buildDate: got %q want %q", payload.BuildDate, "2026-05-05T08:30:00Z")
	}
	if payload.CommitWithDirty != "abc1234-dirty" {
		t.Fatalf("unexpected commitWithDirty: got %q want %q", payload.CommitWithDirty, "abc1234-dirty")
	}
}

func TestPublicLoginStoresIdentityAndSessionEndpointReturnsIt(t *testing.T) {
	cfg := config{
		SessionCookieName:       "auth_session",
		SessionCookieMaxAgeSecs: 3600,
		JoinCodeTTL:             2 * time.Hour,
		JoinCodeLength:          4,
		CookieSecure:            false,
	}
	store := newJoinCodeStore(cfg)
	code, _ := store.rotateAndGet(globalDemoAccessCodeKey, time.Now())
	hashKey, blockKey, err := generateCookieKeys()
	if err != nil {
		t.Fatalf("unexpected key error: %v", err)
	}
	sc, err := newSessionSecureCookie(hashKey, blockKey)
	if err != nil {
		t.Fatalf("unexpected securecookie error: %v", err)
	}

	mux := http.NewServeMux()
	registerHandlers(mux, handlerDeps{
		cfg:           cfg,
		codes:         store,
		kube:          &stubKubeClient{},
		sessionCookie: sc,
		orders:        newCoffeeRuntime(),
		changes:       newCoffeeChangeRuntime(8),
	})

	loginBody := `{"code":"` + code + `","stableId":"` + testStableID + `","displayName":"` + testDisplayName + `","email":"` + testEmail + `"}`
	loginReq := httptest.NewRequest(http.MethodPost, "http://auth-service/public/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusNoContent {
		t.Fatalf("unexpected login status: got %d want %d body=%q", loginRec.Code, http.StatusNoContent, loginRec.Body.String())
	}

	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected session cookie to be set")
	}

	sessionReq := httptest.NewRequest(http.MethodGet, "http://auth-service/public/session", nil)
	sessionReq.AddCookie(cookies[0])
	sessionRec := httptest.NewRecorder()
	mux.ServeHTTP(sessionRec, sessionReq)

	if sessionRec.Code != http.StatusOK {
		t.Fatalf("unexpected session status: got %d want %d body=%q", sessionRec.Code, http.StatusOK, sessionRec.Body.String())
	}

	var payload map[string]string
	if err := json.NewDecoder(sessionRec.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode session response: %v", err)
	}
	if payload["stableId"] != testStableID {
		t.Fatalf("unexpected stableId: got %q want %q", payload["stableId"], testStableID)
	}
	if payload["displayName"] != testDisplayName {
		t.Fatalf("unexpected displayName: got %q want %q", payload["displayName"], testDisplayName)
	}
	if payload["email"] != testEmail {
		t.Fatalf("unexpected email: got %q want %q", payload["email"], testEmail)
	}
}

// TestPublicLoginRejectsInvalidIdentity is the security regression test for
// the fail-closed contract at the login layer: a request that can't form a
// valid audience identity (bad stableId, bad displayName, bad email) is
// rejected with 400, so no invalid session cookie ever gets signed.
func TestPublicLoginRejectsInvalidIdentity(t *testing.T) {
	cfg := config{
		SessionCookieName:       "auth_session",
		SessionCookieMaxAgeSecs: 3600,
		JoinCodeTTL:             2 * time.Hour,
		JoinCodeLength:          4,
		CookieSecure:            false,
	}
	store := newJoinCodeStore(cfg)
	code, _ := store.rotateAndGet(globalDemoAccessCodeKey, time.Now())
	hashKey, blockKey, err := generateCookieKeys()
	if err != nil {
		t.Fatalf("unexpected key error: %v", err)
	}
	sc, err := newSessionSecureCookie(hashKey, blockKey)
	if err != nil {
		t.Fatalf("unexpected securecookie error: %v", err)
	}

	cases := []struct {
		name string
		body string
	}{
		{name: "blank displayName", body: `{"code":"` + code + `","stableId":"521541","displayName":"   ","email":"521541@demo.configbutler.ai"}`},
		{name: "missing stableId", body: `{"code":"` + code + `","displayName":"Alice","email":"521541@demo.configbutler.ai"}`},
		{name: "bad stableId (leading zero)", body: `{"code":"` + code + `","stableId":"012345","displayName":"Alice","email":"521541@demo.configbutler.ai"}`},
		{name: "displayName with angle brackets", body: `{"code":"` + code + `","stableId":"521541","displayName":"Mallory <m@evil.example>","email":"521541@demo.configbutler.ai"}`},
		{name: "missing email", body: `{"code":"` + code + `","stableId":"521541","displayName":"Alice"}`},
		{name: "bad email", body: `{"code":"` + code + `","stableId":"521541","displayName":"Alice","email":"not-an-email"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			registerHandlers(mux, handlerDeps{
				cfg:           cfg,
				codes:         store,
				kube:          &stubKubeClient{},
				sessionCookie: sc,
				orders:        newCoffeeRuntime(),
				changes:       newCoffeeChangeRuntime(8),
			})

			req := httptest.NewRequest(http.MethodPost, "http://auth-service/public/login", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%q", rec.Code, rec.Body.String())
			}
			if cookies := rec.Result().Cookies(); len(cookies) > 0 && cookies[0].MaxAge != -1 {
				t.Fatalf("expected no session cookie set, got %+v", cookies[0])
			}
		})
	}
}

func TestPublicLoginRequiresValidCode(t *testing.T) {
	cfg := config{
		SessionCookieName:       "auth_session",
		SessionCookieMaxAgeSecs: 3600,
		JoinCodeTTL:             2 * time.Hour,
		JoinCodeLength:          4,
		CookieSecure:            false,
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

	mux := http.NewServeMux()
	registerHandlers(mux, handlerDeps{
		cfg:           cfg,
		codes:         store,
		kube:          &stubKubeClient{},
		sessionCookie: sc,
		orders:        newCoffeeRuntime(),
		changes:       newCoffeeChangeRuntime(8),
	})

	req := httptest.NewRequest(http.MethodPost, "http://auth-service/public/login", strings.NewReader(`{"code":"WRONG","stableId":"`+testStableID+`","displayName":"`+testDisplayName+`","email":"`+testEmail+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: got %d want %d body=%q", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestAdminPatchUsesDisplayNameFromSessionCookie(t *testing.T) {
	cfg := config{
		SessionCookieName:       "auth_session",
		SessionCookieMaxAgeSecs: 3600,
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

	before := coffeeConfig{
		Metadata: kubeObjectMeta{Generation: 7},
		Spec: coffeeConfigSpec{
			ShopName:   "TestNet Coffee",
			BannerText: "Before",
		},
	}
	after := before
	after.Metadata.Generation = 8
	after.Spec.BannerText = "After"

	stub := &stubKubeClient{
		coffeeConfig: before,
		patchResult:  after,
	}
	changes := newCoffeeChangeRuntime(8)

	mux := http.NewServeMux()
	registerHandlers(mux, handlerDeps{
		cfg:           cfg,
		kube:          stub,
		sessionCookie: sc,
		orders:        newCoffeeRuntime(),
		changes:       changes,
	})

	cookieRec := httptest.NewRecorder()
	if err := setTestSessionCookie(t, cookieRec, cfg, sc, testStableID, testDisplayName, testEmail, time.Now()); err != nil {
		t.Fatalf("failed to set session cookie: %v", err)
	}
	cookies := cookieRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected session cookie")
	}

	req := httptest.NewRequest(http.MethodPatch, "http://auth-service/public/admin/coffeeconfig", strings.NewReader(`{"spec":{"bannerText":"After"}}`))
	req.Header.Set("Content-Type", "application/merge-patch+json")
	req.Header.Set("X-Admin-Actor", "Mallory")
	req.Header.Set("X-Change-Reason", "demo update")
	req.AddCookie(cookies[0])
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected patch status: got %d want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}

	snapshot := changes.snapshot()
	if len(snapshot.Changes) != 1 {
		t.Fatalf("unexpected change count: got %d want %d", len(snapshot.Changes), 1)
	}
	if snapshot.Changes[0].Actor != testDisplayName {
		t.Fatalf("unexpected actor: got %q want %q", snapshot.Changes[0].Actor, testDisplayName)
	}
	if snapshot.Changes[0].Reason != "demo update" {
		t.Fatalf("unexpected reason: got %q want %q", snapshot.Changes[0].Reason, "demo update")
	}
}

// adminPatchTestEnv bundles the boilerplate for the CommitRequest integration
// tests below — secure cookie, session-bearing request, and a stub kube client.
type adminPatchTestEnv struct {
	mux  *http.ServeMux
	stub *stubKubeClient
	cfg  config
	sc   *securecookie.SecureCookie
}

func newAdminPatchTestEnv(t *testing.T, cfg config) *adminPatchTestEnv {
	t.Helper()
	if cfg.SessionCookieName == "" {
		cfg.SessionCookieName = "auth_session"
	}
	if cfg.SessionCookieMaxAgeSecs == 0 {
		cfg.SessionCookieMaxAgeSecs = 3600
	}
	hashKey, blockKey, err := generateCookieKeys()
	if err != nil {
		t.Fatalf("generateCookieKeys: %v", err)
	}
	sc, err := newSessionSecureCookie(hashKey, blockKey)
	if err != nil {
		t.Fatalf("newSessionSecureCookie: %v", err)
	}

	stub := &stubKubeClient{
		coffeeConfig: coffeeConfig{Spec: coffeeConfigSpec{ShopName: "Before"}},
		patchResult:  coffeeConfig{Spec: coffeeConfigSpec{ShopName: "After"}},
	}

	mux := http.NewServeMux()
	registerHandlers(mux, handlerDeps{
		cfg:           cfg,
		kube:          stub,
		sessionCookie: sc,
		orders:        newCoffeeRuntime(),
		changes:       newCoffeeChangeRuntime(8),
	})

	return &adminPatchTestEnv{mux: mux, stub: stub, cfg: cfg, sc: sc}
}

func (e *adminPatchTestEnv) sendPatch(t *testing.T, stableID, displayName, email, reason string) *httptest.ResponseRecorder {
	t.Helper()
	cookieRec := httptest.NewRecorder()
	if err := setTestSessionCookie(t, cookieRec, e.cfg, e.sc, stableID, displayName, email, time.Now()); err != nil {
		t.Fatalf("setTestSessionCookie: %v", err)
	}
	cookies := cookieRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected session cookie")
	}

	req := httptest.NewRequest(http.MethodPatch, "http://auth-service/public/admin/coffeeconfig",
		strings.NewReader(`{"spec":{"shopName":"After"}}`))
	req.Header.Set("Content-Type", "application/merge-patch+json")
	if reason != "" {
		req.Header.Set("X-Change-Reason", reason)
	}
	req.AddCookie(cookies[0])
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

// TestAdminPatchCreatesCommitRequestWhenConfigured verifies the happy-path
// integration: a configured GitTarget name causes a CommitRequest create to
// follow a successful CoffeeConfig patch, with the same actor and a trimmed
// message taken from the X-Change-Reason header.
func TestAdminPatchCreatesCommitRequestWhenConfigured(t *testing.T) {
	env := newAdminPatchTestEnv(t, config{
		ConfigButlerGitTargetName: "voter-coffee",
	})

	rec := env.sendPatch(t, testStableID, testDisplayName, testEmail, "demo update")
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%q", rec.Code, rec.Body.String())
	}

	if env.stub.commitRequestCalls != 1 {
		t.Fatalf("expected exactly 1 CommitRequest call, got %d", env.stub.commitRequestCalls)
	}
	got := env.stub.lastCommitRequest
	wantUser := "demo:" + testStableID
	if got.Identity.Username != wantUser {
		t.Fatalf("Identity.Username: got %q want %q", got.Identity.Username, wantUser)
	}
	if got.Identity.DisplayName != testDisplayName {
		t.Fatalf("Identity.DisplayName: got %q want %q", got.Identity.DisplayName, testDisplayName)
	}
	if got.Identity.Email != testEmail {
		t.Fatalf("Identity.Email: got %q want %q", got.Identity.Email, testEmail)
	}
	if got.GitTargetName != "voter-coffee" {
		t.Fatalf("GitTargetName: got %q want %q", got.GitTargetName, "voter-coffee")
	}
	if got.Message != "demo update" {
		t.Fatalf("Message: got %q want %q", got.Message, "demo update")
	}
}

// TestAdminPatchUsernameIsStableIDOnly is the regression test for the
// split-identity design: editing the display name on a subsequent save must
// not change the K8s username that lands in the audit log / CommitRequest.
func TestAdminPatchUsernameIsStableIDOnly(t *testing.T) {
	env := newAdminPatchTestEnv(t, config{
		ConfigButlerGitTargetName: "voter-coffee",
	})

	// First save with the default display name.
	if rec := env.sendPatch(t, testStableID, "Anonymous "+testStableID, testEmail, "first"); rec.Code != http.StatusOK {
		t.Fatalf("first patch status: got %d body=%q", rec.Code, rec.Body.String())
	}
	firstUser := env.stub.lastCommitRequest.Identity.Username

	// Second save under the same stableID but a personalized display name.
	if rec := env.sendPatch(t, testStableID, "Simon Koudijs", testEmail, "second"); rec.Code != http.StatusOK {
		t.Fatalf("second patch status: got %d body=%q", rec.Code, rec.Body.String())
	}
	secondUser := env.stub.lastCommitRequest.Identity.Username

	if firstUser != secondUser {
		t.Fatalf("Username changed when display name changed: %q vs %q", firstUser, secondUser)
	}
	if firstUser != "demo:"+testStableID {
		t.Fatalf("Username: got %q want %q", firstUser, "demo:"+testStableID)
	}
}

// TestAdminPatchSkipsCommitRequestWhenGitTargetNotSet ensures the side effect
// is fully opt-in: with no CONFIGBUTLER_GIT_TARGET_NAME the patch path must
// behave exactly as before (no CommitRequest create).
func TestAdminPatchSkipsCommitRequestWhenGitTargetNotSet(t *testing.T) {
	env := newAdminPatchTestEnv(t, config{})

	rec := env.sendPatch(t, testStableID, testDisplayName, testEmail, "demo update")
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%q", rec.Code, rec.Body.String())
	}
	if env.stub.commitRequestCalls != 0 {
		t.Fatalf("expected no CommitRequest calls, got %d", env.stub.commitRequestCalls)
	}
}

// Invalid-identity fail-closed is now exercised at the login layer in
// TestPublicLoginRejectsInvalidIdentity. Once a session cookie exists, the
// identity in it has already been validated, so the PATCH handler doesn't
// have an "invalid session" branch to test independently.

// TestAdminPatchSucceedsWhenCommitRequestFails proves the contract from the
// plan: once the CoffeeConfig is written, a CommitRequest failure must not
// turn the response into an error. The user already saved.
func TestAdminPatchSucceedsWhenCommitRequestFails(t *testing.T) {
	env := newAdminPatchTestEnv(t, config{
		ConfigButlerGitTargetName: "voter-coffee",
	})
	env.stub.commitRequestErr = errors.New("simulated commit request failure")

	rec := env.sendPatch(t, testStableID, testDisplayName, testEmail, "demo update")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK even when CommitRequest fails, got %d body=%q", rec.Code, rec.Body.String())
	}
	if env.stub.commitRequestCalls != 1 {
		t.Fatalf("expected one CommitRequest attempt, got %d", env.stub.commitRequestCalls)
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
			name:       "token review error returns 500",
			authHeader: "Bearer error-token",
			reviewErr:  errors.New("kube unavailable"),
			wantCode:   http.StatusInternalServerError,
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
		SessionCookieName:       "auth_session",
		SessionCookieMaxAgeSecs: 3600,
		CookieSecure:            false,
	}
	now := time.Now()

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
			name:       "valid session returns kubeconfig",
			method:     http.MethodGet,
			url:        "http://auth-service/public/kubeconfig",
			wantCode:   http.StatusOK,
			wantInBody: []string{"kind: Config", "token: stub-token", "namespace: voter", "server: https://auth-service"},
		},
		{
			name:           "server URL comes from X-Forwarded headers",
			method:         http.MethodGet,
			url:            "http://auth-service/public/kubeconfig",
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
			name:        "POST returns 405",
			method:      http.MethodPost,
			url:         "http://auth-service/public/kubeconfig",
			wantCode:    http.StatusMethodNotAllowed,
			wantMissing: []string{"kind: Config"},
		},
		{
			name:        "token request failure returns 500",
			method:      http.MethodGet,
			url:         "http://auth-service/public/kubeconfig",
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
				kube:          stub,
				sessionCookie: sc,
				defaultNS:     "voter",
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
			if tc.wantCode != http.StatusUnauthorized {
				cookieRec := httptest.NewRecorder()
				if err := setTestSessionCookie(t, cookieRec, cfg, sc, testStableID, testDisplayName, testEmail, now); err != nil {
					t.Fatalf("failed to set session cookie: %v", err)
				}
				for _, cookie := range cookieRec.Result().Cookies() {
					req.AddCookie(cookie)
				}
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
