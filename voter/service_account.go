package main

import (
	"fmt"
	"strings"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Voter's own Kubernetes identity: its ServiceAccount in the cluster, or
// STREAM_KUBECONFIG outside one. The tally reconciler is its only user -- the
// one thing here that acts as the deployment rather than on behalf of a
// participant, because it writes a round's status and no participant may.
//
// Live streams used to share this identity's watches, through Voter's own
// krm-stream gateway. krm-foyer serves /stream/v1 now, with shared watches of
// its own, so this is a plain client.

// serviceAccountRESTConfig configures one cluster for Voter's own client and
// for participant clients. Only TLS trust crosses into the participant
// configuration; credentials remain separate.
func serviceAccountRESTConfig(appConfig *config) (*rest.Config, error) {
	var cfg *rest.Config
	var err error
	if appConfig.StreamKubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", appConfig.StreamKubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("service-account credentials (use STREAM_KUBECONFIG outside a cluster): %w", err)
	}
	if cfg.Insecure {
		return nil, fmt.Errorf("the cluster must verify TLS")
	}
	if host := strings.TrimSpace(appConfig.KubernetesAPIServer); host != "" {
		if appConfig.StreamKubeconfig != "" && host != cfg.Host {
			return nil, fmt.Errorf("KUBERNETES_API_SERVER must match STREAM_KUBECONFIG server")
		}
		cfg.Host = host
	}
	appConfig.KubernetesAPIServer = cfg.Host
	appConfig.participantTLS = &rest.TLSClientConfig{CAFile: cfg.CAFile, CAData: append([]byte(nil), cfg.CAData...), ServerName: cfg.ServerName}
	return cfg, nil
}

func newServiceAccountClient(appConfig *config) (dynamic.Interface, error) {
	cfg, err := serviceAccountRESTConfig(appConfig)
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(cfg)
}
