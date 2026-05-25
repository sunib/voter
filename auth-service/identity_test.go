package main

import (
	"strings"
	"testing"
)

func TestAudienceIdentityFromSessionASCIINames(t *testing.T) {
	cases := []struct {
		name        string
		nickname    string
		wantUser    string
		wantDisplay string
	}{
		{name: "simple", nickname: "Simon Koudijs", wantUser: "demo:simon-koudijs", wantDisplay: "Simon Koudijs"},
		{name: "trims whitespace", nickname: "  Simon Koudijs  ", wantUser: "demo:simon-koudijs", wantDisplay: "Simon Koudijs"},
		{name: "system: prefix no longer special-cased", nickname: "system:masters", wantUser: "demo:system-masters", wantDisplay: "system:masters"},
		{name: "kubernetes-admin", nickname: "kubernetes-admin", wantUser: "demo:kubernetes-admin", wantDisplay: "kubernetes-admin"},
		{name: "generated default", nickname: "Demo Guest 42", wantUser: "demo:demo-guest-42", wantDisplay: "Demo Guest 42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := audienceIdentityFromSession(tc.nickname, "", "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id.Username != tc.wantUser {
				t.Fatalf("Username: got %q want %q", id.Username, tc.wantUser)
			}
			if id.DisplayName != tc.wantDisplay {
				t.Fatalf("DisplayName: got %q want %q", id.DisplayName, tc.wantDisplay)
			}
			if !strings.HasSuffix(id.Email, "@"+demoDefaultEmailDomain) {
				t.Fatalf("Email: got %q, want suffix @%s", id.Email, demoDefaultEmailDomain)
			}
		})
	}
}

// TestAudienceIdentityFromSessionUnicodeNames pins the contract that non-ASCII
// names transliterate via gosimple/slug and never collapse to empty. We don't
// pin the exact slug (unidecode tables can shift across versions) — only that
// the result is non-empty ASCII and prefixed with demo:.
func TestAudienceIdentityFromSessionUnicodeNames(t *testing.T) {
	cases := []string{"Łukasz", "Müller", "佐藤", "Renée", "Søren"}
	for _, nickname := range cases {
		t.Run(nickname, func(t *testing.T) {
			id, err := audienceIdentityFromSession(nickname, "", "")
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", nickname, err)
			}
			if !strings.HasPrefix(id.Username, demoIdentityUsernamePrefix) {
				t.Fatalf("Username missing demo: prefix: %q", id.Username)
			}
			slug := strings.TrimPrefix(id.Username, demoIdentityUsernamePrefix)
			if slug == "" {
				t.Fatalf("transliteration produced empty slug for %q", nickname)
			}
			for _, r := range slug {
				if r > 127 {
					t.Fatalf("slug contained non-ASCII rune %q in %q", r, slug)
				}
			}
			if id.DisplayName != nickname {
				t.Fatalf("DisplayName: got %q want %q (display name should round-trip the original)", id.DisplayName, nickname)
			}
		})
	}
}

func TestAudienceIdentityFromSessionRejects(t *testing.T) {
	cases := []struct {
		name     string
		nickname string
	}{
		{name: "empty", nickname: ""},
		{name: "whitespace only", nickname: "   "},
		{name: "punctuation only collapses to empty slug", nickname: "!!!---"},
		{name: "name with newline (git author break)", nickname: "Alice\nBob"},
		{name: "name with angle bracket", nickname: "Mallory <evil@example.com>"},
		{name: "name with carriage return", nickname: "Alice\rBob"},
		{name: "name too long", nickname: strings.Repeat("a", demoDisplayNameMaxLen+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := audienceIdentityFromSession(tc.nickname, "", "")
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tc.nickname)
			}
		})
	}
}

func TestAudienceIdentityFromSessionEmailHandling(t *testing.T) {
	t.Run("blank email becomes slug@domain", func(t *testing.T) {
		id, err := audienceIdentityFromSession("Simon Koudijs", "", "demo.example.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id.Email != "simon-koudijs@demo.example.com" {
			t.Fatalf("Email: got %q want %q", id.Email, "simon-koudijs@demo.example.com")
		}
	})

	t.Run("blank email + blank domain falls back to default", func(t *testing.T) {
		id, err := audienceIdentityFromSession("Simon Koudijs", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "simon-koudijs@" + demoDefaultEmailDomain
		if id.Email != want {
			t.Fatalf("Email: got %q want %q", id.Email, want)
		}
	})

	t.Run("valid user email is used as-is", func(t *testing.T) {
		id, err := audienceIdentityFromSession("Simon Koudijs", "simon@example.com", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id.Email != "simon@example.com" {
			t.Fatalf("Email: got %q want %q", id.Email, "simon@example.com")
		}
	})

	t.Run("invalid email is rejected", func(t *testing.T) {
		invalid := []string{
			"not-an-email",
			"missing-domain@",
			"@missing-local",
			"spaces in@email.com",
			"Foo <foo@example.com>", // name+address form not accepted
			"foo@example.com\nBcc: evil@example.com",
			"foo<@example.com",
		}
		for _, raw := range invalid {
			if _, err := audienceIdentityFromSession("Simon", raw, ""); err == nil {
				t.Fatalf("expected error for %q, got nil", raw)
			}
		}
	})

	t.Run("email whitespace is trimmed", func(t *testing.T) {
		id, err := audienceIdentityFromSession("Simon", "  simon@example.com  ", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id.Email != "simon@example.com" {
			t.Fatalf("Email: got %q want %q", id.Email, "simon@example.com")
		}
	})
}

func TestAudienceIdentityFromSessionSlugLengthCap(t *testing.T) {
	// A long display name should produce a slug capped at demoSlugMaxLen with
	// no trailing dash after truncation.
	longName := strings.Repeat("a", demoDisplayNameMaxLen)
	id, err := audienceIdentityFromSession(longName, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	slug := strings.TrimPrefix(id.Username, demoIdentityUsernamePrefix)
	if len(slug) > demoSlugMaxLen {
		t.Fatalf("slug length %d exceeds cap %d", len(slug), demoSlugMaxLen)
	}
	if strings.HasSuffix(slug, "-") {
		t.Fatalf("slug ends with dash after truncation: %q", slug)
	}
}
