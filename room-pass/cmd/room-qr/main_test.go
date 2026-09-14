package main

import (
	"strings"
	"testing"
	"time"
)

// The URL is the entire contract between this screen and the application.
// Getting it wrong is a room full of people scanning something that does not
// work, which is not the moment to find out.
func TestJoinURL(t *testing.T) {
	for _, tc := range []struct{ name, base, loginPath, next, code, want string }{
		{
			"code and destination together",
			"https://app.example.com", "/auth/login", "/answer/round-1", "BCDFGH",
			"https://app.example.com/auth/login?code=BCDFGH&return=%2Fanswer%2Fround-1",
		},
		{
			// The application's own default is "/", so saying it again only
			// makes the symbol denser for no gain.
			"the front page needs no return parameter",
			"https://app.example.com", "/auth/login", "/", "BCDFGH",
			"https://app.example.com/auth/login?code=BCDFGH",
		},
		{
			"a query in the destination survives encoding",
			"https://app.example.com", "/auth/login", "/answer/r1?lang=nl", "BCDFGH",
			"https://app.example.com/auth/login?code=BCDFGH&return=%2Fanswer%2Fr1%3Flang%3Dnl",
		},
		{
			// The login endpoint is the application's, not Room Pass's. An app
			// that starts login somewhere else gets a QR code that points there.
			"another application starts login elsewhere",
			"https://app.example.com", "/signin", "/", "BCDFGH",
			"https://app.example.com/signin?code=BCDFGH",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := options{base: tc.base, loginPath: tc.loginPath, next: tc.next}
			if got := o.joinURL(tc.code); got != tc.want {
				t.Errorf("joinURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A camera app follows whatever the symbol says, without a user ever reading
// it. So the flags that end up inside it are checked before anything is drawn.
func TestOptionsValidate(t *testing.T) {
	ok := options{base: "https://app.example.com", loginPath: "/auth/login", next: "/", interval: time.Second}
	with := func(f func(*options)) options {
		o := ok
		f(&o)
		return o
	}

	for _, tc := range []struct {
		name string
		o    options
		bad  bool
	}{
		{"a bare https origin", ok, false},
		{"localhost may be plaintext for development", with(func(o *options) { o.base = "http://localhost:8080" }), false},
		// Handing the room an http:// URL means handing it a session cookie
		// over plaintext, and nobody inspects a QR code before scanning it.
		{"a plaintext origin is refused", with(func(o *options) { o.base = "http://app.example.com" }), true},
		{"a missing origin is refused", with(func(o *options) { o.base = "" }), true},
		{"an origin with a path is refused", with(func(o *options) { o.base = "https://app.example.com/app" }), true},
		{"an origin with a query is refused", with(func(o *options) { o.base = "https://app.example.com?x=1" }), true},
		// The application validates this again and falls back to "/", but a
		// presenter deserves to be told now rather than to discover it when
		// three hundred people land on the wrong page.
		{"an off-site destination is refused", with(func(o *options) { o.next = "https://evil.test/" }), true},
		{"a protocol-relative destination is refused", with(func(o *options) { o.next = "//evil.test/" }), true},
		{"a relative destination is refused", with(func(o *options) { o.next = "answer/round-1" }), true},
		{"a deep path is fine", with(func(o *options) { o.next = "/answer/round-1" }), false},
		// --login-path ends up inside the symbol exactly like --next does, so
		// it is checked exactly like --next.
		{"another login path is fine", with(func(o *options) { o.loginPath = "/signin" }), false},
		{"an off-site login path is refused", with(func(o *options) { o.loginPath = "https://evil.test/login" }), true},
		{"a protocol-relative login path is refused", with(func(o *options) { o.loginPath = "//evil.test/login" }), true},
		{"a relative login path is refused", with(func(o *options) { o.loginPath = "auth/login" }), true},
		{"a login path carrying a query is refused", with(func(o *options) { o.loginPath = "/auth/login?code=X" }), true},
		{"a hot-spinning interval is refused", with(func(o *options) { o.interval = time.Millisecond }), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.o.validate()
			if tc.bad && err == nil {
				t.Errorf("validate() accepted %+v", tc.o)
			}
			if !tc.bad && err != nil {
				t.Errorf("validate() rejected %+v: %v", tc.o, err)
			}
		})
	}
}

// Read aloud, a code is grouped. Room Pass's Normalize strips the separators
// again, so what is on screen and what is typed stay the same code.
func TestSpaced(t *testing.T) {
	for in, want := range map[string]string{
		"BCDFGH":       "BCD FGH",
		"BCDFGHJK":     "BCD FGH JK",
		"BCD":          "BCD",
		"BCDFGHJKLMNP": "BCD FGH JKL MNP",
	} {
		got := spaced(in)
		if got != want {
			t.Errorf("spaced(%q) = %q, want %q", in, got, want)
		}
		// Grouping is presentation. It must never change the code itself.
		if strings.ReplaceAll(got, " ", "") != in {
			t.Errorf("spaced(%q) = %q, which is a different code", in, got)
		}
	}
}
