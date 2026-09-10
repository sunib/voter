package main

import (
	"errors"

	"github.com/gorilla/securecookie"
)

func newSessionSecureCookie(hashKey, blockKey []byte) (*securecookie.SecureCookie, error) {
	if len(hashKey) == 0 || len(blockKey) == 0 {
		return nil, errors.New("missing secure cookie keys")
	}
	sc := securecookie.New(hashKey, blockKey)
	sc.SetSerializer(securecookie.JSONEncoder{})
	return sc, nil
}
