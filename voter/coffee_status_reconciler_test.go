package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func coffeeStatusFixture(spec, status map[string]any) *dynamicfake.FakeDynamicClient {
	config := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "examples.configbutler.ai/v1alpha1", "kind": "CoffeeConfig",
		"metadata": map[string]any{"name": "demo-coffee", "namespace": "voter", "generation": int64(7)},
	}}
	if spec != nil {
		config.Object["spec"] = spec
	}
	if status != nil {
		config.Object["status"] = status
	}
	gvrToList := map[schema.GroupVersionResource]string{
		coffeeConfigResource: "CoffeeConfigList",
		secretResource:       "SecretList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToList, config)
}

func newStatusReconciler(client dynamic.Interface, ledger *voucherLedger) *coffeeStatusReconciler {
	if ledger == nil {
		ledger = newVoucherLedger()
	}
	return newCoffeeStatusReconciler(client, "voter", "demo-coffee", ledger)
}

func storeSecret(t *testing.T, r *coffeeStatusReconciler, section, name, value string) {
	t.Helper()
	kept, err := withoutKeyValue(&unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": name, "namespace": "voter"},
		"data":     map[string]any{"apiKey": value},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.secrets[section].GetStore().Add(kept); err != nil {
		t.Fatal(err)
	}
}

func statusPatches(client *dynamicfake.FakeDynamicClient) []k8stesting.PatchAction {
	var patches []k8stesting.PatchAction
	for _, action := range client.Actions() {
		if patch, ok := action.(k8stesting.PatchAction); ok && action.GetVerb() == "patch" {
			patches = append(patches, patch)
		}
	}
	return patches
}

// storedStatus reads back what the fake holds, which is the merge patch applied.
func storedStatus(t *testing.T, client dynamic.Interface) coffeeStatus {
	t.Helper()
	object, err := client.Resource(coffeeConfigResource).Namespace("voter").Get(context.Background(), "demo-coffee", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	status, err := readCoffeeStatus(object)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func condition(t *testing.T, status coffeeStatus, kind string) metav1.Condition {
	t.Helper()
	for _, candidate := range status.Conditions {
		if candidate.Type == kind {
			return candidate
		}
	}
	t.Fatalf("no %s condition in %+v", kind, status.Conditions)
	return metav1.Condition{}
}

func TestCoffeeStatusSaysEachKeyAndNothingOfItsValue(t *testing.T) {
	client := coffeeStatusFixture(nil, nil)
	r := newStatusReconciler(client, nil)
	storeSecret(t, r, "mail", "coffee-mail", "dGFhcnQ=")
	storeSecret(t, r, "payments", "coffee-payments", "")

	if err := r.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	patches := statusPatches(client)
	if len(patches) != 1 {
		t.Fatalf("want one patch, got %d", len(patches))
	}
	if patches[0].GetSubresource() != "status" {
		t.Fatalf("patched %q, not status", patches[0].GetSubresource())
	}
	if strings.Contains(string(patches[0].GetPatch()), "dGFhcnQ=") {
		t.Fatalf("the key's value is in the patch: %s", patches[0].GetPatch())
	}
	status := storedStatus(t, client)
	want := map[string]coffeeKeyStatus{"mail": {Set: true}, "payments": {Set: false}}
	if !jsonEqual(status.Keys, want) {
		t.Fatalf("got %v, want %v", status.Keys, want)
	}
	if status.ObservedGeneration != 7 {
		t.Fatalf("observedGeneration is %d, want 7", status.ObservedGeneration)
	}
}

// A status write moves the menu's resourceVersion under whoever is editing it, and
// its own watch event comes straight back here. Finding nothing to do is what ends
// that loop, and a new value for a key already set must say nothing new either.
func TestCoffeeStatusWritesOnlyWhenAnAnswerChanges(t *testing.T) {
	client := coffeeStatusFixture(map[string]any{
		"products": []any{map[string]any{"sku": "flat-white", "enabled": true}},
	}, nil)
	r := newStatusReconciler(client, nil)
	storeSecret(t, r, "mail", "coffee-mail", "dGFhcnQ=")

	for range 3 {
		if err := r.reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	storeSecret(t, r, "mail", "coffee-mail", "bmV3")
	if err := r.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if patches := statusPatches(client); len(patches) != 1 {
		t.Fatalf("want the one first patch, got %d", len(patches))
	}
}

func TestCoffeeStatusKeepsTheTransitionTimeWhileTheStatusStands(t *testing.T) {
	spec := coffeeConfigSpec{Products: []coffeeProductSpec{{SKU: "a", Enabled: true}}}
	first := metav1.NewTime(time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC))
	later := metav1.NewTime(first.Add(time.Hour))
	ledger := newVoucherLedger()

	one := computeCoffeeStatus(1, spec, coffeeStatus{}, nil, ledger, first)
	spec.Products = append(spec.Products, coffeeProductSpec{SKU: "b", Enabled: true})
	two := computeCoffeeStatus(2, spec, one, nil, ledger, later)

	menu := condition(t, two, coffeeConditionMenu)
	if !menu.LastTransitionTime.Equal(&first) {
		t.Fatalf("a True that stayed True moved its time to %v", menu.LastTransitionTime)
	}
	if !strings.Contains(menu.Message, "2 of 2") {
		t.Fatalf("the message did not follow the menu: %q", menu.Message)
	}
	if two.ObservedGeneration != 2 {
		t.Fatalf("observedGeneration is %d, want 2", two.ObservedGeneration)
	}

	spec.Products = []coffeeProductSpec{{SKU: "a"}}
	three := computeCoffeeStatus(3, spec, two, nil, ledger, later)
	if moved := condition(t, three, coffeeConditionMenu); !moved.LastTransitionTime.Equal(&later) {
		t.Fatalf("a change of status kept the old time: %v", moved.LastTransitionTime)
	}
}

func TestCoffeeConditions(t *testing.T) {
	product := coffeeProductSpec{SKU: "flat-white", Enabled: true}
	ref := &secretKeyRef{Name: "coffee-mail", Key: "apiKey"}
	voucher := coffeeVoucherSpec{Code: "TESTNET", Enabled: true, MaximumUsage: 1}
	bothSet := map[string]coffeeKeyStatus{"mail": {Set: true}, "payments": {Set: true}}
	noneSet := map[string]coffeeKeyStatus{"mail": {}, "payments": {}}

	cases := []struct {
		name         string
		spec         coffeeConfigSpec
		keys         map[string]coffeeKeyStatus
		redeemed     int
		kind         string
		status       metav1.ConditionStatus
		reason       string
		readyReason  string
		notInMessage string
	}{
		{
			name: "a menu with something to order", spec: coffeeConfigSpec{Products: []coffeeProductSpec{product}},
			kind: coffeeConditionMenu, status: metav1.ConditionTrue, reason: coffeeReasonProductsEnabled, readyReason: coffeeReasonShopReady,
		},
		{
			name: "every product switched off", spec: coffeeConfigSpec{Products: []coffeeProductSpec{{SKU: "x"}}},
			kind: coffeeConditionMenu, status: metav1.ConditionFalse, reason: coffeeReasonNoEnabledProducts, readyReason: coffeeReasonNoEnabledProducts,
		},
		{
			name: "no provider needs no key", spec: coffeeConfigSpec{Products: []coffeeProductSpec{product}}, keys: noneSet,
			kind: coffeeConditionKeys, status: metav1.ConditionTrue, reason: coffeeReasonNoKeysRequired, readyReason: coffeeReasonShopReady,
		},
		{
			name: "both named and set", keys: bothSet,
			spec: coffeeConfigSpec{Products: []coffeeProductSpec{product}, Mail: coffeeMailSpec{Provider: "sendgrid", APIKeySecretRef: ref}, Payments: coffeePaymentsSpec{Provider: "stripe", APIKeySecretRef: ref}},
			kind: coffeeConditionKeys, status: metav1.ConditionTrue, reason: coffeeReasonAllKeysSet, readyReason: coffeeReasonShopReady,
		},
		{
			name: "the mail key is named and empty", keys: map[string]coffeeKeyStatus{"payments": {Set: true}},
			spec: coffeeConfigSpec{Products: []coffeeProductSpec{product}, Mail: coffeeMailSpec{Provider: "sendgrid", APIKeySecretRef: ref}},
			kind: coffeeConditionKeys, status: metav1.ConditionFalse, reason: coffeeReasonMailKeyMissing, readyReason: coffeeReasonMailKeyMissing,
		},
		{
			name: "the payments key is named and empty", keys: map[string]coffeeKeyStatus{"mail": {Set: true}},
			spec: coffeeConfigSpec{Products: []coffeeProductSpec{product}, Payments: coffeePaymentsSpec{Provider: "stripe", APIKeySecretRef: ref}},
			kind: coffeeConditionKeys, status: metav1.ConditionFalse, reason: coffeeReasonPaymentsKeyMissing, readyReason: coffeeReasonPaymentsKeyMissing,
		},
		{
			name: "both named and empty", keys: noneSet,
			spec: coffeeConfigSpec{Products: []coffeeProductSpec{product}, Mail: coffeeMailSpec{Provider: "sendgrid", APIKeySecretRef: ref}, Payments: coffeePaymentsSpec{Provider: "stripe", APIKeySecretRef: ref}},
			kind: coffeeConditionKeys, status: metav1.ConditionFalse, reason: coffeeReasonKeysMissing, readyReason: coffeeReasonKeysMissing,
		},
		{
			name: "a provider on a menu older than the reference rule", keys: noneSet,
			spec: coffeeConfigSpec{Products: []coffeeProductSpec{product}, Mail: coffeeMailSpec{Provider: "sendgrid"}},
			kind: coffeeConditionKeys, status: metav1.ConditionFalse, reason: coffeeReasonMailKeyMissing, readyReason: coffeeReasonMailKeyMissing,
		},
		{
			name: "no voucher enabled", spec: coffeeConfigSpec{Products: []coffeeProductSpec{product}, Vouchers: []coffeeVoucherSpec{{Code: "OFF", MaximumUsage: 1}}},
			kind: coffeeConditionVouchers, status: metav1.ConditionTrue, reason: coffeeReasonNoVouchers, readyReason: coffeeReasonShopReady,
		},
		{
			name: "a voucher with room left", spec: coffeeConfigSpec{Products: []coffeeProductSpec{product}, Vouchers: []coffeeVoucherSpec{voucher}},
			kind: coffeeConditionVouchers, status: metav1.ConditionTrue, reason: coffeeReasonVouchersRedeemable, readyReason: coffeeReasonShopReady,
		},
		{
			name: "a voucher used up says so, without a count", redeemed: 1,
			spec: coffeeConfigSpec{Products: []coffeeProductSpec{product}, Vouchers: []coffeeVoucherSpec{voucher}},
			kind: coffeeConditionVouchers, status: metav1.ConditionFalse, reason: coffeeFailureVoucherDepleted, readyReason: coffeeFailureVoucherDepleted, notInMessage: "used",
		},
		{
			name: "a limit of zero is unlimited", redeemed: 5,
			spec: coffeeConfigSpec{Products: []coffeeProductSpec{product}, Vouchers: []coffeeVoucherSpec{{Code: "TESTNET", Enabled: true}}},
			kind: coffeeConditionVouchers, status: metav1.ConditionTrue, reason: coffeeReasonVouchersRedeemable, readyReason: coffeeReasonShopReady,
		},
		{
			name: "a voucher for nothing on the menu",
			spec: coffeeConfigSpec{Products: []coffeeProductSpec{product}, Vouchers: []coffeeVoucherSpec{{Code: "TESTNET", Enabled: true, AppliesToProducts: []string{"tea"}}}},
			kind: coffeeConditionVouchers, status: metav1.ConditionFalse, reason: coffeeFailureVoucherNotApplicable, readyReason: coffeeFailureVoucherNotApplicable,
		},
		{
			name: "the menu's fault comes before the keys' and the vouchers'", keys: noneSet, redeemed: 1,
			spec: coffeeConfigSpec{Products: []coffeeProductSpec{{SKU: "x"}}, Mail: coffeeMailSpec{Provider: "sendgrid"}, Vouchers: []coffeeVoucherSpec{voucher}},
			kind: coffeeConditionMenu, status: metav1.ConditionFalse, reason: coffeeReasonNoEnabledProducts, readyReason: coffeeReasonNoEnabledProducts,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ledger := newVoucherLedger()
			for range tc.redeemed {
				ledger.redeem("TESTNET", 0)
			}
			status := computeCoffeeStatus(3, tc.spec, coffeeStatus{}, tc.keys, ledger, metav1.Now())
			got := condition(t, status, tc.kind)
			if got.Status != tc.status || got.Reason != tc.reason {
				t.Fatalf("got %s %s (%s), want %s %s", got.Status, got.Reason, got.Message, tc.status, tc.reason)
			}
			if tc.notInMessage != "" && strings.Contains(got.Message, tc.notInMessage) {
				t.Fatalf("%q is in the message: %q", tc.notInMessage, got.Message)
			}
			ready := condition(t, status, coffeeConditionReady)
			if ready.Reason != tc.readyReason {
				t.Fatalf("Ready's reason is %s, want %s", ready.Reason, tc.readyReason)
			}
			if wantTrue := tc.readyReason == coffeeReasonShopReady; (ready.Status == metav1.ConditionTrue) != wantTrue {
				t.Fatalf("Ready is %s with reason %s", ready.Status, ready.Reason)
			}
		})
	}
}

// The status and the till have to agree: the status says a voucher is used up at
// the same redemption after which the till refuses it.
func TestDepletedAgreesWithRedeem(t *testing.T) {
	ledger := newVoucherLedger()
	if ledger.depleted(" TestNet ", 2) {
		t.Fatal("depleted before any redemption")
	}
	ledger.redeem("TESTNET", 2)
	if ledger.depleted("testnet", 2) {
		t.Fatal("depleted with one allowance left")
	}
	ledger.redeem("TESTNET", 2)
	if !ledger.depleted("testnet", 2) {
		t.Fatal("not depleted at the limit")
	}
	if _, ok := ledger.redeem("TESTNET", 2); ok {
		t.Fatal("the till honoured a voucher the status calls depleted")
	}
	if ledger.depleted("testnet", 0) {
		t.Fatal("a limit of zero means unlimited")
	}
	if ledger.depleted("", 2) {
		t.Fatal("no code is no voucher")
	}
}

func TestCoffeeStatusFirstOrderTime(t *testing.T) {
	spec := map[string]any{"products": []any{map[string]any{"sku": "flat-white", "enabled": true}}}
	placed := time.Date(2026, 11, 6, 9, 14, 3, 0, time.UTC)

	t.Run("a placed order sets it once", func(t *testing.T) {
		client := coffeeStatusFixture(spec, nil)
		r := newStatusReconciler(client, nil)
		if err := r.reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		if storedStatus(t, client).FirstOrderTime != nil {
			t.Fatal("a first order nobody placed")
		}

		r.orderPlaced(placed)
		r.orderPlaced(placed.Add(time.Minute))
		if err := r.reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := storedStatus(t, client).FirstOrderTime
		if got == nil || !got.Time.Equal(placed) {
			t.Fatalf("firstOrderTime is %v, want %v", got, placed)
		}

		r.orderPlaced(placed.Add(time.Hour))
		if err := r.reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := storedStatus(t, client).FirstOrderTime; !got.Time.Equal(placed) {
			t.Fatalf("a later order moved it to %v", got)
		}
	})

	stored := func() map[string]any {
		return map[string]any{"firstOrderTime": "2026-11-05T08:00:00Z"}
	}

	t.Run("a start clears what the last run left", func(t *testing.T) {
		client := coffeeStatusFixture(spec, stored())
		r := newStatusReconciler(client, nil)
		if err := r.reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := storedStatus(t, client).FirstOrderTime; got != nil {
			t.Fatalf("still says %v after a start", got)
		}
		patches := statusPatches(client)
		if len(patches) != 1 || !strings.Contains(string(patches[0].GetPatch()), `"firstOrderTime":null`) {
			t.Fatalf("a merge patch removes a field with a null: %s", patches[0].GetPatch())
		}
	})

	t.Run("an order before the first pass is not cleared with it", func(t *testing.T) {
		client := coffeeStatusFixture(spec, stored())
		r := newStatusReconciler(client, nil)
		r.orderPlaced(placed)
		if err := r.reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := storedStatus(t, client).FirstOrderTime; got == nil || !got.Time.Equal(placed) {
			t.Fatalf("firstOrderTime is %v, want this run's %v", got, placed)
		}
	})

	t.Run("a write that fails keeps the order for the next pass", func(t *testing.T) {
		client := coffeeStatusFixture(spec, nil)
		r := newStatusReconciler(client, nil)
		r.orderPlaced(placed)
		refuse := true
		client.PrependReactor("patch", "coffeeconfigs", func(k8stesting.Action) (bool, runtime.Object, error) {
			if refuse {
				return true, nil, context.DeadlineExceeded
			}
			return false, nil, nil
		})
		if err := r.reconcile(context.Background()); err == nil {
			t.Fatal("want the write's error")
		}
		refuse = false
		if err := r.reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := storedStatus(t, client).FirstOrderTime; got == nil || !got.Time.Equal(placed) {
			t.Fatalf("firstOrderTime is %v, want %v", got, placed)
		}
	})
}

// Every shop-only deployment has no reconciler, and its orders go through the same
// handler.
func TestOrderPlacedWithoutAReconcilerIsNothing(t *testing.T) {
	var none *coffeeStatusReconciler
	none.orderPlaced(time.Now())
}

// Only a placed order is a first order. A refusal is something the room did, for
// the feed, and nobody got a coffee.
func TestOnlyAPlacedOrderReachesTheStatus(t *testing.T) {
	client := fakeCoffeeClients(demoCoffeeConfig(testnetVoucher(1)))
	vouchers := newVoucherLedger()
	reconciler := newCoffeeStatusReconciler(client, storefrontNamespace, testConfig().CoffeeConfigName, vouchers)
	mux := http.NewServeMux()
	registerParticipantStorefrontHandlers(mux, handlerDeps{
		cfg: testConfig(), defaultNS: storefrontNamespace, serviceAccount: client,
		vouchers: vouchers, orders: newOrderLog(), coffeeStatus: reconciler,
	})
	pending := func() *time.Time {
		reconciler.mu.Lock()
		defer reconciler.mu.Unlock()
		return reconciler.firstOrder
	}

	doJSON[coffeeOrderResponse](t, mux, signedInRequest(t, testConfig(), "POST", "/public/orders", orderBody("", "no-such-coffee")), 200)
	if got := pending(); got != nil {
		t.Fatalf("a refused order was told as placed at %v", got)
	}
	doJSON[coffeeOrderResponse](t, mux, signedInRequest(t, testConfig(), "POST", "/public/orders", orderBody("", "coffee-espresso")), 200)
	if pending() == nil {
		t.Fatal("a placed order did not reach the status")
	}
}

func TestCoffeeStatusWithoutTheMenuDoesNothing(t *testing.T) {
	client := coffeeStatusFixture(nil, nil)
	r := newCoffeeStatusReconciler(client, "voter", "somewhere-else", newVoucherLedger())
	if err := r.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if patches := statusPatches(client); len(patches) != 0 {
		t.Fatalf("want no patch, got %d", len(patches))
	}
}

func TestWithoutKeyValueKeepsNoValue(t *testing.T) {
	kept, err := withoutKeyValue(&unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "coffee-mail", "namespace": "voter", "resourceVersion": "7"},
		"data":     map[string]any{"apiKey": "dGFhcnQ="},
	}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(kept.(*unstructured.Unstructured).Object)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "dGFhcnQ=") || strings.Contains(string(encoded), `"data"`) {
		t.Fatalf("the value is still cached: %s", encoded)
	}
	if kept.(*unstructured.Unstructured).GetResourceVersion() != "7" {
		t.Fatalf("an informer needs the resourceVersion: %s", encoded)
	}
}

func jsonEqual(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

// client-go may run a transform over an object twice, and the second time the
// value is already gone: that must not read as a key that is not set.
func TestWithoutKeyValueCanRunTwice(t *testing.T) {
	once, err := withoutKeyValue(&unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "coffee-mail", "namespace": "voter"},
		"data":     map[string]any{"apiKey": "dGFhcnQ="},
	}})
	if err != nil {
		t.Fatal(err)
	}
	twice, err := withoutKeyValue(once)
	if err != nil {
		t.Fatal(err)
	}
	if set, _, _ := unstructured.NestedBool(twice.(*unstructured.Unstructured).Object, "set"); !set {
		t.Fatalf("a set key reads as not set after a second pass: %v", twice)
	}
}

// A key that is already set when Voter starts arrives in the informer's first
// list, not in a watch event. The fake ignores field selectors, so only the mail
// informer's answer means anything here.
func TestCoffeeStatusSeesAKeyAlreadySetAtStart(t *testing.T) {
	client := coffeeStatusFixture(nil, nil)
	secret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "coffee-mail", "namespace": "voter"},
		"data":     map[string]any{"apiKey": "dGFhcnQ="},
	}}
	if _, err := client.Resource(secretResource).Namespace("voter").Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go newStatusReconciler(client, nil).Run(ctx)

	for ctx.Err() == nil {
		for _, patch := range statusPatches(client) {
			var got struct {
				Status struct {
					Keys map[string]struct {
						Set bool `json:"set"`
					} `json:"keys"`
				} `json:"status"`
			}
			if err := json.Unmarshal(patch.GetPatch(), &got); err != nil {
				t.Fatal(err)
			}
			if !got.Status.Keys["mail"].Set {
				t.Fatalf("the mail key is set, and the status says not: %s", patch.GetPatch())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no status was written")
}

// A placed order reaches the status through Run, the way a real one does: a poke,
// the coalesce window, one read.
func TestCoffeeStatusFollowsAnOrderWhileRunning(t *testing.T) {
	client := coffeeStatusFixture(map[string]any{
		"products": []any{map[string]any{"sku": "flat-white", "enabled": true}},
		"vouchers": []any{map[string]any{"code": "TESTNET", "enabled": true, "maximumUsage": int64(1)}},
	}, nil)
	ledger := newVoucherLedger()
	r := newStatusReconciler(client, ledger)
	r.coalesce = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go r.Run(ctx)

	for ctx.Err() == nil && len(statusPatches(client)) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := ledger.redeem("TESTNET", 1); !ok {
		t.Fatal("the first redemption was refused")
	}
	r.orderPlaced(time.Now())

	for ctx.Err() == nil {
		status := storedStatus(t, client)
		if status.FirstOrderTime != nil {
			if ready := condition(t, status, coffeeConditionReady); ready.Reason != coffeeFailureVoucherDepleted {
				t.Fatalf("Ready's reason is %s", ready.Reason)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the order never reached the status")
}
