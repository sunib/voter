package main

// Request-scoped Kubernetes clients built from a participant's Dex ID token.
//
// This is the heart of the demo. The participant's own token is the credential,
// so the kube-apiserver authenticates them as demo:<dex-subject>, RBAC decides
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
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"strings"

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
