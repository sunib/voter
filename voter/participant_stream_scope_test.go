package main

import (
	"testing"

	"github.com/ConfigButler/krm-stream/gateway"
)

// The allowlist is the inner of the two locks: RBAC says what this identity may
// read, and this says what the endpoint will carry at all. It exists so that a
// verb granted to the audience for some other reason -- a Role edited in the
// platform repo, say -- cannot by itself turn into a new stream.
func TestStreamAllowlistRefusesWhatItDoesNotName(t *testing.T) {
	const ns = "voter"
	allow := streamAllowlist{
		{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "coffeeconfigs", Namespace: ns, Name: "demo-coffee"},
		{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "quizsessions", Namespace: ns},
		{Group: "roompass.configbutler.ai", Version: "v1alpha1", Resource: "rooms", Namespace: ns, Name: "demo"},
	}

	scope := func(group, resource, namespace, name string) gateway.Scope {
		return gateway.Scope{Group: group, Version: "v1alpha1", Resource: resource, Namespace: namespace, Name: name}
	}

	for _, tc := range []struct {
		name  string
		scope gateway.Scope
		allow bool
	}{
		{"the configured CoffeeConfig", scope("examples.configbutler.ai", "coffeeconfigs", ns, "demo-coffee"), true},
		{"another CoffeeConfig", scope("examples.configbutler.ai", "coffeeconfigs", ns, "somebody-elses"), false},
		{"every round in the namespace", scope("examples.configbutler.ai", "quizsessions", ns, ""), true},
		{"one round by name", scope("examples.configbutler.ai", "quizsessions", ns, "demo-round-1"), true},
		{"rounds in another namespace", scope("examples.configbutler.ai", "quizsessions", "kube-system", ""), false},
		{"the configured Room", scope("roompass.configbutler.ai", "rooms", ns, "demo"), true},
		// The join code is operator-only credential material, so the endpoint
		// serves exactly one Room and never a collection of them.
		{"another Room", scope("roompass.configbutler.ai", "rooms", ns, "other-room"), false},
		{"every Room", scope("roompass.configbutler.ai", "rooms", ns, ""), false},
		{"participants, which nothing streams", scope("roompass.configbutler.ai", "participants", ns, ""), false},
		{"a core resource", scope("", "secrets", ns, "voter-oidc-client"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := allow.check(tc.scope)
			if tc.allow && err != nil {
				t.Fatalf("scope %+v: want allowed, got %v", tc.scope, err)
			}
			if !tc.allow && err == nil {
				t.Fatalf("scope %+v: want refused, got allowed", tc.scope)
			}
		})
	}
}

// A version the host does not serve must not slip through on a resource it does.
func TestStreamAllowlistPinsTheVersion(t *testing.T) {
	allow := streamAllowlist{
		{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "quizsessions", Namespace: "voter"},
	}
	s := gateway.Scope{Group: "examples.configbutler.ai", Version: "v1", Resource: "quizsessions", Namespace: "voter"}
	if err := allow.check(s); err == nil {
		t.Fatal("a v1 scope was allowed by a v1alpha1 allowlist")
	}
}
