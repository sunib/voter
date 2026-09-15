package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/securecookie"
	"github.com/prometheus/client_golang/prometheus"

	api "github.com/sunib/voter/room-pass/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type uidClient struct{ client.Client }

func (c uidClient) Create(ctx context.Context, o client.Object, opts ...client.CreateOption) error {
	o.SetUID(types.UID("uid-" + o.GetName()))
	return c.Client.Create(ctx, o, opts...)
}
func fixture(t *testing.T, upstream string) (*Server, client.Client) {
	t.Helper()
	now := time.Now()
	scheme := runtime.NewScheme()
	_ = api.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	room := &api.Room{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "room-pass", UID: "room-uid"}, Spec: api.RoomSpec{Title: "Demo", EndsAt: metav1.NewTime(now.Add(time.Hour)), Enrollment: "Open", MaxParticipants: 100, AudienceGroup: "demo:test", AllowedReturnURLs: []string{"https://demo.test/app/"}}, Status: api.RoomStatus{ValidJoinCodes: []api.Code{{Code: "BCDFGH", IssuedAt: metav1.NewTime(now.Add(-time.Second)), ExpiresAt: metav1.NewTime(now.Add(time.Hour))}}}}
	db := uidClient{fake.NewClientBuilder().WithScheme(scheme).WithObjects(room).Build()}
	s, e := New(Config{Room: client.ObjectKeyFromObject(room), ConnectorID: "room-pass", JoinOrigin: "https://demo.test", IssuerOrigin: "https://login.test", DexUpstream: upstream, AllowedReturns: []string{"https://demo.test/app/"}, HashKey: []byte(strings.Repeat("h", 32)), BlockKey: []byte(strings.Repeat("b", 32))}, db)
	if e != nil {
		t.Fatal(e)
	}
	return s, db
}
func TestEnrollmentConcurrencyAndLifecycle(t *testing.T) {
	s, db := fixture(t, "http://dex.test")
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, e := s.enrollParticipant(context.Background(), "bcd-fgh", fmt.Sprintf("Ada %d", i)); e == nil {
				ok.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if ok.Load() != 100 {
		t.Fatalf("shared-room burst: %d", ok.Load())
	}
	if _, e := s.enrollParticipant(context.Background(), "BCDFGH", "overflow"); e == nil {
		t.Fatal("cap bypassed")
	}
	ps := &api.ParticipantList{}
	_ = db.List(context.Background(), ps)
	p := ps.Items[0]
	ss := session{RoomUID: "room-uid", Name: p.Name, UID: string(p.UID), Expires: time.Now().Add(time.Hour).Unix()}
	room := &api.Room{}
	_ = db.Get(context.Background(), s.cfg.Room, room)
	room.Spec.Enrollment = "Closed"
	_ = db.Update(context.Background(), room)
	if _, _, e := s.identity(context.Background(), ss); e != nil {
		t.Fatal("closure invalidated existing session", e)
	}
	// New server, same API and keys: enrollment survives a process restart.
	restarted, e := New(s.cfg, db)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = restarted.identity(context.Background(), ss); e != nil {
		t.Fatal(e)
	}
	p.Spec.Revoked = true
	_ = db.Update(context.Background(), &p)
	if _, _, e = s.identity(context.Background(), ss); e == nil {
		t.Fatal("revocation ignored")
	}
	p.Spec.Revoked = false
	p.UID = "replacement"
	_ = db.Update(context.Background(), &p)
	if _, _, e = s.identity(context.Background(), ss); e == nil {
		t.Fatal("replacement participant revived cookie")
	}
	room.UID = "replacement-room"
	_ = db.Update(context.Background(), room)
	if _, _, e = s.identity(context.Background(), ss); e == nil {
		t.Fatal("replacement room revived cookie")
	}
}

type browser struct {
	cookies map[string]map[string]*http.Cookie
}

func (b *browser) request(s *Server, method, raw string, form url.Values) *httptest.ResponseRecorder {
	u, _ := url.Parse(raw)
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, raw, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "https://demo.test")
	}
	r.Header.Set("X-Remote-User", "forged")
	r.Header.Set("X-Remote-Group", "system:masters")
	for _, c := range b.cookies[u.Host] {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if b.cookies[u.Host] == nil {
		b.cookies[u.Host] = map[string]*http.Cookie{}
	}
	for _, c := range w.Result().Cookies() {
		b.cookies[u.Host][c.Name] = c
	}
	return w
}
func newBrowser() *browser { return &browser{cookies: map[string]map[string]*http.Cookie{}} }
func TestHandoffHeadersReplayAndCSRF(t *testing.T) {
	var got http.Header
	var state string
	dex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		state = r.URL.Query().Get("state")
		w.WriteHeader(204)
	}))
	defer dex.Close()
	s, _ := fixture(t, dex.URL)
	reg := prometheus.NewRegistry()
	if _, err := s.Instrument(reg); err != nil {
		t.Fatal(err)
	}
	b := newBrowser()
	w := b.request(s, "GET", "https://login.test/callback/room-pass?state=dex-transaction", nil)
	for i := 0; i < 3; i++ {
		if w.Code != 303 {
			t.Fatalf("binding step %d: %d %s", i, w.Code, w.Body.String())
		}
		w = b.request(s, "GET", w.Header().Get("Location"), nil)
	}
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Extract values from the rendered form without duplicating protocol internals.
	value := func(name string) string {
		prefix := fmt.Sprintf(`name="%s" value="`, name)
		_, rest, _ := strings.Cut(w.Body.String(), prefix)
		v, _, _ := strings.Cut(rest, `"`)
		return v
	}
	form := url.Values{"csrf": {value("csrf")}, "handoff": {value("handoff")}, "return": {value("return")}, "code": {"BCD-FGH"}, "name": {"Ada"}, "group": {"system:masters"}, "id": {"attacker"}}
	bad := url.Values{}
	for k, v := range form {
		bad[k] = v
	}
	bad.Set("csrf", "wrong")
	if r := b.request(s, "POST", "https://demo.test/join", bad); r.Code != 403 {
		t.Fatal("CSRF accepted")
	}
	w = b.request(s, "POST", "https://demo.test/join", form)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	complete := w.Header().Get("Location")
	if wrong := newBrowser().request(s, "GET", complete, nil); wrong.Code != 403 {
		t.Fatal("wrong browser accepted")
	}
	w = b.request(s, "GET", complete, nil)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if got.Get("X-Remote-User") != "Ada" || got.Get("X-Remote-Group") != "demo:test" || got.Get("X-Remote-User-Id") == "attacker" || state != "dex-transaction" {
		t.Fatal("identity contract", got, state)
	}
	if w = b.request(s, "GET", complete, nil); w.Code != 403 {
		t.Fatal("replay accepted")
	}
	assertCounter(t, reg, "room_pass_identity_handoffs_total", 1)
	for _, path := range []string{"/callback", "/callback/other", "/callback/room", "/callback/room-pass/", "/%63allback", "/unknown"} {
		if w = b.request(s, "GET", "https://login.test"+path, nil); w.Code < 400 {
			t.Fatalf("alias reached Dex: %s", path)
		}
	}
}
func TestStolenCrossHostLinkCannotEnroll(t *testing.T) {
	s, _ := fixture(t, "http://dex.test")
	attacker := newBrowser()
	victim := newBrowser()
	w := attacker.request(s, "GET", "https://login.test/callback/room-pass?state=attack", nil)
	stolen := w.Header().Get("Location")
	w = victim.request(s, "GET", stolen, nil)
	confirm := w.Header().Get("Location")
	if w = victim.request(s, "GET", confirm, nil); w.Code != 403 {
		t.Fatal("unrelated issuer browser confirmed")
	}
	if w = attacker.request(s, "GET", stolen, nil); w.Code != 403 {
		t.Fatal("original binding handle reusable")
	}
}

func TestCurrentRoomStateAndCookieTampering(t *testing.T) {
	s, db := fixture(t, "http://dex.test")
	ctx := context.Background()
	ss, e := s.enrollParticipant(ctx, "BCDFGH", "Ada")
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	if e = s.cookie(w, "__Host-rp-session", ss, 3600); e != nil {
		t.Fatal(e)
	}
	cookie := w.Result().Cookies()[0]
	cookie.Value = "tampered" + cookie.Value
	req := httptest.NewRequest("GET", "https://demo.test/join", nil)
	req.AddCookie(cookie)
	var decoded session
	if s.decode(req, "__Host-rp-session", &decoded) == nil {
		t.Fatal("tampered cookie accepted")
	}
	room := &api.Room{}
	_ = db.Get(ctx, s.cfg.Room, room)
	room.Spec.EndsAt = metav1.NewTime(time.Now().Add(-time.Minute))
	_ = db.Update(ctx, room)
	if _, _, e = s.identity(ctx, ss); e == nil {
		t.Fatal("expiry ignored")
	}
	room.Spec.EndsAt = metav1.NewTime(time.Now().Add(time.Hour))
	_ = db.Update(ctx, room)
	if _, _, e = s.identity(ctx, ss); e != nil {
		t.Fatal("extension lost enrollment", e)
	}
	room.Spec.Stopped = true
	_ = db.Update(ctx, room)
	if _, _, e = s.identity(ctx, ss); e == nil {
		t.Fatal("stop ignored")
	}
	_ = db.Delete(ctx, room)
	if _, _, e = s.identity(ctx, ss); e == nil {
		t.Fatal("missing room accepted")
	}
}
func TestHandoffCapacityAndExpiry(t *testing.T) {
	s, _ := fixture(t, "http://dex.test")
	s.cfg.MaxHandoffs = 1
	b := newBrowser()
	if w := b.request(s, "GET", "https://login.test/callback/room-pass?state=one", nil); w.Code != 303 {
		t.Fatal(w.Code)
	}
	if w := b.request(s, "GET", "https://login.test/callback/room-pass?state=two", nil); w.Code != 429 {
		t.Fatal("capacity not enforced")
	}
	s.now = func() time.Time { return time.Now().Add(4 * time.Minute) }
	if w := b.request(s, "GET", "https://login.test/callback/room-pass?state=three", nil); w.Code != 303 {
		t.Fatal("expired slot not pruned", w.Code)
	}
}

// A browser sends "Origin: null" on a form POST whenever the referrer policy
// strips the origin -- which this server itself used to cause by sending
// Referrer-Policy: no-referrer. Every real browser was rejected while curl,
// which implements no referrer policy, passed. Guard the shape of the check so
// that cannot come back.
func TestCSRFOriginHandling(t *testing.T) {
	s := &Server{
		cfg:     Config{JoinOrigin: "https://voter.example"},
		cookies: securecookie.New([]byte("0123456789abcdef0123456789abcdef"), []byte("0123456789abcdef")),
	}
	token := "tok"
	encoded, err := s.cookies.Encode("__Host-rp-csrf", token)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, origin, field string
		want                string
	}{
		{"matching origin passes", "https://voter.example", token, ""},
		{"null origin passes on a valid token", "null", token, ""},
		{"absent origin passes on a valid token", "", token, ""},
		{"a different origin is still refused", "https://evil.example", token, "origin-mismatch"},
		{"a wrong token is refused whatever the origin", "https://voter.example", "nope", "csrf-mismatch"},
		{"a wrong token is refused on a null origin too", "null", "nope", "csrf-mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/join", strings.NewReader("csrf="+tc.field))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			r.AddCookie(&http.Cookie{Name: "__Host-rp-csrf", Value: encoded})
			if got := s.csrfReason(r); got != tc.want {
				t.Errorf("csrfReason = %q, want %q", got, tc.want)
			}
		})
	}
}

// A returning participant is shown the name they chose. It is the only place
// they can see which identity they are about to continue as, and that name ends
// up on a Git commit -- so it must be both present and escaped, since the
// participant typed it.
func TestEnrolledPageShowsTheChosenName(t *testing.T) {
	if !strings.Contains(pageSource, "{{.EnrolledName}}") {
		t.Fatal("the join page no longer shows the enrolled participant's name")
	}
	tmpl := template.Must(template.New("t").Parse(pageSource))
	var out strings.Builder
	if err := tmpl.Execute(&out, map[string]any{
		"Title": "Demo", "Message": "m", "Form": true, "CSRF": "c",
		"Handoff": "", "Return": "https://demo.test/app/",
		"Enrolled": true, "EnrolledName": `Ada <script>alert(1)</script>`,
	}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "Ada &lt;script&gt;") {
		t.Errorf("the display name must be escaped; got: %s", got)
	}
	if strings.Contains(got, "<script>alert(1)</script>") {
		t.Error("a participant-supplied name was rendered as markup")
	}
}

// Enrollment cookies identify an object incarnation, not just a reusable name.
// Replacing or revoking a Participant must stop a subsequent identity handoff.
func TestIdentityRequiresCurrentEnrollment(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*api.Participant)
		expire bool
	}{
		{name: "active enrollment"},
		{name: "revoked participant", change: func(p *api.Participant) { p.Spec.Revoked = true }},
		{name: "replacement participant", change: func(p *api.Participant) { p.UID = "replacement-uid" }},
		{name: "different room UID", change: func(p *api.Participant) { p.Spec.RoomRef.UID = "another-room" }},
		{name: "different room name", change: func(p *api.Participant) { p.Spec.RoomRef.Name = "another-room" }},
		{name: "expired enrollment cookie", expire: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := fixture(t, "http://dex.test")
			ctx := context.Background()
			ss, err := s.enrollParticipant(ctx, "BCDFGH", "Ada")
			if err != nil {
				t.Fatal(err)
			}
			if tc.change != nil {
				p := &api.Participant{}
				if err := db.Get(ctx, client.ObjectKey{Namespace: s.cfg.Room.Namespace, Name: ss.Name}, p); err != nil {
					t.Fatal(err)
				}
				tc.change(p)
				if err := db.Update(ctx, p); err != nil {
					t.Fatal(err)
				}
			}
			if tc.expire {
				ss.Expires = s.now().Unix()
			}
			_, _, err = s.identity(ctx, ss)
			wantDenied := tc.change != nil || tc.expire
			if (err != nil) != wantDenied {
				t.Fatalf("identity error = %v, want denied = %v", err, wantDenied)
			}
		})
	}
}

// The join POST is the start of a redirect chain that crosses three origins,
// and Chromium checks form-action against every hop. A source list that covers
// only the first two works perfectly until the application is moved to its own
// host, and then every browser login fails while curl and the Go test client --
// neither of which enforces CSP -- keep passing.
func TestFormActionCoversTheApplicationOrigin(t *testing.T) {
	for _, tc := range []struct {
		name    string
		issuer  string
		returns []string
		want    []string
		absent  []string
	}{
		{
			name:    "application on its own host is permitted",
			issuer:  "https://login.example",
			returns: []string{"https://app.example/"},
			want:    []string{"'self'", "https://login.example", "https://app.example"},
		},
		{
			name:    "the common deployment, where the app shares the join origin",
			issuer:  "https://dex.example",
			returns: []string{"https://voter.example/"},
			want:    []string{"'self'", "https://dex.example", "https://voter.example"},
		},
		{
			name:   "several applications each get their origin",
			issuer: "https://login.example",
			returns: []string{
				"https://one.example/app/",
				"https://two.example/",
				"https://one.example/other/",
			},
			want: []string{"https://one.example", "https://two.example"},
		},
		{
			name:    "a path never reaches the source list",
			issuer:  "https://login.example",
			returns: []string{"https://app.example/deep/path/"},
			want:    []string{"https://app.example"},
			absent:  []string{"https://app.example/deep/path/", "/deep/"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formActionSources(Config{IssuerOrigin: tc.issuer, AllowedReturns: tc.returns})
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("form-action %q is missing %q", got, want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(got, absent) {
					t.Errorf("form-action %q leaked %q; only origins belong here", got, absent)
				}
			}
			// A duplicate is harmless but signals the de-duplication broke,
			// and this header is sent on every join render.
			if n := strings.Count(got, "'self'"); n != 1 {
				t.Errorf("form-action %q contains 'self' %d times", got, n)
			}
		})
	}
}

func TestFormActionIgnoresUnusableReturnEntries(t *testing.T) {
	// Config validation already rejects these at startup; this pins that a
	// malformed entry can never widen the header to something like "://" or an
	// empty source, which browsers parse unpredictably.
	got := formActionSources(Config{
		IssuerOrigin:   "https://login.example",
		AllowedReturns: []string{"", "   ", "not-a-url", "/relative/only"},
	})
	if got != "'self' https://login.example" {
		t.Fatalf("form-action = %q, want only 'self' and the issuer", got)
	}
}

// A scanned QR code turns the join page into one field. The code arrives in a
// cookie set by the application on this shared host, is rendered back as a
// hidden field, and comes home through the ordinary POST -- where it is
// checked against the Room like any typed code. None of that makes the cookie
// trusted; these tests pin the boundary.
func TestScannedCodeIsAPrefillAndNothingMore(t *testing.T) {
	s, _ := fixture(t, "http://dex.test")

	get := func(url string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", url, nil)
		r.Host = "demo.test"
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	handoff := func(code string) *http.Cookie {
		return &http.Cookie{Name: joinCodeHandoffCookie, Value: code}
	}

	t.Run("the cookie prefills the form and retires the code field", func(t *testing.T) {
		body := get("https://demo.test/join", handoff("bcd-fgh")).Body.String()
		if !strings.Contains(body, `<input type="hidden" name="code" value="BCDFGH">`) {
			t.Errorf("the scanned code was not carried into the form: %s", body)
		}
		if strings.Contains(body, `placeholder="BCDFGH"`) {
			t.Error("the code field is still being asked for after a scan")
		}
		if !strings.Contains(body, "Choose a display name") {
			t.Error("the page still asks for a code the participant already scanned")
		}
	})

	t.Run("a query parameter works too, for a QR pointing straight here", func(t *testing.T) {
		body := get("https://demo.test/join?code=BCDFGH").Body.String()
		if !strings.Contains(body, `value="BCDFGH"`) {
			t.Errorf("the query code was not carried into the form: %s", body)
		}
	})

	t.Run("the code is used once and then expired", func(t *testing.T) {
		res := get("https://demo.test/join", handoff("BCDFGH")).Result()
		for _, c := range res.Cookies() {
			if c.Name == joinCodeHandoffCookie {
				if c.MaxAge >= 0 {
					t.Errorf("MaxAge = %d, want the hand-off cookie expired after use", c.MaxAge)
				}
				return
			}
		}
		t.Error("the hand-off cookie was left in the jar for the next join")
	})

	t.Run("an ordinary visit still asks for a code", func(t *testing.T) {
		body := get("https://demo.test/join").Body.String()
		if !strings.Contains(body, `placeholder="BCDFGH"`) {
			t.Errorf("the code field disappeared without a scan: %s", body)
		}
	})

	t.Run("junk in the cookie is not rendered as a code", func(t *testing.T) {
		for _, bad := range []string{
			"", "   ", strings.Repeat("B", 13), "BCD FGH", "BCD_FGH",
			`"><script>alert(1)</script>`,
		} {
			body := get("https://demo.test/join", handoff(bad)).Body.String()
			if !strings.Contains(body, `placeholder="BCDFGH"`) {
				t.Errorf("cookie %q suppressed the code field: %s", bad, body)
			}
			if strings.Contains(body, "<script>alert(1)</script>") {
				t.Errorf("cookie %q was rendered as markup", bad)
			}
		}
	})

	t.Run("a forged code is still only a wrong code", func(t *testing.T) {
		// The cookie is plain text and anything on this host can write it.
		// That has to be worth nothing: the POST re-checks the value against
		// the Room's valid codes, so a made-up one fails exactly as a typed
		// made-up one fails.
		if _, err := s.enrollParticipant(context.Background(), "ZZZZZZ", "Mallory"); err == nil {
			t.Fatal("a code that the Room never issued was accepted")
		}
	})
}

// Prefilling must not quietly relax the page's other rules. A closed Room does
// not open because somebody arrived with a code in a cookie.
func TestScannedCodeDoesNotReopenAClosedRoom(t *testing.T) {
	s, db := fixture(t, "http://dex.test")
	room := &api.Room{}
	if err := db.Get(context.Background(), s.cfg.Room, room); err != nil {
		t.Fatal(err)
	}
	room.Spec.Enrollment = "Closed"
	if err := db.Update(context.Background(), room); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "https://demo.test/join", nil)
	r.Host = "demo.test"
	r.AddCookie(&http.Cookie{Name: joinCodeHandoffCookie, Value: "BCDFGH"})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "Joining is closed") {
		t.Errorf("a scan was offered a form into a closed room: %s", body)
	}
	if strings.Contains(body, `name="code"`) {
		t.Errorf("a closed room still rendered a join form: %s", body)
	}
}

func TestWrongCodeReturnsACorrectableForm(t *testing.T) {
	s, db := fixture(t, "http://dex.test")
	b := newBrowser()
	value := func(body, name string) string {
		_, rest, _ := strings.Cut(body, fmt.Sprintf(`name="%s" value="`, name))
		v, _, _ := strings.Cut(rest, `"`)
		return v
	}
	// Everything the template wrote for one input, so an attribute added elsewhere
	// in the form cannot make this pass by accident.
	input := func(body, name string) string {
		_, rest, _ := strings.Cut(body, fmt.Sprintf(`name="%s"`, name))
		v, _, _ := strings.Cut(rest, ">")
		return v
	}
	w := b.request(s, "GET", "https://demo.test/join", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	form := url.Values{"csrf": {value(w.Body.String(), "csrf")}, "return": {"https://demo.test/app/"}, "code": {"WRONG"}, "name": {"Ada"}}
	w = b.request(s, "POST", "https://demo.test/join", form)
	body := w.Body.String()
	if w.Code != 403 || !strings.Contains(body, "That code is invalid") {
		t.Fatalf("wrong code: %d %s", w.Code, body)
	}
	// The participant stays on a form they can correct: the code is marked, the name
	// they already typed survives, and nothing was enrolled on the way.
	if !strings.Contains(input(body, "code"), `aria-invalid="true"`) {
		t.Fatalf("code field not marked: %s", body)
	}
	if strings.Contains(input(body, "name"), "aria-invalid") || !strings.Contains(input(body, "name"), `value="Ada"`) {
		t.Fatalf("name field marked or cleared: %s", body)
	}
	ps := &api.ParticipantList{}
	_ = db.List(context.Background(), ps)
	if len(ps.Items) != 0 {
		t.Fatalf("a rejected code enrolled someone: %d", len(ps.Items))
	}
	// The token minted by that re-render has to be good, or the retry is another dead end.
	form.Set("csrf", value(body, "csrf"))
	form.Set("code", "BCDFGH")
	if w = b.request(s, "POST", "https://demo.test/join", form); w.Code != 303 {
		t.Fatalf("retry after a wrong code: %d %s", w.Code, w.Body.String())
	}
	_ = db.List(context.Background(), ps)
	if len(ps.Items) != 1 || ps.Items[0].Spec.DisplayName != "Ada" {
		t.Fatalf("retry did not enroll Ada: %v", ps.Items)
	}
	// A rejected name marks the name field instead, and leaves the code alone.
	nb := newBrowser()
	w = nb.request(s, "GET", "https://demo.test/join", nil)
	bad := url.Values{"csrf": {value(w.Body.String(), "csrf")}, "return": {"https://demo.test/app/"}, "code": {"BCDFGH"}, "name": {"Ada<b>"}}
	w = nb.request(s, "POST", "https://demo.test/join", bad)
	body = w.Body.String()
	if w.Code != 400 || !strings.Contains(body, "angle brackets") {
		t.Fatalf("rejected name: %d %s", w.Code, body)
	}
	if !strings.Contains(input(body, "name"), `aria-invalid="true"`) || strings.Contains(input(body, "code"), "aria-invalid") {
		t.Fatalf("wrong field marked: %s", body)
	}
	if strings.Contains(body, "<b>") {
		t.Fatalf("echoed name was not escaped: %s", body)
	}
}

// The address on the join page is a promise: whatever it shows while someone
// types is the address that ends up on their commits. These pairs are the
// contract both halves implement -- the table is also checked against the
// page's script in test/browser/room-auth.spec.js, which runs it in a browser.
func TestParticipantIDFoldsNamesPredictably(t *testing.T) {
	for name, want := range map[string]string{
		"Ada Demo":                     "ada-demo",
		"Simon Koudijs":                "simon-koudijs",
		"  Jan-Willem   van der Berg ": "jan-willem-van-der-berg",
		"Renée O'Hara":                 "renee-o-hara",
		"JOSÉ":                         "jose",
		"Ångström":                     "angstrom",
		"Zoë-Ann  ":                    "zoe-ann",
		"Müller":                       "muller",
		"ﬁona":                         "fiona",
		"a--b":                         "a-b",
		"--lead--":                     "lead",
		"N1ck 2":                       "n1ck-2",
		"🎉 Party 🎉":                    "party",
		"日本 Taro":                      "taro",
		"🎉":                            "",
		"日本語":                          "",
		"A very very long display name that goes past the forty character cap": "a-very-very-long-display-name-that-goes",
	} {
		if got := participantID(name); got != want {
			t.Errorf("participantID(%q) = %q, want %q", name, got, want)
		}
	}
	id := participantID("A very very long display name that goes past the forty character cap")
	if len(id) > maxParticipantID || strings.HasPrefix(id, "-") || strings.HasSuffix(id, "-") {
		t.Errorf("identifier is not a usable object name or local part: %q", id)
	}
}

// The pairing the voter app depends on: a submission's object name is built as
// "<round>-<lowercased display name>", so lowercasing what validName STORES has
// to reproduce participantID exactly. Anything that makes the two folds diverge
// -- a different cap, a different combining-mark range, a stray allowed rune --
// silently points a ballot at a participant who does not exist, so it is
// asserted over the same corpus the fold itself is tested with.
func TestLabelNameLowercasesToParticipantID(t *testing.T) {
	for _, name := range []string{
		"Ada Demo", "Simon Koudijs", "  Jan-Willem   van der Berg ",
		"Renée O'Hara", "JOSÉ", "Ångström", "Zoë-Ann  ", "Müller", "ﬁona",
		"a--b", "--lead--", "N1ck 2", "🎉 Party 🎉", "日本 Taro", "İstanbul",
		"A very very long display name that goes past the forty character cap",
	} {
		if got, want := strings.ToLower(labelName(name)), participantID(name); got != want {
			t.Errorf("ToLower(labelName(%q)) = %q, want participantID = %q", name, got, want)
		}
	}
}

// What validName returns is what gets stored, so it has to satisfy the CRD
// pattern on Participant.spec.displayName: a legal Kubernetes label value that
// is also legal, once lowercased, as the tail of a DNS-1123 object name.
func TestValidNameStoresAFoldedName(t *testing.T) {
	legal := regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9]*[A-Za-z0-9])?$`)
	for raw, want := range map[string]string{
		"Ada Lovelace": "Ada-Lovelace",
		"Renée O'Hara": "Renee-O-Hara",
		"  --Ada!!  ":  "Ada",
		"Zoë-Ann":      "Zoe-Ann",
		"日本 Taro":      "Taro",
		// 43 raw bytes, inside validName's 64-byte input cap, but folding past
		// the forty-character identifier cap: the tail is cut, not the name
		// refused, and no trailing separator survives the cut.
		"Alexandra Bartholomew Fitzgerald Montgomery": "Alexandra-Bartholomew-Fitzgerald-Montgom",
	} {
		got, err := validName(raw)
		if err != nil {
			t.Errorf("validName(%q): %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("validName(%q) = %q, want %q", raw, got, want)
		}
		if !legal.MatchString(got) || len(got) > maxParticipantID {
			t.Errorf("validName(%q) = %q, which the CRD pattern would refuse", raw, got)
		}
	}
}

// A name that survives every other check but leaves nothing to build an
// identity from has to be refused at the form, not at the Kubernetes API.
func TestNamesWithoutUsableCharactersAreRefused(t *testing.T) {
	if _, e := validName("🎉"); e == nil {
		t.Error("a name with nothing to fold was accepted")
	}
	if _, e := validName("Ada"); e != nil {
		t.Error(e)
	}
}

// Names are identities now, so the second person to claim one is told, not
// quietly seated at the first person's Participant with their own cookie.
func TestASecondClaimOnANameIsRefused(t *testing.T) {
	s, db := fixture(t, "http://dex.test")
	first, e := s.enrollParticipant(context.Background(), "BCDFGH", "Ada Demo")
	if e != nil {
		t.Fatal(e)
	}
	if first.Name != "p-ada-demo" {
		t.Fatalf("participant object name: %q", first.Name)
	}
	if _, e = s.enrollParticipant(context.Background(), "BCDFGH", "ada  demo"); !errors.Is(e, errNameTaken) {
		t.Fatalf("a colliding name enrolled anyway: %v", e)
	}
	ps := &api.ParticipantList{}
	_ = db.List(context.Background(), ps)
	if len(ps.Items) != 1 {
		t.Fatalf("participants after the refused claim: %d", len(ps.Items))
	}
	if _, e = s.enrollParticipant(context.Background(), "BCDFGH", "Ada Demo 2"); e != nil {
		t.Fatal(e)
	}
}

// The address is the participant ID with a reserved domain, in the headers Dex
// reads and on the page the participant reads. One derivation, no drift.
func TestIssuedAddressMatchesTheParticipantID(t *testing.T) {
	p := &api.Participant{}
	p.Name = participantPrefix + participantID("Ada Demo")
	if got := participantEmail(p); got != "ada-demo@koudijs.dev.test" {
		t.Errorf("participant address: %q", got)
	}
	if got := emailPreview("Ada Demo"); got != participantEmail(p) {
		t.Errorf("the preview promises %q", got)
	}
	if got := emailPreview(""); got != "your-name@koudijs.dev.test" {
		t.Errorf("empty-name placeholder: %q", got)
	}
	if !strings.Contains(previewScript, `"your-name"`) {
		t.Error("the page's script no longer uses the same placeholder")
	}
}

// The preview script runs only because the CSP carries its hash. A rendered
// page proves the two still describe the same bytes: an edit to the script that
// forgot the policy would leave the address frozen in every real browser.
func TestPreviewScriptIsServedUnderItsOwnCSPHash(t *testing.T) {
	s, _ := fixture(t, "http://dex.test")
	w := newBrowser().request(s, "GET", "https://demo.test/join", nil)
	body := w.Body.String()
	open := strings.Index(body, "<script>")
	end := strings.Index(body, "</script>")
	if open < 0 || end < open {
		t.Fatal("the join page no longer carries the preview script")
	}
	sum := sha256.Sum256([]byte(body[open+len("<script>") : end]))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src "+want+";") {
		t.Errorf("the served script is not the one the policy admits.\ncsp:  %s\nwant: %s", csp, want)
	}
	if strings.Contains(csp, "script-src 'unsafe-inline'") {
		t.Error("script-src must name the hash, not fall back to unsafe-inline")
	}
}

// The stored name is folded, so the page has to show the fold BEFORE the form is
// submitted: a participant who types "Ada Lovelace" and is recorded as
// "Ada-Lovelace" in a Git commit they cannot edit should have seen that coming.
func TestJoinPagePreviewsTheStoredName(t *testing.T) {
	s, _ := fixture(t, "http://dex.test")
	b := newBrowser()
	body := b.request(s, "GET", "https://demo.test/join", nil).Body.String()
	if !strings.Contains(body, `id="rp-display"`) {
		t.Error("the form does not show what name will be stored")
	}
	_, rest, _ := strings.Cut(body, `name="csrf" value="`)
	csrf, _, _ := strings.Cut(rest, `"`)
	w := b.request(s, "POST", "https://demo.test/join", url.Values{
		"csrf": {csrf}, "code": {"WRONG"},
		"name": {"Ada Lovelace"}, "return": {"https://demo.test/app/"},
	})
	// Server-rendered, not left to the script: a browser without JavaScript
	// still has to be told what it is about to be called.
	if got := w.Body.String(); !strings.Contains(got, "<strong>Ada-Lovelace</strong>") {
		t.Error("the returned form does not preview the folded name")
	}
}

// Someone who has not typed anything yet still needs to see what the field is
// for, and a browser without script has to be told something true.
func TestJoinPageShowsTheAddressBeforeAnyScriptRuns(t *testing.T) {
	s, _ := fixture(t, "http://dex.test")
	b := newBrowser()
	body := b.request(s, "GET", "https://demo.test/join", nil).Body.String()
	if !strings.Contains(body, "your-name@koudijs.dev.test") {
		t.Error("the empty form does not show what the address will look like")
	}
	if !strings.Contains(body, "there is nothing to fill in") {
		t.Error("the page does not say why the address cannot be typed")
	}
	_, rest, _ := strings.Cut(body, `name="csrf" value="`)
	csrf, _, _ := strings.Cut(rest, `"`)
	// A form that comes back after a bad code keeps the name, so it must also
	// keep the address that name earns.
	w := b.request(s, "POST", "https://demo.test/join", url.Values{
		"csrf": {csrf}, "code": {"WRONG"},
		"name": {"Ada Demo"}, "return": {"https://demo.test/app/"},
	})
	if !strings.Contains(w.Body.String(), "ada-demo@koudijs.dev.test") {
		t.Error("the returned form lost the address for the name it kept")
	}
}

// What the address is FOR is the operator's to say, not Room Pass's. Room Pass
// only knows the address is synthetic; whether it lands on a Git commit, an
// audit line or nothing at all belongs to the application behind it, so the
// page must claim nothing until a Room says what to claim.
func TestAttributionNoteIsTheOperatorsToMake(t *testing.T) {
	s, db := fixture(t, "http://dex.test")
	b := newBrowser()
	body := b.request(s, "GET", "https://demo.test/join", nil).Body.String()
	if strings.Contains(body, "Git") {
		t.Error("the page invents a claim about Git that no Room made")
	}
	if !strings.Contains(body, "never a real mailbox") {
		t.Error("the page drops the one thing Room Pass does know about the address")
	}

	room := &api.Room{}
	if e := db.Get(context.Background(), s.cfg.Room, room); e != nil {
		t.Fatal(e)
	}
	room.Spec.AttributionNote = "It labels your changes in Git."
	if e := db.Update(context.Background(), room); e != nil {
		t.Fatal(e)
	}

	body = b.request(s, "GET", "https://demo.test/join", nil).Body.String()
	if !strings.Contains(body, "It labels your changes in Git.") {
		t.Errorf("the join form drops the operator's note: %s", body)
	}

	// A returning participant is shown the address without being asked to type
	// anything, so that is precisely where the note has to survive too.
	_, rest, _ := strings.Cut(body, `name="csrf" value="`)
	csrf, _, _ := strings.Cut(rest, `"`)
	if w := b.request(s, "POST", "https://demo.test/join", url.Values{
		"csrf": {csrf}, "code": {"BCDFGH"}, "name": {"Ada Demo"}, "return": {"https://demo.test/app/"},
	}); w.Code != 303 {
		t.Fatalf("enrollment: %d %s", w.Code, w.Body.String())
	}
	if body = b.request(s, "GET", "https://demo.test/join", nil).Body.String(); !strings.Contains(body, "It labels your changes in Git.") {
		t.Errorf("the returning page drops the operator's note: %s", body)
	}
}

// A returning participant sees the identity they will reuse, including the
// address, and is not asked for a room code the page does not even show.
func TestReturningParticipantSeesTheirIssuedAddress(t *testing.T) {
	s, _ := fixture(t, "http://dex.test")
	b := newBrowser()
	body := b.request(s, "GET", "https://demo.test/join", nil).Body.String()
	_, rest, _ := strings.Cut(body, `name="csrf" value="`)
	csrf, _, _ := strings.Cut(rest, `"`)
	if w := b.request(s, "POST", "https://demo.test/join", url.Values{
		"csrf": {csrf}, "code": {"BCDFGH"}, "name": {"Ada Demo"}, "return": {"https://demo.test/app/"},
	}); w.Code != 303 {
		t.Fatalf("enrollment: %d %s", w.Code, w.Body.String())
	}
	body = b.request(s, "GET", "https://demo.test/join", nil).Body.String()
	if !strings.Contains(body, "ada-demo@koudijs.dev.test") {
		t.Errorf("the returning page hides the address it will assert: %s", body)
	}
	if strings.Contains(body, "Enter the room code") {
		t.Error("the returning page asks for a code it does not show a field for")
	}
}
