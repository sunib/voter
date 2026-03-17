package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
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

type quizSessionCacheEntry struct {
	session   quizSessionSpec
	expiresAt time.Time
}

type quizSessionCache struct {
	mu      sync.Mutex
	entries map[string]quizSessionCacheEntry
	group   singleflight.Group
}

func newQuizSessionCache() *quizSessionCache {
	return &quizSessionCache{
		entries: map[string]quizSessionCacheEntry{},
	}
}

func (c *quizSessionCache) get(key string, now time.Time) (quizSessionSpec, bool) {
	var zero quizSessionSpec
	if c == nil || strings.TrimSpace(key) == "" {
		return zero, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return zero, false
	}
	if entry.expiresAt.IsZero() || now.Before(entry.expiresAt) {
		return entry.session, true
	}

	delete(c.entries, key)
	return zero, false
}

func (c *quizSessionCache) set(key string, session quizSessionSpec, expiresAt time.Time) {
	if c == nil || strings.TrimSpace(key) == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = quizSessionCacheEntry{session: session, expiresAt: expiresAt}
}

// kubeHandler is the interface used by HTTP handlers — the operations
// handlers need from the Kubernetes client. kubeClient satisfies it.
type kubeHandler interface {
	requestToken(ctx context.Context, namespace, serviceAccount string, audiences []string, ttlSeconds int64) (string, time.Time, error)
	getQuizSession(ctx context.Context, ref sessionRef) (quizSessionSpec, error)
	reviewToken(ctx context.Context, token string) (authenticated bool, username string, err error)
}

type kubeClient struct {
	clientset    kubernetes.Interface
	dynamic      dynamic.Interface
	defaultNS    string
	sessionCache *quizSessionCache
}

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
		clientset:    clientset,
		dynamic:      dynamicClient,
		defaultNS:    defaultNS,
		sessionCache: newQuizSessionCache(),
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

func (c kubeClient) reviewToken(ctx context.Context, token string) (bool, string, error) {
	tr := &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{
			Token: token,
		},
	}
	result, err := c.clientset.AuthenticationV1().TokenReviews().Create(ctx, tr, metav1.CreateOptions{})
	if err != nil {
		return false, "", fmt.Errorf("token review failed: %w", err)
	}
	return result.Status.Authenticated, result.Status.User.Username, nil
}

func (c kubeClient) getQuizSession(ctx context.Context, ref sessionRef) (quizSessionSpec, error) {
	var out quizSessionSpec
	key := sessionKey(ref)

	return getOrFetchQuizSession(c.sessionCache, key, time.Now(), sessionCacheTTL, ctx, func(fetchCtx context.Context) (quizSessionSpec, error) {
		gvr := schema.GroupVersionResource{
			Group:    "examples.configbutler.ai",
			Version:  "v1alpha1",
			Resource: "quizsessions",
		}

		obj, err := c.dynamic.Resource(gvr).Namespace(ref.namespace).Get(fetchCtx, ref.name, metav1.GetOptions{})
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
	})
}

func getOrFetchQuizSession(cache *quizSessionCache, key string, now time.Time, ttl time.Duration, ctx context.Context, fetch func(context.Context) (quizSessionSpec, error)) (quizSessionSpec, error) {
	var zero quizSessionSpec

	if cache == nil || strings.TrimSpace(key) == "" {
		return fetch(ctx)
	}

	if session, ok := cache.get(key, now); ok {
		return session, nil
	}

	resultCh := cache.group.DoChan(key, func() (any, error) {
		if session, ok := cache.get(key, time.Now()); ok {
			return session, nil
		}

		session, err := fetch(ctx)
		if err != nil {
			return zero, err
		}
		cache.set(key, session, time.Now().Add(ttl))
		return session, nil
	})

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return zero, result.Err
		}
		session, ok := result.Val.(quizSessionSpec)
		if !ok {
			return zero, errors.New("quiz session cache returned unexpected type")
		}
		return session, nil
	}
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
