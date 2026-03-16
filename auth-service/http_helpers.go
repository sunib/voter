package main

import (
	"crypto/rand"
	"encoding/base64"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

func randomSessionID() (string, error) {
	// 32 bytes ~ 256 bits of entropy.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func setDeviceSessionCookie(w http.ResponseWriter, cfg config) (string, bool) {
	sid, err := randomSessionID()
	if err != nil {
		// If we fail to set a cookie, we still allow the request in this prototype.
		log.Printf("cookie: failed to generate session id: %v", err)
		return "", false
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cfg.CookieName,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   cfg.CookieMaxAgeSecs,
		Expires:  time.Now().Add(time.Duration(cfg.CookieMaxAgeSecs) * time.Second),
	})
	return sid, true
}

func getDeviceSessionID(r *http.Request, cookieName string) (string, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(c.Value)
	return value, value != ""
}

func normalizeJoinCodeHeader(code string) string {
	return strings.ToLower(strings.TrimSpace(code))
}

func clientIP(r *http.Request) string {
	// Traefik typically sets X-Forwarded-For. trustForwardHeader is configured on Traefik,
	// not here, but logging it is still helpful.
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		// Take first hop.
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return xff
	}
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-Ip")); xrip != "" {
		return xrip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
