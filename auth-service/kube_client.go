package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// kubeHandler is the interface used by HTTP handlers — the two operations
// handlers need from the Kubernetes client. kubeClient satisfies it.
type kubeHandler interface {
	requestToken(ctx context.Context, namespace, serviceAccount string, audiences []string, ttlSeconds int64) (string, time.Time, error)
	getQuizSession(ctx context.Context, ref sessionRef) (quizSessionSpec, error)
}

type kubeClient struct {
	clientset kubernetes.Interface
	dynamic   dynamic.Interface
	defaultNS string
}

const (
	kubeTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	kubeCAPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	kubeAPIServer = "https://kubernetes.default.svc"
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
		clientset: clientset,
		dynamic:   dynamicClient,
		defaultNS: defaultNS,
	}, nil
}

func (c kubeClient) requestToken(ctx context.Context, namespace, serviceAccount string, audiences []string, ttlSeconds int64) (string, time.Time, error) {
	if namespace == "" || serviceAccount == "" {
		return "", time.Time{}, errors.New("missing service account namespace/name")
	}

	tokenRequest := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences: audiences,
		},
	}
	if ttlSeconds > 0 {
		tokenRequest.Spec.ExpirationSeconds = &ttlSeconds
	}

	result, err := c.clientset.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, serviceAccount, tokenRequest, metav1.CreateOptions{})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create token: %w", err)
	}

	token := strings.TrimSpace(result.Status.Token)
	if token == "" {
		return "", time.Time{}, errors.New("token request returned empty token")
	}
	return token, result.Status.ExpirationTimestamp.Time, nil
}

func (c kubeClient) getQuizSession(ctx context.Context, ref sessionRef) (quizSessionSpec, error) {
	var out quizSessionSpec

	gvr := schema.GroupVersionResource{
		Group:    "examples.configbutler.ai",
		Version:  "v1alpha1",
		Resource: "quizsessions",
	}

	obj, err := c.dynamic.Resource(gvr).Namespace(ref.namespace).Get(ctx, ref.name, metav1.GetOptions{})
	if err != nil {
		return out, fmt.Errorf("failed to get quiz session: %w", err)
	}

	// Convert the unstructured object to our spec struct
	specData, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return out, fmt.Errorf("failed to convert object: %w", err)
	}

	// Extract the spec
	if spec, ok := specData["spec"].(map[string]interface{}); ok {
		if state, ok := spec["state"].(string); ok {
			out.Spec.State = state
		}
		if title, ok := spec["title"].(string); ok {
			out.Spec.Title = title
		}
	}

	return out, nil
}

func (c kubeClient) listQuizSessions(ctx context.Context) ([]quizSessionSummary, error) {
	gvr := schema.GroupVersionResource{
		Group:    "examples.configbutler.ai",
		Version:  "v1alpha1",
		Resource: "quizsessions",
	}

	list, err := c.dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list quiz sessions: %w", err)
	}

	items := make([]quizSessionSummary, 0, len(list.Items))
	for _, item := range list.Items {
		state := ""
		title := ""
		if spec, ok := item.Object["spec"].(map[string]interface{}); ok {
			if value, ok := spec["state"].(string); ok {
				state = value
			}
			if value, ok := spec["title"].(string); ok {
				title = value
			}
		}
		items = append(items, quizSessionSummary{
			Namespace: item.GetNamespace(),
			Name:      item.GetName(),
			State:     state,
			Title:     title,
		})
	}

	return items, nil
}

func detectNamespace() (string, error) {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
