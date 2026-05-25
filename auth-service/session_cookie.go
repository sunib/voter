package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/securecookie"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Bumped from 1 → 2 with the nickname → {stableId, displayName, email}
	// shape change so cookies written before the rollout are silently
	// invalidated instead of decoding into a half-populated payload.
	sessionCookieVersion = 2
	sessionCookieSecret  = "auth-session-cookie-keys"
)

type sessionCookiePayload struct {
	StableID    string `json:"stableId"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	IssuedAt    int64  `json:"iat"`
	ExpiresAt   int64  `json:"exp"`
	Version     int    `json:"v"`
}

func ensureSessionCookieKeys(ctx context.Context, kube kubeClient) ([]byte, []byte, error) {
	namespace := "default"
	if kube.defaultNS != "" {
		namespace = kube.defaultNS
	} else {
		log.Printf("kubeClient.defaultNS not set: namespace set to %q", namespace)
	}

	secrets := kube.clientset.CoreV1().Secrets(namespace)
	secret, err := secrets.Get(ctx, sessionCookieSecret, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf("failed to get session cookie secret: %w", err)
		}
		hashKey, blockKey, genErr := generateCookieKeys()
		if genErr != nil {
			return nil, nil, genErr
		}
		payload := map[string][]byte{
			"hashKey":  hashKey,
			"blockKey": blockKey,
		}
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: sessionCookieSecret,
			},
			Type: corev1.SecretTypeOpaque,
			Data: payload,
		}
		if _, createErr := secrets.Create(ctx, secret, metav1.CreateOptions{}); createErr != nil {
			return nil, nil, fmt.Errorf("failed to create session cookie secret: %w", createErr)
		}
		log.Printf("cookie: created secret %s", sessionCookieSecret)
		return hashKey, blockKey, nil
	}
	if len(secret.Data["hashKey"]) == 0 || len(secret.Data["blockKey"]) == 0 {
		return nil, nil, errors.New("session cookie secret missing hashKey/blockKey")
	}
	log.Printf("cookie: found secret %s in namespace %q", sessionCookieSecret, namespace)
	return secret.Data["hashKey"], secret.Data["blockKey"], nil
}

func generateCookieKeys() ([]byte, []byte, error) {
	hashKey := make([]byte, 32)
	blockKey := make([]byte, 32)
	if _, err := rand.Read(hashKey); err != nil {
		return nil, nil, fmt.Errorf("failed to generate hashKey: %w", err)
	}
	if _, err := rand.Read(blockKey); err != nil {
		return nil, nil, fmt.Errorf("failed to generate blockKey: %w", err)
	}
	return hashKey, blockKey, nil
}

func newSessionSecureCookie(hashKey, blockKey []byte) (*securecookie.SecureCookie, error) {
	if len(hashKey) == 0 || len(blockKey) == 0 {
		return nil, errors.New("missing secure cookie keys")
	}
	sc := securecookie.New(hashKey, blockKey)
	sc.SetSerializer(securecookie.JSONEncoder{})
	return sc, nil
}

// setSessionCookie writes the validated identity into a signed cookie. The
// caller (login handler) is responsible for validating stableID/displayName/
// email against the identity.go rules first — setSessionCookie just stores
// what it's given.
func setSessionCookie(w http.ResponseWriter, cfg config, sc *securecookie.SecureCookie, identity audienceIdentity, stableID string, now time.Time) error {
	if sc == nil {
		return errors.New("secure cookie unavailable")
	}
	payload := sessionCookiePayload{
		StableID:    stableID,
		DisplayName: identity.DisplayName,
		Email:       identity.Email,
		IssuedAt:    now.Unix(),
		ExpiresAt:   now.Add(time.Duration(cfg.SessionCookieMaxAgeSecs) * time.Second).Unix(),
		Version:     sessionCookieVersion,
	}
	encoded, err := sc.Encode(cfg.SessionCookieName, payload)
	if err != nil {
		return fmt.Errorf("failed to encode session cookie: %w", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cfg.SessionCookieName,
		Value:    encoded,
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   cfg.SessionCookieMaxAgeSecs,
		Expires:  now.Add(time.Duration(cfg.SessionCookieMaxAgeSecs) * time.Second),
	})
	return nil
}

func getSessionFromCookie(r *http.Request, cfg config, sc *securecookie.SecureCookie, now time.Time) (sessionCookiePayload, bool) {
	var zero sessionCookiePayload
	if sc == nil {
		return zero, false
	}
	c, err := r.Cookie(cfg.SessionCookieName)
	if err != nil {
		return zero, false
	}
	value := strings.TrimSpace(c.Value)
	if value == "" {
		return zero, false
	}
	var payload sessionCookiePayload
	if err := sc.Decode(cfg.SessionCookieName, value, &payload); err != nil {
		return zero, false
	}
	if payload.Version != sessionCookieVersion {
		return zero, false
	}
	if payload.ExpiresAt > 0 && now.Unix() > payload.ExpiresAt {
		return zero, false
	}
	// The login handler validated these before signing, so a re-check on read
	// is defence-in-depth. If anything looks off, drop the session rather than
	// surfacing a half-valid identity.
	if validateStableID(payload.StableID) != nil {
		return zero, false
	}
	if _, err := normalizeDisplayName(payload.DisplayName); err != nil {
		return zero, false
	}
	if validateAuthorEmail(strings.TrimSpace(payload.Email)) != nil {
		return zero, false
	}
	return payload, true
}

func clearSessionCookie(w http.ResponseWriter, cfg config) {
	http.SetCookie(w, &http.Cookie{
		Name:     cfg.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

