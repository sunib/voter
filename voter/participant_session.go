package main

// The participant session cookie.
//
// The Dex ID token is physically stored in the browser, but it is encrypted and
// signed with keys only this service holds, and the cookie is HttpOnly, so
// JavaScript cannot read it. This is a stateless encrypted session, not a
// session id pointing at a server-side store -- which is why a backend restart
// does not sign anyone out, as long as the keys are the same.
//
// Version 3 is a deliberate break from the legacy self-asserted identity
// payload (version 2). A version-2 cookie decodes to nothing here, so an old
// browser session cannot carry a made-up identity into OIDC mode.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/securecookie"
)

const (
	participantCookieVersion = 3

	// Browsers commonly cap a single cookie at 4096 bytes including the name
	// and attributes. A Dex ID token plus securecookie's encryption and base64
	// overhead fits comfortably, but measure rather than assume: exceeding it
	// silently drops the cookie and produces a baffling "logged out
	// immediately" bug. We refuse instead.
	maxEncodedCookieBytes = 3800
)

// participantSession is what the cookie holds. Everything here except IDToken
// is derived from that token and kept only so /auth/session can answer without
// re-parsing it.
type participantSession struct {
	IDToken     string   `json:"idToken"`
	Subject     string   `json:"sub"`
	DisplayName string   `json:"name"`
	Email       string   `json:"email"`
	Groups      []string `json:"groups"`
	// KubeUsername is what the API SERVER derived for this token, not what we
	// think it derived. Empty when the review failed -- the login still stands,
	// because being able to name your Kubernetes identity is not a precondition
	// for having one.
	KubeUsername string `json:"kubeUser,omitempty"`
	// TokenExpiry is the ID token's own exp. The session is never allowed to
	// outlive it, whatever the cookie's MaxAge says.
	TokenExpiry int64 `json:"tokenExp"`
	IssuedAt    int64 `json:"iat"`
	ExpiresAt   int64 `json:"exp"`
	// CSRF is a per-session random value. /auth/session hands it to the SPA,
	// which echoes it in a header on every mutation. An attacker's page can
	// cause the cookie to be sent but cannot read this value out of it.
	CSRF    string `json:"csrf"`
	Version int    `json:"v"`
}

// sessionCookieCodec is set once at startup from the pre-created Secret.
var sessionCookieCodec *securecookie.SecureCookie

func setParticipantSession(w http.ResponseWriter, cfg config, sc *securecookie.SecureCookie, s participantSession, now time.Time) error {
	if sc == nil {
		return errors.New("secure cookie unavailable")
	}

	// The session ends at the EARLIER of the configured lifetime and the ID
	// token's own expiry. An intact cookie must never extend the authority of
	// an expired token.
	sessionEnd := now.Add(time.Duration(cfg.SessionCookieMaxAgeSecs) * time.Second)
	if s.TokenExpiry > 0 {
		if tokenEnd := time.Unix(s.TokenExpiry, 0); tokenEnd.Before(sessionEnd) {
			sessionEnd = tokenEnd
		}
	}
	if !sessionEnd.After(now) {
		return errors.New("token already expired")
	}

	csrf, err := randomToken()
	if err != nil {
		return err
	}

	s.CSRF = csrf
	s.IssuedAt = now.Unix()
	s.ExpiresAt = sessionEnd.Unix()
	s.Version = participantCookieVersion

	encoded, err := sc.Encode(cfg.ParticipantCookieName, s)
	if err != nil {
		return fmt.Errorf("failed to encode session cookie: %w", err)
	}
	if len(encoded) > maxEncodedCookieBytes {
		// Do not truncate and do not silently split across cookies -- either
		// would produce a corrupt credential. Surface it.
		return fmt.Errorf("encoded session cookie is %d bytes, over the %d byte limit", len(encoded), maxEncodedCookieBytes)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cfg.ParticipantCookieName,
		Value:    encoded,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(time.Until(sessionEnd).Seconds()),
		Expires:  sessionEnd,
	})
	return nil
}

func getParticipantSession(r *http.Request, cfg config, sc *securecookie.SecureCookie, now time.Time) (participantSession, bool) {
	var zero participantSession
	if sc == nil {
		return zero, false
	}
	c, err := r.Cookie(cfg.ParticipantCookieName)
	if err != nil || strings.TrimSpace(c.Value) == "" {
		return zero, false
	}
	var s participantSession
	if err := sc.Decode(cfg.ParticipantCookieName, c.Value, &s); err != nil {
		return zero, false
	}
	// A legacy version-2 identity cookie fails here, by design.
	if s.Version != participantCookieVersion {
		return zero, false
	}
	if s.IDToken == "" || s.Subject == "" {
		return zero, false
	}
	// Both clocks matter: the session's own end, and the token's.
	if s.ExpiresAt > 0 && now.Unix() >= s.ExpiresAt {
		return zero, false
	}
	if s.TokenExpiry > 0 && now.Unix() >= s.TokenExpiry {
		return zero, false
	}
	return s, true
}

func clearParticipantSession(w http.ResponseWriter, cfg config) {
	http.SetCookie(w, &http.Cookie{
		Name:     cfg.ParticipantCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

// loadAppCookieKeys reads the pre-created application cookie Secret. Unlike the
// legacy path this NEVER creates the Secret: the deployment supplies it, so the
// ServiceAccount needs no Secret write permission, and two replicas can never
// race to generate different keys.
func loadAppCookieKeys(cfg config) ([]byte, []byte, error) {
	decode := func(name, raw string) ([]byte, error) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, fmt.Errorf("%s is required in OIDC mode", name)
		}
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("%s is not valid base64: %w", name, err)
		}
		// securecookie wants 32 or 64 bytes for the hash key and 16/24/32 for
		// the block key. Check at startup so a bad key is a boot failure, not
		// a runtime surprise on the first login.
		if len(key) != 32 {
			return nil, fmt.Errorf("%s must decode to exactly 32 bytes, got %d", name, len(key))
		}
		return key, nil
	}
	hashKey, err := decode("APP_COOKIE_HASH_KEY", cfg.AppCookieHashKey)
	if err != nil {
		return nil, nil, err
	}
	blockKey, err := decode("APP_COOKIE_BLOCK_KEY", cfg.AppCookieBlockKey)
	if err != nil {
		return nil, nil, err
	}
	return hashKey, blockKey, nil
}

// checkCSRF guards every state-changing, cookie-authenticated request.
// SameSite=Lax alone is not sufficient: it still permits top-level cross-site
// POSTs in some browsers, and it is a defence the application does not control.
func checkCSRF(r *http.Request, cfg config, s participantSession) error {
	if origin := r.Header.Get("Origin"); origin != "" && origin != cfg.AppOrigin {
		return errors.New("unexpected origin")
	}
	supplied := r.Header.Get("X-CSRF-Token")
	if supplied == "" || s.CSRF == "" || subtleCompare(supplied, s.CSRF) != 1 {
		return errors.New("missing or invalid CSRF token")
	}
	return nil
}
