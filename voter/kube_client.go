package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type quizSessionSpec struct {
	Spec struct {
		State string `json:"state"`
		Title string `json:"title"`
	} `json:"spec"`
}

type quizSessionSummary struct {
	Namespace string
	Name      string
	State     string
	Title     string
}

type quizSessionCacheEntry struct {
	session   quizSessionSpec
	expiresAt time.Time
}

type quizSessionCache struct {
	mu      sync.Mutex
	entries map[string]quizSessionCacheEntry
	group   singleflight.Group
}

type kubeClient struct {
	clientset      kubernetes.Interface
	dynamic        dynamic.Interface
	restConfig     *rest.Config
	defaultNS      string
	coffeeName     string
	identityExtras bool
}

// audienceGroupName is the Kubernetes group assigned to impersonated audience
// members. RBAC bindings target this group so any nickname inherits the same
// limited permissions.
const audienceGroupName = "voter-audience"

const (
	kubeTokenPath   = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	kubeCAPath      = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	kubeAPIServer   = "https://kubernetes.default.svc"
	sessionCacheTTL = 5 * time.Second
)

func loadKubeClient(cfg config) (kubeClient, error) {
	var restConfig *rest.Config
	var err error

	if _, tokenErr := os.Stat(kubeTokenPath); tokenErr == nil {
		if _, caErr := os.Stat(kubeCAPath); caErr == nil {
			restConfig = &rest.Config{
				Host: kubeAPIServer,
				TLSClientConfig: rest.TLSClientConfig{
					CAFile: kubeCAPath,
				},
				BearerTokenFile: kubeTokenPath,
			}
		}
	}

	if restConfig == nil {
		// Fall back to kubeconfig
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = os.ExpandEnv("$HOME/.kube/config")
		}
		restConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return kubeClient{}, fmt.Errorf("failed to load kube config: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return kubeClient{}, fmt.Errorf("failed to create clientset: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return kubeClient{}, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	defaultNS, _ := detectNamespace()
	return kubeClient{
		clientset:      clientset,
		dynamic:        dynamicClient,
		restConfig:     restConfig,
		defaultNS:      defaultNS,
		coffeeName:     strings.TrimSpace(cfg.CoffeeConfigName),
		identityExtras: cfg.ConfigButlerIdentityExtrasEnabled,
	}, nil
}

// gitops-reverser serves CommitRequest at v1alpha3 and no longer offers
// v1alpha1 -- api/v1alpha3 is the only API package in the operator. Pinning the
// old version here made every "save now" fail with a no-matches error against a
// current install. Both the typed apiVersion string and the GVR must agree, so
// they share one constant.
const commitRequestAPIVersion = "configbutler.ai/v1alpha3"

func commitRequestGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "configbutler.ai",
		Version:  "v1alpha3",
		Resource: "commitrequests",
	}
}

func coffeeConfigGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "examples.configbutler.ai",
		Version:  "v1alpha1",
		Resource: "coffeeconfigs",
	}
}

func toCoffeeConfig(obj *unstructured.Unstructured) (coffeeConfig, error) {
	var out coffeeConfig
	if obj == nil {
		return out, errors.New("nil coffee config object")
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &out); err != nil {
		return out, fmt.Errorf("failed to decode coffee config: %w", err)
	}
	return out, nil
}

func detectNamespace() (string, error) {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
