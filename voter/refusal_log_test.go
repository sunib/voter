package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Every refusal a participant is given reaches the log, and gets there without
// the handler that refused having remembered to do anything.
//
// 2026-09-17 is what the alternative looks like. Thirty-two places in this
// package write a 4xx and not one of them logged a line, so when a large part of
// the room could not vote and another part could not save, there was nothing to
// read: both defects had to be reconstructed four days later from the
// creationTimestamps of the objects that were NOT refused.
// docs/post-demo-2026-09-17.md.
func TestEveryRefusalIsLogged(t *testing.T) {
	cfg := testConfig()
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	mux := http.NewServeMux()
	// A handler that refuses and says nothing about it, like all thirty-two.
	mux.HandleFunc("/public/refuses", requireIdentity(func(w http.ResponseWriter, _ *http.Request, _ foyerIdentity) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "The round changed. Reload the questions before voting."})
	}))
	mux.HandleFunc("/public/allows", requireIdentity(func(w http.ResponseWriter, _ *http.Request, _ foyerIdentity) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "yes"})
	}))
	mux.HandleFunc("/public/rambles", requireIdentity(func(w http.ResponseWriter, _ *http.Request, _ foyerIdentity) {
		http.Error(w, strings.Repeat("verbose ", 400), http.StatusBadRequest)
	}))
	serve := func(path string) {
		req := signedInRequest(t, cfg, http.MethodPost, path, "")
		mux.ServeHTTP(httptest.NewRecorder(), req)
	}

	serve("/public/refuses")
	line := logged.String()
	for _, want := range []string{
		"refused:",
		"POST /public/refuses",
		"status=409",
		// Who was refused, or the line cannot answer "was it everyone, or a
		// subset?" -- which is the question the demo actually raised.
		"sub=",
		// And in the refusal's own words, so the log and the phone agree.
		"Reload the questions before voting",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the refusal log is missing %q:\n%s", want, line)
		}
	}

	logged.Reset()
	serve("/public/allows")
	if logged.Len() != 0 {
		t.Errorf("a request that was allowed must not be logged as a refusal: %s", logged.String())
	}

	// A handler is free to be long-winded; the log is not obliged to carry all of
	// it. Bounded, but still enough to tell which refusal it was.
	logged.Reset()
	serve("/public/rambles")
	if got := logged.Len(); got > maxRefusalLogBytes+256 {
		t.Errorf("an unbounded refusal body reached the log: %d bytes", got)
	}
	if !strings.Contains(logged.String(), "verbose") {
		t.Errorf("the refusal was truncated past the point of being useful:\n%s", logged.String())
	}
}
