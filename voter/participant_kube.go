package main

// Request-scoped Kubernetes clients built from a participant's Dex ID token.
//
// This is the heart of the demo. The participant's own token is the credential,
// so the kube-apiserver derives their connector-prefixed identity, RBAC decides
// what they may do, and the audit event names THEM -- which is what lets
// gitops-reverser author a Git commit in their name.
//
// The rules this file exists to enforce:
//
//   - Credentials come from the request and nothing else. No ServiceAccount
//     token file, no client certificate, no exec plugin, no auth provider, no
//     impersonation headers. If the token is rejected, the request fails; it
//     never retries as the ServiceAccount.
//   - The destination API server is fixed by deployment configuration. A caller
//     cannot point this at another cluster.
//   - Nothing is cached across requests, so one participant's credential can
//     never be reused for another's request.

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// participantRESTConfig builds a rest.Config carrying ONLY the participant's
// bearer token.
//
// Note what is absent: BearerTokenFile, CertFile/KeyFile, ExecProvider,
// AuthProvider, Impersonate. Every one of those would be a way for the server's
// own identity to leak into a participant's request. The CA file is TLS trust,
// not a credential, so reusing the ServiceAccount's ca.crt is correct.
func participantRESTConfig(cfg config, idToken string) (*rest.Config, error) {
	token := strings.TrimSpace(idToken)
	if token == "" {
		return nil, errors.New("no participant token")
	}

	host := strings.TrimSpace(cfg.KubernetesAPIServer)
	if host == "" {
		host = kubeAPIServer
	}

	rc := &rest.Config{
		Host:        host,
		BearerToken: token,
		TLSClientConfig: rest.TLSClientConfig{
			CAFile: kubeCAPath,
		},
		QPS:   20,
		Burst: 40,
	}

	// If the in-cluster CA is unavailable, fall back to the system trust store
	// rather than to insecure TLS. Never disable verification.
	if _, err := os.Stat(kubeCAPath); err != nil {
		rc.CAFile = ""
	}

	if cfg.participantTLS != nil {
		// Never copy a shared rest.Config: it can contain tokens, client keys,
		// exec plugins, impersonation or a credential-bearing transport wrapper.
		rc.TLSClientConfig = *cfg.participantTLS
		rc.CAData = append([]byte(nil), cfg.participantTLS.CAData...)
	}
	return rc, nil
}

// participantClients is a per-request pair of Kubernetes clients. Build it,
// use it, discard it.
type participantClients struct {
	typed   kubernetes.Interface
	dynamic dynamic.Interface
}

func newParticipantClients(cfg config, idToken string) (participantClients, error) {
	rc, err := participantRESTConfig(cfg, idToken)
	if err != nil {
		return participantClients{}, err
	}
	typed, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return participantClients{}, fmt.Errorf("participant client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		return participantClients{}, fmt.Errorf("participant dynamic client: %w", err)
	}
	return participantClients{typed: typed, dynamic: dyn}, nil
}

// subtleCompare is a constant-time string comparison, used for CSRF tokens so
// the check does not leak the expected value through timing.
func subtleCompare(a, b string) int {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b))
}

// kubernetesUsername asks the API server who it thinks this token belongs to.
//
// The app used to build this string itself -- "demo:" + the Dex subject -- which
// was a guess at the apiserver's claimMappings, and it silently became wrong the
// moment a second connector existed: a LinkedIn login was reported as
// demo:<sub> when Kubernetes actually saw linkedin:<email>.
//
// SelfSubjectReview returns the identity the apiserver ACTUALLY derived, so the
// name shown to the audience is the one that appears in the audit log. Every
// authenticated user may call it (system:basic-user), including a participant
// whose only other permission is to patch one CoffeeConfig.
func kubernetesUsername(ctx context.Context, cfg config, idToken string) (string, []string, error) {
	clients, err := newParticipantClients(cfg, idToken)
	if err != nil {
		return "", nil, err
	}
	review, err := clients.typed.AuthenticationV1().SelfSubjectReviews().Create(
		ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("self subject review: %w", err)
	}
	return review.Status.UserInfo.Username, review.Status.UserInfo.Groups, nil
}
