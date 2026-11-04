package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func coffeeKeysFixture(status map[string]any) (*dynamicfake.FakeDynamicClient, *unstructured.Unstructured) {
	config := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "examples.configbutler.ai/v1alpha1", "kind": "CoffeeConfig",
		"metadata": map[string]any{"name": "demo-coffee", "namespace": "voter"},
	}}
	if status != nil {
		config.Object["status"] = status
	}
	gvrToList := map[schema.GroupVersionResource]string{
		coffeeConfigResource: "CoffeeConfigList",
		secretResource:       "SecretList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToList, config), config
}

func storeSecret(t *testing.T, r *coffeeKeysReconciler, section, name, value string) {
	t.Helper()
	kept, err := withoutKeyValue(&unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": name, "namespace": "voter"},
		"data":     map[string]any{"apiKey": value},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.secrets[section].GetStore().Add(kept); err != nil {
		t.Fatal(err)
	}
}

func statusPatches(client *dynamicfake.FakeDynamicClient) []k8stesting.PatchAction {
	var patches []k8stesting.PatchAction
	for _, action := range client.Actions() {
		if patch, ok := action.(k8stesting.PatchAction); ok && action.GetVerb() == "patch" {
			patches = append(patches, patch)
		}
	}
	return patches
}

func TestCoffeeKeysWritesWhetherEachKeyIsSetAndNothingElse(t *testing.T) {
	client, _ := coffeeKeysFixture(nil)
	r := newCoffeeKeysReconciler(client, "voter", "demo-coffee")
	storeSecret(t, r, "mail", "coffee-mail", "dGFhcnQ=")
	storeSecret(t, r, "payments", "coffee-payments", "")

	if err := r.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	patches := statusPatches(client)
	if len(patches) != 1 {
		t.Fatalf("want one patch, got %d", len(patches))
	}
	if patches[0].GetSubresource() != "status" {
		t.Fatalf("patched %q, not status", patches[0].GetSubresource())
	}
	if strings.Contains(string(patches[0].GetPatch()), "dGFhcnQ=") {
		t.Fatalf("the key's value is in the patch: %s", patches[0].GetPatch())
	}
	var got any
	if err := json.Unmarshal(patches[0].GetPatch(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"status": map[string]any{"keys": map[string]any{
		"mail":     map[string]any{"set": true},
		"payments": map[string]any{"set": false},
	}}}
	if !jsonEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A new value for a key already set says nothing new, and must not move the
// menu's resourceVersion under somebody who is editing it.
func TestCoffeeKeysWritesNothingWhenTheStatusIsRight(t *testing.T) {
	client, _ := coffeeKeysFixture(map[string]any{"keys": map[string]any{
		"mail":     map[string]any{"set": true},
		"payments": map[string]any{"set": false},
	}})
	r := newCoffeeKeysReconciler(client, "voter", "demo-coffee")
	storeSecret(t, r, "mail", "coffee-mail", "bmV3")

	if err := r.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if patches := statusPatches(client); len(patches) != 0 {
		t.Fatalf("want no patch, got %s", patches[0].GetPatch())
	}
}

func TestCoffeeKeysWithoutTheMenuDoesNothing(t *testing.T) {
	client, _ := coffeeKeysFixture(nil)
	r := newCoffeeKeysReconciler(client, "voter", "somewhere-else")
	if err := r.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if patches := statusPatches(client); len(patches) != 0 {
		t.Fatalf("want no patch, got %d", len(patches))
	}
}

func TestWithoutKeyValueKeepsNoValue(t *testing.T) {
	kept, err := withoutKeyValue(&unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "coffee-mail", "namespace": "voter", "resourceVersion": "7"},
		"data":     map[string]any{"apiKey": "dGFhcnQ="},
	}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(kept.(*unstructured.Unstructured).Object)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "dGFhcnQ=") || strings.Contains(string(encoded), `"data"`) {
		t.Fatalf("the value is still cached: %s", encoded)
	}
	if kept.(*unstructured.Unstructured).GetResourceVersion() != "7" {
		t.Fatalf("an informer needs the resourceVersion: %s", encoded)
	}
}

func jsonEqual(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

// client-go may run a transform over an object twice, and the second time the
// value is already gone: that must not read as a key that is not set.
func TestWithoutKeyValueCanRunTwice(t *testing.T) {
	once, err := withoutKeyValue(&unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "coffee-mail", "namespace": "voter"},
		"data":     map[string]any{"apiKey": "dGFhcnQ="},
	}})
	if err != nil {
		t.Fatal(err)
	}
	twice, err := withoutKeyValue(once)
	if err != nil {
		t.Fatal(err)
	}
	if set, _, _ := unstructured.NestedBool(twice.(*unstructured.Unstructured).Object, "set"); !set {
		t.Fatalf("a set key reads as not set after a second pass: %v", twice)
	}
}

// A key that is already set when Voter starts arrives in the informer's first
// list, not in a watch event. The fake ignores field selectors, so only the mail
// informer's answer means anything here.
func TestCoffeeKeysSeesAKeyAlreadySetAtStart(t *testing.T) {
	client, _ := coffeeKeysFixture(nil)
	secret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "coffee-mail", "namespace": "voter"},
		"data":     map[string]any{"apiKey": "dGFhcnQ="},
	}}
	if _, err := client.Resource(secretResource).Namespace("voter").Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go newCoffeeKeysReconciler(client, "voter", "demo-coffee").Run(ctx)

	for ctx.Err() == nil {
		for _, patch := range statusPatches(client) {
			var got struct {
				Status struct {
					Keys map[string]struct {
						Set bool `json:"set"`
					} `json:"keys"`
				} `json:"status"`
			}
			if err := json.Unmarshal(patch.GetPatch(), &got); err != nil {
				t.Fatal(err)
			}
			if !got.Status.Keys["mail"].Set {
				t.Fatalf("the mail key is set, and the status says not: %s", patch.GetPatch())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no status was written")
}
