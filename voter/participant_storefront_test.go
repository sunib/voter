package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

// --- fixtures ---------------------------------------------------------------

const storefrontNamespace = "voter"

// demoCoffeeConfig is shaped like the object actually in the cluster, plus the
// depletable voucher the demo turns on.
func demoCoffeeConfig(vouchers ...map[string]any) *unstructured.Unstructured {
	spec := map[string]any{
		"shopName":   "Koudijs Demo Coffee",
		"bannerText": "Edit this menu",
		"currency":   "EUR",
		"products": []any{
			map[string]any{"sku": "coffee-flat-white", "name": "Flat White", "priceCents": int64(395), "enabled": true},
			map[string]any{"sku": "coffee-espresso", "name": "Espresso", "priceCents": int64(275), "enabled": true},
			map[string]any{"sku": "coffee-decaf", "name": "Decaf", "priceCents": int64(250), "enabled": false},
		},
	}
	if len(vouchers) > 0 {
		list := make([]any, 0, len(vouchers))
		for _, v := range vouchers {
			list = append(list, v)
		}
		spec["vouchers"] = list
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "examples.configbutler.ai/v1alpha1",
		"kind":       "CoffeeConfig",
		"metadata":   map[string]any{"name": "demo-coffee", "namespace": storefrontNamespace},
		"spec":       spec,
	}}
}

func testnetVoucher(maximumUsage int) map[string]any {
	return map[string]any{
		"code":           "TESTNET",
		"enabled":        true,
		"discountType":   "percentage",
		"discountValue":  int64(100),
		"maximumUsage":   int64(maximumUsage),
		"displayMessage": "TestNet coffee is on us",
	}
}

// fakeCoffeeClients returns an in-memory API server holding objs, standing in
// for Voter's own ServiceAccount client. Errors are injected with reactors, so
// a denial can be exercised without a cluster.
func fakeCoffeeClients(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	listKinds := map[schema.GroupVersionResource]string{
		coffeeConfigGVR(): "CoffeeConfigList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objs...)
}

// testConfig is the configuration the handlers read: names, not credentials.
func testConfig() config {
	return config{CoffeeConfigName: "demo-coffee", ParticipantConnectorID: "room-pass"}
}

// storefrontFixture wires a mux with a fake API server as Voter's own client.
func storefrontFixture(t *testing.T, objs ...runtime.Object) (*http.ServeMux, config, *voucherLedger, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	cfg := testConfig()
	dyn := fakeCoffeeClients(objs...)
	ledger := newVoucherLedger()
	deps := handlerDeps{
		cfg:            cfg,
		defaultNS:      storefrontNamespace,
		serviceAccount: dyn,
		vouchers:       ledger,
		orders:         newOrderLog(),
	}
	mux := http.NewServeMux()
	registerParticipantStorefrontHandlers(mux, deps)
	// The feed shares the /public/orders path with the POST above, so a fixture
	// that registered only one of them would not exercise the method routing
	// that keeps them apart. Tests read the log back over HTTP rather than
	// holding the struct, which is the same path the screen uses.
	registerParticipantOrderFeedHandlers(mux, deps)
	return mux, cfg, ledger, dyn
}

// identityFor is krm-foyer's Krm-Foyer-Identity header for one person: the
// unpadded base64url of the JSON its /auth/check returns.
func identityFor(t *testing.T, username, displayName, connector string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"userInfo":    map[string]any{"username": username, "groups": []string{"demo:voter-audience", "system:authenticated"}},
		"displayName": displayName,
		"connector":   connector,
	})
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// signedInRequest builds a request as the edge forwards it for a signed-in
// participant: with krm-foyer's identity header, and no cookie.
func signedInRequest(t *testing.T, _ config, method, target, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	// Set because the order feed shows it and shows nothing else about a
	// participant; a fixture without one could not tell the two apart.
	req.Header.Set(identityHeader, identityFor(t, "demo:demo-subject", "Demo Attendee", "room-pass"))
	return req
}

func doJSON[T any](t *testing.T, mux *http.ServeMux, req *http.Request, wantStatus int) T {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, wantStatus, rec.Body.String())
	}
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return out
}

func orderBody(voucher string, items ...string) string {
	lines := make([]string, 0, len(items))
	for _, sku := range items {
		lines = append(lines, `{"sku":"`+sku+`","quantity":1}`)
	}
	return `{"voucherCode":"` + voucher + `","items":[` + strings.Join(lines, ",") + `]}`
}

// --- storefront -------------------------------------------------------------

func TestStorefrontReturnsTheConfiguredShop(t *testing.T) {
	mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig())

	got := doJSON[storefrontResponse](t, mux, signedInRequest(t, cfg, "GET", "/public/storefront", ""), 200)

	if got.Shop.Name != "Koudijs Demo Coffee" || got.Shop.Currency != "EUR" {
		t.Fatalf("shop = %+v", got.Shop)
	}
	// The disabled product must not be offered.
	if len(got.Products) != 2 {
		t.Fatalf("products = %d, want the 2 enabled ones", len(got.Products))
	}
	for _, p := range got.Products {
		if p.SKU == "coffee-decaf" {
			t.Fatal("a disabled product was offered on the storefront")
		}
		if p.DisplayPriceCents != p.BasePriceCents {
			t.Fatalf("%s: display %d != base %d with no voucher", p.SKU, p.DisplayPriceCents, p.BasePriceCents)
		}
	}
}

func TestStorefrontVoucherStates(t *testing.T) {
	for _, tc := range []struct {
		name      string
		voucher   map[string]any
		query     string
		wantState string
		wantPrice int
	}{
		{"no voucher in the url", testnetVoucher(10), "", "not-present", 395},
		{"a code that does not exist", testnetVoucher(10), "?voucher=NOPE", "invalid", 395},
		{"a valid voucher discounts the price", testnetVoucher(10), "?voucher=TESTNET", "assumed-applied", 0},
		{"casing does not matter", testnetVoucher(10), "?voucher=testnet", "assumed-applied", 0},
		{
			"a disabled voucher is invalid",
			map[string]any{"code": "TESTNET", "enabled": false, "discountType": "percentage", "discountValue": int64(100)},
			"?voucher=TESTNET", "invalid", 395,
		},
		{
			"a voucher for nothing on sale is not applicable",
			map[string]any{
				"code": "TESTNET", "enabled": true, "discountType": "percentage",
				"discountValue": int64(100), "appliesToProducts": []any{"coffee-decaf"},
			},
			"?voucher=TESTNET", "not-applicable", 395,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig(tc.voucher))
			got := doJSON[storefrontResponse](t, mux, signedInRequest(t, cfg, "GET", "/public/storefront"+tc.query, ""), 200)

			if got.Voucher.State != tc.wantState {
				t.Fatalf("voucher state = %q, want %q", got.Voucher.State, tc.wantState)
			}
			for _, p := range got.Products {
				if p.SKU == "coffee-flat-white" && p.DisplayPriceCents != tc.wantPrice {
					t.Fatalf("flat white display price = %d, want %d", p.DisplayPriceCents, tc.wantPrice)
				}
			}
		})
	}
}

// The bug the demo is built around: the storefront keeps showing the discount
// after the allowance is gone. If this ever starts reporting depletion up
// front, the talk loses its point -- so it is pinned deliberately.
func TestStorefrontStillShowsADepletedVoucherAsApplied(t *testing.T) {
	mux, cfg, ledger, _ := storefrontFixture(t, demoCoffeeConfig(testnetVoucher(1)))
	if _, ok := ledger.redeem("TESTNET", 1); !ok {
		t.Fatal("could not exhaust the voucher")
	}

	got := doJSON[storefrontResponse](t, mux, signedInRequest(t, cfg, "GET", "/public/storefront?voucher=TESTNET", ""), 200)

	if got.Voucher.State != "assumed-applied" {
		t.Fatalf("voucher state = %q, want assumed-applied even when depleted", got.Voucher.State)
	}
	for _, p := range got.Products {
		if p.SKU == "coffee-flat-white" && p.DisplayPriceCents != 0 {
			t.Fatalf("flat white shows %d, want the free price the participant expects to see", p.DisplayPriceCents)
		}
	}
}

// --- orders -----------------------------------------------------------------

func TestOrderPlacedWithoutAVoucher(t *testing.T) {
	mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig())

	got := doJSON[coffeeOrderResponse](t, mux,
		signedInRequest(t, cfg, "POST", "/public/orders", orderBody("", "coffee-flat-white")), 200)

	if got.Status != coffeeOrderPlaced {
		t.Fatalf("status = %q, want placed (%+v)", got.Status, got.Failure)
	}
	if got.TotalPriceCents != 395 {
		t.Fatalf("total = %d, want the undiscounted 395", got.TotalPriceCents)
	}
	if got.OrderID == "" {
		t.Fatal("a placed order has no id")
	}
}

func TestOrderAppliesTheVoucherAndCountsIt(t *testing.T) {
	mux, cfg, ledger, _ := storefrontFixture(t, demoCoffeeConfig(testnetVoucher(5)))

	got := doJSON[coffeeOrderResponse](t, mux,
		signedInRequest(t, cfg, "POST", "/public/orders", orderBody("TESTNET", "coffee-flat-white")), 200)

	if got.Status != coffeeOrderPlaced || got.TotalPriceCents != 0 {
		t.Fatalf("got %+v, want a placed order costing 0", got)
	}
	if used := ledger.used("TESTNET"); used != 1 {
		t.Fatalf("ledger used = %d, want 1", used)
	}
}

// The demo's punchline, end to end through the handler: the second order fails
// because maximumUsage is 1, and it fails at SUBMIT, not on the storefront.
func TestOrderRejectedWhenTheVoucherIsDepleted(t *testing.T) {
	mux, cfg, ledger, _ := storefrontFixture(t, demoCoffeeConfig(testnetVoucher(1)))

	first := doJSON[coffeeOrderResponse](t, mux,
		signedInRequest(t, cfg, "POST", "/public/orders", orderBody("TESTNET", "coffee-espresso")), 200)
	if first.Status != coffeeOrderPlaced {
		t.Fatalf("first order = %+v, want placed", first)
	}

	second := doJSON[coffeeOrderResponse](t, mux,
		signedInRequest(t, cfg, "POST", "/public/orders", orderBody("TESTNET", "coffee-espresso")), 200)

	if second.Status != coffeeOrderRejected {
		t.Fatalf("second order = %+v, want rejected", second)
	}
	if second.Failure == nil || second.Failure.Code != coffeeFailureVoucherDepleted {
		t.Fatalf("failure = %+v, want %s", second.Failure, coffeeFailureVoucherDepleted)
	}
	if used := ledger.used("TESTNET"); used != 1 {
		t.Fatalf("ledger used = %d after a refused order, want the unchanged 1", used)
	}
}

// The fix the operator performs on stage. The limit is read from the
// CoffeeConfig on every order, so raising it must work with no restart.
func TestRaisingMaximumUsageUnblocksOrdersImmediately(t *testing.T) {
	mux, cfg, _, dyn := storefrontFixture(t, demoCoffeeConfig(testnetVoucher(1)))

	_ = doJSON[coffeeOrderResponse](t, mux,
		signedInRequest(t, cfg, "POST", "/public/orders", orderBody("TESTNET", "coffee-espresso")), 200)
	blocked := doJSON[coffeeOrderResponse](t, mux,
		signedInRequest(t, cfg, "POST", "/public/orders", orderBody("TESTNET", "coffee-espresso")), 200)
	if blocked.Status != coffeeOrderRejected {
		t.Fatalf("expected the second order to be refused, got %+v", blocked)
	}

	// The operator edits the CoffeeConfig; Flux applies it.
	updated := demoCoffeeConfig(testnetVoucher(50))
	if _, err := dyn.Resource(coffeeConfigGVR()).Namespace(storefrontNamespace).
		Update(t.Context(), updated, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update coffee config: %v", err)
	}

	after := doJSON[coffeeOrderResponse](t, mux,
		signedInRequest(t, cfg, "POST", "/public/orders", orderBody("TESTNET", "coffee-espresso")), 200)
	if after.Status != coffeeOrderPlaced {
		t.Fatalf("after raising maximumUsage the order was still refused: %+v", after)
	}
}

func TestOrderFailureCases(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		wantCode string
	}{
		{"an empty basket", orderBody(""), coffeeFailureEmptyOrder},
		{"only zero quantities", `{"items":[{"sku":"coffee-espresso","quantity":0}]}`, coffeeFailureEmptyOrder},
		{"a product that is not on sale", orderBody("", "coffee-decaf"), coffeeFailureProductUnavailable},
		{"a product that does not exist", orderBody("", "coffee-unicorn"), coffeeFailureProductUnavailable},
		{"a voucher that does not exist", orderBody("NOPE", "coffee-espresso"), coffeeFailureVoucherInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux, cfg, ledger, _ := storefrontFixture(t, demoCoffeeConfig(testnetVoucher(10)))
			got := doJSON[coffeeOrderResponse](t, mux,
				signedInRequest(t, cfg, "POST", "/public/orders", tc.body), 200)

			if got.Status != coffeeOrderRejected {
				t.Fatalf("status = %q, want rejected", got.Status)
			}
			if got.Failure == nil || got.Failure.Code != tc.wantCode {
				t.Fatalf("failure = %+v, want %s", got.Failure, tc.wantCode)
			}
			if got.OrderID != "" {
				t.Fatalf("a rejected order was given an id: %q", got.OrderID)
			}
			// A rejected order must never consume an allowance.
			if used := ledger.used("TESTNET"); used != 0 {
				t.Fatalf("ledger used = %d after a rejected order, want 0", used)
			}
		})
	}
}

// A voucher that exists but applies to nothing in the basket is refused, and --
// importantly -- refused without spending an allowance.
func TestOrderWithAnInapplicableVoucherSpendsNothing(t *testing.T) {
	voucher := map[string]any{
		"code": "TESTNET", "enabled": true, "discountType": "percentage",
		"discountValue": int64(100), "maximumUsage": int64(5),
		"appliesToProducts": []any{"coffee-flat-white"},
	}
	mux, cfg, ledger, _ := storefrontFixture(t, demoCoffeeConfig(voucher))

	got := doJSON[coffeeOrderResponse](t, mux,
		signedInRequest(t, cfg, "POST", "/public/orders", orderBody("TESTNET", "coffee-espresso")), 200)

	if got.Failure == nil || got.Failure.Code != coffeeFailureVoucherNotApplicable {
		t.Fatalf("failure = %+v, want %s", got.Failure, coffeeFailureVoucherNotApplicable)
	}
	if used := ledger.used("TESTNET"); used != 0 {
		t.Fatalf("ledger used = %d, want 0", used)
	}
}

// --- authorization ----------------------------------------------------------

// Voter reads the menu as itself, so a 401 or 403 is about Voter's credential
// or Role -- a deployment fault, reported as a 502 rather than as the person's
// own refusal. A missing menu stays a 404.
func TestStorefrontReportsKubernetesFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{
			"Voter's own Role refused is a deployment fault",
			apierrors.NewForbidden(schema.GroupResource{Group: "examples.configbutler.ai", Resource: "coffeeconfigs"},
				"demo-coffee", errors.New("the voter Role does not grant get")),
			http.StatusBadGateway,
		},
		{
			"not found stays not found",
			apierrors.NewNotFound(schema.GroupResource{Group: "examples.configbutler.ai", Resource: "coffeeconfigs"}, "demo-coffee"),
			http.StatusNotFound,
		},
		{
			"Voter's own token refused is a deployment fault",
			apierrors.NewUnauthorized("token expired"),
			http.StatusBadGateway,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux, cfg, _, dyn := storefrontFixture(t, demoCoffeeConfig())
			dyn.PrependReactor("get", "coffeeconfigs", func(ktesting.Action) (bool, runtime.Object, error) {
				return true, nil, tc.err
			})

			for _, target := range []struct {
				method, path, body string
			}{
				{"GET", "/public/storefront", ""},
				{"POST", "/public/orders", orderBody("", "coffee-espresso")},
			} {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, signedInRequest(t, cfg, target.method, target.path, target.body))
				if rec.Code != tc.want {
					t.Errorf("%s %s: status = %d, want %d (body %s)",
						target.method, target.path, rec.Code, tc.want, rec.Body.String())
				}
			}
		})
	}
}

// Without krm-foyer's header there is nobody to serve. Behind a correct edge
// that cannot happen (the check answers 401 itself), so this is the backstop
// for a route that forgot to ask it -- and no header a browser controls may
// stand in for one.
func TestStorefrontAndOrdersRequireAnIdentity(t *testing.T) {
	mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig())

	for _, tc := range []struct{ method, path, body, identity string }{
		{"GET", "/public/storefront", "", ""},
		{"POST", "/public/orders", orderBody("", "coffee-espresso"), ""},
		{"GET", "/public/storefront", "", "not base64url!"},
		{"GET", "/public/storefront", "", base64.RawURLEncoding.EncodeToString([]byte(`{"displayName":"no username"}`))},
	} {
		req := signedInRequest(t, cfg, tc.method, tc.path, tc.body)
		req.Header.Del(identityHeader)
		if tc.identity != "" {
			req.Header.Set(identityHeader, tc.identity)
		}
		// Headers an attacker controls must not substitute for a session.
		req.Header.Set("Authorization", "Bearer attacker-token")
		req.Header.Set("Impersonate-User", "system:admin")
		req.Header.Set("X-Remote-Group", "system:masters")

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestStorefrontAndOrdersRejectWrongMethods(t *testing.T) {
	mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig())

	for _, tc := range []struct{ method, path string }{
		{"POST", "/public/storefront"},
		{"DELETE", "/public/storefront"},
		// GET is deliberately absent: it is the order feed now. DELETE still
		// has to be refused, and by the mux, since neither handler sees it.
		{"DELETE", "/public/orders"},
		{"POST", "/public/orders/stream"},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, signedInRequest(t, cfg, tc.method, tc.path, "{}"))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want 405", tc.method, tc.path, rec.Code)
		}
	}
}

func TestOrderRejectsUnreadableBodies(t *testing.T) {
	mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig())

	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"not json", "this is not json", http.StatusBadRequest},
		{"oversized", `{"items":[` + strings.Repeat(`{"sku":"x","quantity":1},`, 2000) + `{"sku":"y","quantity":1}]}`, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, signedInRequest(t, cfg, "POST", "/public/orders", tc.body))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

// --- voucher usage ----------------------------------------------------------

func TestVoucherUsageReflectsPlacedOrders(t *testing.T) {
	mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig(testnetVoucher(5)))

	type usageResponse struct {
		VoucherUsage map[string]int `json:"voucherUsage"`
		Scope        string         `json:"scope"`
	}

	before := doJSON[usageResponse](t, mux, signedInRequest(t, cfg, "GET", "/public/vouchers", ""), 200)
	if len(before.VoucherUsage) != 0 {
		t.Fatalf("usage = %v before any order, want empty", before.VoucherUsage)
	}

	for range 2 {
		got := doJSON[coffeeOrderResponse](t, mux,
			signedInRequest(t, cfg, "POST", "/public/orders", orderBody("TESTNET", "coffee-espresso")), 200)
		if got.Status != coffeeOrderPlaced {
			t.Fatalf("order = %+v, want placed", got)
		}
	}

	after := doJSON[usageResponse](t, mux, signedInRequest(t, cfg, "GET", "/public/vouchers", ""), 200)
	// Keyed by the normalized code, which is what the admin screen looks up.
	if after.VoucherUsage["testnet"] != 2 {
		t.Fatalf("usage = %v, want testnet:2", after.VoucherUsage)
	}
	// The scope is part of the contract: these counts are one replica's, since
	// boot. A reader who assumes cluster-wide totals will misread them.
	if after.Scope != "process" {
		t.Fatalf("scope = %q, want process", after.Scope)
	}
}

func TestVoucherUsageRequiresAnIdentityAndRejectsWrites(t *testing.T) {
	mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig(testnetVoucher(5)))

	anon := signedInRequest(t, cfg, "GET", "/public/vouchers", "")
	anon.Header.Del(identityHeader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, anon)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, signedInRequest(t, cfg, "POST", "/public/vouchers", "{}"))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
}

// --- SSE test helpers --------------------------------------------------------

// mustOutbound turns the httptest request built by signedInRequest into one an
// http.Client can send: same cookies and headers, real URL.
func mustOutbound(t *testing.T, in *http.Request, url string) *http.Request {
	t.Helper()
	out, err := http.NewRequest(in.Method, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	out.Header = in.Header.Clone()
	for _, c := range in.Cookies() {
		out.AddCookie(c)
	}
	if in.Context() != nil {
		out = out.WithContext(in.Context())
	}
	return out
}

// readSSE decodes `data:` frames into events until the body ends.
func readSSE(body io.Reader, out chan<- map[string]any) {
	defer close(out)
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// Heartbeats are SSE comments, deliberately not events.
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(line[len("data:"):])), &ev); err != nil {
			continue
		}
		out <- ev
	}
}

// waitForEvent returns the next event, or the next of a given type when one is
// named. Nil means the context ended first.
func waitForEvent(t *testing.T, ctx context.Context, events <-chan map[string]any, want string) map[string]any {
	t.Helper()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if want == "" {
				return ev
			}
			if name, _ := ev["type"].(string); name == want {
				return ev
			}
		case <-ctx.Done():
			return nil
		}
	}
}
