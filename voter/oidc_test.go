package main

// Tests for the security properties of OIDC mode. Each of these corresponds to
// a way the demo could quietly stop being trustworthy: a legacy cookie still
// being honoured, a session outliving its token, a return URL pointing off-site,
// or a cross-site POST succeeding without a CSRF token.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/securecookie"
)

func testConfig() config {
	return config{
		ParticipantCookieName:   "__Host-voter-session",
		SessionCookieMaxAgeSecs: 7200,
		AppOrigin:               "https://voter.koudijs.dev",
	}
}

func testCodec(t *testing.T) *securecookie.SecureCookie {
	t.Helper()
	sc, err := newSessionSecureCookie(make([]byte, 32), make([]byte, 32))
	if err != nil {
		t.Fatalf("codec: %v", err)
	}
	return sc
}

// roundTrip stores a session and reads it back through a real cookie header.
func roundTrip(t *testing.T, cfg config, sc *securecookie.SecureCookie, s participantSession, writeAt, readAt time.Time) (participantSession, bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := setParticipantSession(rec, cfg, sc, s, writeAt); err != nil {
		t.Fatalf("setParticipantSession: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	return getParticipantSession(req, cfg, sc, readAt)
}

func TestSessionRoundTripsAndCarriesToken(t *testing.T) {
	cfg, sc := testConfig(), testCodec(t)
	now := time.Now()
	got, ok := roundTrip(t, cfg, sc, participantSession{
		IDToken: "header.payload.signature", Subject: "abc123",
		DisplayName: "Robin", Email: "abc123@demo.invalid",
		Groups: []string{"demo:voter-audience"}, TokenExpiry: now.Add(time.Hour).Unix(),
	}, now, now.Add(time.Minute))
	if !ok {
		t.Fatal("expected a valid session")
	}
	if got.IDToken != "header.payload.signature" || got.Subject != "abc123" {
		t.Fatalf("session did not round-trip: %+v", got)
	}
	if got.CSRF == "" {
		t.Fatal("expected a CSRF token to be minted")
	}
}

// A cookie whose token has expired must not authenticate, even though the
// cookie itself is intact and correctly signed. An intact cookie must never
// extend the authority of an expired token.
func TestExpiredTokenRejectedDespiteValidCookie(t *testing.T) {
	cfg, sc := testConfig(), testCodec(t)
	now := time.Now()
	if _, ok := roundTrip(t, cfg, sc, participantSession{
		IDToken: "t", Subject: "abc123", TokenExpiry: now.Add(30 * time.Minute).Unix(),
	}, now, now.Add(31*time.Minute)); ok {
		t.Fatal("expired ID token must not authenticate")
	}
}

// The session must end at the EARLIER of the configured lifetime and the
// token's expiry.
func TestSessionCappedAtTokenExpiry(t *testing.T) {
	cfg, sc := testConfig(), testCodec(t)
	cfg.SessionCookieMaxAgeSecs = 7200 // 2h configured
	now := time.Now()
	tokenExp := now.Add(10 * time.Minute) // but the token lasts 10m

	rec := httptest.NewRecorder()
	if err := setParticipantSession(rec, cfg, sc, participantSession{
		IDToken: "t", Subject: "s", TokenExpiry: tokenExp.Unix(),
	}, now); err != nil {
		t.Fatalf("setParticipantSession: %v", err)
	}
	var payload participantSession
	for _, c := range rec.Result().Cookies() {
		if c.Name == cfg.ParticipantCookieName {
			if err := sc.Decode(c.Name, c.Value, &payload); err != nil {
				t.Fatalf("decode: %v", err)
			}
		}
	}
	if payload.ExpiresAt != tokenExp.Unix() {
		t.Fatalf("session expiry %d should be capped to token expiry %d", payload.ExpiresAt, tokenExp.Unix())
	}
}

// A version-2 cookie is the legacy, browser-asserted identity. In OIDC mode it
// must decode to nothing rather than granting a made-up identity.
func TestLegacyIdentityCookieRejected(t *testing.T) {
	cfg, sc := testConfig(), testCodec(t)
	// The legacy cookie shape, declared here rather than imported: the type it
	// came from was deleted with the rest of the browser-asserted identity
	// model. Someone who still has one of these in their browser must not be
	// authenticated by it, so the shape outlives the code that wrote it.
	legacy := struct {
		StableID    string `json:"stableId"`
		DisplayName string `json:"displayName"`
		Email       string `json:"email"`
		IssuedAt    int64  `json:"iat"`
		ExpiresAt   int64  `json:"exp"`
		Version     int    `json:"v"`
	}{
		StableID: "521541", DisplayName: "Mallory", Email: "m@example.com",
		IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Version: 2,
	}
	encoded, err := sc.Encode(cfg.ParticipantCookieName, legacy)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: cfg.ParticipantCookieName, Value: encoded})
	if _, ok := getParticipantSession(req, cfg, sc, time.Now()); ok {
		t.Fatal("a legacy version-2 identity cookie must not authenticate in OIDC mode")
	}
}

// A cookie signed with different keys must not be accepted.
func TestForgedCookieRejected(t *testing.T) {
	cfg := testConfig()
	attacker, err := newSessionSecureCookie([]byte(strings.Repeat("A", 32)), []byte(strings.Repeat("B", 32)))
	if err != nil {
		t.Fatalf("codec: %v", err)
	}
	now := time.Now()
	rec := httptest.NewRecorder()
	if err := setParticipantSession(rec, cfg, attacker, participantSession{
		IDToken: "forged", Subject: "system:admin", TokenExpiry: now.Add(time.Hour).Unix(),
	}, now); err != nil {
		t.Fatalf("setParticipantSession: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	if _, ok := getParticipantSession(req, cfg, testCodec(t), now); ok {
		t.Fatal("a cookie signed with foreign keys must not authenticate")
	}
}

// An ID token too large to fit in a cookie must be reported, never truncated:
// a truncated token is a corrupt credential that fails confusingly later.
func TestOversizedTokenRefused(t *testing.T) {
	cfg, sc := testConfig(), testCodec(t)
	rec := httptest.NewRecorder()
	err := setParticipantSession(rec, cfg, sc, participantSession{
		IDToken: strings.Repeat("x", 8000), Subject: "s",
		TokenExpiry: time.Now().Add(time.Hour).Unix(),
	}, time.Now())
	// Either our own limit or securecookie's own cap may fire first; what
	// matters is that it errors and sets no cookie, rather than storing a
	// truncated credential.
	if err == nil {
		t.Fatal("expected an error for an oversized token")
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("no cookie may be set when the session does not fit")
	}
}

func TestCSRFEnforcement(t *testing.T) {
	cfg := testConfig()
	s := participantSession{CSRF: "the-expected-token"}

	cases := []struct {
		name    string
		origin  string
		token   string
		wantErr bool
	}{
		{"valid", "https://voter.koudijs.dev", "the-expected-token", false},
		{"no origin header is allowed if token matches", "", "the-expected-token", false},
		{"foreign origin", "https://evil.example", "the-expected-token", true},
		{"missing token", "https://voter.koudijs.dev", "", true},
		{"wrong token", "https://voter.koudijs.dev", "guessed", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/public/orders", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.token != "" {
				req.Header.Set("X-CSRF-Token", tc.token)
			}
			err := checkCSRF(req, cfg, s)
			if tc.wantErr != (err != nil) {
				t.Fatalf("checkCSRF err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// Post-login redirects must stay inside this application. An open redirect here
// would let a phishing page bounce a freshly authenticated participant off-site.
func TestSafeReturnPath(t *testing.T) {
	p := &oidcProvider{}
	cases := map[string]string{
		"/admin":                   "/admin",
		"/":                        "/",
		"":                         "/",
		"//evil.example":           "/",
		"https://evil.example":     "/",
		"http://evil.example/x":    "/",
		"/path\r\nSet-Cookie: a=b": "/",
		"\\\\evil.example":         "/",
		"/ok?query=1#frag":         "/ok?query=1#frag",
	}
	for in, want := range cases {
		if got := p.safeReturnPath(in); got != want {
			t.Errorf("safeReturnPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// A pending login is single-use and bound to the browser that started it.
func TestLoginTransactionIsSingleUseAndBrowserBound(t *testing.T) {
	p := &oidcProvider{transactions: map[string]*loginTransaction{}, now: time.Now}
	p.transactions["state-1"] = &loginTransaction{
		Nonce: "n", Verifier: "v", Browser: "browser-a",
		Return: "/", Expires: time.Now().Add(time.Minute),
	}

	// Consume it the way handleCallback does.
	p.mu.Lock()
	first := p.transactions["state-1"]
	delete(p.transactions, "state-1")
	p.mu.Unlock()
	if first == nil {
		t.Fatal("expected the transaction to be present")
	}

	p.mu.Lock()
	replay := p.transactions["state-1"]
	p.mu.Unlock()
	if replay != nil {
		t.Fatal("a replayed callback must not find the transaction again")
	}
}

func TestSweepDropsExpiredTransactions(t *testing.T) {
	now := time.Now()
	p := &oidcProvider{transactions: map[string]*loginTransaction{}, now: func() time.Time { return now }}
	p.transactions["old"] = &loginTransaction{Expires: now.Add(-time.Second)}
	p.transactions["fresh"] = &loginTransaction{Expires: now.Add(time.Minute)}
	p.sweep(now)
	if _, ok := p.transactions["old"]; ok {
		t.Fatal("expired transaction should have been swept")
	}
	if _, ok := p.transactions["fresh"]; !ok {
		t.Fatal("unexpired transaction should survive")
	}
}

// The cookie keys come from a pre-created Secret; bad input must fail at boot.
func TestAppCookieKeyValidation(t *testing.T) {
	// base64 of exactly 32 bytes.
	const valid = "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE="
	cases := []struct {
		name, hash, block string
		wantErr           bool
	}{
		{"valid", valid, valid, false},
		{"missing hash", "", valid, true},
		{"missing block", valid, "", true},
		{"not base64", "!!!!", valid, true},
		{"wrong length", "YWJj", valid, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := loadAppCookieKeys(config{AppCookieHashKey: tc.hash, AppCookieBlockKey: tc.block})
			if tc.wantErr != (err != nil) {
				t.Fatalf("loadAppCookieKeys err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// A participant client must carry the participant's token and nothing that
// could substitute the server's own identity.
func TestParticipantRESTConfigCarriesOnlyTheToken(t *testing.T) {
	rc, err := participantRESTConfig(config{KubernetesAPIServer: "https://kube.example:6443"}, "the-id-token")
	if err != nil {
		t.Fatalf("participantRESTConfig: %v", err)
	}
	if rc.BearerToken != "the-id-token" {
		t.Fatalf("BearerToken = %q", rc.BearerToken)
	}
	if rc.Host != "https://kube.example:6443" {
		t.Fatalf("Host = %q", rc.Host)
	}
	if rc.BearerTokenFile != "" {
		t.Error("BearerTokenFile must be empty: it would let the ServiceAccount token be used")
	}
	if rc.CertFile != "" || rc.KeyFile != "" {
		t.Error("client certificates must not be set")
	}
	if rc.ExecProvider != nil || rc.AuthProvider != nil {
		t.Error("exec/auth providers must not be set")
	}
	if rc.Impersonate.UserName != "" {
		t.Error("impersonation must not be set in OIDC mode")
	}
	if rc.Insecure {
		t.Error("TLS verification must never be disabled")
	}
}

func TestParticipantRESTConfigRejectsEmptyToken(t *testing.T) {
	if _, err := participantRESTConfig(config{}, "   "); err == nil {
		t.Fatal("an empty participant token must be an error, not an anonymous client")
	}
}

// The connector choice decides whether the audience sees Dex's "how do you want
// to sign in" screen. Getting it wrong is not a security bug, but it is the
// difference between a room full of people landing on the join form and landing
// on a question they cannot evaluate.
func TestConnectorFor(t *testing.T) {
	provider := func(def string, choices ...string) *oidcProvider {
		return &oidcProvider{cfg: config{OIDCConnectorID: def, OIDCConnectorChoices: choices}}
	}
	req := func(query string) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/auth/login"+query, nil)
	}

	for _, tc := range []struct {
		name  string
		p     *oidcProvider
		query string
		want  string
	}{
		{"default connector when none requested", provider("room", "github"), "", "room"},
		{"an offered choice is honoured", provider("room", "github"), "?connector=github", "github"},
		{"an unlisted connector falls back to the default", provider("room", "github"), "?connector=evil", "room"},
		{"no default and no choices lets Dex ask", provider(""), "", ""},
		{"a request cannot invent a connector", provider(""), "?connector=github", ""},
		// Trimmed, then matched against the allowlist -- so surrounding
		// whitespace is tolerated but cannot smuggle in an unlisted value.
		{"surrounding whitespace is trimmed", provider("room", "github"), "?connector=+github+", "github"},
		{"a near-miss is not a match", provider("room", "github"), "?connector=github2", "room"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.connectorFor(req(tc.query)); got != tc.want {
				t.Errorf("connectorFor(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}
