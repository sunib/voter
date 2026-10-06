package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A scanned QR code hands the room code to Room Pass over a cookie, because
// the join URL is built by Room Pass after Dex and has nowhere to carry it.
// What goes into that cookie is bounded here; whether the code is VALID is
// Room Pass's question, asked against the Room's rotating status.
func TestSafeJoinCode(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"a plain code passes through", "BCDFGH", "BCDFGH"},
		{"lowercase is normalised the way Room Pass normalises it", "bcdfgh", "BCDFGH"},
		{"a presenter's hyphens are stripped", "BCD-FGH", "BCDFGH"},
		{"surrounding whitespace is trimmed", "  BCDFGH  ", "BCDFGH"},
		{"digits are allowed, so a future alphabet still works", "BC1FG2", "BC1FG2"},
		{"an absent code is not a code", "", ""},
		{"whitespace alone is not a code", "   ", ""},
		// The Room CRD caps joinCode.length at 12. Anything longer was never
		// a code, so it has no business in a Set-Cookie header.
		{"an over-long value is refused", strings.Repeat("B", 13), ""},
		{"a maximum-length code is accepted", strings.Repeat("B", 12), strings.Repeat("B", 12)},
		// Not an escaping test -- http.SetCookie would sanitise these anyway.
		// This is about refusing to forward anything that was never a code.
		{"header injection is refused", "BCD\r\nSet-Cookie: x=y", ""},
		{"a cookie attribute is refused", "BCDFGH; Path=/", ""},
		{"punctuation is refused", "BCD_FGH", ""},
		{"a unicode lookalike is refused", "ВCDFGH", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := safeJoinCode(tc.in); got != tc.want {
				t.Errorf("safeJoinCode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The hand-off cookie has to survive a redirect chain that leaves this origin
// for the issuer and comes back. That rules out SameSite=Strict, and it has to
// stay unreadable to script on the way.
func TestJoinCodeHandoffCookieAttributes(t *testing.T) {
	rec := httptest.NewRecorder()
	setJoinCodeHandoff(rec, "bcd-fgh")

	var found *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == joinCodeHandoffCookie {
			found = c
		}
	}
	if found == nil {
		t.Fatalf("no %s cookie was set; got %v", joinCodeHandoffCookie, rec.Result().Cookies())
	}
	if found.Value != "BCDFGH" {
		t.Errorf("value = %q, want the normalised code", found.Value)
	}
	if !found.HttpOnly {
		t.Error("the hand-off cookie must not be readable by script")
	}
	if !found.Secure {
		t.Error("the hand-off cookie must be Secure; the __Host- prefix requires it")
	}
	if found.SameSite != http.SameSiteLaxMode {
		// Strict drops the cookie on the top-level navigation back from the
		// issuer, which is the one request that needs it.
		t.Errorf("SameSite = %v, want Lax", found.SameSite)
	}
	if found.Path != "/" {
		t.Errorf("Path = %q, want / so Room Pass's /join receives it", found.Path)
	}
	if found.MaxAge <= 0 {
		t.Errorf("MaxAge = %d, want a bounded positive lifetime", found.MaxAge)
	}

	// A login that did not come from a QR code must not set anything at all.
	empty := httptest.NewRecorder()
	setJoinCodeHandoff(empty, "")
	if cookies := empty.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("an ordinary login set %d cookies, want none", len(cookies))
	}
}

// The QR code's target: the code into Room Pass's cookie, and the browser to
// krm-foyer's login through Room Pass. The code never goes into a URL past
// this one -- that URL ends up in the issuer's logs and the browser's history.
func TestJoinRoom(t *testing.T) {
	mux := http.NewServeMux()
	registerJoinRoomHandler(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/join-room?code=bcd-fgh", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want a redirect to the login", rec.Code)
	}
	location := rec.Header().Get("Location")
	if location != "/auth/login?return_to=%2F&oidc.connector_id=room-pass" {
		t.Errorf("Location = %q, want krm-foyer's login through Room Pass", location)
	}
	if strings.Contains(strings.ToUpper(location), "BCDFGH") {
		t.Errorf("the room code leaked into the login URL: %s", location)
	}
	var handoff string
	for _, c := range rec.Result().Cookies() {
		if c.Name == joinCodeHandoffCookie {
			handoff = c.Value
		}
	}
	if handoff != "BCDFGH" {
		t.Errorf("hand-off cookie = %q, want the scanned code", handoff)
	}

	for _, target := range []string{"/join-room", "/join-room?code=", "/join-room?code=BCD_FGH"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", target, rec.Code)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s: set a cookie for something that is not a code", target)
		}
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/join-room?code=BCDFGH", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status = %d, want 405", rec.Code)
	}
}
