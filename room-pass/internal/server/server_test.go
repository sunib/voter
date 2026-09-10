package server

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/securecookie"

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
		go func() {
			defer wg.Done()
			if _, e := s.enrollParticipant(context.Background(), "bcd-fgh", "Ada"); e == nil {
				ok.Add(1)
			}
		}()
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
