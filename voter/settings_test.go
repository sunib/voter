package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testMenu = `apiVersion: examples.configbutler.ai/v1alpha1
kind: CoffeeConfig
metadata:
  name: demo-coffee
spec:
  shopName: From a folder
  currency: EUR
  products:
    - sku: tea
      name: Tea
      priceCents: 200
      enabled: true
`

func writeSettingsFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func folderConfig(dir string) config {
	return config{ShopOnly: true, SettingsDir: dir, CoffeeConfigName: "demo-coffee"}
}

func TestConfigValidate(t *testing.T) {
	bao := config{
		ShopOnly: true, SettingsDir: "/s", SettingsKeys: settingsKeysOpenBaoAge,
		OpenBaoAddr: "https://b", OpenBaoRole: "r", OpenBaoAgeSecretPath: "p", OpenBaoTokenFile: "f",
	}
	without := func(change func(*config)) config {
		c := bao
		change(&c)
		return c
	}
	tests := []struct {
		name string
		cfg  config
		ok   bool
	}{
		{"demo, as today", config{}, true},
		{"production, as today", config{ShopOnly: true}, true},
		{"menu from a folder, no keys", config{ShopOnly: true, SettingsDir: "/s"}, true},
		{"production after the cutover", without(func(c *config) { c.DontDoThis = true }), true},
		{"folder with the editor on", config{SettingsDir: "/s"}, false},
		{"keys without a folder", without(func(c *config) { c.SettingsDir = "" }), false},
		{"the page without keys", config{ShopOnly: true, SettingsDir: "/s", DontDoThis: true}, false},
		{"no OpenBao address", without(func(c *config) { c.OpenBaoAddr = "" }), false},
		{"no OpenBao role", without(func(c *config) { c.OpenBaoRole = "" }), false},
		{"no OpenBao path", without(func(c *config) { c.OpenBaoAgeSecretPath = "" }), false},
		{"no OpenBao token file", without(func(c *config) { c.OpenBaoTokenFile = "" }), false},
		{"env is not a key method", without(func(c *config) { c.SettingsKeys = "env" }), false},
		{"a typo must not read as no keys", without(func(c *config) { c.SettingsKeys = "openbao" }), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.validate(); (err == nil) != tt.ok {
				t.Fatalf("validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestLoadSettingsFindsTheMenuByContent(t *testing.T) {
	dir := t.TempDir()
	writeSettingsFile(t, dir, "anything.yaml", testMenu)
	writeSettingsFile(t, dir, "notes.yaml", "kind: ConfigMap\nmetadata: {name: x}\n")
	writeSettingsFile(t, dir, "broken.yaml", "{{{ not yaml")
	writeSettingsFile(t, dir, "ignored.txt", "not read")

	loaded, err := loadSettings(folderConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.menu.Spec.ShopName != "From a folder" || len(loaded.menu.Spec.Products) != 1 {
		t.Fatalf("menu = %+v", loaded.menu.Spec)
	}
	if !loaded.menu.Spec.Products[0].Enabled {
		t.Fatal("the product did not parse as enabled")
	}
	if len(loaded.revision) != 16 {
		t.Fatalf("revision = %q", loaded.revision)
	}
	if loaded.keys != nil {
		t.Fatal("no SETTINGS_KEYS must open nothing")
	}
}

func TestLoadSettingsRefusesABadMenu(t *testing.T) {
	tests := map[string]string{
		"no menu":       "kind: ConfigMap\nmetadata: {name: demo-coffee}\n",
		"another name":  strings.Replace(testMenu, "demo-coffee", "other", 1),
		"no currency":   strings.Replace(testMenu, "currency: EUR\n", "", 1),
		"no products":   "kind: CoffeeConfig\nmetadata: {name: demo-coffee}\nspec: {currency: EUR}\n",
		"a wrong shape": "kind: CoffeeConfig\nmetadata: {name: demo-coffee}\nspec: {currency: EUR, products: 3}\n",
	}
	for name, menu := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeSettingsFile(t, dir, "coffeeconfig.yaml", menu)
			if _, err := loadSettings(folderConfig(dir)); err == nil {
				t.Fatal("a bad menu must refuse to start")
			}
		})
	}
	t.Run("two menus", func(t *testing.T) {
		dir := t.TempDir()
		writeSettingsFile(t, dir, "a.yaml", testMenu)
		writeSettingsFile(t, dir, "b.yaml", testMenu)
		if _, err := loadSettings(folderConfig(dir)); err == nil {
			t.Fatal("two menus must refuse to start")
		}
	})
}

// How a ConfigMap is mounted: each file a symlink into a ..data directory.
func TestLoadSettingsReadsAConfigMapMount(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "..2026_11_04")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSettingsFile(t, data, "coffeeconfig.yaml", testMenu)
	if err := os.Symlink(filepath.Base(data), filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..data", "coffeeconfig.yaml"), filepath.Join(dir, "coffeeconfig.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSettings(folderConfig(dir)); err != nil {
		t.Fatal(err)
	}
}

func TestRevisionFollowsTheMenuAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	writeSettingsFile(t, dir, "coffeeconfig.yaml", testMenu)
	revision := func() string {
		t.Helper()
		loaded, err := loadSettings(folderConfig(dir))
		if err != nil {
			t.Fatal(err)
		}
		return loaded.revision
	}
	first := revision()
	if again := revision(); again != first {
		t.Fatalf("the same menu gave %s and %s", first, again)
	}
	writeSettingsFile(t, dir, "coffee-mail.sops.yaml", "kind: Secret\nmetadata: {name: coffee-mail}\ndata: {apiKey: 'ENC[x]'}\nsops: {version: 3}\n")
	if withKey := revision(); withKey != first {
		t.Fatalf("an encrypted file moved the revision: %s and %s", first, withKey)
	}
	writeSettingsFile(t, dir, "coffeeconfig.yaml", strings.Replace(testMenu, "priceCents: 200", "priceCents: 300", 1))
	if changed := revision(); changed == first {
		t.Fatal("a new menu kept the revision")
	}
}

func TestRevisionEndpoint(t *testing.T) {
	get := func(deps handlerDeps) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		registerHandlers(mux, deps)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/revision.json", nil))
		return rec
	}
	rec := get(handlerDeps{settings: &settings{revision: "abc"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("Cache-Control = %q", cache)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["revision"] != "abc" {
		t.Fatalf("revision = %q", body["revision"])
	}
	// Still JSON from Kubernetes, not the SPA's HTML: a tab can be open across a
	// change of mode.
	rec = get(handlerDeps{})
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if body["revision"] != "" {
		t.Fatalf("revision = %q", body["revision"])
	}
}

func TestConfigEndpointSaysWhereTheMenuIsFrom(t *testing.T) {
	source := func(deps handlerDeps) any {
		mux := http.NewServeMux()
		registerHandlers(mux, deps)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/config.json", nil))
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body["menuSource"]
	}
	if got := source(handlerDeps{}); got != "kubernetes" {
		t.Errorf("no settings: %v", got)
	}
	if got := source(handlerDeps{settings: &settings{}}); got != "folder" {
		t.Errorf("settings: %v", got)
	}
}

func withIdentity(t *testing.T, r *http.Request) *http.Request {
	t.Helper()
	r.Header.Set(identityHeader, identityFor(t, "demo:alice", "Alice", "room-pass"))
	return r
}

func folderStorefront(t *testing.T) *http.ServeMux {
	t.Helper()
	dir := t.TempDir()
	writeSettingsFile(t, dir, "coffeeconfig.yaml", testMenu)
	loaded, err := loadSettings(folderConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	// No serviceAccount: a read would fail with "no Kubernetes client".
	mux := http.NewServeMux()
	registerParticipantStorefrontHandlers(mux, handlerDeps{
		cfg: folderConfig(dir), settings: loaded,
		vouchers: newVoucherLedger(), orders: newOrderLog(),
	})
	return mux
}

func TestStorefrontFromAFolder(t *testing.T) {
	mux := folderStorefront(t)
	req := withIdentity(t, httptest.NewRequest(http.MethodGet, "/public/storefront", nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	var body storefrontResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Shop.Name != "From a folder" || len(body.Products) != 1 {
		t.Fatalf("body = %+v", body)
	}
	if body.Revision == "" {
		t.Fatal("the answer must say which menu it is")
	}
}

// The folder changes where the menu comes from, and nothing about who may ask.
func TestAFolderStillRequiresAnIdentity(t *testing.T) {
	mux := folderStorefront(t)
	for _, r := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/public/storefront", nil),
		httptest.NewRequest(http.MethodPost, "/public/orders", strings.NewReader(`{}`)),
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: got %d, want 401", r.Method, r.URL.Path, rec.Code)
		}
	}
}

func TestStorefrontFromKubernetesHasNoRevision(t *testing.T) {
	if strings.Contains(mustJSON(t, storefrontResponse{}), "revision") {
		t.Fatal("an empty revision must not be in the answer")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestSecretValueCannotLeakByAccident(t *testing.T) {
	v := secretValue("hunter2")
	values := map[string]secretValue{"apiKey": v}
	state := keyState{Name: "coffee-mail", Values: values}
	for name, got := range map[string]string{
		"%s":   fmt.Sprintf("%s", v), //nolint:staticcheck // the verb is what is under test
		"%v":   fmt.Sprintf("%v", v),
		"%#v":  fmt.Sprintf("%#v", v),
		"map":  fmt.Sprintf("%v", values),
		"#map": fmt.Sprintf("%#v", values),
		"%+v":  fmt.Sprintf("%+v", state),
		"json": mustJSON(t, state),
	} {
		if strings.Contains(got, "hunter2") {
			t.Errorf("%s leaks it: %s", name, got)
		}
	}
	if v.reveal() != "hunter2" {
		t.Fatal("reveal must be the way out")
	}
}

// The real binary, on files it encrypted itself with a key made for this test,
// so neither a key nor a fixture is committed.
type sopsFixture struct {
	dir      string
	identity string
}

func newSopsFixture(t *testing.T) sopsFixture {
	t.Helper()
	for _, tool := range []string{"sops", "age-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	out, err := exec.Command("age-keygen").Output()
	if err != nil {
		t.Fatal(err)
	}
	var identity, recipient string
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "# public key: "); ok {
			recipient = value
		}
		if strings.HasPrefix(line, "AGE-SECRET-KEY-") {
			identity = line
		}
	}
	dir := t.TempDir()
	writeSettingsFile(t, dir, "coffeeconfig.yaml", testMenu)
	fixture := sopsFixture{dir: dir, identity: identity}
	fixture.seal(t, recipient, "voter-coffee-mail.sops.yaml", "coffee-mail", "mail-made-up-value")
	return fixture
}

func (f sopsFixture) seal(t *testing.T, recipient, file, name, value string) {
	t.Helper()
	path := writeSettingsFile(t, f.dir, file, fmt.Sprintf(
		"apiVersion: v1\nkind: Secret\nmetadata:\n  name: %s\ndata:\n  apiKey: %s\ntype: Opaque\n",
		name, base64.StdEncoding.EncodeToString([]byte(value))))
	cmd := exec.Command("sops", "--encrypt", "--in-place", "--age", recipient, "--encrypted-regex", "^(data|stringData)$", path)
	cmd.Env = []string{}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sops --encrypt: %v: %s", err, out)
	}
}

// fakeOpenBao answers as OpenBao does, after refusing the first refuseLogins
// logins.
func fakeOpenBao(t *testing.T, identity string, refuseLogins int32) *httptest.Server {
	t.Helper()
	var logins atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/kubernetes/login":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if logins.Add(1) <= refuseLogins || body["role"] != "test-role" || body["jwt"] != "projected-token" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = fmt.Fprint(w, `{"auth":{"client_token":"bao-token"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/secret/data/test/age" && r.Header.Get("X-Vault-Token") == "bao-token":
			_, _ = fmt.Fprintf(w, `{"data":{"data":{"identity":%q}}}`, identity)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func (f sopsFixture) config(t *testing.T, baoAddr string) config {
	t.Helper()
	cfg := folderConfig(f.dir)
	cfg.SettingsKeys = settingsKeysOpenBaoAge
	cfg.OpenBaoAddr = baoAddr
	cfg.OpenBaoRole = "test-role"
	cfg.OpenBaoAgeSecretPath = "secret/data/test/age"
	cfg.OpenBaoTokenFile = writeSettingsFile(t, t.TempDir(), "token", "projected-token\n")
	return cfg
}

func runKeysUntil(t *testing.T, keys *settingsKeys, done func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go keys.run(ctx)
	for !done() {
		if ctx.Err() != nil {
			t.Fatalf("timed out: %+v", keys.snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestKeysOpenThroughOpenBao(t *testing.T) {
	f := newSopsFixture(t)
	// A key in Voter's own environment must not be what opens the file: it is the
	// right key here, and OpenBao is the only way it may arrive.
	t.Setenv("SOPS_AGE_KEY", f.identity)
	loaded, err := loadSettings(f.config(t, fakeOpenBao(t, f.identity, 0).URL))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.keys.openPending(context.Background()) != true {
		t.Fatalf("not opened: %+v", loaded.keys.snapshot())
	}
	states := loaded.keys.snapshot()
	// Found by metadata.name although the file is voter-coffee-mail.sops.yaml.
	if len(states) != 1 || states[0].Name != "coffee-mail" {
		t.Fatalf("states = %+v", states)
	}
	if got := states[0].Values["apiKey"].reveal(); got != "mail-made-up-value" {
		t.Fatalf("apiKey = %q", got)
	}
}

func TestTheChildInheritsNothing(t *testing.T) {
	f := newSopsFixture(t)
	t.Setenv("SOPS_AGE_KEY", f.identity)
	path := filepath.Join(f.dir, "voter-coffee-mail.sops.yaml")
	if _, err := openSopsFile(context.Background(), path, nil); err == nil {
		t.Fatal("with no credentials it must refuse, whatever Voter's own environment holds")
	}
}

func TestAKeyThatWillNotOpenStillSells(t *testing.T) {
	f := newSopsFixture(t)
	// A real age key, but not the one the file was sealed to.
	out, err := exec.Command("age-keygen").Output()
	if err != nil {
		t.Fatal(err)
	}
	wrong := ""
	for line := range strings.Lines(string(out)) {
		if strings.HasPrefix(line, "AGE-SECRET-KEY-") {
			wrong = strings.TrimSpace(line)
		}
	}
	loaded, err := loadSettings(f.config(t, fakeOpenBao(t, wrong, 0).URL))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.keys.openPending(context.Background()) {
		t.Fatal("the wrong key must not open it")
	}
	state := loaded.keys.snapshot()[0]
	if state.Failure == "" || state.opened() {
		t.Fatalf("state = %+v", state)
	}
	if loaded.menu.Spec.ShopName == "" {
		t.Fatal("the menu must be there regardless")
	}
}

func TestKeysAreRetriedWhenOpenBaoRefuses(t *testing.T) {
	f := newSopsFixture(t)
	loaded, err := loadSettings(f.config(t, fakeOpenBao(t, f.identity, 2).URL))
	if err != nil {
		t.Fatal(err)
	}
	loaded.keys.retryFirst = time.Millisecond
	runKeysUntil(t, loaded.keys, func() bool { return loaded.keys.snapshot()[0].opened() })
	if failure := loaded.keys.snapshot()[0].Failure; failure != "" {
		t.Fatalf("a key that opened still says %q", failure)
	}
}

func TestDontDoThisIsOffByDefault(t *testing.T) {
	tests := map[string]handlerDeps{
		"no settings":       {},
		"switch off":        {settings: &settings{keys: newSettingsKeys(nil, nil)}},
		"switch on, no key": {cfg: config{DontDoThis: true}, settings: &settings{}},
	}
	for name, deps := range tests {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			registerHandlers(mux, deps)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dont-do-this", nil))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("got %d, want 404", rec.Code)
			}
		})
	}
}

func dontDoThisMux(t *testing.T, loaded *settings) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	registerHandlers(mux, handlerDeps{cfg: config{DontDoThis: true}, settings: loaded})
	return mux
}

func TestDontDoThisEscapesWhatSomebodyTyped(t *testing.T) {
	dir := t.TempDir()
	path := writeSettingsFile(t, dir, "coffee-mail.sops.yaml", "data: {apiKey: 'ENC[AES256_GCM,data:abc]'}\n")
	keys := newSettingsKeys(map[string]string{"coffee-mail": path}, nil)
	keys.record(keyState{
		Name: "coffee-mail", Path: path, OpenedAt: time.Now(),
		Values: map[string]secretValue{"apiKey": "<script>alert(1)</script>"},
	})
	rec := httptest.NewRecorder()
	dontDoThisMux(t, &settings{keys: keys, reference: "cr.example/settings@sha256:abc"}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dont-do-this", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script>") {
		t.Fatalf("not escaped: %s", body)
	}
	for _, want := range []string{"&lt;script&gt;", "ENC[AES256_GCM,data:abc]", "cr.example/settings@sha256:abc"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Errorf("X-Robots-Tag = %q", got)
	}
}

func TestDontDoThisChecksWhatItSays(t *testing.T) {
	dir := t.TempDir()
	path := writeSettingsFile(t, dir, "a.yaml", "data: {apiKey: 'ENC[AES256_GCM,data:abc]'}\n")
	keys := newSettingsKeys(map[string]string{"a": path}, nil)
	keys.record(keyState{Name: "a", Path: path, Values: map[string]secretValue{"apiKey": "made-up-value"}})
	loaded := &settings{keys: keys}
	missing := filepath.Join(dir, "no-token")

	clean := buildDontDoThisPage(loaded, []string{"PATH=/bin"}, missing)
	if !strings.HasPrefix(clean.Disk, "The folder holds ciphertext only") ||
		!strings.HasPrefix(clean.Environment, "Not in") ||
		!strings.HasPrefix(clean.Kubernetes, "No ServiceAccount") {
		t.Fatalf("a clean pod read as %+v", clean)
	}

	bad := buildDontDoThisPage(loaded, []string{"X=made-up-value"}, path)
	if !strings.HasPrefix(bad.Environment, "FOUND") || !strings.HasPrefix(bad.Kubernetes, "This pod HAS") {
		t.Fatalf("a bad pod read as %+v", bad)
	}

	writeSettingsFile(t, dir, "a.yaml", "data: {apiKey: bWFkZS11cA==}\n")
	if plain := buildDontDoThisPage(loaded, nil, missing); !strings.Contains(plain.Disk, "NOT encrypted") {
		t.Fatalf("a plain file read as %q", plain.Disk)
	}
}

func TestDontDoThisShowsAKeyThatDidNotOpen(t *testing.T) {
	keys := newSettingsKeys(map[string]string{"a": "/nowhere"}, nil)
	keys.record(keyState{Name: "a", Path: "/nowhere", Failure: "OpenBao did not log this pod in"})
	rec := httptest.NewRecorder()
	dontDoThisMux(t, &settings{keys: keys}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dont-do-this", nil))
	if !strings.Contains(rec.Body.String(), "unavailable: OpenBao did not log this pod in") {
		t.Fatalf("body = %s", rec.Body)
	}
}

// A value appears on that one page, and in no other answer of the application.
func TestAKeyAppearsNowhereElse(t *testing.T) {
	const value = "only-on-one-page"
	keys := newSettingsKeys(map[string]string{"a": "/nowhere"}, nil)
	keys.record(keyState{Name: "a", Path: "/nowhere", Values: map[string]secretValue{"apiKey": value}})
	dir := t.TempDir()
	writeSettingsFile(t, dir, "coffeeconfig.yaml", testMenu)
	loaded, err := loadSettings(folderConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	loaded.keys = keys
	deps := handlerDeps{
		cfg:      config{DontDoThis: true, CoffeeConfigName: "demo-coffee", ShopOnly: true},
		settings: loaded, vouchers: newVoucherLedger(), orders: newOrderLog(),
	}
	mux := http.NewServeMux()
	registerParticipantStorefrontHandlers(mux, deps)
	registerParticipantOrderFeedHandlers(mux, deps)
	registerHandlers(mux, deps)

	for _, path := range []string{"/config.json", "/revision.json", "/healthz", "/public/build-info", "/public/storefront", "/public/vouchers", "/public/orders"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, withIdentity(t, httptest.NewRequest(http.MethodGet, path, nil)))
		if strings.Contains(rec.Body.String(), value) {
			t.Errorf("%s shows the value", path)
		}
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dont-do-this", nil))
	if !strings.Contains(rec.Body.String(), value) {
		t.Fatal("the page itself must show it, or this test proves nothing")
	}
}

func TestServeFinishesTheRequestInFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			_, _ = w.Write([]byte("placed"))
		}),
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, listener) }()

	answered := make(chan string, 1)
	go func() {
		res, err := http.Get("http://" + listener.Addr().String() + "/")
		if err != nil {
			answered <- "error: " + err.Error()
			return
		}
		defer func() { _ = res.Body.Close() }()
		buf := make([]byte, 16)
		n, _ := res.Body.Read(buf)
		answered <- string(buf[:n])
	}()

	<-started
	stop() // SIGTERM, with an order on the way through.
	select {
	case err := <-served:
		t.Fatalf("serve returned with a request in flight: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if got := <-answered; got != "placed" {
		t.Fatalf("the request got %q", got)
	}
	if err := <-served; err != nil {
		t.Fatalf("serve = %v", err)
	}
}
