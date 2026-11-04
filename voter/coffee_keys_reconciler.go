package main

// The controller that says whether the menu's keys are set.
//
// The one person who may replace a key holds `patch` on its Secret and never
// `get`, so the menu page cannot find out for itself whether a key is there, and
// the people who may not replace it certainly cannot. This writes the answer on
// the CoffeeConfig's status.keys, which every reader of the menu already streams,
// so the page shows a key as set the moment it is.
//
// One bit, and on purpose: nothing else derived from a key is safe to show, since
// people type short words and a short word is found from its hash, its length or
// its last characters in seconds (docs/coffee-api-keys-design.md). It runs as the
// ServiceAccount, may list and watch those two Secrets by name, and writes ONLY
// coffeeconfigs/status. withoutKeyValue drops the value before anything is cached,
// so what this process holds on to is the bit too. A status write moves no
// generation and gitops-reverser strips status before it writes, so it is no
// change to the menu, and it happens only when a key goes from empty to set or
// back: a new value for a key already set writes nothing here.

import (
	"context"
	"encoding/json"
	"log"
	"reflect"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

var (
	coffeeConfigResource = schema.GroupVersionResource{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "coffeeconfigs"}
	secretResource       = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
)

// coffeeKeySecrets is each key's place in status.keys and the Secret it is in.
// The CRD allows these names and no others.
var coffeeKeySecrets = map[string]string{"mail": "coffee-mail", "payments": "coffee-payments"}

// coffeeKeysResync catches what no Secret event announces: the CoffeeConfig
// being created again with no status, after a namespace rebuild.
const coffeeKeysResync = time.Minute

type coffeeKeysReconciler struct {
	dynamic   dynamic.Interface
	namespace string
	config    string
	secrets   map[string]cache.SharedIndexInformer
	changed   chan struct{}
}

// newCoffeeKeysReconciler watches the two Secrets in namespace, one informer each
// because RBAC only allows a list or watch by name when it asks for one name.
func newCoffeeKeysReconciler(client dynamic.Interface, namespace, config string) *coffeeKeysReconciler {
	r := &coffeeKeysReconciler{
		dynamic:   client,
		namespace: namespace,
		config:    config,
		secrets:   map[string]cache.SharedIndexInformer{},
		changed:   make(chan struct{}, 1),
	}
	notify := func(any) {
		select {
		case r.changed <- struct{}{}:
		default:
		}
	}
	for section, name := range coffeeKeySecrets {
		byName := func(options *metav1.ListOptions) { options.FieldSelector = "metadata.name=" + name }
		informer := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, namespace, byName).
			ForResource(secretResource).Informer()
		// As in the seat controller, only an informer already started refuses
		// either of these, and this one has not been.
		if err := informer.SetTransform(withoutKeyValue); err != nil {
			log.Printf("coffee-keys: transform: %v", err)
		}
		if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    notify,
			UpdateFunc: func(_, updated any) { notify(updated) },
			DeleteFunc: notify,
		}); err != nil {
			log.Printf("coffee-keys: event handler: %v", err)
		}
		r.secrets[section] = informer
	}
	return r
}

// withoutKeyValue is all of a Secret this process keeps: its identity and whether
// it holds a key. Base64 of a value is empty only for an empty value.
//
// client-go may run this twice over one object: a watch-list (API servers with
// WatchList on, not the fixture's) transforms into a temporary store and then
// again into the informer's. The second pass finds the bit, not the value, and
// must leave it: reading it as no value would call every set key not set.
func withoutKeyValue(obj any) (any, error) {
	secret, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return obj, nil
	}
	if _, done := secret.Object["set"]; done {
		return secret, nil
	}
	value, _, _ := unstructured.NestedString(secret.Object, "data", "apiKey")
	return &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{
			"name":            secret.GetName(),
			"namespace":       secret.GetNamespace(),
			"resourceVersion": secret.GetResourceVersion(),
		},
		"set": value != "",
	}}, nil
}

// Run blocks until ctx is done. One replica, as for the tally.
func (r *coffeeKeysReconciler) Run(ctx context.Context) {
	synced := make([]cache.InformerSynced, 0, len(r.secrets))
	for _, informer := range r.secrets {
		go informer.Run(ctx.Done())
		synced = append(synced, informer.HasSynced)
	}
	if !cache.WaitForCacheSync(ctx.Done(), synced...) {
		return
	}
	ticker := time.NewTicker(coffeeKeysResync)
	defer ticker.Stop()
	for {
		if err := r.reconcile(ctx); err != nil {
			log.Printf("coffee-keys: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-r.changed:
		case <-ticker.C:
		}
	}
}

// reconcile writes status.keys if it says anything else than the two caches.
func (r *coffeeKeysReconciler) reconcile(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client := r.dynamic.Resource(coffeeConfigResource).Namespace(r.namespace)
	config, err := client.Get(ctx, r.config, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	want := r.keyStatus()
	if current, _, _ := unstructured.NestedMap(config.Object, "status", "keys"); reflect.DeepEqual(current, want) {
		return nil
	}
	patch, err := json.Marshal(map[string]any{"status": map[string]any{"keys": want}})
	if err != nil {
		return err
	}
	_, err = client.Patch(ctx, r.config, types.MergePatchType, patch, metav1.PatchOptions{}, "status")
	return err
}

// keyStatus is status.keys. A Secret that is not there holds no key.
func (r *coffeeKeysReconciler) keyStatus() map[string]any {
	keys := map[string]any{}
	for section, informer := range r.secrets {
		set := false
		for _, obj := range informer.GetStore().List() {
			if secret, ok := obj.(*unstructured.Unstructured); ok {
				set, _, _ = unstructured.NestedBool(secret.Object, "set")
			}
		}
		keys[section] = map[string]any{"set": set}
	}
	return keys
}
