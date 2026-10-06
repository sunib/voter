package main

// Who is asking, as krm-foyer vouches for it.
//
// Voter keeps no session of its own any more. The edge sends every /public/
// request through krm-foyer's /auth/check?identity=true (Traefik ForwardAuth),
// which answers for the browser's krm-foyer session -- including its CSRF and
// same-origin rules for a write -- and hands back one header,
// Krm-Foyer-Identity: the API server's own userInfo for that person, with the
// session's display name and connector (krm-foyer docs/ingress.md, "Identity for
// a domain backend"). Never a token: Voter learns who someone is, and cannot act
// as them in Kubernetes.
//
// The header can only be trusted because of where it comes from:
//   - the edge removes a browser's copy before adding krm-foyer's
//     (authResponseHeaders), on the one route that always asks the check;
//   - a NetworkPolicy admits only the edge to Voter's port, so nothing else can
//     send Voter a header the check did not write.
// Both are deployment, not code. This file can only refuse a request that
// arrives without the header, which means one of them is wrong.

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
)

const identityHeader = "Krm-Foyer-Identity"

// foyerIdentity is the header's JSON. Only what Voter uses is decoded.
type foyerIdentity struct {
	// UserInfo is the API server's answer to a SelfSubjectReview with the
	// person's own token: the name RBAC and the audit log use.
	UserInfo authenticationv1.UserInfo `json:"userInfo"`
	// DisplayName and Connector are the krm-foyer session's.
	DisplayName string `json:"displayName"`
	Connector   string `json:"connector"`
}

// maxIdentityBytes bounds the header before decoding. A userInfo is a name, a
// handful of groups and two or three extras.
const maxIdentityBytes = 8 * 1024

func decodeIdentity(r *http.Request) (foyerIdentity, bool) {
	var id foyerIdentity
	values := r.Header.Values(identityHeader)
	if len(values) != 1 || values[0] == "" || len(values[0]) > maxIdentityBytes {
		return id, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(values[0])
	if err != nil {
		return id, false
	}
	if err := json.Unmarshal(raw, &id); err != nil || id.UserInfo.Username == "" {
		return id, false
	}
	return id, true
}

// requireIdentity hands a handler the person krm-foyer vouched for, and logs
// every refusal the handler gives them.
func requireIdentity(next func(http.ResponseWriter, *http.Request, foyerIdentity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := decodeIdentity(r)
		if !ok {
			// Behind a correct edge this cannot happen: the check answers 401
			// itself for someone signed out, and never forwards without the
			// header. So this is the deployment, and the log says so.
			log.Printf("identity: refused %s %s: no usable %s header; is the route behind krm-foyer's identity check?",
				r.Method, r.URL.Path, identityHeader)
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":    "authentication required",
				"loginUrl": "/auth/login",
			})
			return
		}
		recorder := &refusalRecorder{ResponseWriter: w}
		next(recorder, r, id)
		recorder.log(r, id)
	}
}

// refusalRecorder writes down every refusal a person was given.
//
// Here rather than at each refusal for one reason: a refusal a handler can
// forget to log is exactly the defect this repairs. On 2026-09-17 not one of
// thirty-two refusing call sites wrote a line -- the application logged every
// coffee order and every login and no refusal at all. So two defects survived a
// demo in front of two hundred people and had to be reconstructed four days
// later from the creationTimestamps of the objects that did get written.
// docs/post-demo-2026-09-17.md.
type refusalRecorder struct {
	http.ResponseWriter
	status int
	// body is the refusal's own words, bounded: these are short JSON objects or
	// http.Error strings, and a truncated one still says which refusal it was.
	body []byte
}

const maxRefusalLogBytes = 256

func (rec *refusalRecorder) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
	}
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *refusalRecorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	if rec.status >= 400 && len(rec.body) < maxRefusalLogBytes {
		rec.body = append(rec.body, b[:min(len(b), maxRefusalLogBytes-len(rec.body))]...)
	}
	return rec.ResponseWriter.Write(b)
}

// Flush keeps the order feed streaming. Without it the SSE handler's
// http.Flusher assertion fails against this wrapper and every subscriber waits
// for a buffer that is never sent.
func (rec *refusalRecorder) Flush() {
	if flusher, ok := rec.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (rec *refusalRecorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

func (rec *refusalRecorder) log(r *http.Request, id foyerIdentity) {
	if rec.status < 400 {
		return
	}
	log.Printf("refused: %s %s status=%d sub=%s connector=%s: %s",
		r.Method, r.URL.Path, rec.status, id.UserInfo.Username, id.Connector,
		strings.TrimSpace(string(rec.body)))
}
