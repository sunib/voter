package main

import (
	"encoding/json"
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

func TestVotingRound(t *testing.T) {
	cfg := authorizationFixture(t)
	round := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "examples.configbutler.ai/v1alpha1", "kind": "QuizSession",
		"metadata": map[string]any{"name": "demo", "namespace": "voter", "uid": "round-uid", "resourceVersion": "1"},
		"spec": map[string]any{"state": "live", "questions": []any{
			map[string]any{"id": "choice", "title": "Choose", "type": "singleChoice", "required": true, "choices": []any{"A", "B"}},
			map[string]any{"id": "text", "title": "Explain", "type": "freeText"},
		}},
	}}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{quizSessions: "QuizSessionList", quizSubmissions: "QuizSubmissionList"}, round)
	mux := http.NewServeMux()
	calls := 0
	registerParticipantQuizHandlers(mux, handlerDeps{cfg: cfg, defaultNS: "voter", newClients: func(_ config, token string) (participantClients, error) {
		calls++
		if token != "alice" && token != "bob" {
			t.Fatalf("wrong credential %q", token)
		}
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
	const valid = `{"uid":"round-uid","resourceVersion":"1","answers":[{"questionId":"choice","singleChoice":"A"},{"questionId":"text","freeText":"hello"}]}`
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"required", `{"uid":"round-uid","resourceVersion":"1","answers":[]}`, 400},
		{"invalid choice", strings.Replace(valid, `"A"`, `"C"`, 1), 400},
		{"wrong type", strings.Replace(valid, `"freeText"`, `"singleChoice"`, 1), 400},
		{"stale round", strings.Replace(valid, `"1"`, `"0"`, 1), 409},
		{"recreated round", strings.Replace(valid, `round-uid`, `old-uid`, 1), 409},
		{"forged metadata", strings.Replace(valid, `"answers":`, `"metadata":{},"answers":`, 1), 400},
		{"trailing body", valid + `{}`, 400},
		{"valid", valid, 201},
		{"duplicate", valid, 409},
		{"duplicate with changed answers", strings.Replace(valid, `"A"`, `"B"`, 1), 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := request("POST", "/public/rounds/demo", "alice", tc.body, true)
			if rec.Code != tc.want {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
		})
	}
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		before := len(client.Actions())
		rec := request(method, "/public/rounds/demo", "alice", valid, true)
		if rec.Code != http.StatusMethodNotAllowed || len(client.Actions()) != before {
			t.Fatalf("%s must not edit a QuizSubmission: status=%d", method, rec.Code)
		}
	}
	submissions, err := client.Resource(quizSubmissions).Namespace("voter").List(t.Context(), metav1.ListOptions{})
	if err != nil || len(submissions.Items) != 1 {
		t.Fatalf("retry must leave one QuizSubmission: %v, %v", submissions, err)
	}
	answers, _, err := unstructured.NestedSlice(submissions.Items[0].Object, "spec", "answers")
	if err != nil || len(answers) != 2 || answers[0].(map[string]any)["singleChoice"] != "A" {
		t.Fatalf("submitted answers were overwritten: %v, %v", answers, err)
	}
	before := calls
	if rec := request("POST", "/public/rounds/demo", "bob", valid, false); rec.Code != 403 || calls != before {
		t.Fatal("missing CSRF reached Kubernetes")
	}
	if rec := request("POST", "/public/rounds/demo", "bob", valid, true); rec.Code != 201 {
		t.Fatalf("second participant: %s", rec.Body)
	}
	rec := request("GET", "/public/rounds/demo/results", "alice", "", true)
	var result struct {
		Total     int
		Questions []quizResult
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || result.Total != 2 || result.Questions[0].Choices["A"] != 2 || len(result.Questions[1].Text) != 2 {
		t.Fatalf("wrong results: %s", rec.Body)
	}
	_ = unstructured.SetNestedField(round.Object, "closed", "spec", "state")
	_, err = client.Resource(quizSessions).Namespace("voter").Update(t.Context(), round, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rec := request("POST", "/public/rounds/demo", "bob", valid, true); rec.Code != 409 || !strings.Contains(rec.Body.String(), "not open") {
		t.Fatalf("closed round: %s", rec.Body)
	}
	// An old round's QuizSubmissions must not count after delete/recreate under the same name.
	round.SetUID("new-uid")
	_, err = client.Resource(quizSessions).Namespace("voter").Update(t.Context(), round, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rec = request("GET", "/public/rounds/demo/results", "alice", "", true)
	if !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Fatalf("old votes leaked: %s", rec.Body)
	}
}

func TestQuizAnswerValidation(t *testing.T) {
	var questions []quizQuestion
	if err := json.Unmarshal([]byte(`[{"id":"multi","title":"Multi","type":"multiChoice","choices":["a","b"],"required":true},{"id":"score","title":"Score","type":"scale0to10"},{"id":"n","title":"Number","type":"number","min":-2,"max":2},{"id":"text","title":"Text","type":"freeText","required":true}]`), &questions); err != nil {
		t.Fatal(err)
	}
	valid := `[{"questionId":"multi","multiChoice":["a","b"]},{"questionId":"score","number":0},{"questionId":"n","number":-2},{"questionId":"text","freeText":"ok"}]`
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"all types", valid, true},
		{"empty required", strings.Replace(valid, `["a","b"]`, `[]`, 1), false},
		{"duplicate choice", strings.Replace(valid, `["a","b"]`, `["a","a"]`, 1), false},
		{"range", strings.Replace(valid, `"number":0`, `"number":11`, 1), false},
		{"numeric min", strings.Replace(valid, `"number":-2`, `"number":-3`, 1), false},
		{"blank required", strings.Replace(valid, `"ok"`, `"  "`, 1), false},
		{"two fields", strings.Replace(valid, `"freeText":"ok"`, `"freeText":"ok","number":1`, 1), false},
		{"duplicate question", strings.Replace(valid, `"questionId":"n"`, `"questionId":"score"`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var answers []quizAnswer
			if err := json.Unmarshal([]byte(tc.body), &answers); err != nil {
				t.Fatal(err)
			}
			err := validateQuizAnswers(questions, answers)
			if (err == nil) != tc.valid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}
