package main

// The controller that puts a seat's last why on the seat.
//
// A phone claiming or changing a seat sends its why ahead as a CommitRequest,
// labelled with the seat it is for (frontend/src/api/seats.ts). gitops-reverser
// commits the seat with that message and reports the commit on the request's
// status. This copies the last such commit onto the Seat's status.lastCommit,
// so `kubectl get seats` says why each seat looks the way it does.
//
// The label is the phone's word and proves nothing: anyone may create a request
// naming anyone's seat. The request's status.author is not the phone's word --
// gitops-reverser fills it in from the audit event of the write it committed --
// so a request counts only when its author is the person who claimed the seat.
//
// It runs as the ServiceAccount, reads commitrequests and seats, and writes ONLY
// seats/status. A status write moves no generation and gitops-reverser strips
// status before it writes, so neither this watch nor the audit trail sees it as
// a change to the seat.

import (
	"context"
	"encoding/json"
	"log"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

var (
	seats          = schema.GroupVersionResource{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "seats"}
	commitRequests = schema.GroupVersionResource{Group: "configbutler.ai", Version: "v1alpha3", Resource: "commitrequests"}
)

// seatCommitLabel names the Seat a CommitRequest is for. The phone sets it
// (seats.ts, SEAT_COMMIT_LABEL); the informer below sees no other request.
const seatCommitLabel = "voter.configbutler.ai/seat"

// seatCommit is status.lastCommit.
type seatCommit struct {
	Message       string `json:"message,omitempty"`
	Commit        string `json:"commit,omitempty"`
	CommittedAt   string `json:"committedAt,omitempty"`
	CommitRequest string `json:"commitRequest,omitempty"`
}

type seatReconciler struct {
	dynamic   dynamic.Interface
	namespace string
	// target is the seats GitTarget. A request naming another is not a seat's.
	target string

	queue    workqueue.TypedRateLimitingInterface[string]
	requests cache.SharedIndexInformer
	seats    cache.SharedIndexInformer
}

// newSeatReconciler watches seats in namespace and their CommitRequests in
// requestNamespace, which is the GitTarget's (CONFIGBUTLER_COMMITREQUEST_NAMESPACE,
// else the same one).
func newSeatReconciler(client dynamic.Interface, namespace, requestNamespace, target string) *seatReconciler {
	r := &seatReconciler{
		dynamic:   client,
		namespace: namespace,
		target:    target,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: "seat-commit"},
		),
	}
	labelled := func(options *metav1.ListOptions) { options.LabelSelector = seatCommitLabel }
	r.requests = dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, requestNamespace, labelled).
		ForResource(commitRequests).Informer()
	r.seats = dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, namespace, nil).
		ForResource(seats).Informer()

	// Every request event: the one that matters is the update that reports the
	// commit, and a request is small and short-lived.
	r.handle(r.requests, func(obj *unstructured.Unstructured) string {
		return obj.GetLabels()[seatCommitLabel]
	}, true)
	// A seat's ADD only: the request can finish before this cache has the seat
	// it claimed. Its updates are colour and emoji, which a request already
	// covers, or this controller's own status write, which must not come back.
	r.handle(r.seats, func(obj *unstructured.Unstructured) string { return obj.GetName() }, false)
	return r
}

func (r *seatReconciler) handle(
	informer cache.SharedIndexInformer,
	seatOf func(*unstructured.Unstructured) string,
	updates bool,
) {
	enqueue := func(obj any) {
		if typed, ok := obj.(*unstructured.Unstructured); ok {
			if seat := seatOf(typed); seat != "" {
				r.queue.Add(seat)
			}
		}
	}
	handler := cache.ResourceEventHandlerFuncs{AddFunc: enqueue}
	if updates {
		handler.UpdateFunc = func(_, updated any) { enqueue(updated) }
	}
	if _, err := informer.AddEventHandler(handler); err != nil {
		// As in the tally: only an already-stopped informer refuses, and this one
		// has not been started.
		log.Printf("seat-commit: event handler: %v", err)
	}
}

// Run blocks until ctx is done. One replica, as for the tally.
func (r *seatReconciler) Run(ctx context.Context) {
	defer r.queue.ShutDown()
	go r.requests.Run(ctx.Done())
	go r.seats.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), r.requests.HasSynced, r.seats.HasSynced) {
		return
	}
	go func() {
		<-ctx.Done()
		r.queue.ShutDown()
	}()
	for {
		seat, shutdown := r.queue.Get()
		if shutdown {
			return
		}
		err := r.reconcile(ctx, seat)
		switch {
		case err == nil || apierrors.IsNotFound(err):
			r.queue.Forget(seat)
		default:
			log.Printf("seat-commit: seat %s: %v", seat, err)
			r.queue.AddRateLimited(seat)
		}
		r.queue.Done(seat)
	}
}

// reconcile writes the seat's last committed why, from the two caches.
func (r *seatReconciler) reconcile(ctx context.Context, name string) error {
	obj, exists, err := r.seats.GetStore().GetByKey(r.namespace + "/" + name)
	if err != nil || !exists {
		// Not claimed yet, or no longer: the seat's ADD brings it back here.
		return err
	}
	seat, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil
	}
	latest, found := lastSeatCommit(seat, r.target, cachedObjects(r.requests))
	if !found {
		// Requests are deleted after a while (configbutler.ai/delete-after), and
		// the why they carried stays on the seat. Nothing found clears nothing.
		return nil
	}
	current := storedSeatCommit(seat)
	if current == latest || current.CommittedAt > latest.CommittedAt {
		return nil
	}
	patch, err := json.Marshal(map[string]any{"status": map[string]any{"lastCommit": latest}})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err = r.dynamic.Resource(seats).Namespace(r.namespace).
		Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}, "status")
	return err
}

// lastSeatCommit picks the newest request that committed this seat's why: for
// this seat, on the seats target, authored by whoever claimed it, and
// committed no earlier than the seat was created -- a deleted and reclaimed
// seat starts over, even for the same person.
//
// committedAt is the Ready condition's transition, in RFC 3339 seconds, so it
// compares as a string. A tie goes to the later request name, which is at
// least deterministic.
func lastSeatCommit(seat *unstructured.Unstructured, target string, requests []*unstructured.Unstructured) (seatCommit, bool) {
	owner, _, _ := unstructured.NestedString(seat.Object, "spec", "claimedBy", "username")
	created := seat.GetCreationTimestamp().UTC().Format(time.RFC3339)
	var best seatCommit
	found := false
	for _, request := range requests {
		if request.GetLabels()[seatCommitLabel] != seat.GetName() {
			continue
		}
		if ref, _, _ := unstructured.NestedString(request.Object, "spec", "gitTargetRef", "name"); ref != target {
			continue
		}
		if author, _, _ := unstructured.NestedString(request.Object, "status", "author"); owner == "" || author != owner {
			continue
		}
		commit, _, _ := unstructured.NestedString(request.Object, "status", "commit")
		committedAt, ready := readyTransition(request)
		if commit == "" || !ready || committedAt < created {
			continue
		}
		message, _, _ := unstructured.NestedString(request.Object, "spec", "message")
		candidate := seatCommit{
			Message:       message,
			Commit:        commit,
			CommittedAt:   committedAt,
			CommitRequest: request.GetName(),
		}
		if !found || candidate.CommittedAt > best.CommittedAt ||
			(candidate.CommittedAt == best.CommittedAt && candidate.CommitRequest > best.CommitRequest) {
			best, found = candidate, true
		}
	}
	return best, found
}

// readyTransition is when the request's Ready condition turned True, normalised
// to UTC so it compares with the seat's creation time.
func readyTransition(request *unstructured.Unstructured) (string, bool) {
	conditions, _, _ := unstructured.NestedSlice(request.Object, "status", "conditions")
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok || condition["type"] != "Ready" || condition["status"] != "True" {
			continue
		}
		at, _ := condition["lastTransitionTime"].(string)
		parsed, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return "", false
		}
		return parsed.UTC().Format(time.RFC3339), true
	}
	return "", false
}

func storedSeatCommit(seat *unstructured.Unstructured) seatCommit {
	stored, _, _ := unstructured.NestedStringMap(seat.Object, "status", "lastCommit")
	return seatCommit{
		Message:       stored["message"],
		Commit:        stored["commit"],
		CommittedAt:   stored["committedAt"],
		CommitRequest: stored["commitRequest"],
	}
}
