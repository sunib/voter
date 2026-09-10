package main

import (
	"net"
	"net/http"
	"strings"
)

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
