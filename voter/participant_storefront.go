package main

// The storefront and order endpoints, on participant credentials.
//
// These are the two screens an attendee actually touches, and they were the
// biggest thing missing after the legacy session was deleted: the SPA called
// them and got a 404, so a successful login landed on an empty page.
//
// Both read the CoffeeConfig with the PARTICIPANT's own token. A participant
// who is not in the audience group gets Kubernetes' 403 verbatim -- that
// denial is the demo, not an error to paper over.

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// maxOrderBytes bounds an order submission. An order is a short list of SKUs
// and quantities; anything larger is not a coffee order.
const maxOrderBytes = 16 * 1024

// coffeeOrderResponse is what the order screen receives. It deliberately
// reports a REJECTED order with HTTP 200 plus a failure block rather than a
// 4xx: the request was well-formed and understood, and the frontend needs the
// failure code to explain which of the several "no" answers this was.
//
// The exception is a Kubernetes denial, which keeps its own status code --
// that one really is an authorization answer and must not be flattened.
type coffeeOrderResponse struct {
	OrderID         string              `json:"orderId"`
	Status          string              `json:"status"`
	Currency        string              `json:"currency"`
	TotalPriceCents int                 `json:"totalPriceCents"`
	Items           []coffeeOrderLine   `json:"items,omitempty"`
	Failure         *coffeeOrderFailure `json:"failure,omitempty"`
}

const (
	coffeeOrderPlaced   = "placed"
	coffeeOrderRejected = "rejected"
)

func registerParticipantStorefrontHandlers(mux *http.ServeMux, deps handlerDeps) {
	cfg := deps.cfg

	// GET /public/storefront?voucher=CODE
	//
	// The voucher state here is deliberately optimistic: a voucher that exists,
	// is enabled and applies to something reads as "assumed-applied" even when
	// it is already depleted. That is not an oversight -- it is the bug the
	// demo is about. Depletion surfaces at submit time, in placeCoffeeOrder.
	mux.HandleFunc("/public/storefront", requireParticipant(cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		noStore(w)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		cc, err := deps.readCoffeeConfig(ctx, s.IDToken)
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, buildStorefront(cc, r.URL.Query().Get("voucher")))
	}))

	// POST /public/orders
	mux.HandleFunc("/public/orders", requireParticipant(cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		noStore(w)
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxOrderBytes+1))
		if err != nil {
			http.Error(w, "failed to read the order", http.StatusBadRequest)
			return
		}
		if len(body) > maxOrderBytes {
			http.Error(w, "order too large", http.StatusRequestEntityTooLarge)
			return
		}
		var req coffeeOrderRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "the order could not be read as JSON", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		cc, err := deps.readCoffeeConfig(ctx, s.IDToken)
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, placeCoffeeOrder(cc, req, deps.vouchers, s.Subject))
	}))
}

// readCoffeeConfig fetches the one configured CoffeeConfig with the
// participant's own credentials. Both endpoints go through it so neither can
// drift into reading with a different identity.
func (d handlerDeps) readCoffeeConfig(ctx context.Context, idToken string) (coffeeConfig, error) {
	clients, err := d.participantClientsFor(idToken)
	if err != nil {
		return coffeeConfig{}, err
	}
	obj, err := clients.dynamic.Resource(coffeeConfigGVR()).Namespace(d.defaultNS).
		Get(ctx, d.cfg.CoffeeConfigName, metav1.GetOptions{})
	if err != nil {
		return coffeeConfig{}, err
	}
	return toCoffeeConfig(obj)
}

// placeCoffeeOrder is the whole ordering decision, and it is deliberately a
// pure function of (config, request, ledger) so it can be tested without a
// Kubernetes API at all.
//
// Order of checks matters. Pricing and availability come first, because a
// participant who ordered something unavailable should hear about that rather
// than about a voucher. Depletion is checked LAST and only for an order that
// would otherwise have succeeded -- otherwise a rejected order would burn an
// allowance nobody drank.
func placeCoffeeOrder(cc coffeeConfig, req coffeeOrderRequest, ledger *voucherLedger, subject string) coffeeOrderResponse {
	prepared, failure := prepareCoffeeOrder(cc, req)
	if failure != nil {
		return coffeeOrderResponse{
			OrderID:  "",
			Status:   coffeeOrderRejected,
			Currency: prepared.Currency,
			Failure:  failure,
		}
	}

	// A voucher was named, exists, is enabled and applies to something in this
	// order. Only now does it cost an allowance.
	if prepared.Voucher != nil && ledger != nil {
		used, ok := ledger.redeem(prepared.Voucher.Code, prepared.Voucher.MaximumUsage)
		if !ok {
			log.Printf("order: voucher depleted code=%s used=%d max=%d sub=%s",
				prepared.Voucher.Code, used, prepared.Voucher.MaximumUsage, subject)
			return coffeeOrderResponse{
				Status:   coffeeOrderRejected,
				Currency: prepared.Currency,
				Failure: &coffeeOrderFailure{
					Code: coffeeFailureVoucherDepleted,
					// Names the real cause. The audience is about to watch
					// someone fix exactly this field.
					Message: "This voucher has been used the maximum number of times.",
				},
			}
		}
	}

	orderID, err := randomToken()
	if err != nil {
		// The order was counted but we cannot name it. Give the allowance back
		// rather than charging someone for a coffee they were never told they
		// had.
		if prepared.Voucher != nil && ledger != nil {
			ledger.release(prepared.Voucher.Code)
		}
		return coffeeOrderResponse{
			Status:   coffeeOrderRejected,
			Currency: prepared.Currency,
			Failure: &coffeeOrderFailure{
				Code:    "OrderIdUnavailable",
				Message: "The order could not be recorded. Please try again.",
			},
		}
	}

	log.Printf("order: placed id=%s sub=%s items=%d total=%d voucher=%q",
		orderID, subject, len(prepared.Items), prepared.TotalPriceCents,
		strings.TrimSpace(prepared.VoucherCode))

	return coffeeOrderResponse{
		OrderID:         orderID,
		Status:          coffeeOrderPlaced,
		Currency:        prepared.Currency,
		TotalPriceCents: prepared.TotalPriceCents,
		Items:           prepared.Items,
	}
}
