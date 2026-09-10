package main

import (
	"errors"

	"github.com/gorilla/securecookie"
)

const (
// Bumped from 1 → 2 with the nickname → {stableId, displayName, email}
// shape change so cookies written before the rollout are silently
// invalidated instead of decoding into a half-populated payload.
)

func newSessionSecureCookie(hashKey, blockKey []byte) (*securecookie.SecureCookie, error) {
	if len(hashKey) == 0 || len(blockKey) == 0 {
		return nil, errors.New("missing secure cookie keys")
	}
	sc := securecookie.New(hashKey, blockKey)
	sc.SetSerializer(securecookie.JSONEncoder{})
	return sc, nil
}
