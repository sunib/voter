package main

// Reading the order feed: one snapshot endpoint and one SSE stream.
//
// Both are hand-written rather than served through the krm-stream gateway, and
// that is not laziness. The gateway's job is to share a Kubernetes watch and
// authorize each subscriber against the API server's answer about them. There
// is no watch here and no Kubernetes object to be authorized against, because
// an order is not one -- see coffee_orders.go. Routing this through the gateway
// would have meant inventing a resource just so the plumbing would fit, which
// is the exact mistake the demo is arguing against.
//
// What authorization there is, is the session: you must be signed in, and the
// feed shows display names only.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// orderFeedHeartbeat keeps intermediaries from closing an idle stream. Traefik
// and most load balancers time a connection out on silence, and a coffee demo
// is silent for minutes at a stretch.
const orderFeedHeartbeat = 20 * time.Second

func registerParticipantOrderFeedHandlers(mux *http.ServeMux, deps handlerDeps) {
	cfg := deps.cfg

	// Both paths below are registered BY METHOD, because POST /public/orders
	// belongs to the storefront and GET to the feed. That split costs one
	// thing: ServeMux only answers 405 itself when no other pattern matches
	// the path, and "/" -- the SPA fallback -- matches everything. Without
	// these two, DELETE /public/orders would quietly serve the index page.
	//
	// They are not behind requireParticipant. A method nothing implements is
	// not an authorization question, and answering 401 first would tell a
	// caller to go and authenticate for a verb that will never work.
	methodNotAllowed := func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
	mux.HandleFunc("/public/orders", methodNotAllowed)
	mux.HandleFunc("/public/orders/stream", methodNotAllowed)

	// GET /public/orders
	//
	// The first paint, and the whole feed for a browser without EventSource.
	// "scope" carries the same warning as /public/vouchers: these are this
	// replica's orders, since this boot.
	mux.HandleFunc("GET /public/orders", requireParticipant(cfg, func(w http.ResponseWriter, _ *http.Request, _ participantSession) {
		noStore(w)
		writeJSON(w, http.StatusOK, map[string]any{
			"orders":   deps.orders.snapshot(),
			"scope":    "process",
			"capacity": orderLogCapacity,
		})
	}))

	// GET /public/orders/stream
	mux.HandleFunc("GET /public/orders/stream", requireParticipant(cfg, func(w http.ResponseWriter, r *http.Request, _ participantSession) {
		noStore(w)

		flusher, canFlush := w.(http.Flusher)
		if !canFlush {
			http.Error(w, "streaming is not supported here", http.StatusInternalServerError)
			return
		}
		if deps.orders == nil {
			http.Error(w, "the order feed is not running", http.StatusServiceUnavailable)
			return
		}

		// Subscribe BEFORE writing the snapshot, so an order placed between the
		// two arrives on the channel instead of falling into the gap. The
		// client de-duplicates on seq, which is why an overlap is safe and a
		// hole would not be.
		id, events, ok := deps.orders.subscribe()
		if !ok {
			// A refusal a screen can render, not a dropped connection.
			http.Error(w, "too many watchers on the order feed", http.StatusServiceUnavailable)
			return
		}
		defer deps.orders.unsubscribe(id)

		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Connection", "keep-alive")
		// Nginx-family proxies buffer a response body by default, which turns a
		// live feed into one long silence followed by everything at once.
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		for _, rec := range deps.orders.snapshot() {
			if !writeOrderEvent(w, flusher, rec) {
				return
			}
		}

		ticker := time.NewTicker(orderFeedHeartbeat)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case rec, open := <-events:
				if !open {
					return
				}
				if !writeOrderEvent(w, flusher, rec) {
					return
				}
			case <-ticker.C:
				// An SSE comment. Clients ignore it; proxies count it as life.
				if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}))
}

// writeOrderEvent sends one record and reports whether the connection survived
// it. A write error on a hijacked-feeling long response is how a closed browser
// tab reaches us, so it ends the stream rather than being logged as a fault.
func writeOrderEvent(w http.ResponseWriter, flusher http.Flusher, rec coffeeOrderRecord) bool {
	payload, err := json.Marshal(rec)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", rec.Seq, payload); err != nil {
		return false
	}
	flusher.Flush()
	return true
}
