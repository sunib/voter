package main

import (
	"context"
	"sync"
	"time"
)

type tokenCacheEntry struct {
	token     string
	expiresAt time.Time
}

type tokenCache struct {
	mu      sync.Mutex
	entries map[string]tokenCacheEntry
}

func newTokenCache() *tokenCache {
	return &tokenCache{entries: map[string]tokenCacheEntry{}}
}

func (c *tokenCache) get(key string, now time.Time, skew time.Duration) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return "", false
	}
	if entry.expiresAt.IsZero() || now.Add(skew).Before(entry.expiresAt) {
		return entry.token, true
	}
	delete(c.entries, key)
	return "", false
}

func (c *tokenCache) set(key, token string, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = tokenCacheEntry{token: token, expiresAt: expiresAt}
}

type tokenRequester interface {
	requestToken(ctx context.Context, namespace, serviceAccount string, audiences []string, ttlSeconds int64) (string, time.Time, error)
}

func getOrRequestToken(cache *tokenCache, requester tokenRequester, key string, now time.Time, skew time.Duration, saNamespace, saName string, audiences []string, ttlSeconds int64, ctx context.Context) (string, error) {
	tokenToUse, ok := cache.get(key, now, skew)
	if ok {
		return tokenToUse, nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	token, exp, err := requester.requestToken(reqCtx, saNamespace, saName, audiences, ttlSeconds)
	if err != nil {
		return "", err
	}
	cache.set(key, token, exp)
	return token, nil
}
