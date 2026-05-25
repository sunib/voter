package main

import (
	"errors"
	"net/mail"
	"regexp"
	"strings"
)

// audienceIdentity is the derived identity used to attribute Kubernetes writes
// (impersonation user + group) and ConfigButler Git authoring (display name +
// email via impersonation extras).
//
// Username derives from the stable client-generated ID, never from the display
// name — so editing the display name mid-session does not change the K8s
// identity, and Unicode display names cannot produce odd-looking usernames.
type audienceIdentity struct {
	Username    string // e.g. "demo:521541"
	DisplayName string // e.g. "Anonymous 521541" (or whatever the user typed)
	Email       string // e.g. "521541@demo.configbutler.ai" (or the user's own)
}

const (
	demoIdentityUsernamePrefix = "demo:"
	demoDisplayNameMaxLen      = 64

	// gitops-reverser reads these impersonation extras off the audit event to
	// pick the Git commit author name + email.
	configButlerDisplayNameExtraKey = "configbutler.ai/claims/display-name"
	configButlerEmailExtraKey       = "configbutler.ai/claims/email"
)

// stableIDPattern: 6 ASCII digits, no leading zero. Matches the frontend
// generator and keeps the K8s username short and grep-friendly.
var stableIDPattern = regexp.MustCompile(`^[1-9][0-9]{5}$`)

// audienceIdentityFromSession is a pure validator — the server never
// synthesizes defaults. The frontend always sends a stableID, displayName,
// and email; anything missing or malformed is rejected.
func audienceIdentityFromSession(stableID, displayName, email string) (audienceIdentity, error) {
	if err := validateStableID(stableID); err != nil {
		return audienceIdentity{}, err
	}
	name, err := normalizeDisplayName(displayName)
	if err != nil {
		return audienceIdentity{}, err
	}
	trimmedEmail := strings.TrimSpace(email)
	if err := validateAuthorEmail(trimmedEmail); err != nil {
		return audienceIdentity{}, err
	}

	return audienceIdentity{
		Username:    demoIdentityUsernamePrefix + stableID,
		DisplayName: name,
		Email:       trimmedEmail,
	}, nil
}

func validateStableID(raw string) error {
	if !stableIDPattern.MatchString(raw) {
		return errors.New("stableId must be 6 digits, leading digit 1-9")
	}
	return nil
}

func normalizeDisplayName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("display name is required")
	}
	if strings.ContainsAny(name, "<>\n\r") {
		return "", errors.New("display name contains characters unsafe for git author lines")
	}
	if len(name) > demoDisplayNameMaxLen {
		return "", errors.New("display name is too long")
	}
	return name, nil
}

// validateAuthorEmail accepts only bare addresses (no "Name <addr>" form) and
// rejects characters that would break a git author signature line.
func validateAuthorEmail(raw string) error {
	if raw == "" {
		return errors.New("email is required")
	}
	if strings.ContainsAny(raw, "<>\n\r") {
		return errors.New("invalid author email")
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil || addr.Name != "" || addr.Address != raw {
		return errors.New("invalid author email")
	}
	return nil
}
