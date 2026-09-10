package main

// OIDC login against Dex, as decided in room-pass/advised_architecture.md.
//
// The backend is a CONFIDENTIAL OAuth client. It completes the authorization
// code exchange with PKCE server-side, verifies the resulting ID token, and
// stores that token in an encrypted HttpOnly cookie. JavaScript never sees it.
//
// What this file deliberately does NOT do:
//
//   - It never accepts an identity from the browser. There is no "who are you"
//     field anywhere in this flow. The subject comes from a signed Dex token
//     and nowhere else.
//   - It never mints a token of its own. Kubernetes trusts Dex, not this
//     service, so there is no application JWT and no signing key here.
//   - It never impersonates. The participant's own ID token is the credential
//     used against the Kubernetes API (see participant_kube.go).
//
// The authproxy connector issues no refresh token, so a session ends when the
// ID token expires and the participant logs in again. Room Pass remembers the
// enrollment, so that second login needs no room code.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	// loginTransactionLifetime bounds how long an unfinished login may sit in
	// memory. Short, because the only thing between /auth/login and
	// /auth/callback is a Room Pass join form.
	loginTransactionLifetime = 10 * time.Minute
	// maxLoginTransactions caps memory. A restart loses pending logins (the
	// participant just starts again); established cookies are unaffected
	// because they are self-contained.
	maxLoginTransactions = 2000
	// browserBindingCookie ties a pending login to the browser that started
	// it, so a copied /auth/callback URL cannot complete someone else's login.
	browserBindingCookie = "__Host-voter-login"
)

// oidcProvider holds everything the login endpoints need. Built once at
// startup; nil when the deployment runs in legacy mode.
type oidcProvider struct {
	cfg      config
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config

	mu           sync.Mutex
	transactions map[string]*loginTransaction
	now          func() time.Time
}

type loginTransaction struct {
	Nonce    string
	Verifier string
	Browser  string
	Return   string
	Expires  time.Time
}

// randomToken returns 256 bits of URL-safe randomness. Used for state, nonce,
// PKCE verifier and the browser binding value.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("randomness unavailable: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// newOIDCProvider performs discovery against the issuer and builds the client.
// It fails closed: a deployment configured for OIDC that cannot reach or
// validate its issuer must not start and silently fall back to anything.
func newOIDCProvider(ctx context.Context, cfg config) (*oidcProvider, error) {
	switch {
	case strings.TrimSpace(cfg.OIDCIssuerURL) == "":
		return nil, errors.New("OIDC_ISSUER_URL is required when OIDC_ENABLED=true")
	case strings.TrimSpace(cfg.OIDCClientID) == "":
		return nil, errors.New("OIDC_CLIENT_ID is required when OIDC_ENABLED=true")
	case strings.TrimSpace(cfg.OIDCClientSecret) == "":
		return nil, errors.New("OIDC_CLIENT_SECRET is required when OIDC_ENABLED=true")
	case strings.TrimSpace(cfg.OIDCRedirectURL) == "":
		return nil, errors.New("OIDC_REDIRECT_URL is required when OIDC_ENABLED=true")
	}

	discoveryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	provider, err := oidc.NewProvider(discoveryCtx, cfg.OIDCIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery against %s failed: %w", cfg.OIDCIssuerURL, err)
	}

	return &oidcProvider{
		cfg: cfg,
		// ClientID here is the AUDIENCE check: the token must have been issued
		// for this client. The same value is what the kube-apiserver accepts
		// from this issuer.
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID}),
		oauth: oauth2.Config{
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.OIDCRedirectURL,
			// "federated:id" is not decoration. Dex only emits the
			// federated_claims.connector_id claim when a client asks for this
			// scope (see tokens/issuer.go in the pinned Dex), and the
			// kube-apiserver's authenticator derives the username prefix from
			// exactly that claim: github: for an operator login, demo: for a
			// Room Pass participant. Drop the scope and every token from this
			// client is rejected as carrying no connector.
			Scopes: []string{oidc.ScopeOpenID, "profile", "email", "groups", "federated:id"},
		},
		transactions: map[string]*loginTransaction{},
		now:          time.Now,
	}, nil
}

// sweep drops expired transactions. Called under the lock on every insert, so
// the map cannot grow without bound just because logins are abandoned.
func (p *oidcProvider) sweep(now time.Time) {
	for k, tx := range p.transactions {
		if now.After(tx.Expires) {
			delete(p.transactions, k)
		}
	}
}

// safeReturnPath keeps a post-login redirect inside this application. Only
// local absolute paths are allowed, never a full URL, never a protocol-relative
// "//evil.example" and never anything derived from a forwarding header.
func (p *oidcProvider) safeReturnPath(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "/"
	}
	if strings.ContainsAny(value, "\\\r\n") {
		return "/"
	}
	return value
}

// handleLogin starts the authorization code flow. GET only, and always a
// top-level browser navigation -- never a fetch() from the SPA, because it ends
// in a cross-host redirect to the issuer.
func (p *oidcProvider) handleLogin(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, err1 := randomToken()
	nonce, err2 := randomToken()
	verifier, err3 := randomToken()
	browser, err4 := randomToken()
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		http.Error(w, "login unavailable", http.StatusInternalServerError)
		return
	}

	now := p.now()
	p.mu.Lock()
	p.sweep(now)
	if len(p.transactions) >= maxLoginTransactions {
		p.mu.Unlock()
		http.Error(w, "too many logins in progress, please retry shortly", http.StatusServiceUnavailable)
		return
	}
	p.transactions[state] = &loginTransaction{
		Nonce:    nonce,
		Verifier: verifier,
		Browser:  browser,
		Return:   p.safeReturnPath(r.URL.Query().Get("return")),
		Expires:  now.Add(loginTransactionLifetime),
	}
	p.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     browserBindingCookie,
		Value:    browser,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(loginTransactionLifetime.Seconds()),
	})

	// S256 PKCE. Belt and braces for a confidential client, but it costs
	// nothing and closes code interception if the secret ever leaks.
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	http.Redirect(w, r, p.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	), http.StatusFound)
}

// handleCallback completes the exchange. Every check here is load-bearing;
// none of them may be relaxed to "make the demo work".
func (p *oidcProvider) handleCallback(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Dex reports upstream failures here. Surface it as a failed login rather
	// than a confusing exchange error.
	if e := r.URL.Query().Get("error"); e != "" {
		log.Printf("oidc: authorization failed error=%q", e)
		http.Redirect(w, r, "/?login=failed", http.StatusFound)
		return
	}

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}

	// Single use: the transaction is removed before the code is exchanged, so
	// a replayed callback finds nothing.
	p.mu.Lock()
	tx := p.transactions[state]
	delete(p.transactions, state)
	p.mu.Unlock()

	if tx == nil || p.now().After(tx.Expires) {
		http.Error(w, "login expired, please start again", http.StatusBadRequest)
		return
	}

	// The browser that finishes must be the browser that started.
	binding, err := r.Cookie(browserBindingCookie)
	if err != nil || binding.Value != tx.Browser {
		http.Error(w, "login could not be verified, please start again", http.StatusBadRequest)
		return
	}
	clearBrowserBinding(w)

	exchangeCtx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	token, err := p.oauth.Exchange(exchangeCtx, code,
		oauth2.SetAuthURLParam("code_verifier", tx.Verifier))
	if err != nil {
		// Never log the code or the error body verbatim: both can carry
		// credential material.
		log.Printf("oidc: code exchange failed")
		http.Redirect(w, r, "/?login=failed", http.StatusFound)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		log.Printf("oidc: token response carried no id_token")
		http.Redirect(w, r, "/?login=failed", http.StatusFound)
		return
	}

	// Verifies signature against the issuer's JWKS, plus issuer, audience and
	// expiry. Nonce is checked separately below.
	idToken, err := p.verifier.Verify(exchangeCtx, rawIDToken)
	if err != nil {
		log.Printf("oidc: id_token verification failed: %v", err)
		http.Redirect(w, r, "/?login=failed", http.StatusFound)
		return
	}
	if idToken.Nonce != tx.Nonce {
		log.Printf("oidc: id_token nonce mismatch")
		http.Redirect(w, r, "/?login=failed", http.StatusFound)
		return
	}

	var claims struct {
		Name   string   `json:"name"`
		Email  string   `json:"email"`
		Groups []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		log.Printf("oidc: id_token claims could not be decoded")
		http.Redirect(w, r, "/?login=failed", http.StatusFound)
		return
	}

	if err := setParticipantSession(w, p.cfg, sessionCookieCodec, participantSession{
		IDToken:     rawIDToken,
		Subject:     idToken.Subject,
		DisplayName: claims.Name,
		Email:       claims.Email,
		Groups:      claims.Groups,
		TokenExpiry: idToken.Expiry.Unix(),
	}, p.now()); err != nil {
		// The most likely cause is an oversized cookie. Fail loudly rather
		// than silently truncating a credential.
		log.Printf("oidc: could not establish session: %v", err)
		http.Error(w, "session could not be established", http.StatusInternalServerError)
		return
	}

	log.Printf("oidc: login ok sub=%s groups=%v expires=%s", idToken.Subject, claims.Groups, idToken.Expiry.UTC().Format(time.RFC3339))
	http.Redirect(w, r, tx.Return, http.StatusFound)
}

func clearBrowserBinding(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     browserBindingCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}
