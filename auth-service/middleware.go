package main

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// contextKey is a private type for context keys to avoid collisions.
type contextKey string

// sessionRefKey is the context key for the session reference.
const sessionRefKey contextKey = "sessionRef"

// sessionContext holds the resolved session reference and metadata.
type sessionContext struct {
	ref                 sessionRef
	resolvedViaJoinCode bool
}

// getSessionRef retrieves the session reference from the request context.
// Returns the sessionRef and a boolean indicating if it was found.
func getSessionRef(r *http.Request) (sessionRef, bool) {
	ctx := r.Context()
	sessCtx, ok := ctx.Value(sessionRefKey).(sessionContext)
	if !ok {
		return sessionRef{}, false
	}
	return sessCtx.ref, true
}

// wasResolvedViaJoinCode returns true if the session was resolved via a join code header.
func wasResolvedViaJoinCode(r *http.Request) bool {
	ctx := r.Context()
	sessCtx, ok := ctx.Value(sessionRefKey).(sessionContext)
	if !ok {
		return false
	}
	return sessCtx.resolvedViaJoinCode
}

// requireSessionMiddleware extracts the session from either:
// 1. X-Join-Code header or query param (takes precedence, replaces existing session)
// 2. Session cookie (fallback)
//
// On success, it stores the sessionRef in the request context.
// On failure, it responds with an appropriate error and calls w.WriteHeader().
func requireSessionMiddleware(deps handlerDeps, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		var ref sessionRef
		var resolvedViaJoinCode bool

		// 1. Check join code first - always replaces any existing session
		// Check header first, then fall back to query parameter from X-Forwarded-Uri,
		// then finally check the incoming request URL query parameter
		joinCode := normalizeJoinCodeHeader(r.Header.Get("X-Join-Code"))
		if joinCode == "" {
			forwardedURI := r.Header.Get("X-Forwarded-Uri")
			if forwardedURI != "" {
				parsedURL, err := url.Parse(forwardedURI)
				if err == nil {
					joinCode = normalizeJoinCodeHeader(parsedURL.Query().Get("code"))
				}
			}
		}
		if joinCode == "" {
			joinCode = normalizeJoinCodeHeader(r.URL.Query().Get("code"))
		}
		if joinCode != "" {
			sessionKey, ok := deps.codes.resolve(joinCode, now)
			if !ok {
				// Invalid join code destroys session
				clearSessionCookie(w, deps.cfg)
				http.Error(w, "invalid join code", http.StatusForbidden)
				return
			}
			ref, ok = parseSessionKey(sessionKey)
			if !ok {
				clearSessionCookie(w, deps.cfg)
				http.Error(w, "invalid join code", http.StatusForbidden)
				return
			}

			err := setSessionCookie(w, deps.cfg, deps.sessionCookie, ref, now)
			if err != nil {
				http.Error(w, "failed to set session", http.StatusUnauthorized)
				return
			}

			resolvedViaJoinCode = true
		} else {
			// 2. No join code - fall back to session cookie
			var hasSession bool
			ref, hasSession = getSessionFromCookie(r, deps.cfg, deps.sessionCookie, now)
			if !hasSession {
				http.Error(w, "invalid/missing session", http.StatusUnauthorized)
				return
			}
		}

		// Store session context in request
		sessCtx := sessionContext{
			ref:                 ref,
			resolvedViaJoinCode: resolvedViaJoinCode,
		}
		ctx := context.WithValue(r.Context(), sessionRefKey, sessCtx)
		next(w, r.WithContext(ctx))
	}
}
