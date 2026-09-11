// A deliberately small server-side OIDC client for the disposable demo cluster.
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gorilla/securecookie"
	"golang.org/x/oauth2"
)

const origin = "https://demo.roompass.test:18443"

type session struct{ Token, Name, State, Verifier string }

func id() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func main() {
	ca, e := os.ReadFile("/tls/ca.crt")
	if e != nil {
		log.Fatal(e)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	protocol := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	ctx := oidc.ClientContext(context.Background(), protocol)
	var provider *oidc.Provider
	for {
		provider, e = oidc.NewProvider(ctx, "https://login.roompass.test:18443")
		if e == nil {
			break
		}
		time.Sleep(time.Second)
	}
	oauth := oauth2.Config{ClientID: "room-pass-demo", Endpoint: provider.Endpoint(), RedirectURL: origin + "/app/callback", Scopes: []string{"openid", "profile", "email", "groups"}}
	verifier := provider.Verifier(&oidc.Config{ClientID: oauth.ClientID})
	hash, _ := hex.DecodeString(id())
	block, _ := hex.DecodeString(id())
	cookies := securecookie.New(hash, block).MaxAge(300)
	set := func(w http.ResponseWriter, s session) {
		v, e := cookies.Encode("__Host-rp-demo", s)
		if e != nil {
			http.Error(w, "Session failed", 500)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "__Host-rp-demo", Value: v, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	}
	get := func(r *http.Request) (session, error) {
		var s session
		c, e := r.Cookie("__Host-rp-demo")
		if e != nil {
			return s, e
		}
		e = cookies.Decode(c.Name, c.Value, &s)
		return s, e
	}
	kubeCA, e := os.ReadFile("/kube/ca.crt")
	if e != nil {
		log.Fatal(e)
	}
	kp := x509.NewCertPool()
	kp.AppendCertsFromPEM(kubeCA)
	kube := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: kp, MinVersion: tls.VersionTLS12}}}
	page := template.Must(template.New("app").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Room Pass demo</title><style>body{font:18px system-ui;max-width:36rem;margin:3rem auto;padding:1rem;color:#172033}button,a{font:inherit;padding:.8rem}pre{white-space:pre-wrap}</style><h1>Room Pass demo</h1>{{if .Name}}<p>Welcome, {{.Name}}.</p><form action="/app/write" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Write a message to Kubernetes</button></form><pre>{{.Result}}</pre><p>This uses your demo identity. Work resources are outside its permissions.</p>{{else}}<p>Join the room, then make a real Kubernetes change.</p>{{end}}<p><a href="/app/login">{{if .Name}}Sign in again{{else}}Join the demo{{end}}</a></p></html>`))
	http.HandleFunc("/app/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
		s, e := get(r)
		switch r.URL.Path {
		case "/app/login":
			if r.Method != "GET" {
				w.WriteHeader(405)
				return
			}
			s = session{State: id(), Verifier: oauth2.GenerateVerifier()}
			set(w, s)
			// The QR hand-off, exactly as the real application does it: a
			// login started by scanning carries the room code, and the only
			// way to get it to Room Pass is a cookie on this shared host,
			// because the join URL is built after Dex from a handoff id this
			// client never sees. SameSite=Lax is the load-bearing part -- the
			// request that needs this cookie is a top-level navigation
			// arriving from the issuer's origin.
			if code := joinCode(r.URL.Query().Get("code")); code != "" {
				http.SetCookie(w, &http.Cookie{Name: "__Host-room-pass-joincode", Value: code, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
			}
			http.Redirect(w, r, oauth.AuthCodeURL(s.State, oauth2.S256ChallengeOption(s.Verifier)), 303)
			return
		case "/app/callback":
			if e != nil || s.State == "" || s.State != r.URL.Query().Get("state") {
				http.Error(w, "Invalid login state", 403)
				return
			}
			token, e := oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(s.Verifier))
			if e != nil {
				http.Error(w, "Token exchange failed", 403)
				return
			}
			raw, _ := token.Extra("id_token").(string)
			verified, e := verifier.Verify(ctx, raw)
			if e != nil {
				http.Error(w, "Token invalid", 403)
				return
			}
			var claims struct {
				Name string `json:"name"`
			}
			_ = verified.Claims(&claims)
			set(w, session{Token: raw, Name: claims.Name, State: id()})
			http.Redirect(w, r, "/app/", 303)
			return
		case "/app/write":
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if e != nil || s.Token == "" || r.Method != "POST" || r.Header.Get("Origin") != origin || r.ParseForm() != nil || r.FormValue("csrf") != s.State {
				http.Error(w, "Reload and sign in", 403)
				return
			}
			payload, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]string{"generateName": "browser-", "namespace": "demo"}, "data": map[string]string{"message": "Hello from " + s.Name}})
			req, _ := http.NewRequest("POST", "https://kubernetes.default.svc/api/v1/namespaces/demo/configmaps", strings.NewReader(string(payload)))
			req.Header.Set("Authorization", "Bearer "+s.Token)
			req.Header.Set("Content-Type", "application/json")
			resp, e := kube.Do(req)
			if e != nil {
				http.Error(w, "Kubernetes unavailable", 503)
				return
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 16384))
			result := fmt.Sprintf("Kubernetes returned %d\n%s", resp.StatusCode, b)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = page.Execute(w, map[string]string{"Name": s.Name, "CSRF": s.State, "Result": result})
			return
		case "/app/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = page.Execute(w, map[string]string{"Name": s.Name, "CSRF": s.State})
			return
		default:
			http.NotFound(w, r)
		}
	})
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	log.Fatal((&http.Server{Addr: ":8080", ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, Handler: http.DefaultServeMux}).ListenAndServe())
}

// joinCode bounds what may go into the hand-off cookie. Room Pass decides
// whether the code is valid; this only refuses to forward things that were
// never a code.
func joinCode(raw string) string {
	code := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(raw), "-", ""))
	if code == "" || len(code) > 12 {
		return ""
	}
	for _, r := range code {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return code
}
