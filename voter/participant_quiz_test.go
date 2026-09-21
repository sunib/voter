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
		"metadata": map[string]any{"name": "demo", "namespace": "voter", "uid": "round-uid", "resourceVersion": "1", "generation": int64(1)},
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
	const valid = `{"uid":"round-uid","generation":1,"answers":[{"questionId":"choice","singleChoice":"A"},{"questionId":"text","freeText":"hello"}]}`
	if rec := request("GET", "/public/rounds/demo", "alice", "", true); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"voted":false`) {
		t.Fatalf("round before voting: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"required", `{"uid":"round-uid","generation":1,"answers":[]}`, 400},
		{"invalid choice", strings.Replace(valid, `"A"`, `"C"`, 1), 400},
		{"wrong type", strings.Replace(valid, `"freeText"`, `"singleChoice"`, 1), 400},
		// The questions were edited under the voter: generation moved.
		{"edited round", strings.Replace(valid, `"generation":1`, `"generation":2`, 1), 409},
		// The round was deleted and recreated under the same name, so it is back
		// at generation 1 with questions this ballot never saw. Only the UID tells
		// them apart, which is why the ballot still carries one.
		{"recreated round", strings.Replace(valid, `"round-uid"`, `"a-newer-round-uid"`, 1), 409},
		// uid and generation together carry the whole staleness check, so a client
		// that pins neither is refused rather than quietly voting into whatever the
		// round has become.
		{"unpinned round", `{"answers":[{"questionId":"choice","singleChoice":"A"}]}`, 409},
		{"half-pinned round", `{"uid":"round-uid","answers":[{"questionId":"choice","singleChoice":"A"}]}`, 409},
		{"resourceVersion is no longer a field", strings.Replace(valid, `{"uid"`, `{"resourceVersion":"1","uid"`, 1), 400},
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
	// The form is skipped for a returning voter, so the GET must say so before it is drawn.
	if rec := request("GET", "/public/rounds/demo", "alice", "", true); !strings.Contains(rec.Body.String(), `"voted":true`) {
		t.Fatalf("voter was not warned before the form: %s", rec.Body)
	}
	if rec := request("GET", "/public/rounds/demo", "bob", "", true); !strings.Contains(rec.Body.String(), `"voted":false`) {
		t.Fatalf("another participant must still get the form: %s", rec.Body)
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
	// The name and both labels are a contract with gitops-reverser, not
	// cosmetics: the name is what makes a second ballot collide, and the labels
	// are what the mirror files the document by. A silent change here moves
	// every document in the audit trail.
	if got, want := submissions.Items[0].GetName(), "demo-alice"; got != want {
		t.Errorf("submission name = %q, want %q", got, want)
	}
	if got := submissions.Items[0].GetLabels(); got[roundLabel] != "demo" || got[submitterLabel] != "alice" {
		t.Errorf("submission labels = %v, want round=demo submitter=alice", got)
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
}

// Voting is for people who came through Room Pass. An operator reaches these
// handlers with a perfectly valid session -- requireParticipant checks that the
// cookie is good, not which connector issued it -- so the refusal has to be
// here, and it has to be narrow: the same session still has to open and close
// rounds and read results, because that IS the operator page.
func TestOnlyRoomPassSessionsMayVote(t *testing.T) {
	cfg := authorizationFixture(t)
	round := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "examples.configbutler.ai/v1alpha1", "kind": "QuizSession",
		"metadata": map[string]any{"name": "demo", "namespace": "voter", "uid": "round-uid", "resourceVersion": "1", "generation": int64(1)},
		"spec": map[string]any{"state": "live", "questions": []any{
			map[string]any{"id": "choice", "title": "Choose", "type": "singleChoice", "required": true, "choices": []any{"A", "B"}},
		}},
	}}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{quizSessions: "QuizSessionList", quizSubmissions: "QuizSubmissionList"}, round)
	mux := http.NewServeMux()
	registerParticipantQuizHandlers(mux, handlerDeps{cfg: cfg, defaultNS: "voter", newClients: func(_ config, _ string) (participantClients, error) {
		return participantClients{dynamic: client}, nil
	}})
	as := func(connector, method, path, body string) *httptest.ResponseRecorder {
		req := authorizedRequestAs(t, cfg, method, "operator", connector)
		req.URL.Path = path
		req.Body = io.NopCloser(strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	const ballot = `{"uid":"round-uid","generation":1,"answers":[{"questionId":"choice","singleChoice":"A"}]}`

	rec := as("github", "POST", "/public/rounds/demo", ballot)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "NotAParticipant") {
		t.Fatalf("an operator was allowed to vote: %d %s", rec.Code, rec.Body)
	}
	// Nothing was written: the refusal happens before any Kubernetes call.
	list, err := client.Resource(quizSubmissions).Namespace("voter").List(t.Context(), metav1.ListOptions{})
	if err != nil || len(list.Items) != 0 {
		t.Fatalf("a refused vote still reached Kubernetes: %v %v", list, err)
	}
	// ...but the operator page keeps working.
	if rec := as("github", "GET", "/public/rounds/demo/results", ""); rec.Code != 200 {
		t.Errorf("an operator must still read results: %d %s", rec.Code, rec.Body)
	}
	// And a participant is unaffected. Cast before the round is closed below.
	if rec := as(testParticipantConnector, "POST", "/public/rounds/demo", ballot); rec.Code != 201 {
		t.Errorf("a Room Pass participant was refused: %d %s", rec.Code, rec.Body)
	}
	if rec := as("github", "POST", "/public/rounds/demo/state", `{"state":"closed"}`); rec.Code != 200 {
		t.Errorf("an operator must still close a round: %d %s", rec.Code, rec.Body)
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

// The tally controller writes quizsessions/status on every ballot, and once a
// minute at rest. That moves metadata.resourceVersion and deliberately leaves
// metadata.generation alone -- so a ballot filled in before the write has to
// still be accepted after it.
//
// This is the regression test for 2026-09-17, where the ballot pinned
// resourceVersion: a tally write between drawing the form and pressing Submit
// cost the voter their ballot, and during the demo there was one about every
// second. docs/post-demo-2026-09-17.md has the measurements.
func TestATallyWriteDoesNotInvalidateABallot(t *testing.T) {
	cfg := authorizationFixture(t)
	round := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "examples.configbutler.ai/v1alpha1", "kind": "QuizSession",
		"metadata": map[string]any{"name": "demo", "namespace": "voter", "uid": "round-uid", "resourceVersion": "1", "generation": int64(1)},
		"spec": map[string]any{"state": "live", "questions": []any{
			map[string]any{"id": "choice", "title": "Choose", "type": "singleChoice", "required": true, "choices": []any{"A", "B"}},
		}},
	}}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{quizSessions: "QuizSessionList", quizSubmissions: "QuizSubmissionList"}, round)
	mux := http.NewServeMux()
	registerParticipantQuizHandlers(mux, handlerDeps{cfg: cfg, defaultNS: "voter", newClients: func(_ config, _ string) (participantClients, error) {
		return participantClients{dynamic: client}, nil
	}})

	// What alice's phone captured when it drew the form. It is not read again.
	const ballot = `{"uid":"round-uid","generation":1,"answers":[{"questionId":"choice","singleChoice":"A"}]}`

	// Meanwhile the room votes and the controller republishes the tally. Status
	// only: the questions did not change, so generation does not move and
	// resourceVersion does.
	rounds := client.Resource(quizSessions).Namespace("voter")
	live, err := rounds.Get(t.Context(), "demo", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read the round: %v", err)
	}
	live.SetResourceVersion("2")
	if err := unstructured.SetNestedField(live.Object, int64(7), "status", "counted"); err != nil {
		t.Fatalf("build the tally: %v", err)
	}
	if _, err := rounds.UpdateStatus(t.Context(), live, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("tally write: %v", err)
	}
	after, err := rounds.Get(t.Context(), "demo", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("re-read the round: %v", err)
	}
	if after.GetResourceVersion() == "1" || after.GetGeneration() != 1 {
		t.Fatalf("fixture does not reproduce a status write: rv=%q generation=%d",
			after.GetResourceVersion(), after.GetGeneration())
	}

	req := authorizedRequest(t, cfg, "POST", "alice")
	req.URL.Path = "/public/rounds/demo"
	req.Body = io.NopCloser(strings.NewReader(ballot))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("a tally write cost a voter their ballot: %d %s", rec.Code, rec.Body)
	}
}
