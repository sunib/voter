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

// The REAL policy this time, not a copy of it.
//
// The allowlist test above builds its own list, which is right for testing the
// matching rule and useless for catching the failure that actually happened: a
// screen following a kind the shared machinery had never been told about. That
// one does not surface as a refusal, it surfaces as INTERNAL, because there is
// no watch to fan out -- which is how the Databases pages failed on 2026-09-22.
func TestScopePolicyAdmitsWhatTheScreensFollow(t *testing.T) {
	const ns = "voter"
	for _, tc := range []struct {
		name  string
		scope gateway.Scope
	}{
		{"the coffee menu", gateway.Scope{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "coffeeconfigs", Namespace: ns, Name: "demo-coffee"}},
		{"rounds opening and closing", gateway.Scope{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "quizsessions", Namespace: ns}},
		{"the database requests, as a list", gateway.Scope{Group: "platform.configbutler.ai", Version: "v1alpha1", Resource: "databases", Namespace: ns}},
		{"one database request, as an editor", gateway.Scope{Group: "platform.configbutler.ai", Version: "v1alpha1", Resource: "databases", Namespace: ns, Name: "loyalty-points-mysql"}},
		// Named, because a save follows the ONE receipt it just created.
		{"the commit receipt a save creates", gateway.Scope{Group: "configbutler.ai", Version: "v1alpha3", Resource: "commitrequests", Namespace: ns, Name: "database-save-pw52m"}},
		{"the room's rotating join code", gateway.Scope{Group: "roompass.configbutler.ai", Version: "v1alpha1", Resource: "rooms", Namespace: ns, Name: "demo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := scopePolicy.Validate(tc.scope); err != nil {
				t.Fatalf("scope %+v: want admitted, got %v", tc.scope, err)
			}
		})
	}
}

// ...and that it is still an allowlist. Adding a kind must not quietly become
// adding a category of kinds.
func TestScopePolicyStillDeniesByDefault(t *testing.T) {
	const ns = "voter"
	for _, tc := range []struct {
		name  string
		scope gateway.Scope
	}{
		{"secrets", gateway.Scope{Version: "v1", Resource: "secrets", Namespace: ns}},
		{"the grant the operator switch creates", gateway.Scope{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings", Namespace: ns}},
		// Same API group as commitrequests, and not the same permission.
		{"the GitTarget a receipt points at", gateway.Scope{Group: "configbutler.ai", Version: "v1alpha3", Resource: "gittargets", Namespace: ns}},
		{"commit receipts in EVERY namespace", gateway.Scope{Group: "configbutler.ai", Version: "v1alpha3", Resource: "commitrequests"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := scopePolicy.Validate(tc.scope); err == nil {
				t.Fatalf("scope %+v: want refused, got allowed", tc.scope)
			}
		})
	}
}
