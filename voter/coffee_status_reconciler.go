package main

// The controller that keeps the menu's status: which version of the menu Voter has
// looked at, whether the coffee bar can deliver what that menu promises, and when
// the first order came in. docs/coffee-status-design.md is the design.
//
// It is the only writer of the CoffeeConfig's status. It runs as the
// ServiceAccount, may watch the menu and the two key Secrets by name, and writes
// ONLY coffeeconfigs/status. A status write moves no generation and gitops-reverser
// strips status before it writes, so it is no change to the menu. It does move the
// resourceVersion, and every reader of the menu streams it, so it writes only when
// an answer changes, and there are no counters here: 200 orders are two writes, not
// 200 on each of 200 phones.
//
// About a key it keeps one bit, on purpose. The one person who may replace a key
// holds `patch` on its Secret and never `get`, so the menu page cannot find out for
// itself whether a key is there; this says so on status.keys. Nothing else derived
// from a key is safe to show, since people type short words and a short word is
// found from its hash, its length or its last characters in seconds
// (docs/coffee-api-keys-design.md). withoutKeyValue drops the value before anything
// is cached, so what this process holds on to is the bit too.
//
// Not in a shop-only deployment, which has no client or no grant for any of this
// (main.go): there the menu comes as it is, and nobody is looking at an object.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

var (
	coffeeConfigResource = schema.GroupVersionResource{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "coffeeconfigs"}
	secretResource       = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
)

// coffeeKeySecrets is each key's place in status.keys and the Secret it is in.
// The CRD allows these names and no others.
var coffeeKeySecrets = map[string]string{"mail": "coffee-mail", "payments": "coffee-payments"}

// The condition types and reasons a reader may rely on. All positive: True is the
// good state. The voucher reasons are the names an order is refused under
// (coffee_logic.go).
const (
	coffeeConditionReady    = "Ready"
	coffeeConditionMenu     = "MenuAvailable"
	coffeeConditionKeys     = "KeysAvailable"
	coffeeConditionVouchers = "VouchersAvailable"

	coffeeReasonShopReady          = "ShopReady"
	coffeeReasonProductsEnabled    = "ProductsEnabled"
	coffeeReasonNoEnabledProducts  = "NoEnabledProducts"
	coffeeReasonAllKeysSet         = "AllKeysSet"
	coffeeReasonNoKeysRequired     = "NoKeysRequired"
	coffeeReasonMailKeyMissing     = "MailKeyMissing"
	coffeeReasonPaymentsKeyMissing = "PaymentsKeyMissing"
	coffeeReasonKeysMissing        = "KeysMissing"
	coffeeReasonVouchersRedeemable = "VouchersRedeemable"
	coffeeReasonNoVouchers         = "NoVouchers"
)

// coffeeStatusResync catches what no event announces, and heals a write that
// failed.
const coffeeStatusResync = time.Minute

// coffeeStatus is the CoffeeConfig's status, whole.
type coffeeStatus struct {
	ObservedGeneration int64                      `json:"observedGeneration,omitempty"`
	FirstOrderTime     *metav1.Time               `json:"firstOrderTime,omitempty"`
	Keys               map[string]coffeeKeyStatus `json:"keys,omitempty"`
	Conditions         []metav1.Condition         `json:"conditions,omitempty"`
}

type coffeeKeyStatus struct {
	Set bool `json:"set"`
}

type coffeeStatusReconciler struct {
	dynamic   dynamic.Interface
	namespace string
	config    string
	vouchers  *voucherLedger
	secrets   map[string]cache.SharedIndexInformer
	// menu only says that something moved: reconcile reads the object itself, so
	// a late echo can neither write twice nor replace a first order with a later one.
	menu    cache.SharedIndexInformer
	changed chan struct{}
	// coalesce is the window a burst collapses into, as for the tally: a room
	// saving together is one status write.
	coalesce time.Duration
	now      func() time.Time

	// cleared says this process has already emptied firstOrderTime. It says "the
	// first order since this Voter started", because a restart also forgives every
	// redemption and empties the order feed. Only Run's goroutine touches it.
	cleared bool

	mu sync.Mutex
	// firstOrder is the earliest order placed that the status does not say yet.
	firstOrder *time.Time
}

// newCoffeeStatusReconciler watches the menu and the two Secrets in namespace, an
// informer for each because RBAC only allows a list or watch by name when it asks
// for one name.
func newCoffeeStatusReconciler(client dynamic.Interface, namespace, config string, vouchers *voucherLedger) *coffeeStatusReconciler {
	r := &coffeeStatusReconciler{
		dynamic:   client,
		namespace: namespace,
		config:    config,
		vouchers:  vouchers,
		secrets:   map[string]cache.SharedIndexInformer{},
		changed:   make(chan struct{}, 1),
		coalesce:  time.Second,
		now:       time.Now,
	}
	notify := func(any) { r.poke() }
	handlers := cache.ResourceEventHandlerFuncs{
		AddFunc:    notify,
		UpdateFunc: func(_, updated any) { notify(updated) },
		DeleteFunc: notify,
	}
	informerFor := func(resource schema.GroupVersionResource, name string) cache.SharedIndexInformer {
		byName := func(options *metav1.ListOptions) { options.FieldSelector = "metadata.name=" + name }
		return dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, namespace, byName).
			ForResource(resource).Informer()
	}
	for section, name := range coffeeKeySecrets {
		informer := informerFor(secretResource, name)
		// As in the seat controller, only an informer already started refuses
		// either of these, and this one has not been.
		if err := informer.SetTransform(withoutKeyValue); err != nil {
			log.Printf("coffee-status: transform: %v", err)
		}
		if _, err := informer.AddEventHandler(handlers); err != nil {
			log.Printf("coffee-status: event handler: %v", err)
		}
		r.secrets[section] = informer
	}
	r.menu = informerFor(coffeeConfigResource, config)
	if _, err := r.menu.AddEventHandler(handlers); err != nil {
		log.Printf("coffee-status: event handler: %v", err)
	}
	return r
}

func (r *coffeeStatusReconciler) poke() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

// orderPlaced tells the status an order was placed. It never blocks and cannot
// fail an order. A nil reconciler is a deployment without one, which is every
// shop-only deployment, so there is nothing to tell.
func (r *coffeeStatusReconciler) orderPlaced(at time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.firstOrder == nil {
		r.firstOrder = &at
	}
	r.mu.Unlock()
	r.poke()
}

// withoutKeyValue is all of a Secret this process keeps: its identity and whether
// it holds a key. Base64 of a value is empty only for an empty value.
//
// client-go may run this twice over one object: a watch-list (API servers with
// WatchList on, not the fixture's) transforms into a temporary store and then
// again into the informer's. The second pass finds the bit, not the value, and
// must leave it: reading it as no value would call every set key not set.
func withoutKeyValue(obj any) (any, error) {
	secret, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return obj, nil
	}
	if _, done := secret.Object["set"]; done {
		return secret, nil
	}
	value, _, _ := unstructured.NestedString(secret.Object, "data", "apiKey")
	return &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{
			"name":            secret.GetName(),
			"namespace":       secret.GetNamespace(),
			"resourceVersion": secret.GetResourceVersion(),
		},
		"set": value != "",
	}}, nil
}

// Run blocks until ctx is done. One replica, as for the tally. Nothing is written
// until it has read the menu and both Secrets, so it knows or it says nothing: there
// is no Unknown here, and a stale observedGeneration says that it has stopped.
func (r *coffeeStatusReconciler) Run(ctx context.Context) {
	synced := make([]cache.InformerSynced, 0, len(r.secrets)+1)
	for _, informer := range r.secrets {
		go informer.Run(ctx.Done())
		synced = append(synced, informer.HasSynced)
	}
	go r.menu.Run(ctx.Done())
	synced = append(synced, r.menu.HasSynced)
	if !cache.WaitForCacheSync(ctx.Done(), synced...) {
		return
	}
	ticker := time.NewTicker(coffeeStatusResync)
	defer ticker.Stop()
	for {
		if err := r.reconcile(ctx); err != nil {
			log.Printf("coffee-status: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.changed:
			select {
			case <-ctx.Done():
				return
			case <-time.After(r.coalesce):
			}
			// All accounted for by the read that follows.
			select {
			case <-r.changed:
			default:
			}
		}
	}
}

// reconcile writes the status if it should say anything other than it does.
func (r *coffeeStatusReconciler) reconcile(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client := r.dynamic.Resource(coffeeConfigResource).Namespace(r.namespace)
	object, err := client.Get(ctx, r.config, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	config, err := toCoffeeConfig(object)
	if err != nil {
		return err
	}
	stored, err := readCoffeeStatus(object)
	if err != nil {
		return err
	}

	r.mu.Lock()
	pending := r.firstOrder
	r.mu.Unlock()
	previous := stored
	if !r.cleared {
		previous.FirstOrderTime = nil
	}
	if previous.FirstOrderTime == nil && pending != nil {
		at := metav1.NewTime(*pending)
		previous.FirstOrderTime = &at
	}

	want := computeCoffeeStatus(config.Metadata.Generation, config.Spec, previous, r.keyBits(), r.vouchers, metav1.NewTime(r.now()))
	patch, err := coffeeStatusPatch(stored, want)
	if err != nil {
		return err
	}
	if patch != nil {
		if _, err := client.Patch(ctx, r.config, types.MergePatchType, patch, metav1.PatchOptions{}, "status"); err != nil {
			return err
		}
	}

	r.cleared = true
	r.mu.Lock()
	if r.firstOrder == pending {
		r.firstOrder = nil
	}
	r.mu.Unlock()
	return nil
}

func readCoffeeStatus(object *unstructured.Unstructured) (coffeeStatus, error) {
	var status coffeeStatus
	stored, found, err := unstructured.NestedMap(object.Object, "status")
	if err != nil || !found {
		return status, err
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return status, err
	}
	if err := json.Unmarshal(encoded, &status); err != nil {
		return status, fmt.Errorf("failed to decode the menu's status: %w", err)
	}
	return status, nil
}

// coffeeStatusPatch is the merge patch from what is stored to want, or nil when
// they say the same. Comparing what would be written is what ends the loop: this
// write's own watch event finds nothing to do. A time that goes has to be said
// with a null, which is how a merge patch removes a field.
func coffeeStatusPatch(stored, want coffeeStatus) ([]byte, error) {
	before, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	after, err := json.Marshal(want)
	if err != nil {
		return nil, err
	}
	if string(before) == string(after) {
		return nil, nil
	}
	var fields map[string]any
	if err := json.Unmarshal(after, &fields); err != nil {
		return nil, err
	}
	if stored.FirstOrderTime != nil && want.FirstOrderTime == nil {
		fields["firstOrderTime"] = nil
	}
	return json.Marshal(map[string]any{"status": fields})
}

// keyBits is whether each key has a value. A Secret that is not there holds no key.
func (r *coffeeStatusReconciler) keyBits() map[string]coffeeKeyStatus {
	keys := map[string]coffeeKeyStatus{}
	for section, informer := range r.secrets {
		set := false
		for _, obj := range informer.GetStore().List() {
			if secret, ok := obj.(*unstructured.Unstructured); ok {
				set, _, _ = unstructured.NestedBool(secret.Object, "set")
			}
		}
		keys[section] = coffeeKeyStatus{Set: set}
	}
	return keys
}

// computeCoffeeStatus is the whole judgement, and like placeCoffeeOrder a pure
// function, so it is tested without an API server. previous supplies the conditions
// whose lastTransitionTime is kept while their status stands, and the first order.
func computeCoffeeStatus(generation int64, spec coffeeConfigSpec, previous coffeeStatus, keys map[string]coffeeKeyStatus, vouchers *voucherLedger, now metav1.Time) coffeeStatus {
	parts := []metav1.Condition{
		menuCondition(spec),
		keysCondition(spec, keys),
		vouchersCondition(spec, vouchers),
	}
	ready := metav1.Condition{
		Type:    coffeeConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  coffeeReasonShopReady,
		Message: "The coffee bar can deliver what its menu says.",
	}
	for _, part := range parts {
		if part.Status != metav1.ConditionTrue {
			ready.Status, ready.Reason, ready.Message = metav1.ConditionFalse, part.Reason, part.Message
			break
		}
	}

	status := coffeeStatus{
		ObservedGeneration: generation,
		FirstOrderTime:     previous.FirstOrderTime,
		Keys:               keys,
		Conditions:         slices.Clone(previous.Conditions),
	}
	for _, condition := range append([]metav1.Condition{ready}, parts...) {
		condition.LastTransitionTime = now
		meta.SetStatusCondition(&status.Conditions, condition)
	}
	return status
}

func menuCondition(spec coffeeConfigSpec) metav1.Condition {
	enabled := 0
	for _, product := range spec.Products {
		if product.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return metav1.Condition{
			Type: coffeeConditionMenu, Status: metav1.ConditionFalse, Reason: coffeeReasonNoEnabledProducts,
			Message: "No product on the menu can be ordered.",
		}
	}
	return metav1.Condition{
		Type: coffeeConditionMenu, Status: metav1.ConditionTrue, Reason: coffeeReasonProductsEnabled,
		Message: fmt.Sprintf("%d of %d products can be ordered.", enabled, len(spec.Products)),
	}
}

// keysCondition says a value is there, never that it works: that is all anything
// here can know. A key counts only when the menu names it, so an empty Secret is
// no fault on a menu without that provider. It is made from the bits in status.keys,
// so it says nothing about a key those do not. A menu stored before the CRD tied a
// provider to its reference may have only the provider, and needs its key as much.
func keysCondition(spec coffeeConfigSpec, keys map[string]coffeeKeyStatus) metav1.Condition {
	named := func(provider string, ref *secretKeyRef) bool {
		return strings.TrimSpace(provider) != "" || ref != nil
	}
	mailNamed := named(spec.Mail.Provider, spec.Mail.APIKeySecretRef)
	paymentsNamed := named(spec.Payments.Provider, spec.Payments.APIKeySecretRef)
	mailMissing := mailNamed && !keys["mail"].Set
	paymentsMissing := paymentsNamed && !keys["payments"].Set

	condition := metav1.Condition{Type: coffeeConditionKeys, Status: metav1.ConditionFalse}
	switch {
	case mailMissing && paymentsMissing:
		condition.Reason, condition.Message = coffeeReasonKeysMissing, "The mail key and the payments key the menu names have no value."
	case mailMissing:
		condition.Reason, condition.Message = coffeeReasonMailKeyMissing, "The mail key the menu names has no value."
	case paymentsMissing:
		condition.Reason, condition.Message = coffeeReasonPaymentsKeyMissing, "The payments key the menu names has no value."
	case !mailNamed && !paymentsNamed:
		condition.Status, condition.Reason, condition.Message = metav1.ConditionTrue, coffeeReasonNoKeysRequired, "The menu names no keys."
	default:
		condition.Status, condition.Reason, condition.Message = metav1.ConditionTrue, coffeeReasonAllKeysSet, "Every key the menu names has a value."
	}
	return condition
}

// vouchersCondition says whether the menu can keep its promises, and states the
// limit where it cannot: that is in the spec. It never states how many were used,
// and it does not need to. Depletion is worked out as the till works it out, so the
// status knows at the last allowance, before the next person is refused, and it says
// what Voter will do now: a restart forgives redemptions, and then this is True again.
func vouchersCondition(spec coffeeConfigSpec, vouchers *voucherLedger) metav1.Condition {
	var depleted, notApplicable []string
	enabled := 0
	for _, voucher := range spec.Vouchers {
		if !voucher.Enabled {
			continue
		}
		enabled++
		if vouchers.depleted(voucher.Code, voucher.MaximumUsage) {
			depleted = append(depleted, fmt.Sprintf("Voucher %s has reached its maximumUsage of %d.", voucher.Code, voucher.MaximumUsage))
		}
		if !voucherAppliesToAnyEnabledProduct(voucher, spec.Products) {
			notApplicable = append(notApplicable, fmt.Sprintf("Voucher %s applies to no product that can be ordered.", voucher.Code))
		}
	}
	message := strings.Join(append(depleted, notApplicable...), " ")
	switch {
	case len(depleted) > 0:
		return metav1.Condition{Type: coffeeConditionVouchers, Status: metav1.ConditionFalse, Reason: coffeeFailureVoucherDepleted, Message: message}
	case len(notApplicable) > 0:
		return metav1.Condition{Type: coffeeConditionVouchers, Status: metav1.ConditionFalse, Reason: coffeeFailureVoucherNotApplicable, Message: message}
	case enabled == 0:
		return metav1.Condition{Type: coffeeConditionVouchers, Status: metav1.ConditionTrue, Reason: coffeeReasonNoVouchers, Message: "No voucher is enabled."}
	}
	return metav1.Condition{Type: coffeeConditionVouchers, Status: metav1.ConditionTrue, Reason: coffeeReasonVouchersRedeemable, Message: "Every enabled voucher can be honoured."}
}
