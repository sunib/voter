package main

import (
	"context"
	"net/http"
	"time"
)

type contextKey string

const sessionPayloadKey contextKey = "sessionPayload"

func getBrowserSession(r *http.Request) (sessionCookiePayload, bool) {
	var zero sessionCookiePayload
	payload, ok := r.Context().Value(sessionPayloadKey).(sessionCookiePayload)
	if !ok {
		return zero, false
	}
	return payload, true
}

func requireSessionMiddleware(deps handlerDeps, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		payload, ok := getSessionFromCookie(r, deps.cfg, deps.sessionCookie, time.Now())
		if !ok {
			clearSessionCookie(w, deps.cfg)
			http.Error(w, "session required", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), sessionPayloadKey, payload)
		next(w, r.WithContext(ctx))
	}
}
