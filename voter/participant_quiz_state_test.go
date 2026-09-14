package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// Opening and closing a round from the operator page.
//
// The property that matters most is the one this test cannot see directly: the
// handler holds NO admin check, because the patch travels on the caller's own
// token and RBAC answers it. So the test asserts the observable half of that --
// the credential reaching Kubernetes is the caller's, never the service
// account's -- and then that the guards which ARE this handler's job hold.
func TestRoundStateChange(t *testing.T) {
	cfg := authorizationFixture(t)
	round := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "examples.configbutler.ai/v1alpha1", "kind": "QuizSession",
		"metadata": map[string]any{"name": "demo", "namespace": "voter", "uid": "round-uid", "resourceVersion": "1"},
		"spec":     map[string]any{"state": "closed", "title": "Does GitOps belong on stage?"},
	}}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{quizSessions: "QuizSessionList", quizSubmissions: "QuizSubmissionList"},
		round,
	)

	mux := http.NewServeMux()
	seen := []string{}
	registerParticipantQuizHandlers(mux, handlerDeps{cfg: cfg, defaultNS: "voter", newClients: func(_ config, token string) (participantClients, error) {
		// The whole authorization story rests on this: had the handler reached
		// for a shared service-account client, Kubernetes would authorize Voter
		// instead of the operator and every caller could open a round.
		seen = append(seen, token)
		return participantClients{dynamic: client}, nil
	}})

	request := func(method, path, token, body string, csrf bool) *httptest.ResponseRecorder {
		req := authorizedRequest(t, cfg, method, token)
		req.URL.Path = path
		req.Body = io.NopCloser(strings.NewReader(body))
		if !csrf {
			req.Header.Del("X-CSRF-Token")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	state := func() string {
		got, err := client.Resource(quizSessions).Namespace("voter").Get(t.Context(), "demo", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("reading the round back: %v", err)
		}
		value, _, err := unstructured.NestedString(got.Object, "spec", "state")
		if err != nil {
			t.Fatalf("reading spec.state: %v", err)
		}
		return value
	}

	if rec := request("POST", "/public/rounds/demo/state", "operator", `{"state":"live"}`, true); rec.Code != 200 {
		t.Fatalf("opening a round: %d %s", rec.Code, rec.Body)
	}
	if got := state(); got != "live" {
		t.Fatalf("spec.state after opening = %q, want live", got)
	}
	if len(seen) != 1 || seen[0] != "operator" {
		t.Fatalf("patch carried %v, want the caller's own token", seen)
	}

	if rec := request("POST", "/public/rounds/demo/state", "operator", `{"state":"closed"}`, true); rec.Code != 200 {
		t.Fatalf("closing a round: %d %s", rec.Code, rec.Body)
	}
	if got := state(); got != "closed" {
		t.Fatalf("spec.state after closing = %q, want closed", got)
	}

	// Only the two states a button drives. "draft" is where a round is written,
	// in Git; letting this endpoint return a live round to it would hide it from
	// the room mid-demo with no record of who did it.
	for _, tc := range []struct {
		name, body string
	}{
		{"draft", `{"state":"draft"}`},
		{"empty", `{"state":""}`},
		{"unknown", `{"state":"open"}`},
		{"missing field", `{}`},
		{"not an object", `"live"`},
		{"malformed", `{`},
	} {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			before := len(client.Actions())
			rec := request("POST", "/public/rounds/demo/state", "operator", tc.body, true)
			if rec.Code != 400 {
				t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
			}
			if len(client.Actions()) != before {
				t.Fatal("a refused state still reached Kubernetes")
			}
			if got := state(); got != "closed" {
				t.Fatalf("a refused state changed the round to %q", got)
			}
		})
	}

	// A read cannot change a round, whatever the body says.
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		before := len(client.Actions())
		rec := request(method, "/public/rounds/demo/state", "operator", `{"state":"live"}`, true)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s: status %d, want 405", method, rec.Code)
		}
		if len(client.Actions()) != before {
			t.Fatalf("%s reached Kubernetes", method)
		}
	}

	// State change is a mutation, so it needs CSRF proof like every other one.
	before := len(seen)
	if rec := request("POST", "/public/rounds/demo/state", "operator", `{"state":"live"}`, false); rec.Code != 403 {
		t.Fatalf("missing CSRF: status %d, want 403", rec.Code)
	}
	if len(seen) != before {
		t.Fatal("a request without CSRF proof still built a Kubernetes client")
	}
	if got := state(); got != "closed" {
		t.Fatalf("a request without CSRF proof opened the round (%q)", got)
	}
}
