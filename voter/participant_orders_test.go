package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
)

// orderFeedFixture is storefrontFixture plus a handle on the log itself, for
// the two facts no endpoint reports: how many watchers are attached, and
// whether one let go.
func orderFeedFixture(t *testing.T, objs ...runtime.Object) (*httptest.Server, *http.ServeMux, config, *orderLog) {
	t.Helper()
	old := sessionCookieCodec
	sessionCookieCodec = testCodec(t)
	t.Cleanup(func() { sessionCookieCodec = old })

	cfg := testConfig()
	cfg.CoffeeConfigName = "demo-coffee"

	newClients, _ := fakeCoffeeClients(objs...)
	log := newOrderLog()
	deps := handlerDeps{
		cfg:        cfg,
		defaultNS:  storefrontNamespace,
		newClients: newClients,
		vouchers:   newVoucherLedger(),
		orders:     log,
	}
	mux := http.NewServeMux()
	registerParticipantStorefrontHandlers(mux, deps)
	registerParticipantOrderFeedHandlers(mux, deps)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, mux, cfg, log
}

// ordersFeedResponse mirrors the snapshot endpoint's body.
type ordersFeedResponse struct {
	Orders   []coffeeOrderRecord `json:"orders"`
	Scope    string              `json:"scope"`
	Capacity int                 `json:"capacity"`
}

// --- the log itself ---------------------------------------------------------

func TestOrderLogForgetsTheOldestOrdersPastCapacity(t *testing.T) {
	log := newOrderLog()
	for i := 0; i < orderLogCapacity+10; i++ {
		log.record(coffeeOrderRecord{Status: coffeeOrderPlaced})
	}

	got := log.snapshot()
	if len(got) != orderLogCapacity {
		t.Fatalf("kept %d records, want %d", len(got), orderLogCapacity)
	}
	// The sequence keeps counting even though the early records are gone: it
	// numbers what happened, not what is still remembered.
	if got[0].Seq != 11 {
		t.Errorf("oldest kept seq = %d, want 11", got[0].Seq)
	}
	if last := got[len(got)-1].Seq; last != int64(orderLogCapacity+10) {
		t.Errorf("newest seq = %d, want %d", last, orderLogCapacity+10)
	}
}

func TestOrderLogRefusesWatchersPastTheCap(t *testing.T) {
	log := newOrderLog()
	for i := 0; i < orderLogMaxSubscribers; i++ {
		if _, _, ok := log.subscribe(); !ok {
			t.Fatalf("subscriber %d refused below the cap", i)
		}
	}
	if _, _, ok := log.subscribe(); ok {
		t.Fatal("subscribed past the cap; the fan-out is unbounded")
	}

	log.unsubscribe(1)
	if _, _, ok := log.subscribe(); !ok {
		t.Fatal("a released slot was not reusable")
	}
	if n := log.subscribers(); n != orderLogMaxSubscribers {
		t.Errorf("subscribers = %d, want %d", n, orderLogMaxSubscribers)
	}
}

// --- the snapshot endpoint --------------------------------------------------

func TestOrderFeedRecordsPlacedAndRejectedOrders(t *testing.T) {
	// maximumUsage 1, so the second TESTNET order is the demo's refusal.
	mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig(testnetVoucher(1)))

	doJSON[coffeeOrderResponse](t, mux,
		signedInRequest(t, cfg, "POST", "/public/orders", orderBody("TESTNET", "coffee-espresso")), 200)
	doJSON[coffeeOrderResponse](t, mux,
		signedInRequest(t, cfg, "POST", "/public/orders", orderBody("TESTNET", "coffee-espresso")), 200)

	feed := doJSON[ordersFeedResponse](t, mux,
		signedInRequest(t, cfg, "GET", "/public/orders", ""), 200)

	if feed.Scope != "process" {
		t.Errorf("scope = %q, want %q — the feed must admit it is per-replica", feed.Scope, "process")
	}
	if len(feed.Orders) != 2 {
		t.Fatalf("feed holds %d orders, want 2: %+v", len(feed.Orders), feed.Orders)
	}

	placed, refused := feed.Orders[0], feed.Orders[1]
	if placed.Status != coffeeOrderPlaced || placed.OrderID == "" {
		t.Errorf("first order = %+v, want a placed order with an id", placed)
	}
	// A refusal is something the room did, so it is in the feed -- with the
	// items it would have been, which is what makes the entry readable.
	if refused.Status != coffeeOrderRejected {
		t.Errorf("second order status = %q, want %q", refused.Status, coffeeOrderRejected)
	}
	if refused.FailureCode != coffeeFailureVoucherDepleted {
		t.Errorf("failure code = %q, want %q", refused.FailureCode, coffeeFailureVoucherDepleted)
	}
	if len(refused.Items) != 1 || refused.Items[0].SKU != "coffee-espresso" {
		t.Errorf("refused items = %+v, want the espresso that was asked for", refused.Items)
	}
	if refused.Seq <= placed.Seq {
		t.Errorf("seq did not advance: %d then %d", placed.Seq, refused.Seq)
	}
}

// The feed reaches every signed-in phone, so it may carry only what the room
// already sees on each other's badges. A regression here leaks the OIDC subject
// of every attendee to every other attendee.
func TestOrderFeedShowsDisplayNamesAndNothingElseAboutAPerson(t *testing.T) {
	mux, cfg, _, _ := storefrontFixture(t, demoCoffeeConfig())

	doJSON[coffeeOrderResponse](t, mux,
		signedInRequest(t, cfg, "POST", "/public/orders", orderBody("", "coffee-flat-white")), 200)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, signedInRequest(t, cfg, "GET", "/public/orders", ""))
	body := rec.Body.String()

	var feed ordersFeedResponse
	if err := json.Unmarshal([]byte(body), &feed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(feed.Orders) != 1 || feed.Orders[0].Who != "Demo Attendee" {
		t.Fatalf("feed = %+v, want one order by the display name", feed.Orders)
	}
	for _, secret := range []string{"demo-subject", "participant-token"} {
		if strings.Contains(body, secret) {
			t.Errorf("feed body leaks %q: %s", secret, body)
		}
	}
}

func TestOrderFeedNeedsASession(t *testing.T) {
	mux, _, _, _ := storefrontFixture(t, demoCoffeeConfig())

	for _, path := range []string{"/public/orders", "/public/orders/stream"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session: status = %d, want 401", path, rec.Code)
		}
	}
}

// --- the stream -------------------------------------------------------------

// TestOrderFeedStreamsOrdersAsTheyHappen is the behaviour the screen depends on:
// the snapshot arrives first, and an order placed afterwards reaches an already
// open connection without a reload.
func TestOrderFeedStreamsOrdersAsTheyHappen(t *testing.T) {
	srv, mux, cfg, _ := orderFeedFixture(t, demoCoffeeConfig())

	// One order before the watcher connects, so the replayed snapshot is not
	// empty and a client that only handled live events would fail here.
	doJSON[coffeeOrderResponse](t, mux,
		signedInRequest(t, cfg, "POST", "/public/orders", orderBody("", "coffee-flat-white")), 200)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := srv.URL + "/public/orders/stream"
	req := mustOutbound(t, signedInRequest(t, cfg, "GET", url, ""), url).WithContext(ctx)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type = %q, want text/event-stream", got)
	}

	events := make(chan map[string]any, 8)
	go readSSE(resp.Body, events)

	replayed := waitForEvent(t, ctx, events, "")
	if replayed == nil {
		t.Fatal("no snapshot replayed onto a new stream")
	}
	if replayed["status"] != coffeeOrderPlaced {
		t.Errorf("replayed event = %+v, want the order placed before connecting", replayed)
	}

	doJSON[coffeeOrderResponse](t, mux,
		signedInRequest(t, cfg, "POST", "/public/orders", orderBody("", "coffee-espresso")), 200)

	live := waitForEvent(t, ctx, events, "")
	if live == nil {
		t.Fatal("an order placed while watching never reached the stream")
	}
	items, _ := live["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("live event = %+v, want one item", live)
	}
	first, _ := items[0].(map[string]any)
	if first["sku"] != "coffee-espresso" {
		t.Errorf("live event item = %+v, want the espresso just ordered", first)
	}
}

// A stream that stays subscribed after its client is gone would leak a slot per
// disconnect, and the cap would eventually refuse everyone.
func TestOrderFeedReleasesItsSlotWhenTheClientLeaves(t *testing.T) {
	srv, _, cfg, log := orderFeedFixture(t, demoCoffeeConfig())

	ctx, cancel := context.WithCancel(context.Background())
	url := srv.URL + "/public/orders/stream"
	req := mustOutbound(t, signedInRequest(t, cfg, "GET", url, ""), url).WithContext(ctx)
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open stream: %v", err)
	}
	cancel()
	_ = resp.Body.Close()

	// The handler notices through its request context, which the server closes
	// on its own schedule, so this waits rather than asserting immediately.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if log.subscribers() == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("the stream never released its subscriber slot: %d still attached", log.subscribers())
}

// The SPA fallback at "/" matches every path, which disables ServeMux's own 405
// for a path whose handlers are registered by method. Pin the answer in a mux
// shaped like production's -- the storefront fixture has no "/" and so cannot
// see this.
func TestOrderPathsRejectWrongMethodsBehindTheSPAFallback(t *testing.T) {
	old := sessionCookieCodec
	sessionCookieCodec = testCodec(t)
	t.Cleanup(func() { sessionCookieCodec = old })

	cfg := testConfig()
	cfg.CoffeeConfigName = "demo-coffee"
	newClients, _ := fakeCoffeeClients(demoCoffeeConfig())
	deps := handlerDeps{
		cfg:        cfg,
		defaultNS:  storefrontNamespace,
		newClients: newClients,
		vouchers:   newVoucherLedger(),
		orders:     newOrderLog(),
	}

	mux := http.NewServeMux()
	registerParticipantStorefrontHandlers(mux, deps)
	registerParticipantOrderFeedHandlers(mux, deps)
	registerHandlers(mux, deps) // last, and it owns "/"

	for _, tc := range []struct{ method, path string }{
		{"DELETE", "/public/orders"},
		{"PUT", "/public/orders"},
		{"POST", "/public/orders/stream"},
		{"DELETE", "/public/orders/stream"},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, signedInRequest(t, cfg, tc.method, tc.path, ""))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want 405 (the SPA answered instead)",
				tc.method, tc.path, rec.Code)
		}
	}
}
