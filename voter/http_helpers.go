package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
)

// noStore keeps a response out of every cache and its URL out of Referer.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// writeJSON is the one place responses are encoded, so a handler cannot
// accidentally send a body without its content type.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// randomToken returns 256 bits of URL-safe randomness, for order IDs.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("randomness unavailable: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
