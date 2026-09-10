package main

import (
	"strings"
	"testing"
)

func TestAudienceIdentityFromSessionHappyPath(t *testing.T) {
	cases := []struct {
		name        string
		stableID    string
		displayName string
		email       string
	}{
		{name: "default-looking values", stableID: "521541", displayName: "Anonymous 521541", email: "521541@demo.configbutler.ai"},
		{name: "user-personalized display name", stableID: "521541", displayName: "Simon Koudijs", email: "521541@demo.configbutler.ai"},
		{name: "user-personalized email", stableID: "521541", displayName: "Simon Koudijs", email: "simon@example.com"},
		{name: "smallest legal stableID", stableID: "100000", displayName: "Anonymous 100000", email: "100000@demo.configbutler.ai"},
		{name: "largest legal stableID", stableID: "999999", displayName: "Anonymous 999999", email: "999999@demo.configbutler.ai"},
		{name: "unicode display name passes through", stableID: "521541", displayName: "佐藤", email: "521541@demo.configbutler.ai"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := audienceIdentityFromSession(tc.stableID, tc.displayName, tc.email)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			wantUser := "demo:" + tc.stableID
			if id.Username != wantUser {
				t.Fatalf("Username: got %q want %q", id.Username, wantUser)
			}
			if id.DisplayName != tc.displayName {
				t.Fatalf("DisplayName: got %q want %q", id.DisplayName, tc.displayName)
			}
			if id.Email != tc.email {
				t.Fatalf("Email: got %q want %q", id.Email, tc.email)
			}
		})
	}
}

// TestAudienceIdentityFromSessionUsernameIsStableIDOnly is the load-bearing
// regression: the K8s username must never derive from the display name. A
// user that edits "Anonymous 521541" → "Simon" stays demo:521541, not
// demo:simon.
func TestAudienceIdentityFromSessionUsernameIsStableIDOnly(t *testing.T) {
	first, err := audienceIdentityFromSession("521541", "Anonymous 521541", "521541@demo.configbutler.ai")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := audienceIdentityFromSession("521541", "Simon Koudijs", "521541@demo.configbutler.ai")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.Username != second.Username {
		t.Fatalf("username changed when display name changed: %q vs %q", first.Username, second.Username)
	}
	if first.Username != "demo:521541" {
		t.Fatalf("Username: got %q want %q", first.Username, "demo:521541")
	}
}

func TestValidateStableIDRejects(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"abc",
		"12345",    // too short
		"1234567",  // too long
		"012345",   // leading zero
		"52154a",   // non-digit
		"52154 ",   // trailing space
		"521541\n", // newline
		"٥٢١٥٤١",   // non-ASCII digits
	}
	for _, raw := range bad {
		if err := validateStableID(raw); err == nil {
			t.Fatalf("expected error for %q, got nil", raw)
		}
	}
}

func TestAudienceIdentityFromSessionRejectsBadDisplayName(t *testing.T) {
	cases := []struct {
		name        string
		displayName string
	}{
		{name: "empty", displayName: ""},
		{name: "whitespace only", displayName: "   "},
		{name: "contains newline", displayName: "Alice\nBob"},
		{name: "contains carriage return", displayName: "Alice\rBob"},
		{name: "contains angle bracket", displayName: "Mallory <evil@example.com>"},
		{name: "too long", displayName: strings.Repeat("a", demoDisplayNameMaxLen+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := audienceIdentityFromSession("521541", tc.displayName, "521541@demo.configbutler.ai")
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tc.displayName)
			}
		})
	}
}

func TestAudienceIdentityFromSessionRejectsBadEmail(t *testing.T) {
	cases := []struct {
		name  string
		email string
	}{
		{name: "empty", email: ""},
		{name: "whitespace only", email: "   "},
		{name: "not an email", email: "not-an-email"},
		{name: "missing domain", email: "foo@"},
		{name: "missing local", email: "@bar"},
		{name: "spaces in local", email: "spaces in@example.com"},
		{name: "name+address form rejected", email: "Foo <foo@example.com>"},
		{name: "newline injection", email: "foo@example.com\nBcc: evil@example.com"},
		{name: "angle bracket in local", email: "foo<@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := audienceIdentityFromSession("521541", "Anonymous 521541", tc.email)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tc.email)
			}
		})
	}
}

func TestAudienceIdentityFromSessionTrimsEmail(t *testing.T) {
	id, err := audienceIdentityFromSession("521541", "Simon", "  simon@example.com  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Email != "simon@example.com" {
		t.Fatalf("Email: got %q want %q", id.Email, "simon@example.com")
	}
}

func TestImpersonateExtraHeaderNameEscapesKubernetesExtraKey(t *testing.T) {
	got := impersonateExtraHeaderName(configButlerDisplayNameExtraKey)
	want := "Impersonate-Extra-configbutler.ai%2Fclaims%2Fdisplay-name"
	if got != want {
		t.Fatalf("header name: got %q want %q", got, want)
	}
	if strings.Contains(got, "/") {
		t.Fatalf("header name must not contain raw slash: %q", got)
	}
}
