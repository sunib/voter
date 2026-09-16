package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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
	"golang.org/x/text/unicode/norm"
	"golang.org/x/time/rate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	// The name has to survive the fold with something left over, because that
	// result is the Participant's object name and the participant's whole
	// address. A name of only emoji or punctuation passes every check above and
	// still leaves nothing to build an identity from.
	//
	// What is STORED is the folded form, not what was typed: a display name is
	// carried into a Kubernetes label value (the voter app labels each ballot
	// with it) and into an object name, and neither accepts a space. Folding
	// here rather than repairing it downstream means there is one name, legal
	// everywhere, and no caller has to know the rules.
	//
	// The cost is a name with no ASCII fold at all -- CJK, emoji -- which is
	// refused below rather than transliterated. Entry 3 of
	// docs/deliberate-simplifications.md says why we took that trade.
	name := labelName(v)
	if name == "" {
		return "", errors.New("Choose a name with at least one letter or number.")
	}
	return name, nil
}

// participantPrefix keeps every Participant object name inside its own
// namespace of names now that participants choose what that name is made of.
const participantPrefix = "p-"

// maxParticipantID bounds the identifier below both the RFC 1123 object-name
// limit and the 64-byte local part of an address, with room for the prefix.
const maxParticipantID = 40

// participantID derives the Participant's object name and the local part of its
// address from the display name the participant typed. The name is the
// identity here -- no random suffix -- so the mapping must be stable, safe as a
// Kubernetes name, safe in a Git author line, and reproducible in the browser:
// the join page previews the address while it is being typed, and a preview
// that disagrees with the address actually issued would be worse than no
// preview at all.
//
// Diacritics fold rather than vanish ("Renée" becomes "renee") by decomposing to
// NFKD and skipping the combining marks that step splits off. Only the
// Combining Diacritical Marks block is skipped, because that is precisely what
// the page's script can skip without shipping a Unicode table of its own.
// Everything else outside [a-z0-9] collapses to a single separating dash.
func participantID(display string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(norm.NFKD.String(display)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		case r >= 0x0300 && r <= 0x036F:
		default:
			dash = true
		}
	}
	id := b.String()
	if len(id) > maxParticipantID {
		id = id[:maxParticipantID]
	}
	return strings.TrimRight(id, "-")
}

// demoEmail is the only place an address is formed. The domain says both where
// the identity came from and that it is not a mailbox: RFC 2606 reserves .test,
// so koudijs.dev.test cannot be registered by anyone, ever. The application
// behind Room Pass needs an address-shaped identifier, not somewhere to send
// mail. What it does with it is its own business -- see
// Room.spec.attributionNote.
//
// .test rather than .invalid is a deliberate readability choice: a participant
// reads this address on the join page before choosing a name, and "test" says
// "not a real address" where "invalid" reads as "something went wrong". The
// trade is that .test carries no promise of an NXDOMAIN the way .invalid does
// -- RFC 6761 6.2 expects .test names to resolve inside a private network -- so
// nothing may treat "it does not resolve" as a control.
// labelName folds a typed name into the form that is STORED as
// Participant.spec.displayName. It is participantID's rule with the case left
// alone, and the two are locked together by exactly that: lowercasing this
// result reproduces participantID, which is what lets the voter app build a
// submission's object name as "<round>-<lowercased display name>" and have it
// address the same participant this identity already names.
//
// Case survives because this value is read by people: it is a Git commit author
// line, a label on every ballot, and a filename in the mirrored audit trail,
// where "Ada-Lovelace" beats "ada-lovelace". Everything else -- NFKD so
// diacritics fold rather than vanish, the combining-mark skip, the cap, the
// trimmed separator -- is participantID's behaviour unchanged, because any
// divergence would break the pairing above.
func labelName(display string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFKD.String(display) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		case r >= 0x0300 && r <= 0x036F:
		default:
			dash = true
		}
	}
	name := b.String()
	if len(name) > maxParticipantID {
		name = name[:maxParticipantID]
	}
	return strings.TrimRight(name, "-")
}

func demoEmail(id string) string { return id + "@koudijs.dev.test" }

func participantEmail(p *api.Participant) string {
	return demoEmail(strings.TrimPrefix(p.Name, participantPrefix))
}

// emailPreviewPlaceholder stands in until a name has any usable character. The
// page's script repeats this literal; changing one means changing both.
const emailPreviewPlaceholder = "your-name"

// displayPreview is what validName would STORE for this name, shown on the join
// page so a participant sees the folding before they commit to it rather than
// afterwards in a Git commit they cannot edit.
func displayPreview(name string) string {
	if folded := labelName(name); folded != "" {
		return folded
	}
	return "your name"
}

func emailPreview(name string) string {
	id := participantID(name)
	if id == "" {
		id = emailPreviewPlaceholder
	}
	return demoEmail(id)
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
	// script-src names one hash, not 'unsafe-inline': the join page's address
	// preview is the only script Room Pass serves, and nothing else may run.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; script-src "+previewScriptSource+"; img-src 'self'; form-action "+s.formAction+"; frame-ancestors 'none'")
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
// from the same host -- one ingress routing /join to Room Pass and everything
// else to the application -- the application origin IS 'self' and the list looks
// complete while silently depending on that coincidence.
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

// previewScript keeps the stored NAME and the address on the join page in step
// with the name box as
// it is typed. It is the browser half of participantID and must agree with it
// character for character, including the placeholder and the 40-character cap:
// the whole point of showing the address is that it is the one the participant
// will get. No regular expressions and no Unicode tables, so the two halves
// stay comparable by eye.
const previewScript = `(function(){var n=document.getElementById("rp-name"),o=document.getElementById("rp-email"),d=document.getElementById("rp-display");if(!n||!o){return}function fold(v){var s=v.normalize("NFKD"),out="",dash=false,i,c;for(i=0;i<s.length;i++){c=s.charAt(i);if((c>="a"&&c<="z")||(c>="A"&&c<="Z")||(c>="0"&&c<="9")){if(dash&&out.length>0){out+="-"}dash=false;out+=c}else if(c<"̀"||c>"ͯ"){dash=true}}if(out.length>40){out=out.slice(0,40)}while(out.length>0&&out.charAt(out.length-1)==="-"){out=out.slice(0,-1)}return out}function show(){var f=fold(n.value);if(d){d.textContent=f||"your name"}o.textContent=(f.toLowerCase()||"your-name")+"@koudijs.dev.test"}n.addEventListener("input",show);show()}())`

// previewScriptSource is the CSP source that admits exactly the script above and
// nothing else. Hashing the same constant the page renders means an edit to the
// script can never leave the policy pointing at the old one; a rendered page is
// checked against this header in the tests.
var previewScriptSource = func() string {
	sum := sha256.Sum256([]byte(previewScript))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}()

var pageSource = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Join the room</title><style>body{font:18px system-ui;margin:3rem auto;padding:0 1rem;max-width:30rem;background:#f8fafc;color:#172033}input,button{box-sizing:border-box;width:100%;padding:.8rem;margin:.4rem 0 1rem;font:inherit}button{background:#1749a5;color:white;border:0;border-radius:.4rem}label{display:block}small{line-height:1.5}.scanned{background:#e8f0fe;border-radius:.4rem;padding:.6rem .8rem;margin:.4rem 0 1rem}.error{color:#b3261e;font-weight:600}input[aria-invalid=true]{border:2px solid #b3261e;background:#fff5f5}.issued{color:#64748b;font-size:.8em;line-height:1.45;margin:-.7rem 0 1.4rem}.issued .line{display:block;font-size:1.15em;margin-bottom:.35rem}.addr{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;color:#475569;word-break:break-all}</style><h1>{{.Title}}</h1><p>{{.Message}}</p>{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}{{if .Form}}<form method="post" action="/join"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="handoff" value="{{.Handoff}}"><input type="hidden" name="return" value="{{.Return}}">{{if .Enrolled}}<p>You’re already enrolled as <strong>{{.EnrolledName}}</strong>. Continue with the same identity.</p><p class="issued"><span class="line">You are joining as <span class="addr">{{.EnrolledEmail}}</span></span>Room Pass built that address from your name, which is why there was nothing to fill in: it is never a real mailbox.{{with .AttributionNote}} {{.}}{{end}}</p>{{else}}{{if .Scanned}}<p class="scanned">Room code <strong>{{.Scanned}}</strong>, from the code you scanned. <input type="hidden" name="code" value="{{.Scanned}}"><input type="hidden" name="scanned" value="1"></p>{{else}}<label>Room code<input name="code" required maxlength="24" autocomplete="off" autocapitalize="characters" placeholder="BCDFGH" value="{{.Code}}"{{if .CodeInvalid}} aria-invalid="true"{{end}}{{if eq .Focus "code"}} autofocus{{end}}></label>{{end}}<label>Display name<input id="rp-name" name="name" required maxlength="64" autocomplete="nickname" value="{{.Name}}"{{if .NameInvalid}} aria-invalid="true"{{end}}{{if eq .Focus "name"}} autofocus{{end}}></label><p class="issued"><span class="line">You will appear as <output id="rp-display" for="rp-name"><strong>{{.Display}}</strong></output></span><span class="line">You will join as <output id="rp-email" for="rp-name" class="addr">{{.Email}}</output></span>Room Pass builds both from your name, so there is nothing to fill in: spaces and accents are folded, and the address is never a real mailbox.{{with .AttributionNote}} {{.}}{{end}}</p>{{end}}<button>Continue</button></form>{{end}}{{if .Enrolled}}<form method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Sign out of this browser</button></form>{{end}}<small>Your name is an unverified label, not a verified identity. It is shown to the application you are joining, together with the address above.</small><script>` + previewScript + `</script></html>`

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
	// notice explains a login transaction that was dropped just below. It is
	// not a failure of anything the participant did, so it does not mark a
	// field; it only says why the page is asking again.
	notice := ""
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
			// A login that ran out of time used to end here, on a bare page
			// telling the participant to start again from the application --
			// with the code and the name they had just typed thrown away. It
			// was also the easiest failure to reach, because the clock runs
			// while somebody reads the form and fixes a rejected name.
			//
			// Nothing about a stale transaction makes this browser unwelcome.
			// Drop it, keep the page, and let them finish joining: they leave
			// enrolled, and the application starts a fresh login when they
			// arrive back -- which by then is one Continue, because this
			// browser has a session cookie.
			handoff = ""
			notice = "That login took too long, so it has to start again. Continue below and you will be sent back to the application to finish."
		}
	}
	var ss session
	enrolled := s.decode(r, "__Host-rp-session", &ss) == nil
	// The name the participant chose, shown back to them when they are already
	// enrolled. Someone returning to this page has no other way to see which
	// identity they are about to continue as -- and the name is the identity
	// here, so it is the one thing worth showing.
	enrolledName, enrolledEmail := "", ""
	if enrolled {
		_, p, e := s.identity(r.Context(), ss)
		enrolled = e == nil
		if enrolled {
			enrolledName = p.Spec.DisplayName
			enrolledEmail = participantEmail(p)
		}
	}
	// A code carried here by a scanned QR code. Only ever a prefill: the POST
	// below re-reads it from the form and Room Pass checks it against the
	// Room's rotating status exactly as it checks a typed one.
	//
	// The marker travels through the POST in a hidden field so that a form
	// coming back with an error still knows the code was scanned rather than
	// typed, and can leave it alone. It decides one thing -- pill or box -- and
	// the value it guards is the participant's own either way, so a forged
	// marker wins nothing that typing the same code would not.
	scanned := ""
	if !enrolled {
		if r.Method == "GET" {
			scanned = scannedCode(r)
		} else if r.FormValue("scanned") != "" {
			scanned = normalizeScannedCode(r.FormValue("code"))
		}
	}
	message := "Enter the room code and choose a display name."
	if scanned != "" {
		message = "Choose a display name to join."
	}
	if enrolled {
		// Neither field is on offer to someone already enrolled; asking for a
		// code above a form that has none is just confusing.
		message = "Welcome back."
	}
	form := true
	if !controller.Active(room, s.now()) {
		message = "This room has closed."
		form = false
	} else if room.Spec.Enrollment != "Open" && !enrolled {
		message = "Joining is closed. If you already joined, return to the application."
		form = false
	}
	// A mistyped code is the one mistake every audience makes, so it returns the form
	// rather than a dead-end error page: the back button would lose both the typed
	// name and a CSRF token that is only minted here. Each render issues a fresh one.
	render := func(status int, failure, field, code, name string) {
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
		// A code the participant cannot correct is worse than no code at all,
		// so a scan whose code was refused comes back as a field to fix rather
		// than as the pill that says "this one is taken care of".
		pill := scanned
		if field == "code" {
			pill = ""
		}
		// Which also changes what the page is asking for: a scan that came back
		// as a field to fix must stop the line under the title claiming there is
		// only a name to fill in.
		heading := message
		if form && !enrolled && pill == "" {
			heading = "Enter the room code and choose a display name."
		}
		focus := field
		if focus == "" && scanned != "" {
			focus = "name"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		// Email is what the script would compute for the name already in the
		// box, so a browser with script disabled and a form that came back with
		// a typed name both still show the address that is actually on offer.
		_ = page.Execute(w, map[string]any{"Title": room.Spec.Title, "AttributionNote": room.Spec.AttributionNote, "Message": heading, "Form": form, "CSRF": csrf, "Handoff": handoff, "Return": dest, "Enrolled": enrolled, "EnrolledName": enrolledName, "EnrolledEmail": enrolledEmail, "Scanned": pill, "Code": code, "Error": failure, "Name": name, "Display": displayPreview(name), "Email": emailPreview(name), "Focus": focus, "CodeInvalid": field == "code", "NameInvalid": field == "name"})
	}
	if r.Method == "GET" {
		render(200, notice, "", "", "")
		return
	}
	// What the participant typed, kept for every path below that has to show
	// the form again. Losing it is what made a single mistake cost a retype of
	// everything.
	code, typed := typedCode(r.FormValue("code")), r.FormValue("name")
	if reason := s.csrfReason(r); reason != "" {
		s.logReject(r, reason,
			"origin", r.Header.Get("Origin"), "expected_origin", s.cfg.JoinOrigin,
			"cookie_names", cookieNames(r), "has_handoff", handoff != "", "enrolled", enrolled)
		// This is overwhelmingly a form that sat open past the token's ten
		// minutes, not an attack -- and "reload and try again" was asking the
		// participant to do by hand what rendering does here, minus the typed
		// name. Nothing has changed yet, so handing back a fresh token and the
		// same answers is safe: a forger still cannot read either.
		render(403, "This page had been open too long. Your answers are still here; please press Continue again.", "", code, typed)
		return
	}
	if !form {
		s.logReject(r, "enrollment-closed", "message", message)
		// message already says what happened, and the page says it in the
		// room's own voice instead of as plain text on a white page.
		render(403, "", "", code, typed)
		return
	}
	if !enrolled {
		if !s.joins.Allow() {
			w.Header().Set("Retry-After", "2")
			render(429, "A lot of people are joining at once. Please press Continue again in a moment.", "", code, typed)
			return
		}
		name, e := validName(typed)
		if e != nil {
			render(400, e.Error(), "name", code, typed)
			return
		}
		ss, e = s.enrollParticipant(r.Context(), code, name)
		if e != nil {
			field := ""
			switch {
			case errors.Is(e, errBadCode):
				field = "code"
			case errors.Is(e, errNameTaken):
				field = "name"
			}
			// The folded name goes back in the box, not the raw one: it is what
			// would have been stored, and it is what the participant has to
			// change. The code goes back untouched -- a name collision is no
			// reason to make somebody read the presenter's screen again.
			render(403, e.Error(), field, code, name)
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
			render(403, "Your enrollment could not be confirmed just now. Please press Continue again.", "", code, typed)
			return
		}
		s.mu.Lock()
		tx := s.transactions[handoff]
		if tx == nil || !s.now().Before(tx.Expires) || tx.Session != nil {
			s.mu.Unlock()
			// Enrolled, but the transaction died between the check at the top
			// of this handler and here. Telling someone who is now a member of
			// the room that their login expired is the least useful true thing
			// we could say: send them to the application, which starts a login
			// this browser can finish in one click.
			http.Redirect(w, r, dest, 303)
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

// An enrollment failure the participant can fix by retyping, so it marks the
// code field rather than the form as a whole.
var errBadCode = errors.New("That code is invalid or joining has closed. Check the presenter’s current code.")

// The other one: a name already enrolled in this room. Both mark the field the
// participant can actually change.
var errNameTaken = errors.New("That name is already taken in this room. Please choose another.")

// maxTypedCodeLength matches the code field's maxlength, so a value that only
// a forged form could carry is bounded before it goes back into the page.
const maxTypedCodeLength = 24

// typedCode is the code as the participant typed it, tidied just enough to be
// put back in the box they typed it into. It is NOT a validity check --
// controller.Accepts owns that -- only a bound on what is echoed, so a refused
// code can be corrected in place instead of read off the presenter's screen
// again. html/template escapes it either way.
func typedCode(raw string) string {
	v := strings.TrimSpace(raw)
	if len(v) > maxTypedCodeLength {
		v = v[:maxTypedCodeLength]
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(v, ""))
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
		return session{}, errBadCode
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
	p := &api.Participant{ObjectMeta: metav1.ObjectMeta{Name: participantPrefix + participantID(name), Namespace: room.Namespace, OwnerReferences: []metav1.OwnerReference{{APIVersion: api.GroupVersion.String(), Kind: "Room", Name: room.Name, UID: room.UID}}}, Spec: api.ParticipantSpec{RoomRef: api.RoomRef{Name: room.Name, UID: string(room.UID)}, DisplayName: name}}
	if e = s.db.Create(ctx, p); e != nil {
		if !apierrors.IsAlreadyExists(e) {
			return session{}, errors.New("Enrollment could not be confirmed. Please retry.")
		}
		// The name is the identity now, so a name already present is someone
		// else's enrollment and must not be handed out twice: two browsers
		// sharing one Participant would be one voter with two ballots. Say so
		// on the name field rather than adopting the object. The cost is that a
		// create whose response was lost also reports the name as taken -- rare
		// next to two people called Jan, and the safe way to be wrong.
		result = "name_taken"
		return session{}, errNameTaken
	}
	result = "enrolled"
	return session{RoomUID: string(room.UID), Name: p.Name, UID: string(p.UID), Expires: s.now().Add(s.cfg.CookieLifetime).Unix()}, nil
}

// handoffLifetime is how long a login transaction -- and the two browser-binding
// cookies that pin it to one browser -- stay usable.
//
// It has to outlast a person filling in a form, not a redirect. The window
// covers reading the page, typing a room code and a display name, being told
// the name is taken, and picking another. Three minutes did not cover that, and
// running out used to end the login outright.
//
// It no longer does -- join() drops a dead transaction and lets the participant
// finish -- but expiring mid-form still costs them a trip back through the
// application, so the window is sized for the slow case rather than the quick
// one. The cost is that abandoned logins hold a MaxHandoffs slot for longer.
// That is the right thing to trade: what stops a copied login link is the pair
// of host-only cookies bound to the transaction, not the clock.
const handoffLifetime = 10 * time.Minute

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
	if e = s.cookie(w, "__Host-rp-browser", browser, int(handoffLifetime.Seconds())); e != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	s.transactions[id] = &transaction{State: state, Browser: browser, Expires: s.now().Add(handoffLifetime)}
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
	r.Header.Set("X-Remote-User-Id", strings.TrimPrefix(p.Name, participantPrefix))
	r.Header.Set("X-Remote-User", p.Spec.DisplayName)
	r.Header.Set("X-Remote-User-Name", p.Spec.DisplayName)
	r.Header.Set("X-Remote-User-Email", participantEmail(p))
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
	if e := s.cookie(w, "__Host-rp-join-browser", jb, int(handoffLifetime.Seconds())); e != nil {
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
