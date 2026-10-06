package main

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func localServiceAccountConfig(t *testing.T, cluster *clientcmdapi.Cluster, auth *clientcmdapi.AuthInfo) config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "service-account-kubeconfig")
	err := clientcmd.WriteToFile(clientcmdapi.Config{CurrentContext: "local", Clusters: map[string]*clientcmdapi.Cluster{"local": cluster}, AuthInfos: map[string]*clientcmdapi.AuthInfo{"shared": auth}, Contexts: map[string]*clientcmdapi.Context{"local": {Cluster: "local", AuthInfo: "shared"}}}, path)
	if err != nil {
		t.Fatal(err)
	}
	return config{StreamKubeconfig: path}
}

// An explicit kubeconfig is Voter's own identity outside a cluster: its token
// and its cluster trust are what the client uses.
func TestExplicitLocalCredentialsAreVotersOwn(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	cert, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	cfg := localServiceAccountConfig(t, &clientcmdapi.Cluster{Server: server.URL, CertificateAuthorityData: ca, TLSServerName: "fixture"}, &clientcmdapi.AuthInfo{Token: "voter-token"})
	rc, err := serviceAccountRESTConfig(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rc.BearerToken != "voter-token" || rc.Host != server.URL || rc.ServerName != "fixture" || string(rc.CAData) != string(ca) {
		t.Fatalf("the kubeconfig's credential and trust were not used: %+v", rc)
	}
}

func TestLocalServiceAccountClientStartsWithoutInClusterCredentials(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	cfg := localServiceAccountConfig(t, &clientcmdapi.Cluster{Server: "https://localhost:6443"}, &clientcmdapi.AuthInfo{Token: "local-shared-token"})
	client, err := newServiceAccountClient(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if client == nil || cfg.KubernetesAPIServer != "https://localhost:6443" {
		t.Fatal("local runtime not configured")
	}
	missing := config{}
	if _, err = serviceAccountRESTConfig(&missing); err == nil {
		t.Fatal("missing explicit credentials silently fell back")
	}
}

func TestLocalCredentialsRejectMismatchedClusterAndInsecureTLS(t *testing.T) {
	cfg := localServiceAccountConfig(t, &clientcmdapi.Cluster{Server: "https://localhost:6443"}, &clientcmdapi.AuthInfo{Token: "shared"})
	cfg.KubernetesAPIServer = "https://different.invalid"
	if _, err := serviceAccountRESTConfig(&cfg); err == nil {
		t.Fatal("different authorization and data clusters accepted")
	}
	cfg = localServiceAccountConfig(t, &clientcmdapi.Cluster{Server: "https://localhost:6443", InsecureSkipTLSVerify: true}, &clientcmdapi.AuthInfo{Token: "shared"})
	if _, err := serviceAccountRESTConfig(&cfg); err == nil {
		t.Fatal("insecure TLS accepted")
	}
}
