package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func localStreamConfig(t *testing.T, cluster *clientcmdapi.Cluster, auth *clientcmdapi.AuthInfo) config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stream-kubeconfig")
	err := clientcmd.WriteToFile(clientcmdapi.Config{CurrentContext: "local", Clusters: map[string]*clientcmdapi.Cluster{"local": cluster}, AuthInfos: map[string]*clientcmdapi.AuthInfo{"shared": auth}, Contexts: map[string]*clientcmdapi.Context{"local": {Cluster: "local", AuthInfo: "shared"}}}, path)
	if err != nil {
		t.Fatal(err)
	}
	return config{StreamKubeconfig: path}
}

func TestExplicitLocalStreamCredentialsKeepParticipantClientsSeparate(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	cert, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	cfg := localStreamConfig(t, &clientcmdapi.Cluster{Server: server.URL, CertificateAuthorityData: ca, TLSServerName: "fixture"}, &clientcmdapi.AuthInfo{Token: "shared-token", ClientCertificateData: []byte("shared-cert"), ClientKeyData: []byte("shared-key"), Impersonate: "shared-user"})
	shared, err := streamRESTConfig(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	participant, err := participantRESTConfig(cfg, "participant-token")
	if err != nil {
		t.Fatal(err)
	}
	if shared.BearerToken != "shared-token" || participant.BearerToken != "participant-token" {
		t.Fatal("credential selection changed")
	}
	if participant.Host != server.URL || participant.ServerName != "fixture" || string(participant.CAData) != string(ca) {
		t.Fatal("participant and shared cluster trust differ")
	}
	if participant.BearerTokenFile != "" || len(participant.CertData) > 0 || len(participant.KeyData) > 0 || participant.Impersonate.UserName != "" || participant.ExecProvider != nil || participant.AuthProvider != nil || participant.WrapTransport != nil {
		t.Fatal("shared credentials leaked into participant client")
	}
	shared.CAData[0] = 'X'
	if participant.CAData[0] == 'X' {
		t.Fatal("participant trust aliases mutable shared configuration")
	}
}

func TestLocalStreamRuntimeStartsWithoutInClusterCredentials(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	cfg := localStreamConfig(t, &clientcmdapi.Cluster{Server: "https://localhost:6443"}, &clientcmdapi.AuthInfo{Token: "local-shared-token"})
	streams, err := newStreamRuntime(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if streams.backend == nil || cfg.KubernetesAPIServer != "https://localhost:6443" {
		t.Fatal("local runtime not configured")
	}
	missing := config{}
	if _, err = streamRESTConfig(&missing); err == nil {
		t.Fatal("missing explicit credentials silently fell back")
	}
}

func TestLocalStreamRejectsMismatchedClusterAndInsecureTLS(t *testing.T) {
	cfg := localStreamConfig(t, &clientcmdapi.Cluster{Server: "https://localhost:6443"}, &clientcmdapi.AuthInfo{Token: "shared"})
	cfg.KubernetesAPIServer = "https://different.invalid"
	if _, err := streamRESTConfig(&cfg); err == nil {
		t.Fatal("different authorization and data clusters accepted")
	}
	cfg = localStreamConfig(t, &clientcmdapi.Cluster{Server: "https://localhost:6443", InsecureSkipTLSVerify: true}, &clientcmdapi.AuthInfo{Token: "shared"})
	if _, err := streamRESTConfig(&cfg); err == nil {
		t.Fatal("insecure TLS accepted")
	}
}

func TestMetricsAreOnlyServedOnTheDedicatedHandler(t *testing.T) {
	public := http.NewServeMux()
	registerHandlers(public, handlerDeps{})
	private := metricsHandler(&streamMetrics{})
	for _, tc := range []struct {
		handler http.Handler
		path    string
		status  int
	}{{public, "/metrics", 404}, {private, "/metrics", 200}, {private, "/auth/session", 404}} {
		rec := httptest.NewRecorder()
		tc.handler.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
		if rec.Code != tc.status {
			t.Fatalf("%s returned %d, want %d", tc.path, rec.Code, tc.status)
		}
	}
}

func TestStreamIdentityResolutionIsSafeAcrossConcurrentCallers(t *testing.T) {
	f := newSharedStreamFixture(t)
	principal := &streamPrincipal{session: participantSession{IDToken: "concurrent"}}
	deps := handlerDeps{cfg: f.cfg}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := principal.resolveSubject(t.Context(), deps); err != nil {
				t.Error(err)
				return
			}
			subject, err := principal.kubernetesSubject()
			if err != nil || subject.User != "resolved:concurrent" {
				t.Errorf("incorrect subject: %v", err)
			}
		})
	}
	wg.Wait()
	if f.identities.Load() != 1 {
		t.Fatalf("resolved identity %d times", f.identities.Load())
	}
}

type failingFlushWriter struct{ *httptest.ResponseRecorder }

func (w failingFlushWriter) SetWriteDeadline(_ time.Time) error { return nil }
func (w failingFlushWriter) FlushError() error                  { return errors.New("flush failed") }

func TestStreamFlushFailureCancelsWithoutWaitingForAnotherWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writer := streamResponseWriter{ResponseWriter: failingFlushWriter{httptest.NewRecorder()}, cancel: cancel}
	writer.Flush()
	if ctx.Err() == nil {
		t.Fatal("flush failure did not cancel the subscription")
	}
}
