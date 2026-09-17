package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// databaseRequest builds a signed-in request to a Database endpoint. Same
// session machinery as the coffee tests -- see authorizedRequestAs -- but with
// this endpoint's own body and path, because the coffee helper hard-codes a
// CoffeeConfig patch.
func databaseRequest(t *testing.T, cfg config, method, path, body string) *http.Request {
	t.Helper()
	now := time.Now()
	rec := httptest.NewRecorder()
	if err := setParticipantSession(rec, cfg, sessionCookieCodec, participantSession{
		IDToken: "participant-token", Subject: "sub", DisplayName: "Ada",
		Connector: testParticipantConnector, TokenExpiry: now.Add(time.Hour).Unix(),
	}, now); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for _, cookie := range rec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	s, ok := getParticipantSession(req, cfg, sessionCookieCodec, now)
	if !ok {
		t.Fatal("fixture session invalid")
	}
	req.Header.Set("X-CSRF-Token", s.CSRF)
	req.Header.Set("Origin", cfg.AppOrigin)
	return req
}

// databaseUpstream records what reached the "API server" and answers with the
// object the handler expects to find there.
type databaseUpstream struct {
	server  *httptest.Server
	methods []string
	paths   []string
	bodies  []string
}

func newDatabaseUpstream(t *testing.T, cfg *config) *databaseUpstream {
	t.Helper()
	up := &databaseUpstream{}
	up.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		up.methods = append(up.methods, r.Method)
		up.paths = append(up.paths, r.URL.Path)
		up.bodies = append(up.bodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"platform.configbutler.ai/v1alpha1","kind":"Database",` +
			`"metadata":{"name":"checkout-postgresql","namespace":"voter","uid":"db-uid","resourceVersion":"7",` +
			`"managedFields":[{"manager":"kubectl"}]},` +
			`"spec":{"engine":"postgresql","tier":"standard","service":"checkout",` +
			`"owner":{"team":"payments-core","costCentre":"CC-finance-07","contact":"pc@example.com"}}}`))
	}))
	t.Cleanup(up.server.Close)
	cfg.KubernetesAPIServer = up.server.URL
	return up
}

func databaseMux(t *testing.T) (*http.ServeMux, config, *databaseUpstream) {
	t.Helper()
	return databaseMuxWith(t, nil)
}

func databaseMuxWith(t *testing.T, tune func(*config)) (*http.ServeMux, config, *databaseUpstream) {
	t.Helper()
	old := sessionCookieCodec
	sessionCookieCodec = testCodec(t)
	t.Cleanup(func() { sessionCookieCodec = old })
	cfg := testConfig()
	up := newDatabaseUpstream(t, &cfg)
	if tune != nil {
		tune(&cfg)
	}
	mux := http.NewServeMux()
	registerParticipantDatabaseHandlers(mux, handlerDeps{cfg: cfg, defaultNS: "voter"})
	return mux, cfg, up
}

// The note a person typed into "why do you need this?" is the half of the page
// that is not a dropdown. With no GitTarget watching Databases there is no
// commit to carry it, so it has to land on the object or it is lost -- and the
// list page reads it straight back off the annotation.
func TestDatabaseCreateRecordsTheIntent(t *testing.T) {
	mux, cfg, up := databaseMux(t)
	body := `{"name":"checkout-postgresql","spec":{"engine":"postgresql","tier":"standard",` +
		`"service":"checkout","owner":{"team":"payments-core","costCentre":"CC-finance-07","contact":"pc@example.com"}}}`
	req := databaseRequest(t, cfg, "POST", "/public/databases", body)
	req.Header.Set("X-Change-Reason", "Checkout needs its own store before Black Friday.")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(up.bodies) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(up.bodies))
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(up.bodies[0]), &sent); err != nil {
		t.Fatal(err)
	}
	meta, _ := sent["metadata"].(map[string]any)
	annotations, _ := meta["annotations"].(map[string]any)
	if annotations[intentAnnotation] != "Checkout needs its own store before Black Friday." {
		t.Fatalf("intent annotation = %#v", annotations)
	}
}

// The patch and the note are ONE write. Two would leave a window in which the
// object records a change nobody gave a reason for, and a failure between them
// would leave that window open forever.
func TestDatabasePatchCarriesTheIntentInTheSameWrite(t *testing.T) {
	mux, cfg, up := databaseMux(t)
	body := `{"uid":"db-uid","resourceVersion":"7","patch":{"spec":{"size":"large"}}}`
	req := databaseRequest(t, cfg, "PATCH", "/public/databases/checkout-postgresql", body)
	req.Header.Set("X-Change-Reason", "Growth forecast doubled.")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	// A GET to check the uid, then the PATCH itself.
	if len(up.methods) != 2 || up.methods[1] != "PATCH" {
		t.Fatalf("upstream calls = %v", up.methods)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(up.bodies[1]), &sent); err != nil {
		t.Fatal(err)
	}
	meta, _ := sent["metadata"].(map[string]any)
	annotations, _ := meta["annotations"].(map[string]any)
	if annotations[intentAnnotation] != "Growth forecast doubled." {
		t.Fatalf("intent annotation = %#v", annotations)
	}
	if meta["resourceVersion"] != "7" {
		t.Fatalf("patch dropped the resourceVersion: %#v", meta)
	}
}

// Not doing the gitops-reverser config yet means exactly this: no CommitRequest
// leaves the process. Pointing a Database save at the COFFEE target would close
// the commit window demo 1 is waiting on, from a page that never touched the
// menu -- so the default is a separate, empty setting and no request at all.
func TestDatabaseSaveMakesNoCommitRequestByDefault(t *testing.T) {
	mux, cfg, up := databaseMuxWith(t, func(cfg *config) {
		// The coffee target IS set, as it is in the deployment. That is the
		// whole point: the database save must not reach for it.
		cfg.ConfigButlerGitTargetName = "voter-demo"
	})
	req := databaseRequest(t, cfg, "PATCH", "/public/databases/checkout-postgresql",
		`{"uid":"db-uid","resourceVersion":"7","patch":{"spec":{"size":"large"}}}`)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	for _, path := range up.paths {
		if strings.Contains(path, "commitrequests") {
			t.Fatalf("a database save created a CommitRequest: %v", up.paths)
		}
	}
	var receipt map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if _, present := receipt["commitRequested"]; present {
		t.Fatalf("receipt claims something about a commit: %#v", receipt)
	}
}

// The name goes into a URL path and then into an object. A readable refusal
// here beats an apiserver 422 quoting a regular expression at a room.
func TestDatabaseCreateRefusesAnUnusableName(t *testing.T) {
	mux, cfg, up := databaseMux(t)
	for _, name := range []string{"", "Checkout", "-leading", "trailing-", "has space", "a/b"} {
		body := `{"name":` + mustJSON(t, name) + `,"spec":{"engine":"postgresql"}}`
		req := databaseRequest(t, cfg, "POST", "/public/databases", body)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("name %q: status = %d, want 400", name, rec.Code)
		}
	}
	if len(up.methods) != 0 {
		t.Fatalf("a refused name still reached the API server: %v", up.methods)
	}
}

// The list is the first paint of a screen the stream then takes over, so the
// two have to agree about shape -- including that neither carries managedFields.
func TestDatabaseListIsProjected(t *testing.T) {
	mux, cfg, _ := databaseMux(t)
	req := databaseRequest(t, cfg, "GET", "/public/databases", "")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "managedFields") {
		t.Fatalf("the list handed the browser managedFields: %s", rec.Body.String())
	}
}

// Delete is not something these pages do, and the handler must not quietly
// offer what the Role does not grant.
func TestDatabaseDeleteIsNotOffered(t *testing.T) {
	mux, cfg, up := databaseMux(t)
	req := databaseRequest(t, cfg, "DELETE", "/public/databases/checkout-postgresql", "")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if len(up.methods) != 0 {
		t.Fatalf("a delete reached the API server: %v", up.methods)
	}
}
