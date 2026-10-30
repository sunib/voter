package main

import (
	"context"
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

const alice = "demo:CgVhbGljZQ"

func seatFixture(name, owner, created string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "examples.configbutler.ai/v1alpha1", "kind": "Seat",
		"metadata": map[string]any{"name": name, "namespace": "voter", "creationTimestamp": created},
		"spec": map[string]any{
			"claimedBy": map[string]any{"username": owner, "displayName": "Alice"},
			"color":     "#e4572e", "emoji": "🦊",
		},
	}}
}

// A seat's CommitRequest as gitops-reverser leaves it: author from the audit
// event, commit and Ready once pushed. An empty sha is one still in flight.
func seatRequest(name, seat, target, author, sha, readyAt, message string) *unstructured.Unstructured {
	status := map[string]any{"author": author}
	if sha != "" {
		status["commit"] = sha
		status["conditions"] = []any{map[string]any{
			"type": "Ready", "status": "True", "reason": "Committed", "lastTransitionTime": readyAt,
		}}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "configbutler.ai/v1alpha3", "kind": "CommitRequest",
		"metadata": map[string]any{
			"name": name, "namespace": "voter",
			"labels": map[string]any{seatCommitLabel: seat},
		},
		"spec":   map[string]any{"gitTargetRef": map[string]any{"name": target}, "message": message},
		"status": status,
	}}
}

func TestLastSeatCommit(t *testing.T) {
	seat := seatFixture("mission2-3-7", alice, "2026-10-30T10:00:00Z")
	cases := []struct {
		name     string
		requests []*unstructured.Unstructured
		want     string // the message picked, "" for none
	}{
		{"the newest of the owner's commits", []*unstructured.Unstructured{
			seatRequest("seat-claim-a", "mission2-3-7", "seats", alice, "aaa", "2026-10-30T10:00:01Z", "by the door"),
			seatRequest("seat-change-b", "mission2-3-7", "seats", alice, "bbb", "2026-10-30T10:05:00Z", "teal is calmer"),
		}, "teal is calmer"},
		// The label is anyone's to write; the author is the audit log's.
		{"not someone else's request naming this seat", []*unstructured.Unstructured{
			seatRequest("seat-change-x", "mission2-3-7", "seats", "demo:bWFsbG9yeQ", "xxx", "2026-10-30T10:05:00Z", "mine now"),
		}, ""},
		{"not another seat's", []*unstructured.Unstructured{
			seatRequest("seat-change-y", "mission2-1-1", "seats", alice, "yyy", "2026-10-30T10:05:00Z", "elsewhere"),
		}, ""},
		{"not another target's", []*unstructured.Unstructured{
			seatRequest("coffee-save-z", "mission2-3-7", "coffee", alice, "zzz", "2026-10-30T10:05:00Z", "cheaper"),
		}, ""},
		{"not one still in flight", []*unstructured.Unstructured{
			seatRequest("seat-change-p", "mission2-3-7", "seats", alice, "", "", "pending"),
		}, ""},
		// The same person, the same seat, an earlier claim of it.
		{"not one from before the seat was claimed", []*unstructured.Unstructured{
			seatRequest("seat-claim-old", "mission2-3-7", "seats", alice, "old", "2026-10-30T09:00:00Z", "last time"),
		}, ""},
		{"a commit in another zone, compared as UTC", []*unstructured.Unstructured{
			seatRequest("seat-claim-a", "mission2-3-7", "seats", alice, "aaa", "2026-10-30T11:00:01+01:00", "by the door"),
		}, "by the door"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := lastSeatCommit(seat, "seats", tc.requests)
			if found != (tc.want != "") || got.Message != tc.want {
				t.Fatalf("got %+v (found %v), want message %q", got, found, tc.want)
			}
		})
	}
}

func seatReconcilerFixture(t *testing.T, objs ...runtime.Object) (*dynamicfake.FakeDynamicClient, *atomic.Int64) {
	t.Helper()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{seats: "SeatList", commitRequests: "CommitRequestList"}, objs...)
	var writes atomic.Int64
	client.PrependReactor("patch", "seats", func(k8stesting.Action) (bool, runtime.Object, error) {
		writes.Add(1)
		return false, nil, nil // carry on to the tracker; this only counts.
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go newSeatReconciler(client, "voter", "voter", "seats").Run(ctx)
	return client, &writes
}

func awaitSeatCommit(t *testing.T, client *dynamicfake.FakeDynamicClient, seat string, want func(seatCommit) bool) seatCommit {
	t.Helper()
	var last seatCommit
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		obj, err := client.Resource(seats).Namespace("voter").Get(context.Background(), seat, metav1.GetOptions{})
		if err == nil {
			if last = storedSeatCommit(obj); want(last) {
				return last
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("lastCommit never satisfied the condition; last was %+v", last)
	return last
}

// The whole path: a claim's request commits, a change's request is created
// and only later reports its commit, and each lands on the seat as it does.
func TestACommittedWhyLandsOnTheSeat(t *testing.T) {
	client, _ := seatReconcilerFixture(t,
		seatFixture("mission2-3-7", alice, "2026-10-30T10:00:00Z"),
		seatRequest("seat-claim-a", "mission2-3-7", "seats", alice, "aaa", "2026-10-30T10:00:01Z", "by the door"),
	)
	got := awaitSeatCommit(t, client, "mission2-3-7", func(c seatCommit) bool { return c.Commit == "aaa" })
	if got.Message != "by the door" || got.CommitRequest != "seat-claim-a" || got.CommittedAt != "2026-10-30T10:00:01Z" {
		t.Fatalf("lastCommit after the claim: %+v", got)
	}

	ctx := context.Background()
	requests := client.Resource(commitRequests).Namespace("voter")
	change := seatRequest("seat-change-b", "mission2-3-7", "seats", alice, "", "", "teal is calmer")
	if _, err := requests.Create(ctx, change, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	committed := seatRequest("seat-change-b", "mission2-3-7", "seats", alice, "bbb", "2026-10-30T10:05:00Z", "teal is calmer")
	if _, err := requests.Update(ctx, committed, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	got = awaitSeatCommit(t, client, "mission2-3-7", func(c seatCommit) bool { return c.Commit == "bbb" })
	if got.Message != "teal is calmer" {
		t.Fatalf("lastCommit after the change: %+v", got)
	}

	// A request's deletion (delete-after) leaves the why where it is.
	if err := requests.Delete(ctx, "seat-change-b", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got = awaitSeatCommit(t, client, "mission2-3-7", func(seatCommit) bool { return true }); got.Commit != "bbb" {
		t.Fatalf("lastCommit after the request was deleted: %+v", got)
	}
}

// The request can finish before the seat it claimed reaches this cache.
func TestARequestThatFinishesFirstWaitsForItsSeat(t *testing.T) {
	client, _ := seatReconcilerFixture(t,
		seatRequest("seat-claim-a", "mission2-3-7", "seats", alice, "aaa", "2026-10-30T10:00:01Z", "by the door"),
	)
	time.Sleep(50 * time.Millisecond)
	if _, err := client.Resource(seats).Namespace("voter").Create(context.Background(),
		seatFixture("mission2-3-7", alice, "2026-10-30T10:00:00Z"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	awaitSeatCommit(t, client, "mission2-3-7", func(c seatCommit) bool { return c.Message == "by the door" })
}

// Its own status write comes back as a seat update, which must not be the
// cause of another.
func TestASeatStatusWriteDoesNotTriggerAnother(t *testing.T) {
	client, writes := seatReconcilerFixture(t,
		seatFixture("mission2-3-7", alice, "2026-10-30T10:00:00Z"),
		seatRequest("seat-claim-a", "mission2-3-7", "seats", alice, "aaa", "2026-10-30T10:00:01Z", "by the door"),
	)
	awaitSeatCommit(t, client, "mission2-3-7", func(c seatCommit) bool { return c.Commit == "aaa" })
	time.Sleep(300 * time.Millisecond)
	if got := writes.Load(); got != 1 {
		t.Fatalf("%d status writes for one commit", got)
	}
}
