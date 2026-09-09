package integration

import (
	"context"
	"os"
	"testing"
	"time"

	api "github.com/sunib/voter/room-pass/api/v1alpha1"
	"github.com/sunib/voter/room-pass/internal/controller"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestAPISchemaAndReconcile(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("set KUBEBUILDER_ASSETS or run task integration")
	}
	env := &envtest.Environment{CRDDirectoryPaths: []string{"../../config/crd"}, ErrorIfCRDPathMissing: true}
	cfg, e := env.Start()
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := env.Stop(); e != nil {
			t.Error(e)
		}
	}()
	scheme := runtime.NewScheme()
	_ = api.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	db, e := client.New(cfg, client.Options{Scheme: scheme})
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = db.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "room-pass"}}); e != nil {
		t.Fatal(e)
	}
	room := &api.Room{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "room-pass"}, Spec: api.RoomSpec{Title: "Demo", EndsAt: metav1.NewTime(time.Now().Add(time.Hour)), MaxParticipants: 3, AudienceGroup: "demo:test", AllowedReturnURLs: []string{"https://demo.test/app/"}, Enrollment: "Open"}}
	if e = db.Create(ctx, room); e != nil {
		t.Fatal(e)
	}
	key := client.ObjectKeyFromObject(room)
	if e = db.Get(ctx, key, room); e != nil {
		t.Fatal(e)
	}
	if room.Spec.JoinCode.ValidFor != "30s" {
		t.Fatalf("defaults missing: %+v", room.Spec.JoinCode)
	}
	for _, mutate := range []func(*api.Room){func(r *api.Room) { r.Spec.Enrollment = "Maybe" }, func(r *api.Room) { r.Spec.AudienceGroup = "system:masters" }, func(r *api.Room) { r.Spec.AllowedReturnURLs = []string{"https://evil.test/"} }, func(r *api.Room) { r.Spec.JoinCode.ValidFor = "60s" }} {
		bad := room.DeepCopy()
		mutate(bad)
		if db.Update(ctx, bad) == nil {
			t.Fatal("invalid update accepted")
		}
	}
	rec := &controller.Reconciler{Client: db, Room: key}
	if _, e = rec.Reconcile(ctx, ctrl.Request{NamespacedName: key}); e != nil {
		t.Fatal(e)
	}
	_ = db.Get(ctx, key, room)
	if room.Status.JoinCode == nil {
		t.Fatal("code not persisted")
	}
	code := room.Status.JoinCode.Code
	rv := room.ResourceVersion
	if _, e = rec.Reconcile(ctx, ctrl.Request{NamespacedName: key}); e != nil {
		t.Fatal(e)
	}
	_ = db.Get(ctx, key, room)
	if room.ResourceVersion != rv || room.Status.JoinCode.Code != code {
		t.Fatal("reconcile not idempotent")
	}
	room.Spec.Stopped = true
	if e = db.Update(ctx, room); e != nil {
		t.Fatal(e)
	}
	room.Spec.Stopped = false
	if db.Update(ctx, room) == nil {
		t.Fatal("stop reversed")
	}
	p := &api.Participant{ObjectMeta: metav1.ObjectMeta{Name: "p-test", Namespace: room.Namespace}, Spec: api.ParticipantSpec{RoomRef: api.RoomRef{Name: room.Name, UID: string(room.UID)}, DisplayName: "Ada"}}
	if e = db.Create(ctx, p); e != nil {
		t.Fatal(e)
	}
	p.Spec.Revoked = true
	if e = db.Update(ctx, p); e != nil {
		t.Fatal(e)
	}
	p.Spec.Revoked = false
	if db.Update(ctx, p) == nil {
		t.Fatal("revocation reversed")
	}
}
