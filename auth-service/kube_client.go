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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8swatch "k8s.io/apimachinery/pkg/watch"
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
	getCoffeeConfig(ctx context.Context) (coffeeConfig, error)
	patchCoffeeConfig(ctx context.Context, patch []byte, identity audienceIdentity) (coffeeConfig, error)
	watchCoffeeConfig(ctx context.Context) (coffeeConfig, k8swatch.Interface, error)
	createCommitRequest(ctx context.Context, params createCommitRequestParams) (string, error)
}

// createCommitRequestParams is the input shape for createCommitRequest. The
// Identity drives Kubernetes impersonation so the CommitRequest's audit
// identity matches the CoffeeConfig patch that preceded it — gitops-reverser
// binds the finalize signal to the open commit window by (effective user,
// GitTarget).
type createCommitRequestParams struct {
	Identity      audienceIdentity
	GitTargetName string
	Namespace     string
	Message       string
}

type kubeClient struct {
	clientset      kubernetes.Interface
	dynamic        dynamic.Interface
	restConfig     *rest.Config
	defaultNS      string
	sessionCache   *quizSessionCache
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
		sessionCache:   newQuizSessionCache(),
		coffeeName:     strings.TrimSpace(cfg.CoffeeConfigName),
		identityExtras: cfg.ConfigButlerIdentityExtrasEnabled,
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

func (c kubeClient) patchQuizSessionJoinCode(ctx context.Context, ref sessionRef, joinCode string) error {
	gvr := schema.GroupVersionResource{
		Group:    "examples.configbutler.ai",
		Version:  "v1alpha1",
		Resource: "quizsessions",
	}
	patch := []byte(fmt.Sprintf(`{"status":{"joinCode":%q}}`, joinCode))
	_, err := c.dynamic.Resource(gvr).Namespace(ref.namespace).Patch(
		ctx, ref.name, types.MergePatchType, patch, metav1.PatchOptions{}, "status",
	)
	if err != nil {
		return fmt.Errorf("failed to patch quiz session status: %w", err)
	}
	return nil
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

func (c kubeClient) getCoffeeConfig(ctx context.Context) (coffeeConfig, error) {
	if c.defaultNS == "" || c.coffeeName == "" {
		return coffeeConfig{}, errors.New("coffee config name not configured in runtime namespace")
	}

	obj, err := c.dynamic.Resource(coffeeConfigGVR()).Namespace(c.defaultNS).Get(ctx, c.coffeeName, metav1.GetOptions{})
	if err != nil {
		return coffeeConfig{}, fmt.Errorf("failed to get coffee config: %w", err)
	}
	return toCoffeeConfig(obj)
}

// patchCoffeeConfig writes the merge patch as the impersonated audience
// identity. A missing/invalid identity is a hard error — never a silent
// fall-through to the auth-service ServiceAccount, since the SA has its own
// (broader) RBAC and that would bypass the voter-audience authorization path.
func (c kubeClient) patchCoffeeConfig(ctx context.Context, patch []byte, identity audienceIdentity) (coffeeConfig, error) {
	if c.defaultNS == "" || c.coffeeName == "" {
		return coffeeConfig{}, errors.New("coffee config name not configured in runtime namespace")
	}

	client, err := c.impersonatedDynamic(identity)
	if err != nil {
		return coffeeConfig{}, fmt.Errorf("failed to build impersonated client: %w", err)
	}

	obj, err := client.Resource(coffeeConfigGVR()).Namespace(c.defaultNS).Patch(
		ctx,
		c.coffeeName,
		types.MergePatchType,
		patch,
		metav1.PatchOptions{},
	)
	if err != nil {
		return coffeeConfig{}, fmt.Errorf("failed to patch coffee config: %w", err)
	}
	return toCoffeeConfig(obj)
}

// createCommitRequest creates a ConfigButler CommitRequest, which finalizes
// the open commit window for the referenced GitTarget. The request is sent
// under the same impersonated identity as the preceding write, so the audit
// event matches and gitops-reverser can bind the finalize signal correctly.
//
// The trimmed Message is set on spec.message when non-empty; an empty/whitespace
// Message leaves spec.message off the object entirely so ConfigButler falls
// back to its generated grouped-commit message.
func (c kubeClient) createCommitRequest(ctx context.Context, params createCommitRequestParams) (string, error) {
	gitTarget := strings.TrimSpace(params.GitTargetName)
	if gitTarget == "" {
		return "", errors.New("missing git target name")
	}

	ns := strings.TrimSpace(params.Namespace)
	if ns == "" {
		ns = c.defaultNS
	}
	if ns == "" {
		return "", errors.New("missing commit request namespace")
	}

	client, err := c.impersonatedDynamic(params.Identity)
	if err != nil {
		return "", fmt.Errorf("failed to build impersonated client: %w", err)
	}

	spec := map[string]interface{}{
		"gitTargetRef": map[string]interface{}{
			"name": gitTarget,
		},
	}
	if msg := strings.TrimSpace(params.Message); msg != "" {
		spec["message"] = msg
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "configbutler.ai/v1alpha1",
			"kind":       "CommitRequest",
			"metadata": map[string]interface{}{
				"generateName": "coffee-save-",
				"namespace":    ns,
			},
			"spec": spec,
		},
	}

	created, err := client.Resource(commitRequestGVR()).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create commit request: %w", err)
	}
	return created.GetName(), nil
}

func commitRequestGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "configbutler.ai",
		Version:  "v1alpha1",
		Resource: "commitrequests",
	}
}

// impersonatedDynamic returns a dynamic client that submits requests with
// Impersonate-User, Impersonate-Group, and (when enabled) ConfigButler claim
// extras, so that the audit log and gitops-reverser see the audience member
// rather than the auth-service ServiceAccount.
//
// The empty-Username case is a hard error rather than a "skip impersonation"
// signal — see patchCoffeeConfig for the rationale.
func (c kubeClient) impersonatedDynamic(identity audienceIdentity) (dynamic.Interface, error) {
	if c.restConfig == nil {
		return nil, errors.New("rest config unavailable for impersonation")
	}
	if strings.TrimSpace(identity.Username) == "" {
		return nil, errors.New("missing impersonation username")
	}

	cfg := rest.CopyConfig(c.restConfig)
	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: identity.Username,
		Groups:   []string{audienceGroupName},
	}
	if c.identityExtras {
		cfg.Impersonate.Extra = map[string][]string{
			configButlerDisplayNameExtraKey: {identity.DisplayName},
			configButlerEmailExtraKey:       {identity.Email},
		}
	}
	return dynamic.NewForConfig(cfg)
}

func (c kubeClient) watchCoffeeConfig(ctx context.Context) (coffeeConfig, k8swatch.Interface, error) {
	current, err := c.getCoffeeConfig(ctx)
	if err != nil {
		return coffeeConfig{}, nil, err
	}

	watcher, err := c.dynamic.Resource(coffeeConfigGVR()).Namespace(c.defaultNS).Watch(ctx, metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("metadata.name", c.coffeeName).String(),
		ResourceVersion: current.Metadata.ResourceVersion,
	})
	if err != nil {
		return coffeeConfig{}, nil, fmt.Errorf("failed to watch coffee config: %w", err)
	}

	return current, watcher, nil
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
