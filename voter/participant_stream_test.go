package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamPinsNamespaceNameAndVersionBeforeOpeningBackend(t *testing.T) {
	cfg := authorizationFixture(t)
	calls := 0
	mux := http.NewServeMux()
	registerParticipantStreamHandlers(mux, handlerDeps{cfg: cfg, defaultNS: "voter", newClients: func(config, string) (participantClients, error) {
		calls++
		return participantClients{}, nil
	}})
	for _, query := range []string{
		"namespace=other&name=testnet&version=v1alpha1",
		"namespace=voter&name=other&version=v1alpha1",
		"namespace=voter&version=v1alpha1",
		"namespace=voter&name=testnet&version=v9",
	} {
		t.Run(query, func(t *testing.T) {
			req := authorizedRequest(t, cfg, "GET", "token")
			req.URL.Path = "/public/stream"
			req.URL.RawQuery = "group=examples.configbutler.ai&resource=coffeeconfigs&" + query
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if !strings.Contains(rec.Body.String(), `"terminal":true`) || strings.Contains(rec.Body.String(), `"object"`) || calls != 0 {
				t.Fatalf("out-of-scope stream reached backend: calls=%d body=%s", calls, rec.Body.String())
			}
		})
	}
}

func TestStreamSessionDeadlineClosesOnlyExpiredSubscriber(t *testing.T) {
	cfg := authorizationFixture(t)
	shortClosed, longClosed := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"BOOKMARK","object":{"apiVersion":"examples.configbutler.ai/v1alpha1","kind":"CoffeeConfig","metadata":{"resourceVersion":"1","annotations":{"k8s.io/initial-events-end":"true"}}}}` + "\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		if r.Header.Get("Authorization") == "Bearer short" {
			close(shortClosed)
		} else {
			close(longClosed)
		}
	}))
	defer upstream.Close()
	cfg.KubernetesAPIServer = upstream.URL
	mux := http.NewServeMux()
	registerParticipantStreamHandlers(mux, handlerDeps{cfg: cfg, defaultNS: "voter"})
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	url := server.URL + "/public/stream?group=examples.configbutler.ai&resource=coffeeconfigs&version=v1alpha1&namespace=voter&name=testnet"
	open := func(token string, expiry time.Time) (*http.Response, *http.Request) {
		t.Helper()
		rec := httptest.NewRecorder()
		if err := setParticipantSession(rec, cfg, sessionCookieCodec, participantSession{IDToken: token, Subject: token, TokenExpiry: expiry.Unix()}, time.Now()); err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, cookie := range rec.Result().Cookies() {
			req.AddCookie(cookie)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatalf("stream status=%d", response.StatusCode)
		}
		return response, req
	}
	short, request := open("short", time.Now().Add(2*time.Second))
	defer short.Body.Close()
	long, _ := open("long", time.Now().Add(time.Hour))
	defer long.Body.Close()
	if _, err := io.Copy(io.Discard, short.Body); err != nil {
		t.Fatalf("stream did not close at expiry: %v", err)
	}
	select {
	case <-shortClosed:
	case <-ctx.Done():
		t.Fatal("expired upstream was not cancelled")
	}
	select {
	case <-longClosed:
		t.Fatal("other subscriber was cancelled")
	default:
	}
	response, err := server.Client().Do(request.Clone(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("expired reconnect status=%d", response.StatusCode)
	}
}
