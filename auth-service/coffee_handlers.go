package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8swatch "k8s.io/apimachinery/pkg/watch"
)

type adminSessionResponse struct {
	Nickname string `json:"nickname"`
}

func registerCoffeeHandlers(mux *http.ServeMux, deps handlerDeps) {
	mux.HandleFunc("/public/storefront", requireSessionMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := deps.kube.getCoffeeConfig(ctx)
		if err != nil {
			writeKubeError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, buildStorefront(cfg, r.URL.Query().Get("voucher")))
	}))

	mux.HandleFunc("/public/storefront/watch", requireSessionMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		streamCoffeeConfigWatch(w, r, deps)
	}))

	mux.HandleFunc("/public/orders", requireSessionMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req coffeeOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid order body", http.StatusBadRequest)
			return
		}
		session, _ := getBrowserSession(r)
		if req.Source == nil {
			req.Source = map[string]string{}
		}
		req.Source["participantNickname"] = session.Nickname

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := deps.kube.getCoffeeConfig(ctx)
		if err != nil {
			writeKubeError(w, err)
			return
		}

		prepared, failure := prepareCoffeeOrder(cfg, req)
		record := deps.orders.submit(prepared, failure)
		resp := coffeeOrderResponse{
			OrderID:         record.OrderID,
			Status:          record.Status,
			Currency:        record.Currency,
			TotalPriceCents: record.TotalPriceCents,
		}
		statusCode := http.StatusCreated
		if record.Status == coffeeOrderStatusRejected {
			resp.Failure = &coffeeOrderFailure{
				Code:    record.FailureCode,
				Message: record.FailureMessage,
			}
			if record.FailureCode == coffeeFailureVoucherDepleted {
				statusCode = http.StatusConflict
			} else {
				statusCode = http.StatusBadRequest
			}
		}

		writeJSON(w, statusCode, resp)
	}))

	mux.HandleFunc("/public/admin/session", requireAdminMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		session, ok := getBrowserSession(r)
		if !ok {
			clearSessionCookie(w, deps.cfg)
			http.Error(w, "session required", http.StatusUnauthorized)
			return
		}

		writeJSON(w, http.StatusOK, adminSessionResponse{
			Nickname: session.Nickname,
		})
	}))

	mux.HandleFunc("/public/admin/coffeeconfig", requireAdminMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			cfg, err := deps.kube.getCoffeeConfig(ctx)
			if err != nil {
				writeKubeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, cfg)
		case http.MethodPatch:
			patchBody, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "failed to read patch body", http.StatusBadRequest)
				return
			}
			if len(strings.TrimSpace(string(patchBody))) == 0 {
				http.Error(w, "empty patch body", http.StatusBadRequest)
				return
			}

			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			current, err := deps.kube.getCoffeeConfig(ctx)
			if err != nil {
				writeKubeError(w, err)
				return
			}
			updated, err := deps.kube.patchCoffeeConfig(ctx, patchBody)
			if err != nil {
				writeKubeError(w, err)
				return
			}
			if deps.changes != nil {
				session, _ := getBrowserSession(r)
				deps.changes.record(newCoffeeConfigChange(
					time.Now(),
					current,
					updated,
					patchBody,
					session.Nickname,
					r.Header.Get("X-Change-Reason"),
				))
			}
			writeJSON(w, http.StatusOK, updated)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))

	mux.HandleFunc("/public/admin/coffeeconfig/watch", requireAdminMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		streamCoffeeConfigWatch(w, r, deps)
	}))

	mux.HandleFunc("/public/admin/coffeeconfig/changes", requireAdminMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if deps.changes == nil {
			writeJSON(w, http.StatusOK, coffeeConfigChangesSnapshot{})
			return
		}
		writeJSON(w, http.StatusOK, deps.changes.snapshot())
	}))

	mux.HandleFunc("/public/admin/coffeeconfig/changes/stream", requireAdminMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if deps.changes == nil {
			http.Error(w, "change stream unavailable", http.StatusServiceUnavailable)
			return
		}
		streamCoffeeConfigChanges(w, r, deps.changes)
	}))

	mux.HandleFunc("/public/admin/orders", requireAdminMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, deps.orders.snapshot())
	}))

	mux.HandleFunc("/public/admin/orders/debug", requireAdminMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, deps.orders.snapshot())
	}))

	mux.HandleFunc("/public/admin/orders/stream", requireAdminMiddleware(deps, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		streamOrderEvents(w, r, deps.orders)
	}))
}

func requireAdminMiddleware(deps handlerDeps, next http.HandlerFunc) http.HandlerFunc {
	return requireSessionMiddleware(deps, next)
}

func streamCoffeeConfigWatch(w http.ResponseWriter, r *http.Request, deps handlerDeps) {
	initial, watcher, err := deps.kube.watchCoffeeConfig(r.Context())
	if err != nil {
		writeKubeError(w, err)
		return
	}
	defer watcher.Stop()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	setSSEHeaders(w)
	if err := writeSSEData(w, coffeeConfigWatchEvent{Type: "CURRENT", Object: initial}); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return
			}
			cfg, ok := coffeeConfigFromWatchEvent(event)
			if !ok {
				continue
			}
			if err := writeSSEData(w, coffeeConfigWatchEvent{
				Type:   string(event.Type),
				Object: cfg,
			}); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func coffeeConfigFromWatchEvent(event k8swatch.Event) (coffeeConfig, bool) {
	// Only emit full config snapshots. Bookmark and other control events can carry
	// metadata-only objects, which would otherwise clear the frontend state.
	if event.Type != k8swatch.Added && event.Type != k8swatch.Modified {
		return coffeeConfig{}, false
	}

	switch object := event.Object.(type) {
	case *unstructured.Unstructured:
		cfg, err := toCoffeeConfig(object)
		return cfg, err == nil
	default:
		return coffeeConfig{}, false
	}
}

func streamOrderEvents(w http.ResponseWriter, r *http.Request, runtime *coffeeRuntime) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	subscriptionID, ch := runtime.subscribe(16)
	defer runtime.unsubscribe(subscriptionID)

	setSSEHeaders(w)
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case record, ok := <-ch:
			if !ok {
				return
			}
			if err := writeSSEData(w, record); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func streamCoffeeConfigChanges(w http.ResponseWriter, r *http.Request, runtime *coffeeChangeRuntime) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	subscriptionID, ch := runtime.subscribe(16)
	defer runtime.unsubscribe(subscriptionID)

	setSSEHeaders(w)
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case record, ok := <-ch:
			if !ok {
				return
			}
			if err := writeSSEData(w, record); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

func writeSSEData(w http.ResponseWriter, payload any) error {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", bytes)
	return err
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeKubeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if apierrors.IsNotFound(err) {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}
