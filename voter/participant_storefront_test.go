package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// fakeCoffeeClients returns a client factory backed by an in-memory API server
// holding objs. Errors are injected with reactors, the way gitops-reverser's
// tests do it, so an RBAC denial can be exercised without a cluster.
func fakeCoffeeClients(objs ...runtime.Object) (func(config, string) (participantClients, error), *dynamicfake.FakeDynamicClient) {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		coffeeConfigGVR():  "CoffeeConfigList",
		commitRequestGVR(): "CommitRequestList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
	return func(config, string) (participantClients, error) {
		return participantClients{dynamic: dyn}, nil
	}, dyn
}

// storefrontFixture wires a mux with a session codec and a fake API server.
func storefrontFixture(t *testing.T, objs ...runtime.Object) (*http.ServeMux, config, *voucherLedger, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	old := sessionCookieCodec
	sessionCookieCodec = testCodec(t)
	t.Cleanup(func() { sessionCookieCodec = old })

	cfg := testConfig()
	cfg.CoffeeConfigName = "demo-coffee"

	newClients, dyn := fakeCoffeeClients(objs...)
	ledger := newVoucherLedger()
	deps := handlerDeps{
		cfg:        cfg,
		defaultNS:  storefrontNamespace,
		newClients: newClients,
		vouchers:   ledger,
	}
	mux := http.NewServeMux()
	registerParticipantStorefrontHandlers(mux, deps)
	return mux, cfg, ledger, dyn
}

// signedInRequest builds a request carrying a valid session cookie and matching
// CSRF proof, the way the SPA does.
func signedInRequest(t *testing.T, cfg config, method, target, body string) *http.Request {
	t.Helper()
	now := time.Now()
	rec := httptest.NewRecorder()
	if err := setParticipantSession(rec, cfg, sessionCookieCodec, participantSession{
		IDToken:     "participant-token",
		Subject:     "demo-subject",
		TokenExpiry: now.Add(time.Hour).Unix(),
	}, now); err != nil {
		t.Fatalf("session: %v", err)
	}
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	s, ok := getParticipantSession(req, cfg, sessionCookieCodec, now)
	if !ok {
		t.Fatal("fixture session invalid")
	}
	req.Header.Set("X-CSRF-Token", s.CSRF)
	req.Header.Set("Origin", cfg.AppOrigin)
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

// Kubernetes' verdict must reach the participant unchanged. Turning a 403 into
// a 500 would hide the RBAC decision the whole demo exists to show.
func TestStorefrontPreservesKubernetesDenials(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{
			"forbidden stays forbidden",
			apierrors.NewForbidden(schema.GroupResource{Group: "examples.configbutler.ai", Resource: "coffeeconfigs"},
				"demo-coffee", errors.New("not in the audience group")),
			http.StatusForbidden,
		},
		{
			"not found stays not found",
			apierrors.NewNotFound(schema.GroupResource{Group: "examples.configbutler.ai", Resource: "coffeeconfigs"}, "demo-coffee"),
			http.StatusNotFound,
		},
		{
			"an expired token is reported as unauthorized",
			apierrors.NewUnauthorized("token expired"),
			http.StatusUnauthorized,
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

func TestStorefrontAndOrdersRequireASession(t *testing.T) {
	mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig())

	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/public/storefront", ""},
		{"POST", "/public/orders", orderBody("", "coffee-espresso")},
	} {
		req := signedInRequest(t, cfg, tc.method, tc.path, tc.body)
		req.Header.Del("Cookie")
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

func TestOrderRequiresCSRFProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		origin string
		csrf   string
		want   int
	}{
		{"valid proof", "", "valid", http.StatusOK},
		{"absent origin with a valid token", "", "valid", http.StatusOK},
		{"no csrf token", "", "", http.StatusForbidden},
		{"wrong csrf token", "", "wrong", http.StatusForbidden},
		{"foreign origin even with a valid token", "https://evil.example", "valid", http.StatusForbidden},
		{"null origin is rejected", "null", "valid", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig())
			req := signedInRequest(t, cfg, "POST", "/public/orders", orderBody("", "coffee-espresso"))
			if tc.origin == "" {
				req.Header.Del("Origin")
			} else {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.csrf != "valid" {
				req.Header.Set("X-CSRF-Token", tc.csrf)
			}

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// A GET is not a mutation, so it must not demand CSRF proof -- otherwise the
// storefront cannot load before the SPA has fetched /auth/session.
func TestStorefrontDoesNotRequireCSRF(t *testing.T) {
	mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig())
	req := signedInRequest(t, cfg, "GET", "/public/storefront", "")
	req.Header.Del("X-CSRF-Token")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestStorefrontAndOrdersRejectWrongMethods(t *testing.T) {
	mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig())

	for _, tc := range []struct{ method, path string }{
		{"POST", "/public/storefront"},
		{"DELETE", "/public/storefront"},
		{"GET", "/public/orders"},
		{"DELETE", "/public/orders"},
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

// --- credential isolation ---------------------------------------------------

// The fake dynamic client above bypasses the transport, so it cannot prove what
// actually goes on the wire. This one uses the real client against a controlled
// stand-in: the participant's OWN token must be the credential, and headers an
// attacker supplied must not survive into the upstream request.
func TestStorefrontAndOrdersSendTheParticipantsOwnToken(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, wantMethod, wantPath string
	}{
		{
			"storefront", "GET", "/public/storefront", "",
			"GET", "/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/coffeeconfigs/demo-coffee",
		},
		{
			"orders", "POST", "/public/orders", orderBody("", "coffee-espresso"),
			"GET", "/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/coffeeconfigs/demo-coffee",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := sessionCookieCodec
			sessionCookieCodec = testCodec(t)
			t.Cleanup(func() { sessionCookieCodec = old })

			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if got := r.Header.Get("Authorization"); got != "Bearer participant-token" {
					t.Errorf("Authorization = %q, want the participant's own token", got)
				}
				for key := range r.Header {
					lower := strings.ToLower(key)
					if strings.HasPrefix(lower, "impersonate-") || strings.HasPrefix(lower, "x-remote-") {
						t.Errorf("untrusted identity header forwarded upstream: %s", key)
					}
				}
				if r.Method != tc.wantMethod || r.URL.Path != tc.wantPath {
					t.Errorf("upstream operation = %s %s, want %s %s",
						r.Method, r.URL.Path, tc.wantMethod, tc.wantPath)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"apiVersion":"examples.configbutler.ai/v1alpha1","kind":"CoffeeConfig",` +
					`"metadata":{"name":"demo-coffee"},"spec":{"currency":"EUR","products":[` +
					`{"sku":"coffee-espresso","name":"Espresso","priceCents":275,"enabled":true}]}}`))
			}))
			defer upstream.Close()

			cfg := testConfig()
			cfg.CoffeeConfigName = "demo-coffee"
			cfg.KubernetesAPIServer = upstream.URL

			mux := http.NewServeMux()
			registerParticipantStorefrontHandlers(mux, handlerDeps{
				cfg: cfg, defaultNS: "voter", vouchers: newVoucherLedger(),
			})

			req := signedInRequest(t, cfg, tc.method, tc.path, tc.body)
			// Everything an attacker could put on the request.
			req.Header.Set("Authorization", "Bearer attacker-token")
			req.Header.Set("Impersonate-User", "system:admin")
			req.Header.Set("Impersonate-Group", "system:masters")
			req.Header.Set("X-Remote-User", "github:someone-else")
			req.Header.Set("X-Remote-Group", "system:masters")

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
			}
			if calls != 1 {
				t.Fatalf("upstream calls = %d, want exactly 1", calls)
			}
		})
	}
}

// A handler must never fall back to the server's own identity when the
// participant's token is unusable.
func TestStorefrontDoesNotFallBackWhenTheTokenIsUnusable(t *testing.T) {
	old := sessionCookieCodec
	sessionCookieCodec = testCodec(t)
	t.Cleanup(func() { sessionCookieCodec = old })

	cfg := testConfig()
	cfg.CoffeeConfigName = "demo-coffee"

	called := false
	mux := http.NewServeMux()
	registerParticipantStorefrontHandlers(mux, handlerDeps{
		cfg:       cfg,
		defaultNS: "voter",
		vouchers:  newVoucherLedger(),
		newClients: func(config, string) (participantClients, error) {
			called = true
			return participantClients{}, errors.New("no participant token")
		},
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, signedInRequest(t, cfg, "GET", "/public/storefront", ""))

	if !called {
		t.Fatal("the client factory was never consulted")
	}
	if rec.Code == http.StatusOK {
		t.Fatalf("status = 200: the request succeeded without usable participant credentials")
	}
}
