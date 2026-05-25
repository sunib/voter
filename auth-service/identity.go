package main

import (
	"errors"
	"net/mail"
	"strings"

	"github.com/gosimple/slug"
)

// audienceIdentity is the derived identity used to attribute Kubernetes writes
// (impersonation user + group) and ConfigButler Git authoring (display name +
// email via impersonation extras). For user-initiated writes Username must be
// non-empty — an empty Username is a hard error, never a "skip impersonation"
// signal, so participant input can never fall through to the auth-service SA.
type audienceIdentity struct {
	Username    string // e.g. "demo:simon-koudijs"
	DisplayName string // e.g. "Simon Koudijs"
	Email       string // user-provided or "<slug>@<emailDomain>"
}

const (
	demoIdentityUsernamePrefix = "demo:"
	demoSlugMaxLen             = 40
	demoDisplayNameMaxLen      = 64
	demoDefaultEmailDomain     = "demo.configbutler.ai"

	// gitops-reverser reads these impersonation extras off the audit event to
	// pick the Git commit author name + email.
	configButlerDisplayNameExtraKey = "configbutler.ai/claims/display-name"
	configButlerEmailExtraKey       = "configbutler.ai/claims/email"
)

// audienceIdentityFromSession derives the impersonation identity for a save
// request. The synthetic "demo:<slug>" username keeps participant-controlled
// input out of any built-in or directly-bound Kubernetes user namespace.
func audienceIdentityFromSession(nickname, optionalEmail, emailDomain string) (audienceIdentity, error) {
	displayName, err := normalizeDisplayName(nickname)
	if err != nil {
		return audienceIdentity{}, err
	}

	s := slug.MakeLang(displayName, "en")
	if len(s) > demoSlugMaxLen {
		s = strings.TrimRight(s[:demoSlugMaxLen], "-")
	}
	if s == "" {
		return audienceIdentity{}, errors.New("display name cannot be converted to a demo identity")
	}

	domain := strings.TrimSpace(emailDomain)
	if domain == "" {
		domain = demoDefaultEmailDomain
	}
	email := strings.TrimSpace(optionalEmail)
	if email == "" {
		email = s + "@" + domain
	} else if err := validateAuthorEmail(email); err != nil {
		return audienceIdentity{}, err
	}

	return audienceIdentity{
		Username:    demoIdentityUsernamePrefix + s,
		DisplayName: displayName,
		Email:       email,
	}, nil
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
	if strings.ContainsAny(raw, "<>\n\r") {
		return errors.New("invalid author email")
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil || addr.Name != "" || addr.Address != raw {
		return errors.New("invalid author email")
	}
	return nil
}
