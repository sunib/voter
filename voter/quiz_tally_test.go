package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

// tallyRound and the reconciler are exercised through the same objects the API
// server would hold, because the interesting cases are all shapes the CRD
// permits and the application never writes: a ballot with no round label, one
// naming a question that has been edited away, one belonging to another round.

func tallyFixtureRound(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "examples.configbutler.ai/v1alpha1", "kind": "QuizSession",
		"metadata": map[string]any{"name": name, "namespace": "voter", "generation": int64(3)},
		"spec": map[string]any{"state": "live", "title": name, "questions": []any{
			map[string]any{"id": "choice", "title": "Choose", "type": "singleChoice", "choices": []any{"A", "B"}},
			map[string]any{"id": "rating", "title": "Rate", "type": "scale0to10", "min": float64(0), "max": float64(10)},
			map[string]any{"id": "text", "title": "Explain", "type": "freeText"},
		}},
	}}
}

// ballot builds a submission the way the CRD allows, NOT the way the handler
// writes one: labels are an argument because the whole point of selecting on
// spec.sessionRef is that a ballot without them still counts.
func ballot(name, round, at string, labels map[string]any, answers ...any) *unstructured.Unstructured {
	metadata := map[string]any{"name": name, "namespace": "voter"}
	if labels != nil {
		metadata["labels"] = labels
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "examples.configbutler.ai/v1alpha1", "kind": "QuizSubmission",
		"metadata": metadata,
		"spec": map[string]any{
			"sessionRef":  map[string]any{"group": quizSessions.Group, "kind": "QuizSession", "name": round},
			"submittedAt": at, "answers": answers,
		},
	}}
}

func answer(id string, value any) map[string]any {
	field := map[string]string{"choice": "singleChoice", "rating": "number", "text": "freeText"}[id]
	return map[string]any{"questionId": id, field: value}
}

func TestTallyRound(t *testing.T) {
	round := tallyFixtureRound("demo")
	spec, err := decodeQuizSpec(round)
	if err != nil {
		t.Fatal(err)
	}
	ballots := []*unstructured.Unstructured{
		// Cast through the application: both labels, everything valid.
		ballot("demo-alice", "demo", "2026-09-17T13:00:00Z",
			map[string]any{roundLabel: "demo", submitterLabel: "alice"},
			answer("choice", "A"), answer("rating", float64(8)), answer("text", "first")),
		// Pasted with kubectl: NO round label at all. The CRD does not require
		// one, so the API server accepts this, and before spec.sessionRef became
		// the selector it was counted by nobody.
		ballot("demo-kubectl", "demo", "2026-09-17T13:01:00Z", nil,
			answer("choice", "A"), answer("text", "second")),
		// Filed, not counted: "missing" is not a question on this round, which
		// is what a hand-written ballot against edited questions looks like.
		ballot("demo-stale", "demo", "2026-09-17T13:02:00Z", nil,
			map[string]any{"questionId": "missing", "freeText": "nope"}),
		// Another round's ballot, carrying THIS round's label. The label is the
		// thing that used to decide; it must not decide any more.
		ballot("other-mallory", "other", "2026-09-17T13:03:00Z",
			map[string]any{roundLabel: "demo"}, answer("choice", "B")),
	}

	tally := tallyRound("demo", spec.Questions, ballots)
	if tally.Filed != 3 || tally.Counted != 2 {
		t.Fatalf("filed/counted = %d/%d, want 3/2", tally.Filed, tally.Counted)
	}
	if got := tally.Questions[0].Choices["A"]; got != 2 {
		t.Fatalf("choice A = %d, want 2 (the kubectl ballot counts)", got)
	}
	if got := tally.Questions[0].Choices["B"]; got != 0 {
		t.Fatalf("choice B = %d, want 0 (another round's ballot does not)", got)
	}
	if tally.Questions[1].Sum != 8 || tally.Questions[1].Count != 1 {
		t.Fatalf("rating = %v over %d", tally.Questions[1].Sum, tally.Questions[1].Count)
	}
	// Oldest first, so the room reads the answers in the order they were cast
	// and "the most recent 25" in status is a tail.
	if got := tally.Questions[2].Text; len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("text = %v, want [first second]", got)
	}

	t.Run("deleting every ballot returns the tally to zero", func(t *testing.T) {
		empty := tallyRound("demo", spec.Questions, nil)
		if empty.Filed != 0 || empty.Counted != 0 || len(empty.Questions) != 3 {
			t.Fatalf("empty tally = %+v", empty)
		}
		if len(empty.Questions[0].Choices) != 0 {
			t.Fatalf("stale choices survived: %v", empty.Questions[0].Choices)
		}
	})

	t.Run("status keeps a bounded text sample and an exact total", func(t *testing.T) {
		many := make([]*unstructured.Unstructured, 0, 40)
		for i := range 40 {
			many = append(many, ballot(fmt.Sprintf("demo-%02d", i), "demo",
				fmt.Sprintf("2026-09-17T14:%02d:00Z", i), nil, answer("text", fmt.Sprintf("answer %02d", i))))
		}
		status := tallyRound("demo", spec.Questions, many).status(round.GetGeneration(), time.Unix(0, 0))
		text := status.Questions[2]
		if text.TextTotal != 40 {
			t.Fatalf("textTotal = %d, want 40", text.TextTotal)
		}
		if len(text.Text) != statusTextSample {
			t.Fatalf("sample = %d answers, want %d", len(text.Text), statusTextSample)
		}
		// The most recent, not the first 25 the list happened to hand over.
		if text.Text[len(text.Text)-1] != "answer 39" {
			t.Fatalf("sample ends at %q, want the newest answer", text.Text[len(text.Text)-1])
		}
		if status.ObservedGeneration != 3 {
			t.Fatalf("observedGeneration = %d, want the round's generation", status.ObservedGeneration)
		}
		// The CRD caps this list at the same number. A sample that outgrew it
		// would be rejected by the API server on every tally, forever.
		if len(text.Text) > statusTextSample {
			t.Fatalf("sample exceeds the CRD's maxItems")
		}
	})
}

// tallyReconcilerFixture wires a reconciler to a fake API server holding
// objs, runs it, and returns a function that waits for a round's status to
// reach want.
func tallyReconcilerFixture(t *testing.T, objs ...runtime.Object) (*dynamicfake.FakeDynamicClient, func(round string, want func(quizStatus) bool) quizStatus) {
	t.Helper()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{quizSessions: "QuizSessionList", quizSubmissions: "QuizSubmissionList"}, objs...)
	r := newQuizReconciler(client, "voter")
	// Fast enough that the test does not wait on the design's one-second
	// production window, slow enough that it is still a coalescing window.
	r.coalesce = 5 * time.Millisecond
	r.resync = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Run(ctx)

	return client, func(round string, want func(quizStatus) bool) quizStatus {
		t.Helper()
		var last quizStatus
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			obj, err := client.Resource(quizSessions).Namespace("voter").Get(ctx, round, metav1.GetOptions{})
			if err == nil {
				if raw, found, _ := unstructured.NestedMap(obj.Object, "status"); found {
					encoded, _ := json.Marshal(raw)
					last = quizStatus{}
					_ = json.Unmarshal(encoded, &last)
					if want(last) {
						return last
					}
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("status never satisfied the condition; last was %+v", last)
		return last
	}
}

// The property the demo rests on: a ballot created straight against the API,
// with no HTTP handler involved, moves the tally. That is the kubectl path --
// the application is not on it at all -- and it is the one thing a test can pin
// that a live demo cannot be asked to repeat.
func TestAnyWriterMovesTheTally(t *testing.T) {
	client, awaitStatus := tallyReconcilerFixture(t, tallyFixtureRound("demo"))
	ctx := context.Background()

	awaitStatus("demo", func(s quizStatus) bool { return s.LastTallyTime != "" })

	// No labels, no handler, no session: just an object.
	_, err := client.Resource(quizSubmissions).Namespace("voter").Create(ctx,
		ballot("demo-kubectl", "demo", "2026-09-17T13:00:00Z", nil, answer("choice", "A")), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	status := awaitStatus("demo", func(s quizStatus) bool { return s.Counted == 1 })
	if status.Filed != 1 || status.Questions[0].Choices["A"] != 1 {
		t.Fatalf("status after the kubectl ballot: %+v", status)
	}

	// ...and deleting it follows the count back down, which is what the reset
	// between two runs of the demo depends on.
	if err := client.Resource(quizSubmissions).Namespace("voter").Delete(ctx, "demo-kubectl", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	status = awaitStatus("demo", func(s quizStatus) bool { return s.Counted == 0 })
	if status.Filed != 0 || len(status.Questions[0].Choices) != 0 {
		t.Fatalf("status after the delete: %+v", status)
	}
}

// A status write must not trigger the next one.
//
// The controller watches rounds AND writes their status, so its own patch comes
// back as an update event -- and lastTallyTime moves every time, so the object
// really has changed. Without the generation guard this is an unbounded write
// loop against etcd at one write per coalescing window, for ever, on a demo
// cluster. It is the most expensive mistake this file can make and the least
// visible: everything on screen looks perfect.
func TestAStatusWriteDoesNotTriggerAnother(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{quizSessions: "QuizSessionList", quizSubmissions: "QuizSubmissionList"},
		tallyFixtureRound("demo"))
	var writes atomic.Int64
	client.PrependReactor("patch", "quizsessions", func(k8stesting.Action) (bool, runtime.Object, error) {
		writes.Add(1)
		return false, nil, nil // carry on to the tracker; this only counts.
	})
	r := newQuizReconciler(client, "voter")
	r.coalesce = 5 * time.Millisecond
	// Long enough that nothing below is the backstop firing: every write this
	// test sees is a write the controller chose to make.
	r.resync = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && writes.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if writes.Load() == 0 {
		t.Fatal("the round was never tallied at all")
	}
	// Startup writes this round more than once on purpose -- the informer's
	// first Add and the resync's first pass both enqueue it -- so let that
	// settle before measuring. What must not happen is the NEXT one.
	time.Sleep(400 * time.Millisecond)
	settled := writes.Load()
	// ~120 coalescing windows. A self-triggering tally runs through all of them.
	time.Sleep(600 * time.Millisecond)
	if got := writes.Load(); got != settled {
		t.Fatalf("%d status writes, up from %d and still going: the tally is triggering itself", got, settled)
	}
}

// The property the whole design buys: one Go implementation writes status and
// serves REST, so the projector and the Refresh button cannot disagree. Two
// counters would have differed exactly on the ballot that fails validation.
func TestStatusAndRESTAgree(t *testing.T) {
	cfg := authorizationFixture(t)
	round := tallyFixtureRound("demo")
	ballots := []runtime.Object{
		round,
		ballot("demo-alice", "demo", "2026-09-17T13:00:00Z",
			map[string]any{roundLabel: "demo"}, answer("choice", "A"), answer("text", "yes")),
		ballot("demo-kubectl", "demo", "2026-09-17T13:01:00Z", nil, answer("choice", "B")),
		ballot("demo-stale", "demo", "2026-09-17T13:02:00Z", nil,
			map[string]any{"questionId": "missing", "freeText": "nope"}),
		ballot("other-bob", "other", "2026-09-17T13:03:00Z", map[string]any{roundLabel: "demo"}, answer("choice", "A")),
	}
	client, awaitStatus := tallyReconcilerFixture(t, ballots...)
	status := awaitStatus("demo", func(s quizStatus) bool { return s.Counted == 2 })

	mux := http.NewServeMux()
	registerParticipantQuizHandlers(mux, handlerDeps{cfg: cfg, defaultNS: "voter",
		newClients: func(_ config, _ string) (participantClients, error) {
			return participantClients{dynamic: client}, nil
		}})
	req := authorizedRequest(t, cfg, "GET", "alice")
	req.URL.Path = "/public/rounds/demo/results"
	req.Body = http.NoBody
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("results: %d %s", rec.Code, rec.Body)
	}
	var rest struct {
		Total     int `json:"total"`
		Filed     int `json:"filed"`
		Questions []struct {
			Question quizQuestion   `json:"question"`
			Count    int            `json:"count"`
			Choices  map[string]int `json:"choices"`
			Text     []string       `json:"text"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rest); err != nil {
		t.Fatal(err)
	}
	if rest.Total != status.Counted || rest.Filed != status.Filed {
		t.Fatalf("REST %d/%d disagrees with status %d/%d", rest.Total, rest.Filed, status.Counted, status.Filed)
	}
	for i, q := range rest.Questions {
		if q.Question.ID != status.Questions[i].ID || q.Count != status.Questions[i].Count {
			t.Fatalf("question %d: REST %s/%d, status %s/%d", i,
				q.Question.ID, q.Count, status.Questions[i].ID, status.Questions[i].Count)
		}
		for choice, want := range status.Questions[i].Choices {
			if q.Choices[choice] != want {
				t.Fatalf("question %s choice %q: REST %d, status %d", q.Question.ID, choice, q.Choices[choice], want)
			}
		}
	}
	// Both counted the kubectl ballot, which carries no round label at all --
	// so neither is selecting on it.
	if status.Questions[0].Choices["B"] != 1 {
		t.Fatalf("the unlabelled ballot was not counted: %+v", status.Questions[0])
	}
	// The endpoint stays the authority for the FULL free-text list, which is
	// why status may bound its own.
	if text := rest.Questions[2].Text; len(text) != 1 || !strings.Contains(text[0], "yes") {
		t.Fatalf("REST text = %v", text)
	}
}

// A closed round stops being rewritten once its result has settled.
//
// The resync exists so a failed write heals without a restart, and it also moved
// lastTallyTime on every pass -- which meant every round in the namespace was
// rewritten once a minute for ever, and every rewrite is an event on every open
// stream. With two hundred phones in the room that is a real cost paid for a
// timestamp nobody is reading, because the projector only shows the round that
// is open. docs/post-demo-2026-09-17.md.
//
// A LIVE round keeps its heartbeat; TestALiveRoundKeepsItsHeartbeat is the other
// half of this, and the two must not be merged.
func TestAClosedRoundStopsBeingRewritten(t *testing.T) {
	round := tallyFixtureRound("demo")
	_ = unstructured.SetNestedField(round.Object, "closed", "spec", "state")
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{quizSessions: "QuizSessionList", quizSubmissions: "QuizSubmissionList"},
		round, ballot("b1", "demo", "2026-09-17T09:45:27Z", nil, answer("choice", "A")))
	var writes atomic.Int64
	client.PrependReactor("patch", "quizsessions", func(k8stesting.Action) (bool, runtime.Object, error) {
		writes.Add(1)
		return false, nil, nil
	})
	r := newQuizReconciler(client, "voter")
	r.coalesce = 5 * time.Millisecond
	// Fast enough that ~40 resyncs happen inside the window below. Every one of
	// them used to be a write.
	r.resync = 25 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && writes.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if writes.Load() == 0 {
		t.Fatal("the round was never tallied at all")
	}
	time.Sleep(200 * time.Millisecond)
	settled := writes.Load()
	time.Sleep(time.Second)
	if got := writes.Load(); got != settled {
		t.Fatalf("a closed round was rewritten %d more times by the resync; the tally has not settled", got-settled)
	}
}

// And the half that must keep working: while a round is open, the timestamp on
// the projector goes on moving, because a timestamp that has stopped is the only
// thing that tells a dead controller from a room that has finished voting.
func TestALiveRoundKeepsItsHeartbeat(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{quizSessions: "QuizSessionList", quizSubmissions: "QuizSubmissionList"},
		tallyFixtureRound("demo"))
	var writes atomic.Int64
	client.PrependReactor("patch", "quizsessions", func(k8stesting.Action) (bool, runtime.Object, error) {
		writes.Add(1)
		return false, nil, nil
	})
	r := newQuizReconciler(client, "voter")
	r.coalesce = 5 * time.Millisecond
	r.resync = 25 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Run(ctx)

	time.Sleep(200 * time.Millisecond)
	settled := writes.Load()
	time.Sleep(500 * time.Millisecond)
	if got := writes.Load(); got <= settled {
		t.Fatalf("a live round stopped publishing its tally time after %d writes", settled)
	}
}
