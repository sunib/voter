package controller

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"math/big"
	"reflect"
	"strings"
	"time"

	api "github.com/sunib/voter/room-pass/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func Active(r *api.Room, now time.Time) bool {
	return r.DeletionTimestamp == nil && !r.Spec.Stopped && now.Before(r.Spec.EndsAt.Time)
}
func Normalize(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}
func Accepts(r *api.Room, code string, now time.Time) bool {
	if !Active(r, now) || r.Spec.Enrollment != "Open" || r.Status.ObservedGeneration != r.Generation {
		return false
	}
	for _, c := range r.Status.ValidJoinCodes {
		if Normalize(code) == c.Code && !now.Before(c.IssuedAt.Time) && now.Before(c.ExpiresAt.Time) {
			return true
		}
	}
	return false
}
func RandomCode(reader io.Reader, length int) (string, error) {
	const alphabet = "BCDFGHJKLMNPQRSTVWXZ"
	var b strings.Builder
	for i := 0; i < length; i++ {
		n, e := rand.Int(reader, big.NewInt(int64(len(alphabet))))
		if e != nil {
			return "", e
		}
		b.WriteByte(alphabet[n.Int64()])
	}
	return b.String(), nil
}

// Advance mutates only a candidate status. Callers must persist it before serving it.
func Advance(r *api.Room, now time.Time, reader io.Reader) error {
	// Any desired-state change starts a fresh enrollment epoch. In particular,
	// even a close/reopen pair coalesced by the watch cannot revive old codes.
	if r.Status.ObservedGeneration != r.Generation {
		r.Status.JoinCode = nil
		r.Status.ValidJoinCodes = nil
	}
	r.Status.ObservedGeneration = r.Generation
	active := Active(r, now)
	open := active && r.Spec.Enrollment == "Open"
	for _, c := range []struct {
		t  string
		ok bool
	}{{"Ready", true}, {"AuthorizationAllowed", active}, {"EnrollmentOpen", open}} {
		status := metav1.ConditionFalse
		reason := "Disabled"
		if c.ok {
			status = metav1.ConditionTrue
			reason = "Available"
		}
		meta.SetStatusCondition(&r.Status.Conditions, metav1.Condition{Type: c.t, Status: status, Reason: reason, Message: reason, ObservedGeneration: r.Generation, LastTransitionTime: metav1.NewTime(now)})
	}
	if !open {
		r.Status.JoinCode = nil
		r.Status.ValidJoinCodes = nil
		return nil
	}
	rotation, err := time.ParseDuration(r.Spec.JoinCode.RotateEvery)
	if err != nil {
		return err
	}
	ttl, err := time.ParseDuration(r.Spec.JoinCode.ValidFor)
	if err != nil {
		return err
	}
	if rotation < 10*time.Second || ttl < rotation+5*time.Second || ttl > rotation*4 || r.Spec.JoinCode.Length < 6 || r.Spec.JoinCode.Length > 12 {
		return errors.New("invalid code configuration")
	}
	kept := []api.Code{}
	for _, c := range r.Status.ValidJoinCodes {
		if now.Before(c.ExpiresAt.Time) {
			kept = append(kept, c)
		}
	}
	r.Status.ValidJoinCodes = kept
	if r.Status.JoinCode != nil && now.Before(r.Status.JoinCode.IssuedAt.Add(rotation)) && now.Before(r.Status.JoinCode.ExpiresAt.Time) {
		return nil
	}
	for attempt := 0; attempt < 40; attempt++ {
		code, e := RandomCode(reader, r.Spec.JoinCode.Length)
		if e != nil {
			return e
		}
		collision := false
		for _, c := range kept {
			if c.Code == code {
				collision = true
			}
		}
		if collision {
			continue
		}
		c := api.Code{Code: code, IssuedAt: metav1.NewTime(now), ExpiresAt: metav1.NewTime(now.Add(ttl))}
		r.Status.JoinCode = &c
		r.Status.ValidJoinCodes = append(kept, c)
		return nil
	}
	return errors.New("code collision retry budget exhausted")
}

type Reconciler struct {
	Client client.Client
	Room   client.ObjectKey
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.NamespacedName != r.Room {
		return ctrl.Result{}, nil
	}
	room := &api.Room{}
	if err := r.Client.Get(ctx, r.Room, room); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	old := room.DeepCopy()
	participants := &api.ParticipantList{}
	if err := r.Client.List(ctx, participants, client.InNamespace(room.Namespace)); err != nil {
		return ctrl.Result{}, err
	}
	room.Status.ParticipantCount = 0
	for _, p := range participants.Items {
		if p.Spec.RoomRef.UID == string(room.UID) {
			room.Status.ParticipantCount++
		}
	}
	if err := Advance(room, time.Now().UTC(), rand.Reader); err != nil {
		return ctrl.Result{}, err
	}
	if !reflect.DeepEqual(old.Status, room.Status) {
		if err := r.Client.Status().Update(ctx, room); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: time.Second}, nil
}
