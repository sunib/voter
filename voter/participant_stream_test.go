package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ConfigButler/krm-stream/gateway/kube"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type sharedStreamFixture struct {
	cfg        config
	server     *httptest.Server
	runtime    *streamRuntime
	watches    atomic.Int64
	reviews    atomic.Int64
	identities atomic.Int64
	mu         sync.Mutex
	denied     map[string]string
}

func newSharedStreamFixture(t *testing.T) *sharedStreamFixture {
	t.Helper()
	f := &sharedStreamFixture{cfg: authorizationFixture(t), denied: map[string]string{}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for key := range r.Header {
			lower := strings.ToLower(key)
			if strings.HasPrefix(lower, "impersonate-") || strings.HasPrefix(lower, "x-remote-") {
				t.Errorf("untrusted header forwarded: %s", key)
			}
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch {
		case strings.HasSuffix(r.URL.Path, "/selfsubjectreviews"):
			f.identities.Add(1)
			if token == "identity-error" {
				http.Error(w, "identity unavailable", 500)
				return
			}
			_ = json.NewEncoder(w).Encode(authenticationv1.SelfSubjectReview{Status: authenticationv1.SelfSubjectReviewStatus{UserInfo: authenticationv1.UserInfo{
				Username: "resolved:" + token, Groups: []string{"resolved-group"}, UID: "uid:" + token, Extra: map[string]authenticationv1.ExtraValue{"scope": {"fixture"}},
			}}})
		case strings.HasSuffix(r.URL.Path, "/subjectaccessreviews"):
			f.reviews.Add(1)
			if token != "service-account" {
				t.Error("access review did not use service account")
			}
			var review authorizationv1.SubjectAccessReview
			if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
				t.Error(err)
				return
			}
			s := review.Spec
			if s.UID != "uid:"+strings.TrimPrefix(s.User, "resolved:") || len(s.Groups) != 1 || s.Groups[0] != "resolved-group" || len(s.Extra["scope"]) != 1 || s.Extra["scope"][0] != "fixture" {
				t.Errorf("identity not preserved: %+v", s)
			}
			if s.ResourceAttributes.Name != "testnet" || s.ResourceAttributes.Namespace != "voter" {
				t.Errorf("wrong scope: %+v", s.ResourceAttributes)
			}
			f.mu.Lock()
			denial := f.denied[s.User]
			f.mu.Unlock()
			if denial == "error" {
				http.Error(w, "review unavailable", 500)
				return
			}
			if denial == "timeout" {
				<-r.Context().Done()
				return
			}
			review.Status.Allowed = denial != "all" && denial != s.ResourceAttributes.Verb
			_ = json.NewEncoder(w).Encode(review)
		default:
			if token != "service-account" {
				t.Error("watch did not use service account")
			}
			if r.URL.Query().Get("fieldSelector") != "metadata.name=testnet" {
				t.Errorf("unscoped watch: %s", r.URL)
			}
			f.watches.Add(1)
			_, _ = io.WriteString(w, `{"type":"ADDED","object":{"apiVersion":"examples.configbutler.ai/v1alpha1","kind":"CoffeeConfig","metadata":{"name":"testnet","namespace":"voter","uid":"coffee-uid","resourceVersion":"1"},"spec":{"shopName":"Fixture"}}}`+"\n")
			_, _ = io.WriteString(w, `{"type":"BOOKMARK","object":{"apiVersion":"examples.configbutler.ai/v1alpha1","kind":"CoffeeConfig","metadata":{"resourceVersion":"1","annotations":{"k8s.io/initial-events-end":"true"}}}}`+"\n")
			w.(http.Flusher).Flush()
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-r.Context().Done():
					return
				case now := <-ticker.C:
					_, err := fmt.Fprintf(w, `{"type":"MODIFIED","object":{"apiVersion":"examples.configbutler.ai/v1alpha1","kind":"CoffeeConfig","metadata":{"name":"testnet","namespace":"voter","uid":"coffee-uid","resourceVersion":"%d"},"spec":{"shopName":"Fixture %d"}}}`+"\n", now.UnixNano(), now.UnixNano())
					if err != nil {
						return
					}
					w.(http.Flusher).Flush()
				}
			}
		}
	}))
	t.Cleanup(upstream.Close)
	f.cfg.KubernetesAPIServer = upstream.URL
	rc := &rest.Config{Host: upstream.URL, BearerToken: "service-account", QPS: 1000, Burst: 1000}
	rc.ContentType = "application/json"
	typed, err := kubernetes.NewForConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	f.runtime = makeStreamRuntime(kube.NewBackend(dyn), typed)
	f.runtime.reauthorizationInterval = 40 * time.Millisecond
	mux := http.NewServeMux()
	registerParticipantStreamHandlers(mux, handlerDeps{cfg: f.cfg, defaultNS: "voter", streams: f.runtime})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *sharedStreamFixture) open(t *testing.T, token, query string, expiry time.Time) (*http.Response, *http.Request) {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := setParticipantSession(rec, f.cfg, sessionCookieCodec, participantSession{IDToken: token, Subject: "untrusted-for-kubernetes", Groups: []string{"wrong-group"}, KubeUsername: "wrong-username", TokenExpiry: expiry.Unix()}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/public/stream?group=examples.configbutler.ai&resource=coffeeconfigs&"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer browser-forged-token")
	req.Header.Set("Impersonate-User", "cluster-admin")
	req.Header.Set("X-Remote-User", "cluster-admin")
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response, req
}

const streamQuery = "namespace=voter&name=testnet&version=v1alpha1"

func awaitSynced(t *testing.T, r *http.Response) *bufio.Reader {
	t.Helper()
	reader := bufio.NewReader(r.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("no synced frame: %v", err)
		}
		if strings.Contains(line, `"type":"synced"`) {
			return reader
		}
	}
}
func awaitStreamCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("stream condition timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStreamPinsNamespaceNameAndVersionBeforeOpeningBackend(t *testing.T) {
	f := newSharedStreamFixture(t)
	for _, query := range []string{"namespace=other&name=testnet&version=v1alpha1", "namespace=voter&name=other&version=v1alpha1", "namespace=voter&version=v1alpha1", "namespace=voter&name=testnet&version=v9"} {
		response, _ := f.open(t, "allowed", query, time.Now().Add(time.Hour))
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `"terminal":true`) || strings.Contains(string(body), `"object"`) || f.watches.Load() != 0 || f.identities.Load() != 0 || f.reviews.Load() != 0 {
			t.Fatalf("scope reached upstream: %s", body)
		}
	}
}

func TestSharedStreamExpiryAndDisconnectIsolation(t *testing.T) {
	f := newSharedStreamFixture(t)
	short, request := f.open(t, "short", streamQuery, time.Now().Add(2*time.Second))
	awaitSynced(t, short)
	long, _ := f.open(t, "long", streamQuery, time.Now().Add(time.Hour))
	reader := awaitSynced(t, long)
	if f.watches.Load() != 1 {
		t.Fatalf("opened %d watches", f.watches.Load())
	}
	if _, err := io.Copy(io.Discard, short.Body); err != nil {
		t.Fatal(err)
	}
	if f.runtime.metrics.watches.Load() != 1 {
		t.Fatal("expiry stopped another subscriber's watch")
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("remaining subscriber disconnected: %s %v", line, err)
	}
	response, err := f.server.Client().Do(request.Clone(t.Context()))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("expired reconnect status %d", response.StatusCode)
	}
	_ = long.Body.Close()
	awaitStreamCondition(t, func() bool { return f.runtime.metrics.watches.Load() == 0 && f.runtime.metrics.subscribers.Load() == 0 })
	if f.identities.Load() != 2 {
		t.Fatalf("identity should be resolved once per subscription: %d", f.identities.Load())
	}
}

func TestSharedStreamWarmCacheDenialAndRevocation(t *testing.T) {
	for _, denial := range []string{"list", "watch", "error", "timeout"} {
		t.Run(denial, func(t *testing.T) {
			f := newSharedStreamFixture(t)
			warm, _ := f.open(t, "warm", streamQuery, time.Now().Add(time.Hour))
			awaitSynced(t, warm)
			revoke, _ := f.open(t, "revoke", streamQuery, time.Now().Add(time.Hour))
			awaitSynced(t, revoke)
			f.mu.Lock()
			f.denied["resolved:denied"] = denial
			f.denied["resolved:revoke"] = denial
			f.mu.Unlock()
			denied, _ := f.open(t, "denied", streamQuery, time.Now().Add(time.Hour))
			body, err := io.ReadAll(denied.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), `"object"`) || !strings.Contains(string(body), `"terminal":true`) {
				t.Fatalf("warm cache disclosed: %s", body)
			}
			if _, err := io.Copy(io.Discard, revoke.Body); err != nil {
				t.Fatalf("revoked stream not closed: %v", err)
			}
			if f.runtime.metrics.watches.Load() != 1 || f.watches.Load() != 1 {
				t.Fatal("revocation disturbed shared watch")
			}
			if f.runtime.metrics.reviewFailures.Load() < 2 {
				t.Fatal("missing failure metrics")
			}
			_ = warm.Body.Close()
			awaitStreamCondition(t, func() bool { return f.runtime.metrics.watches.Load() == 0 })
		})
	}
}

func TestSharedStreamIdentityFailureDeniesBeforeWatch(t *testing.T) {
	f := newSharedStreamFixture(t)
	response, _ := f.open(t, "identity-error", streamQuery, time.Now().Add(time.Hour))
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"object"`) || f.watches.Load() != 0 || f.reviews.Load() != 0 {
		t.Fatalf("identity failure disclosed data: %s", body)
	}
}

func TestStreamWriteDeadlineReleasesBlockedClient(t *testing.T) {
	result := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := (streamResponseWriter{ResponseWriter: w}).Write(make([]byte, 16*1024*1024))
		result <- err
	}))
	defer server.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: fixture\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	// Deliberately never read the response; the server must release the writer.
	select {
	case err = <-result:
		if err == nil {
			t.Fatal("write unexpectedly fit without a consumer")
		}
	case <-time.After(7 * time.Second):
		t.Fatal("blocked SSE writer exceeded its deadline")
	}
}
