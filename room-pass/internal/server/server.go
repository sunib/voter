package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gorilla/securecookie"
	api "github.com/sunib/voter/room-pass/api/v1alpha1"
	"github.com/sunib/voter/room-pass/internal/controller"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Config struct {
	Room client.ObjectKey
	// ConnectorID is the Dex authproxy connector this gateway serves. Dex
	// derives the connector's callback path from its id, so this value decides
	// which path we accept and forward to: id "room-pass" means
	// /callback/room-pass. Getting it wrong fails closed -- the callback is
	// refused rather than served for the wrong connector.
	ConnectorID                           string
	JoinOrigin, IssuerOrigin, DexUpstream string
	AllowedReturns                        []string
	HashKey, BlockKey                     []byte
	CookieLifetime                        time.Duration
	JoinRate                              rate.Limit
	JoinBurst                             int
	HandoffRate                           rate.Limit
	HandoffBurst, MaxHandoffs             int
}
type session struct {
	RoomUID, Name, UID string
	Expires            int64
}
type transaction struct {
	State, Browser, JoinBrowser string
	Confirmed                   bool
	Expires                     time.Time
	Session                     *session
}
type Server struct {
	metrics *metrics
	cfg     Config
	// formAction is the CSP form-action source list, computed once at startup.
	// See formActionSources for why it is not just 'self' plus the issuer.
	formAction    string
	db            client.Client
	cookies       *securecookie.SecureCookie
	proxy         *httputil.ReverseProxy
	enroll        sync.Mutex
	mu            sync.Mutex
	transactions  map[string]*transaction
	joins, starts *rate.Limiter
	now           func() time.Time
}

func New(cfg Config, db client.Client) (*Server, error) {
	for _, origin := range []string{cfg.JoinOrigin, cfg.IssuerOrigin} {
		u, e := url.Parse(origin)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("origins must be exact HTTPS origins")
		}
	}
	upstream, e := url.Parse(cfg.DexUpstream)
	if e != nil || upstream.Host == "" || (upstream.Scheme != "http" && upstream.Scheme != "https") || upstream.User != nil || upstream.RawQuery != "" || upstream.Fragment != "" || upstream.Path != "" {
		return nil, errors.New("invalid Dex upstream")
	}
	if len(cfg.HashKey) != 32 || len(cfg.BlockKey) != 32 {
		return nil, errors.New("cookie keys must each be 32 bytes")
	}
	if cfg.CookieLifetime <= 0 {
		cfg.CookieLifetime = 24 * time.Hour
	}
	if cfg.CookieLifetime > 7*24*time.Hour {
		return nil, errors.New("cookie lifetime exceeds seven days")
	}
	if cfg.JoinRate <= 0 {
		cfg.JoinRate = 20
	}
	if cfg.JoinBurst <= 0 {
		cfg.JoinBurst = 150
	}
	if cfg.HandoffRate <= 0 {
		cfg.HandoffRate = 20
	}
	if cfg.HandoffBurst <= 0 {
		cfg.HandoffBurst = 150
	}
	if cfg.MaxHandoffs <= 0 {
		cfg.MaxHandoffs = 1000
	}
	if len(cfg.AllowedReturns) == 0 {
		return nil, errors.New("return allowlist is empty")
	}
	for _, raw := range cfg.AllowedReturns {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
			return nil, errors.New("invalid return URL")
		}
	}
	s := &Server{cfg: cfg, formAction: formActionSources(cfg), db: db, cookies: securecookie.New(cfg.HashKey, cfg.BlockKey).MaxAge(int(cfg.CookieLifetime.Seconds())), proxy: httputil.NewSingleHostReverseProxy(upstream), transactions: map[string]*transaction{}, joins: rate.NewLimiter(cfg.JoinRate, cfg.JoinBurst), starts: rate.NewLimiter(cfg.HandoffRate, cfg.HandoffBurst), now: time.Now}
	s.proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		if s.metrics != nil {
			s.metrics.upstreamErrors.Inc()
		}
		http.Error(w, "Identity provider unavailable", 503)
	}
	return s, nil
}
func randomID() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func (s *Server) cookie(w http.ResponseWriter, name string, value any, maxAge int) error {
	encoded, err := s.cookies.Encode(name, value)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: encoded, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
	return nil
}
func (s *Server) decode(r *http.Request, name string, value any) error {
	c, e := r.Cookie(name)
	if e != nil {
		return e
	}
	return s.cookies.Decode(name, c.Value, value)
}

// cookieNames lists the cookie NAMES a request carried. Names only -- the
// values are credentials. "I do have some cookies" is not a diagnosis; knowing
// which ones arrived is.
func cookieNames(r *http.Request) []string {
	cs := r.Cookies()
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func (s *Server) room(ctx context.Context) (*api.Room, error) {
	r := &api.Room{}
	if e := s.db.Get(ctx, s.cfg.Room, r); e != nil {
		return nil, e
	}
	return r, nil
}
func (s *Server) identity(ctx context.Context, ss session) (*api.Room, *api.Participant, error) {
	room, e := s.room(ctx)
	if e != nil {
		return nil, nil, e
	}
	if !controller.Active(room, s.now()) || string(room.UID) != ss.RoomUID || s.now().Unix() >= ss.Expires {
		return nil, nil, errors.New("session expired")
	}
	p := &api.Participant{}
	if e = s.db.Get(ctx, client.ObjectKey{Namespace: room.Namespace, Name: ss.Name}, p); e != nil {
		return nil, nil, e
	}
	if p.DeletionTimestamp != nil || p.Spec.Revoked || string(p.UID) != ss.UID || p.Spec.RoomRef.UID != ss.RoomUID || p.Spec.RoomRef.Name != room.Name {
		return nil, nil, errors.New("enrollment revoked")
	}
	return room, p, nil
}
func (s *Server) Ready(ctx context.Context) error {
	room, e := s.room(ctx)
	if e != nil {
		return e
	}
	if room.Status.ObservedGeneration != room.Generation || !meta.IsStatusConditionTrue(room.Status.Conditions, "Ready") {
		return errors.New("Room reconciliation pending")
	}
	for _, u := range room.Spec.AllowedReturnURLs {
		if !contains(s.cfg.AllowedReturns, u) {
			return errors.New("Room return URL outside platform allowlist")
		}
	}
	ps := &api.ParticipantList{}
	return s.db.List(ctx, ps, client.InNamespace(s.cfg.Room.Namespace))
}
func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
func validName(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if !utf8.ValidString(v) || len(v) == 0 || len(v) > 64 {
		return "", errors.New("Choose a name of 1–64 bytes.")
	}
	for _, c := range v {
		if unicode.IsControl(c) || c == '<' || c == '>' {
			return "", errors.New("Choose a name without control characters or angle brackets.")
		}
	}
	return v, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	w.Header().Set("Cache-Control", "no-store")
	// same-origin, NOT no-referrer. Under no-referrer a browser serialises the
	// Origin header of a form POST as "null", which made the CSRF check below
	// reject every real browser while curl (which implements no referrer
	// policy) sailed through. same-origin still strips the referrer on
	// cross-origin requests, so the handoff token in the URL never leaks to a
	// third party -- which is what no-referrer was here to protect.
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self'; form-action "+s.formAction+"; frame-ancestors 'none'")
	// Never trust client identity or forwarded routing information, even on Dex aliases.
	for k := range r.Header {
		low := strings.ToLower(k)
		if strings.HasPrefix(low, "x-remote-") || strings.HasPrefix(low, "impersonate-") || strings.HasPrefix(low, "x-forwarded-") || low == "forwarded" {
			r.Header.Del(k)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if r.URL.Path == "/healthz" {
		w.WriteHeader(200)
		return
	}
	if r.URL.Path == "/readyz" {
		if e := s.Ready(ctx); e != nil {
			http.Error(w, "Storage or configuration unavailable", 503)
			return
		}
		w.WriteHeader(200)
		return
	}
	join, _ := url.Parse(s.cfg.JoinOrigin)
	issuer, _ := url.Parse(s.cfg.IssuerOrigin)
	switch r.Host {
	case join.Host:
		switch r.URL.Path {
		case "/bind":
			s.bind(w, r)
		case "/join":
			s.join(w, r)
		case "/logout":
			s.logout(w, r)
		default:
			http.NotFound(w, r)
		}
	case issuer.Host:
		if r.URL.Path == "/room-pass/confirm" {
			s.confirm(w, r)
			return
		}
		if r.URL.Path == "/room-pass/complete" {
			s.complete(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/callback") {
			if r.URL.Path != s.callbackPath() || r.Method != "GET" {
				http.Error(w, "Unsupported callback", 403)
				return
			}
			s.start(w, r)
			return
		}
		// Only protocol endpoints reach Dex; ambiguous/encoded paths fail closed.
		if r.URL.RawPath != "" {
			http.NotFound(w, r)
			return
		}
		allowed := r.URL.Path == "/.well-known/openid-configuration" || r.URL.Path == "/keys" || r.URL.Path == "/token" || r.URL.Path == "/userinfo" || r.URL.Path == "/auth" || strings.HasPrefix(r.URL.Path, "/auth/") || r.URL.Path == "/approval" || r.URL.Path == "/device" || strings.HasPrefix(r.URL.Path, "/device/") || strings.HasPrefix(r.URL.Path, "/static/") || strings.HasPrefix(r.URL.Path, "/theme/")
		if !allowed {
			http.NotFound(w, r)
			return
		}
		if (r.URL.Path == "/auth" || strings.HasPrefix(r.URL.Path, "/auth/") || r.URL.Path == "/device/code") && !s.starts.Allow() {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "Please retry shortly", 429)
			return
		}
		s.proxy.ServeHTTP(w, r)
	default:
		http.Error(w, "Unknown host", 400)
	}
}

// callbackPath is the only /callback path this gateway serves. Dex appends the
// connector id to the callback URL for an authproxy connector, so the two must
// agree: connector id "room-pass" produces "/callback/room-pass".
func (s *Server) callbackPath() string {
	return "/callback/" + s.cfg.ConnectorID
}

func (s *Server) csrf(r *http.Request) bool {
	return s.csrfReason(r) == ""
}

// csrfReason returns "" when the request passes, else a short reason for the
// log. All four conditions collapse into one user-facing message on purpose --
// telling a caller which half of the check failed helps an attacker more than
// it helps a participant -- so the detail lives here instead.
//
// Never log the token values themselves: the cookie value IS the credential.
func (s *Server) csrfReason(r *http.Request) string {
	// The double-submit token below is the actual CSRF defence: the cookie is
	// HttpOnly and __Host-scoped, so a cross-site attacker cannot read it and
	// therefore cannot populate the matching form field. The Origin check is
	// belt and braces on top of that.
	//
	// So: reject an Origin that is present and wrong, but tolerate one that is
	// absent or "null". A browser sends "null" for reasons that have nothing to
	// do with the request being forged -- a referrer policy that strips the
	// origin, a sandboxed frame -- and treating that as an attack locks out
	// legitimate participants while stopping no one, because an attacker who
	// could forge the token would not need to spoof the origin anyway.
	switch got := r.Header.Get("Origin"); got {
	case s.cfg.JoinOrigin, "", "null":
	default:
		return "origin-mismatch"
	}
	var v string
	if _, e := r.Cookie("__Host-rp-csrf"); e != nil {
		return "csrf-cookie-absent"
	}
	if e := s.decode(r, "__Host-rp-csrf", &v); e != nil {
		// Wrong signing key (redeployed with new keys) or an expired cookie.
		return "csrf-cookie-undecodable"
	}
	if v == "" {
		return "csrf-cookie-empty"
	}
	if r.FormValue("csrf") == "" {
		return "csrf-field-absent"
	}
	if r.FormValue("csrf") != v {
		return "csrf-mismatch"
	}
	return ""
}

// formActionSources builds the CSP form-action list for the join form.
//
// Chromium applies form-action to every hop of a redirect chain, not only to
// the form's immediate target, and the join POST is the start of a chain that
// crosses three origins before it finishes:
//
//	POST /join            the join origin       -- 'self'
//	 303 /room-pass/complete  the issuer origin  -- IssuerOrigin
//	 302 /auth/callback    the APPLICATION origin
//
// The third one is the one that is easy to forget, because in the common
// deployment it is invisible: when the application and the join form are served
// from the same host -- which is how Traefik routes /join on voter.koudijs.dev
// today -- the application origin IS 'self' and the list looks complete while
// silently depending on that coincidence.
//
// Put the application on its own host and every browser login breaks with a CSP
// error, while curl and any Go HTTP client sail through, because neither
// enforces CSP. That is the same shape as the Referrer-Policy/Origin bug this
// service already shipped once.
//
// The allowed return URLs are exactly the applications Room Pass is willing to
// send a participant back to, so their origins are exactly the origins that must
// be permitted here. Deriving the list from them means adding an application is
// one configuration change rather than two, and there is no way to do half of it.
func formActionSources(cfg Config) string {
	sources := []string{"'self'"}
	seen := map[string]bool{}
	add := func(raw string) {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Scheme == "" || u.Host == "" {
			return
		}
		origin := u.Scheme + "://" + u.Host
		if seen[origin] {
			return
		}
		seen[origin] = true
		sources = append(sources, origin)
	}
	add(cfg.IssuerOrigin)
	for _, raw := range cfg.AllowedReturns {
		add(raw)
	}
	return strings.Join(sources, " ")
}

// logReject records why a request was turned away. The join flow has a dozen
// ways to fail and every one of them used to be silent, which made a browser
// that "just says Invalid form" impossible to debug from the outside.
func (s *Server) logReject(r *http.Request, reason string, extra ...any) {
	if s.metrics != nil {
		s.metrics.rejections.WithLabelValues(reason).Inc()
	}
	args := []any{"reason", reason, "path", r.URL.Path, "method", r.Method, "host", r.Host}
	slog.Warn("room-pass rejected a request", append(args, extra...)...)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.Method != "POST" || r.ParseForm() != nil || !s.csrf(r) {
		http.Error(w, "Invalid sign-out request", 403)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "__Host-rp-session", Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.Redirect(w, r, "/join", 303)
}

var page = template.Must(template.New("join").Parse(pageSource))

const pageSource = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Join the room</title><style>body{font:18px system-ui;margin:3rem auto;padding:0 1rem;max-width:30rem;background:#f8fafc;color:#172033}input,button{box-sizing:border-box;width:100%;padding:.8rem;margin:.4rem 0 1rem;font:inherit}button{background:#1749a5;color:white;border:0;border-radius:.4rem}label{display:block}small{line-height:1.5}.scanned{background:#e8f0fe;border-radius:.4rem;padding:.6rem .8rem;margin:.4rem 0 1rem}</style><h1>{{.Title}}</h1><p>{{.Message}}</p>{{if .Form}}<form method="post" action="/join"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="handoff" value="{{.Handoff}}"><input type="hidden" name="return" value="{{.Return}}">{{if .Enrolled}}<p>You’re already enrolled as <strong>{{.EnrolledName}}</strong>. Continue with the same demo identity.</p>{{else}}{{if .Code}}<p class="scanned">Room code <strong>{{.Code}}</strong>, from the code you scanned. <input type="hidden" name="code" value="{{.Code}}"></p>{{else}}<label>Room code<input name="code" required maxlength="24" autocomplete="off" autocapitalize="characters" placeholder="BCDFGH"></label>{{end}}<label>Display name<input name="name" required maxlength="64" autocomplete="nickname"{{if .Code}} autofocus{{end}}></label>{{end}}<button>Continue</button></form>{{end}}{{if .Enrolled}}<form method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Sign out of this browser</button></form>{{end}}<small>Your name is a demo label, not a verified identity. Demo changes may appear in Git with this name and a generated email address.</small></html>`

func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if e := r.ParseForm(); e != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	room, e := s.room(r.Context())
	if e != nil {
		http.Error(w, "Room temporarily unavailable", 503)
		return
	}
	handoff := r.FormValue("handoff")
	dest := r.FormValue("return")
	if dest == "" {
		for _, u := range room.Spec.AllowedReturnURLs {
			if contains(s.cfg.AllowedReturns, u) {
				dest = u
				break
			}
		}
	}
	if !contains(room.Spec.AllowedReturnURLs, dest) || !contains(s.cfg.AllowedReturns, dest) {
		s.logReject(r, "return-not-allowed", "dest", dest,
			"room_allows", room.Spec.AllowedReturnURLs, "deployment_allows", s.cfg.AllowedReturns)
		http.Error(w, "Unapproved return destination", 400)
		return
	}
	if handoff != "" {
		s.mu.Lock()
		tx := s.transactions[handoff]
		var jb string
		cookieOK := s.decode(r, "__Host-rp-join-browser", &jb) == nil
		valid := tx != nil && tx.Confirmed && cookieOK && tx.JoinBrowser == jb && s.now().Before(tx.Expires) && tx.Session == nil
		s.mu.Unlock()
		if !valid {
			s.logReject(r, "handoff-invalid", "tx_found", tx != nil, "join_browser_cookie", cookieOK,
				"confirmed", tx != nil && tx.Confirmed, "already_used", tx != nil && tx.Session != nil)
			http.Error(w, "Login expired. Start again from the demo.", 400)
			return
		}
	}
	var ss session
	enrolled := s.decode(r, "__Host-rp-session", &ss) == nil
	// The name the participant chose, shown back to them when they are already
	// enrolled. Someone returning to this page has no other way to see which
	// identity they are about to continue as -- and the whole point of the demo
	// is that their name ends up on a Git commit.
	enrolledName := ""
	if enrolled {
		_, p, e := s.identity(r.Context(), ss)
		enrolled = e == nil
		if enrolled {
			enrolledName = p.Spec.DisplayName
		}
	}
	// A code carried here by a scanned QR code. Only ever a prefill: the POST
	// below re-reads it from the form and Room Pass checks it against the
	// Room's rotating status exactly as it checks a typed one.
	scanned := ""
	if r.Method == "GET" && !enrolled {
		scanned = scannedCode(r)
	}
	message := "Enter the room code and choose a display name."
	if scanned != "" {
		message = "Choose a display name to join."
	}
	form := true
	if !controller.Active(room, s.now()) {
		message = "This demo has ended."
		form = false
	} else if room.Spec.Enrollment != "Open" && !enrolled {
		message = "Joining is closed. If you already joined, return to the demo."
		form = false
	}
	if r.Method == "GET" {
		csrf, e := randomID()
		if e != nil {
			http.Error(w, "Try again later", 503)
			return
		}
		if e = s.cookie(w, "__Host-rp-csrf", csrf, 600); e != nil {
			http.Error(w, "Try again later", 503)
			return
		}
		// Single use. The code is in the form now, and a code left in the jar
		// is one that gets silently reused on the next join, long after it
		// stopped being the code on the screen.
		clearJoinCodeHandoff(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, map[string]any{"Title": room.Spec.Title, "Message": message, "Form": form, "CSRF": csrf, "Handoff": handoff, "Return": dest, "Enrolled": enrolled, "EnrolledName": enrolledName, "Code": scanned})
		return
	}
	if reason := s.csrfReason(r); reason != "" {
		s.logReject(r, reason,
			"origin", r.Header.Get("Origin"), "expected_origin", s.cfg.JoinOrigin,
			"cookie_names", cookieNames(r), "has_handoff", handoff != "", "enrolled", enrolled)
		http.Error(w, "Invalid form. Reload and try again.", 403)
		return
	}
	if !form {
		s.logReject(r, "enrollment-closed", "message", message)
		http.Error(w, message, 403)
		return
	}
	if !enrolled {
		if !s.joins.Allow() {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "Please wait a moment and try again.", 429)
			return
		}
		name, e := validName(r.FormValue("name"))
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		ss, e = s.enrollParticipant(r.Context(), r.FormValue("code"), name)
		if e != nil {
			http.Error(w, e.Error(), 403)
			return
		}
		if e = s.cookie(w, "__Host-rp-session", ss, int(s.cfg.CookieLifetime.Seconds())); e != nil {
			http.Error(w, "Unable to save session", 503)
			return
		}
	}
	if handoff != "" {
		// Re-read eligibility before authorizing even when a cookie was valid earlier.
		if _, _, e = s.identity(r.Context(), ss); e != nil {
			http.Error(w, "Enrollment unavailable", 403)
			return
		}
		s.mu.Lock()
		tx := s.transactions[handoff]
		if tx == nil || !s.now().Before(tx.Expires) || tx.Session != nil {
			s.mu.Unlock()
			http.Error(w, "Login expired", 400)
			return
		}
		copy := ss
		tx.Session = &copy
		s.mu.Unlock()
		http.Redirect(w, r, s.cfg.IssuerOrigin+"/room-pass/complete?handoff="+url.QueryEscape(handoff), 303)
		return
	}
	http.Redirect(w, r, dest, 303)
}
func (s *Server) enrollParticipant(ctx context.Context, code, name string) (session, error) {
	result := "storage_error"
	defer func() {
		if s.metrics != nil {
			s.metrics.enrollments.WithLabelValues(result).Inc()
		}
	}()
	s.enroll.Lock()
	defer s.enroll.Unlock()
	room, e := s.room(ctx)
	if e != nil {
		return session{}, errors.New("Room temporarily unavailable")
	}
	if !controller.Accepts(room, code, s.now()) {
		result = "code_or_room_rejected"
		return session{}, errors.New("That code is invalid or joining has closed. Check the presenter’s current code.")
	}
	ps := &api.ParticipantList{}
	if e = s.db.List(ctx, ps, client.InNamespace(room.Namespace)); e != nil {
		return session{}, errors.New("Room temporarily unavailable")
	}
	count := 0
	for _, p := range ps.Items {
		if p.Spec.RoomRef.UID == string(room.UID) {
			count++
		}
	}
	if count >= room.Spec.MaxParticipants {
		result = "full"
		return session{}, errors.New("This room is full. Please ask the presenter.")
	}
	id, e := randomID()
	if e != nil {
		return session{}, errors.New("Enrollment temporarily unavailable")
	}
	p := &api.Participant{ObjectMeta: metav1.ObjectMeta{Name: "p-" + id, Namespace: room.Namespace, OwnerReferences: []metav1.OwnerReference{{APIVersion: api.GroupVersion.String(), Kind: "Room", Name: room.Name, UID: room.UID}}}, Spec: api.ParticipantSpec{RoomRef: api.RoomRef{Name: room.Name, UID: string(room.UID)}, DisplayName: name}}
	if e = s.db.Create(ctx, p); e != nil {
		// A lost create response is resolved at the original name, never a second ID.
		found := &api.Participant{}
		if getErr := s.db.Get(ctx, client.ObjectKeyFromObject(p), found); getErr != nil || found.Spec != p.Spec {
			return session{}, errors.New("Enrollment could not be confirmed. Please retry.")
		}
		p = found
	}
	result = "enrolled"
	return session{RoomUID: string(room.UID), Name: p.Name, UID: string(p.UID), Expires: s.now().Add(s.cfg.CookieLifetime).Unix()}, nil
}
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" || len(state) > 2048 || len(r.URL.Query()) != 1 || len(r.URL.Query()["state"]) != 1 {
		http.Error(w, "Invalid login transaction", 400)
		return
	}
	if !s.starts.Allow() {
		http.Error(w, "Please retry shortly", 429)
		return
	}
	browser, e := randomID()
	if e != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	id, e := randomID()
	if e != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.transactions {
		if !s.now().Before(v.Expires) {
			delete(s.transactions, k)
		}
	}
	if len(s.transactions) >= s.cfg.MaxHandoffs {
		http.Error(w, "Too many pending logins", 429)
		return
	}
	if e = s.cookie(w, "__Host-rp-browser", browser, 180); e != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	s.transactions[id] = &transaction{State: state, Browser: browser, Expires: s.now().Add(3 * time.Minute)}
	http.Redirect(w, r, s.cfg.JoinOrigin+"/bind?handoff="+url.QueryEscape(id), 303)
}
func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	var browser string
	if s.decode(r, "__Host-rp-browser", &browser) != nil {
		s.logReject(r, "issuer-browser-cookie-bad", "cookie_names", cookieNames(r))
		http.Error(w, "Wrong login browser", 403)
		return
	}
	id := r.URL.Query().Get("handoff")
	s.mu.Lock()
	tx := s.transactions[id]
	if tx == nil || tx.Session == nil || tx.Browser != browser || !s.now().Before(tx.Expires) {
		s.mu.Unlock()
		http.Error(w, "Invalid or expired login", 403)
		return
	}
	delete(s.transactions, id)
	s.mu.Unlock()
	room, p, e := s.identity(r.Context(), *tx.Session)
	if e != nil {
		http.Error(w, "Enrollment unavailable", 403)
		return
	}
	id = strings.TrimPrefix(p.Name, "p-")
	r.Header.Set("X-Remote-User-Id", id)
	r.Header.Set("X-Remote-User", p.Spec.DisplayName)
	r.Header.Set("X-Remote-User-Name", p.Spec.DisplayName)
	r.Header.Set("X-Remote-User-Email", id+"@demo.invalid")
	r.Header.Set("X-Remote-Group", room.Spec.AudienceGroup)
	r.Header.Del("Cookie")
	r.Header.Del("Authorization")
	r.URL.Path = s.callbackPath()
	r.URL.RawPath = ""
	r.URL.RawQuery = url.Values{"state": {tx.State}}.Encode()
	if s.metrics != nil {
		s.metrics.handoffs.Inc()
	}
	s.proxy.ServeHTTP(w, r)
}

// Bind proves that the same browser controls both host-only cookie jars before
// showing a form. A copied join link cannot authorize the sender's Dex session.
func (s *Server) bind(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	id := r.URL.Query().Get("handoff")
	var jb string
	if s.decode(r, "__Host-rp-join-browser", &jb) != nil {
		var e error
		jb, e = randomID()
		if e != nil {
			http.Error(w, "Unavailable", 503)
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := s.transactions[id]
	if tx == nil || tx.JoinBrowser != "" || !s.now().Before(tx.Expires) {
		http.Error(w, "Invalid login binding", 403)
		return
	}
	if e := s.cookie(w, "__Host-rp-join-browser", jb, 180); e != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	next, e := randomID()
	if e != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	delete(s.transactions, id)
	s.transactions[next] = tx
	id = next
	tx.JoinBrowser = jb
	http.Redirect(w, r, s.cfg.IssuerOrigin+"/room-pass/confirm?handoff="+url.QueryEscape(id), 303)
}
func (s *Server) confirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	var browser string
	if s.decode(r, "__Host-rp-browser", &browser) != nil {
		s.logReject(r, "issuer-browser-cookie-bad", "cookie_names", cookieNames(r))
		http.Error(w, "Wrong login browser", 403)
		return
	}
	id := r.URL.Query().Get("handoff")
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := s.transactions[id]
	if tx == nil || tx.JoinBrowser == "" || tx.Browser != browser || tx.Confirmed || !s.now().Before(tx.Expires) {
		http.Error(w, "Invalid login binding", 403)
		return
	}
	next, e := randomID()
	if e != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	delete(s.transactions, id)
	s.transactions[next] = tx
	id = next
	tx.Confirmed = true
	http.Redirect(w, r, s.cfg.JoinOrigin+"/join?handoff="+url.QueryEscape(id), 303)
}

// --- Pre-supplied join codes ------------------------------------------------
//
// A Room Pass integration point: an application sharing this host may supply
// the join code ahead of the participant, and the join page then asks only for
// a display name. Room Pass offers this; no particular application owns it.
//
// The motivating case is a QR code on the presenter's screen carrying the
// current code, but nothing here knows about QR codes. Any application that
// has obtained a code by any means can use the same channel.
//
// Why a cookie rather than a parameter. The /join URL is built HERE, after
// Dex, from a handoff id the application never sees, so there is no query
// string for a caller to populate. A cookie works because the join origin and
// the application origin are one host (JOIN_ORIGIN; the deployment's proxy
// splits the paths), which makes this a same-host integration and not a
// general remote API:
//
//	app login endpoint (sets the cookie) -> Dex -> /callback/<connector>
//	   -> /bind -> /confirm -> /join, where the cookie arrives with the browser
//
// The application keeps its own post-login destination in its own login
// transaction. It cannot travel through this gateway's return parameter, which
// is an exact, immutable allowlist entry and cannot carry a deep link.
//
// Nothing here trusts the cookie. Its value reaches the participant's form as
// a prefill and comes back through the ordinary POST, where enrollParticipant
// checks it against the Room's currently valid codes. A forged one is an
// invalid code; a stale one is an expired code. Both already have answers.
//
// See docs/qr-join.md for the integration contract.

// joinCodeHandoffCookie is the name in that contract. Spelled out rather than
// following the internal __Host-rp-* convention: the other cookies here are
// Room Pass talking to itself, while this one is written by somebody else's
// code, where "rp" is an abbreviation only we can expand.
//
// It is a plain cookie on purpose. Signing it would claim the writer vouches
// for the code, and the whole point is that nobody does until the Room says so
// -- which also means an integrator needs no key material from us.
const joinCodeHandoffCookie = "__Host-room-pass-joincode"

// maxScannedCodeLength matches the Room CRD's upper bound for joinCode.length.
const maxScannedCodeLength = 12

// scannedCode returns the code a QR scan carried in, or "" for an ordinary
// visit. The query parameter is supported so a QR can point straight at this
// gateway when no application destination is involved; the cookie is what the
// application uses.
func scannedCode(r *http.Request) string {
	if code := normalizeScannedCode(r.URL.Query().Get("code")); code != "" {
		return code
	}
	if c, e := r.Cookie(joinCodeHandoffCookie); e == nil {
		return normalizeScannedCode(c.Value)
	}
	return ""
}

// normalizeScannedCode bounds what may be echoed back into the form. It is not
// a validity check -- enrollParticipant owns that -- only a guard against
// rendering unbounded junk from a cookie or a query string as if it were a
// code. html/template escapes the value either way.
func normalizeScannedCode(raw string) string {
	code := controller.Normalize(raw)
	if code == "" || len(code) > maxScannedCodeLength {
		return ""
	}
	for _, r := range code {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return code
}

// clearJoinCodeHandoff expires the hand-off cookie. Written raw rather than
// through s.cookie because this cookie is not ours to encode: the application
// set it in plain text, and the attributes must match for the browser to
// accept the deletion.
func clearJoinCodeHandoff(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     joinCodeHandoffCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}
