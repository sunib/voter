package main

import (
	"crypto/rand"
	"math/big"
	"strings"
	"sync"
	"time"
)

type joinCodeEntry struct {
	code      string
	createdAt time.Time
}

type joinCodeIndexEntry struct {
	sessionKey string
	createdAt  time.Time
}

type joinCodeStore struct {
	mu        sync.Mutex
	bySession map[string][]joinCodeEntry
	byCode    map[string]joinCodeIndexEntry
	ttl       time.Duration
	length    int
}

func newJoinCodeStore(cfg config) *joinCodeStore {
	return &joinCodeStore{
		bySession: map[string][]joinCodeEntry{},
		byCode:    map[string]joinCodeIndexEntry{},
		ttl:       cfg.JoinCodeTTL,
		length:    cfg.JoinCodeLength,
	}
}

func (s *joinCodeStore) validate(sessionKey, code string, now time.Time) bool {
	if sessionKey == "" || strings.TrimSpace(code) == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pruneLocked(now)
	entries := s.bySession[sessionKey]
	normalized := normalizeJoinCode(code)
	for _, e := range entries {
		if e.code == normalized {
			return true
		}
	}
	return false
}

func (s *joinCodeStore) rotateAndGet(sessionKey string, now time.Time) (string, bool) {
	if sessionKey == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pruneLocked(now)
	newCode := s.generateUniqueLocked(now)
	s.bySession[sessionKey] = append(s.bySession[sessionKey], joinCodeEntry{code: newCode, createdAt: now})
	s.byCode[newCode] = joinCodeIndexEntry{sessionKey: sessionKey, createdAt: now}
	return newCode, true
}

func (s *joinCodeStore) resolve(code string, now time.Time) (string, bool) {
	normalized := normalizeJoinCode(code)
	if normalized == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pruneLocked(now)
	entry, ok := s.byCode[normalized]
	if !ok {
		return "", false
	}
	if now.Sub(entry.createdAt) > s.ttl {
		delete(s.byCode, normalized)
		return "", false
	}
	return entry.sessionKey, true
}

func (s *joinCodeStore) pruneLocked(now time.Time) {
	for sessionKey, entries := range s.bySession {
		filtered := entries[:0]
		for _, e := range entries {
			if now.Sub(e.createdAt) <= s.ttl {
				filtered = append(filtered, e)
				continue
			}
			if idx, ok := s.byCode[e.code]; ok {
				if idx.sessionKey == sessionKey && idx.createdAt.Equal(e.createdAt) {
					delete(s.byCode, e.code)
				}
			}
		}
		if len(filtered) == 0 {
			delete(s.bySession, sessionKey)
			continue
		}
		s.bySession[sessionKey] = filtered
	}
	for code, entry := range s.byCode {
		if now.Sub(entry.createdAt) > s.ttl {
			delete(s.byCode, code)
		}
	}
}

func (s *joinCodeStore) generateUniqueLocked(now time.Time) string {
	const maxAttempts = 40
	for i := 0; i < maxAttempts; i++ {
		candidate := randomJoinCode(s.length)
		if _, exists := s.byCode[candidate]; !exists {
			return candidate
		}
	}
	return randomJoinCode(s.length)
}

func normalizeJoinCode(code string) string {
	return strings.ToLower(strings.TrimSpace(code))
}

func randomJoinCode(length int) string {
	if length <= 0 {
		length = 4
	}
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	var b strings.Builder
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			b.WriteByte('0')
			continue
		}
		b.WriteByte(charset[n.Int64()])
	}
	return b.String()
}
