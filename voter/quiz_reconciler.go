package main

// The controller that makes a round's result a field on the round.
//
// It watches the KUBERNETES API, not this application's write path, and that is
// the property to be deliberate about rather than to get by accident: a ballot
// pasted with kubectl, applied by Flux, or created by anything else holding a
// token produces a watch event exactly like one typed on a phone, and the
// projected number moves without the application having been involved at all.
// That is the talk's own argument, performed rather than asserted --
// docs/live-results-design.md, and TestAnyWriterMovesTheTally pins it.
//
// It runs as the ServiceAccount and writes ONLY quizsessions/status. The
// subresource split is worth having for its own sake: it can publish the result
// without being able to edit the round.

import (
	"context"
	"encoding/json"
	"log"
	"reflect"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type quizReconciler struct {
	dynamic   dynamic.Interface
	namespace string
	// coalesce is the window a burst collapses into. A 300-vote burst must not
	// be 300 status writes: the first event schedules a recompute, and every
	// event that arrives before it fires is already accounted for by it.
	coalesce time.Duration
	// resync recomputes every round, so a status write that failed, or an event
	// nobody saw, heals without a restart. The watch is the mechanism; this is
	// the backstop, not a poll wearing a watch's clothes.
	//
	// It is also the heartbeat behind "as of 13:22:41" on the projector: a LIVE
	// round nobody is voting in still gets its lastTallyTime moved, so a
	// timestamp that has stopped means the controller has, which is the one thing
	// that tells a dead tally apart from a room that has finished voting.
	//
	// Only a live one. A status write costs an event on every open stream -- two
	// hundred phones -- and a closed or draft round has nobody watching its
	// timestamp to reassure. See sameTally.
	resync time.Duration
	now    func() time.Time

	queue workqueue.TypedRateLimitingInterface[string]
	// Two caches, and the reconciler reads BOTH rather than calling the API:
	// ballots to count, and rounds for the questions to count them against. The
	// rounds watch also means a round opened on the operator's screen is tallied
	// at once -- without it a brand-new round shows no count at all until the
	// resync below comes round, which on stage is a card that says nothing.
	ballots cache.SharedIndexInformer
	rounds  cache.SharedIndexInformer
}

func newQuizReconciler(client dynamic.Interface, namespace string) *quizReconciler {
	r := &quizReconciler{
		dynamic:   client,
		namespace: namespace,
		coalesce:  time.Second,
		resync:    time.Minute,
		now:       time.Now,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: "quiz-tally"},
		),
	}
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, namespace, nil)
	r.ballots = factory.ForResource(quizSubmissions).Informer()
	r.rounds = factory.ForResource(quizSessions).Informer()
	// Every ballot event, in both directions. A deleted ballot has to follow the
	// count back down -- the reset between runs deletes every submission, and
	// the tally has to reach zero on its own.
	r.watch(r.ballots, ballotRound, func(_, _ *unstructured.Unstructured) bool { return true })
	// Rounds are watched for their QUESTIONS, and so that a round opened on the
	// operator's screen is tallied at once rather than at the next resync.
	//
	// The update rule is not decoration. This controller WRITES a round's status,
	// which arrives back here as an update -- and because lastTallyTime moves
	// every time, a status write would enqueue the round that produced it, for
	// ever. metadata.generation is what cuts that: with the status subresource in
	// place it moves only when the SPEC changes, so a tally can never trigger the
	// next one.
	r.watch(r.rounds,
		func(obj *unstructured.Unstructured) string { return obj.GetName() },
		func(old, updated *unstructured.Unstructured) bool {
			return old.GetGeneration() != updated.GetGeneration()
		})
	return r
}

// watch enqueues the round each event concerns. changed decides which updates
// are worth a recompute; adds and deletes always are.
func (r *quizReconciler) watch(
	informer cache.SharedIndexInformer,
	roundOf func(*unstructured.Unstructured) string,
	changed func(old, updated *unstructured.Unstructured) bool,
) {
	object := func(obj any) *unstructured.Unstructured {
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = tombstone.Obj
		}
		typed, _ := obj.(*unstructured.Unstructured)
		return typed
	}
	enqueue := func(obj any) {
		typed := object(obj)
		if typed == nil {
			return
		}
		if round := roundOf(typed); round != "" {
			r.queue.AddAfter(round, r.coalesce)
		}
	}
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: enqueue,
		UpdateFunc: func(old, updated any) {
			before, after := object(old), object(updated)
			if before == nil || after == nil || changed(before, after) {
				enqueue(updated)
			}
		},
		DeleteFunc: enqueue,
	})
	if err != nil {
		// AddEventHandler only fails on an already-stopped informer, which this
		// one cannot be: it was created a moment ago and nothing has run it.
		log.Printf("tally: event handler: %v", err)
	}
}

// Run blocks until ctx is done. One replica, so no leader election: the
// deployment is Recreate with replicas: 1 and the runbook already calls that
// load bearing. Two replicas would both patch the same value -- wasteful rather
// than wrong -- and a lock is not worth carrying for it.
func (r *quizReconciler) Run(ctx context.Context) {
	defer r.queue.ShutDown()
	go r.ballots.Run(ctx.Done())
	go r.rounds.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), r.ballots.HasSynced, r.rounds.HasSynced) {
		return
	}
	go func() {
		<-ctx.Done()
		r.queue.ShutDown()
	}()
	go r.resyncLoop(ctx)
	for {
		round, shutdown := r.queue.Get()
		if shutdown {
			return
		}
		err := r.reconcile(ctx, round)
		switch {
		case err == nil || apierrors.IsNotFound(err):
			// A round that is gone has no status to write, and nothing to retry.
			r.queue.Forget(round)
		default:
			// Rate limited, so a round whose status write keeps failing does not
			// spin: the resync below still picks it up.
			log.Printf("tally: round %s: %v", round, err)
			r.queue.AddRateLimited(round)
		}
		r.queue.Done(round)
	}
}

func (r *quizReconciler) resyncLoop(ctx context.Context) {
	ticker := time.NewTicker(r.resync)
	defer ticker.Stop()
	for {
		for _, round := range cachedObjects(r.rounds) {
			r.queue.Add(round.GetName())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func cachedObjects(informer cache.SharedIndexInformer) []*unstructured.Unstructured {
	items := informer.GetStore().List()
	objects := make([]*unstructured.Unstructured, 0, len(items))
	for _, item := range items {
		if object, ok := item.(*unstructured.Unstructured); ok {
			objects = append(objects, object)
		}
	}
	return objects
}

// reconcile recomputes one round from the two caches and patches its status.
//
// Neither read goes to the API server: the watches this process already holds
// are the cheapest and most current copy of both, which is what lets a 300-vote
// burst cost one status write per second rather than 300 list calls.
func (r *quizReconciler) reconcile(ctx context.Context, round string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	obj, exists, err := r.rounds.GetStore().GetByKey(r.namespace + "/" + round)
	if err != nil || !exists {
		// Deleted, or never seen. There is no status to write and nothing to
		// retry -- the tally goes with the object.
		return err
	}
	cached, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil
	}
	spec, err := decodeQuizSpec(cached)
	if err != nil {
		return err
	}
	status := tallyRound(round, spec.Questions, cachedObjects(r.ballots)).status(cached.GetGeneration(), r.now())
	// A status write is not free to anyone watching. It moves the object's
	// resourceVersion, which pushes an event down every open stream -- two
	// hundred phones, for a round nobody is voting in. lastTallyTime moves on
	// every pass by construction, so without this the sixty-second resync
	// rewrites every round in the namespace for ever.
	//
	// Live rounds keep the heartbeat anyway. The "as of 13:22:41" on the
	// projector is what tells a dead tally apart from a room that has finished
	// voting, and that distinction is only worth anything while a round is open
	// -- which is also the only time anybody is looking at it. A closed or draft
	// round goes quiet, and there is no audience to miss it.
	if spec.State != "live" && sameTally(cached, status) {
		return nil
	}
	// The status a merge patch replaces wholesale: questions is an atomic list
	// in the CRD, so a choice that drops back to zero when its ballot is deleted
	// disappears with it rather than lingering from the previous tally.
	patch, err := json.Marshal(map[string]any{"status": status})
	if err != nil {
		return err
	}
	_, err = r.dynamic.Resource(quizSessions).Namespace(r.namespace).
		Patch(ctx, round, types.MergePatchType, patch, metav1.PatchOptions{}, "status")
	return err
}

// sameTally reports whether the round already carries this result, ignoring the
// one field that moves on every pass.
//
// It compares the SERIALIZED status rather than the decoded one, because that is
// what the patch would send and therefore what the API server would compare: a
// difference this cannot see is a difference that would not have been written.
func sameTally(cached *unstructured.Unstructured, next quizStatus) bool {
	stored, found, err := unstructured.NestedFieldNoCopy(cached.Object, "status")
	if err != nil || !found {
		return false
	}
	// Both sides through JSON, not just the proposed one. The stored status came
	// out of the API server's decoder, where every whole number is an int64; the
	// proposed one is a Go struct full of ints. reflect.DeepEqual is perfectly
	// happy to call those different for ever, and the symptom is not a wrong
	// number on screen -- it is the write this function exists to skip, never
	// being skipped.
	normalized := func(value any) (map[string]any, bool) {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		var out map[string]any
		if err := json.Unmarshal(encoded, &out); err != nil {
			return nil, false
		}
		delete(out, "lastTallyTime")
		return out, true
	}
	current, currentOK := normalized(stored)
	proposed, proposedOK := normalized(next)
	return currentOK && proposedOK && reflect.DeepEqual(current, proposed)
}
