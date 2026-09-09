package controller

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	api "github.com/sunib/voter/room-pass/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testRoom(now time.Time) *api.Room {
	return &api.Room{Spec: api.RoomSpec{Enrollment: "Open", EndsAt: metav1.NewTime(now.Add(time.Hour)), JoinCode: api.JoinCodeSpec{RotateEvery: "15s", ValidFor: "30s", Length: 6}}}
}
func TestRotationPersistenceAndBoundary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := testRoom(now)
	if e := Advance(r, now, rand.Reader); e != nil {
		t.Fatal(e)
	}
	first := r.Status.JoinCode.Code
	if e := Advance(r, now.Add(14*time.Second), bytes.NewReader(nil)); e != nil {
		t.Fatal("unexpected random read", e)
	}
	// A restart uses only persisted state; there is no in-memory code store.
	r = r.DeepCopy()
	if e := Advance(r, now.Add(15*time.Second), rand.Reader); e != nil {
		t.Fatal(e)
	}
	if len(r.Status.ValidJoinCodes) != 2 || !Accepts(r, " "+first[:3]+"-"+first[3:]+" ", now.Add(29*time.Second)) {
		t.Fatal("previous displayed code lost grace period")
	}
	if Accepts(r, first, now.Add(30*time.Second)) {
		t.Fatal("accepted at exact expiry")
	}
	if e := Advance(r, now.Add(2*time.Minute), rand.Reader); e != nil {
		t.Fatal(e)
	}
	if len(r.Status.ValidJoinCodes) != 1 {
		t.Fatal("backfilled missed rotations")
	}
	r.Spec.Enrollment = "Closed"
	if e := Advance(r, now.Add(121*time.Second), rand.Reader); e != nil {
		t.Fatal(e)
	}
	if r.Status.JoinCode != nil || len(r.Status.ValidJoinCodes) != 0 {
		t.Fatal("closed room retains codes")
	}
	r.Spec.Enrollment = "Open"
	if e := Advance(r, now.Add(122*time.Second), rand.Reader); e != nil {
		t.Fatal(e)
	}
	if len(r.Status.ValidJoinCodes) != 1 {
		t.Fatal("reopen did not start fresh")
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("randomness unavailable") }
func TestRandomFailureAndCollision(t *testing.T) {
	now := time.Now().UTC()
	r := testRoom(now)
	if Advance(r, now, brokenReader{}) == nil {
		t.Fatal("random failure ignored")
	}
	if r.Status.JoinCode != nil {
		t.Fatal("published predictable code")
	}
	if e := Advance(r, now, bytes.NewReader(make([]byte, 1024))); e != nil {
		t.Fatal(e)
	}
	if Advance(r, now.Add(15*time.Second), bytes.NewReader(make([]byte, 1024))) == nil {
		t.Fatal("collision accepted")
	}
	if len(r.Status.ValidJoinCodes) != 1 {
		t.Fatal("collision published")
	}
}

func TestCoalescedCloseReopenCannotReviveCodes(t *testing.T) {
	now := time.Now().UTC()
	r := testRoom(now)
	r.Generation = 1
	if e := Advance(r, now, rand.Reader); e != nil {
		t.Fatal(e)
	}
	old := r.Status.JoinCode.Code
	r.Generation = 3 // close then reopen before the watch reconciles either update
	if Accepts(r, old, now.Add(time.Second)) {
		t.Fatal("stale generation accepted")
	}
	if e := Advance(r, now.Add(time.Second), rand.Reader); e != nil {
		t.Fatal(e)
	}
	if Accepts(r, old, now.Add(time.Second)) {
		t.Fatal("old code revived after reopen")
	}
}
