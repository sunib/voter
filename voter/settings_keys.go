package main

// Opening the encrypted Secrets in SETTINGS_DIR.
//
// Voter owns none of the SOPS format: the sops binary does the MAC, the data key
// and the refusals. That is what makes the later step small, where OpenBao does
// the decrypting: sops speaks to it natively, so it is one more case in
// newSopsCredentials and no other code changes.
//
// A key is never read from Voter's own environment or from a mounted file, and
// there is deliberately no mode for it. The credential is fetched, handed to one
// child process and not kept, and the child inherits nothing else.
//
// This runs beside serving and never holds up readiness: a key that will not
// open must not stop anybody buying a coffee.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"sigs.k8s.io/yaml"
)

const (
	settingsKeysOpenBaoAge = "openbao-age"

	sopsTimeout       = 30 * time.Second
	keysRetryFirst    = 5 * time.Second
	keysRetryLongest  = 5 * time.Minute
	openBaoTimeout    = 10 * time.Second
	openBaoMaxBody    = 1 << 20
	sopsFailureMaxLen = 200
)

// secretValue is an opened value, in a type that cannot leak by accident: a log
// line or a JSON body says [redacted]. reveal is the one way out, so grep finds
// every place that shows one.
type secretValue string

const redacted = "[redacted]"

func (secretValue) String() string               { return redacted }
func (secretValue) GoString() string             { return redacted }
func (secretValue) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (v secretValue) reveal() string             { return string(v) }

// sopsCredentials returns the environment for a sops child, and all of it.
type sopsCredentials func(ctx context.Context) (env []string, err error)

// newSopsCredentials is the one place that says how sops gets its key. Nil
// credentials mean nothing is opened.
func newSopsCredentials(cfg config) (sopsCredentials, error) {
	switch cfg.SettingsKeys {
	case "":
		return nil, nil
	case settingsKeysOpenBaoAge:
		return newOpenBaoAgeCredentials(cfg), nil
	}
	return nil, fmt.Errorf("unknown SETTINGS_KEYS %q", cfg.SettingsKeys)
}

// newOpenBaoAgeCredentials logs in to OpenBao with the pod's projected token and
// reads the age key: two calls, no SDK. Each call to it logs in again, because
// the token it would otherwise keep is itself a credential. An answer's body is
// never put in an error, since one of them holds the key.
func newOpenBaoAgeCredentials(cfg config) sopsCredentials {
	client := &http.Client{Timeout: openBaoTimeout}
	addr := strings.TrimRight(cfg.OpenBaoAddr, "/")
	secretPath := strings.TrimLeft(cfg.OpenBaoAgeSecretPath, "/")

	return func(ctx context.Context) ([]string, error) {
		jwt, err := os.ReadFile(cfg.OpenBaoTokenFile)
		if err != nil {
			return nil, fmt.Errorf("the projected token: %w", err)
		}
		loginBody, err := json.Marshal(map[string]string{
			"role": cfg.OpenBaoRole,
			"jwt":  strings.TrimSpace(string(jwt)),
		})
		if err != nil {
			return nil, err
		}
		var login struct {
			Auth struct {
				ClientToken string `json:"client_token"`
			} `json:"auth"`
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr+"/v1/auth/kubernetes/login", bytes.NewReader(loginBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if err := openBaoJSON(client, req, &login); err != nil {
			return nil, fmt.Errorf("OpenBao did not log this pod in: %w", err)
		}

		var kv struct {
			Data struct {
				Data struct {
					Identity string `json:"identity"`
				} `json:"data"`
			} `json:"data"`
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, addr+"/v1/"+secretPath, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Vault-Token", login.Auth.ClientToken)
		if err := openBaoJSON(client, req, &kv); err != nil {
			return nil, fmt.Errorf("logged in, but could not read the key: %w", err)
		}
		if !strings.HasPrefix(kv.Data.Data.Identity, "AGE-SECRET-KEY-") {
			return nil, errors.New("OpenBao's answer holds no age key")
		}
		return []string{"SOPS_AGE_KEY=" + kv.Data.Data.Identity}, nil
	}
}

func openBaoJSON(client *http.Client, req *http.Request, into any) error {
	res, err := client.Do(req) // #nosec G704 -- the address is the deployment's own configuration
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, openBaoMaxBody)).Decode(into); err != nil {
		return errors.New("the answer could not be read")
	}
	return nil
}

// openSopsFile runs sops on one file. Stdout holds the opened values and is never
// logged. Stderr goes in the error, cut to one line.
func openSopsFile(ctx context.Context, path string, env []string) (map[string]secretValue, error) {
	ctx, cancel := context.WithTimeout(ctx, sopsTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sops", "-d", path) // #nosec G204 -- a fixed binary on a file from the configured folder
	// Never nil: a nil Env is the one value that means "inherit all of mine".
	cmd.Env = append([]string{}, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New(sopsFailure(err, stderr.String()))
	}

	var secret struct {
		Data map[string]string `json:"data"`
	}
	// Not the parser's error: it quotes what it read, which is the opened file.
	if err := yaml.Unmarshal(stdout.Bytes(), &secret); err != nil {
		return nil, errors.New("sops' output is not a Secret")
	}
	values := make(map[string]secretValue, len(secret.Data))
	for name, encoded := range secret.Data {
		value, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("data.%s is not base64", name)
		}
		values[name] = secretValue(value)
	}
	return values, nil
}

func sopsFailure(err error, stderr string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(stderr), "\n")
	if line == "" {
		return err.Error()
	}
	if len(line) > sopsFailureMaxLen {
		line = line[:sopsFailureMaxLen]
	}
	return line
}

// keyState is one encrypted file: not yet opened, opened, or refused and why.
type keyState struct {
	Name     string
	Path     string
	Values   map[string]secretValue
	OpenedAt time.Time
	Failure  string
}

func (s keyState) opened() bool { return s.Values != nil }

type settingsKeys struct {
	credentials sopsCredentials
	retryFirst  time.Duration

	mu     sync.RWMutex
	states map[string]keyState
}

// newSettingsKeys takes every encrypted Secret in the folder, with no list of
// names, so a third key needs no change here.
func newSettingsKeys(files map[string]string, credentials sopsCredentials) *settingsKeys {
	states := make(map[string]keyState, len(files))
	for name, path := range files {
		states[name] = keyState{Name: name, Path: path}
	}
	return &settingsKeys{credentials: credentials, retryFirst: keysRetryFirst, states: states}
}

// run opens what has not opened, backing off, until everything has or ctx ends.
// What is open stays open.
func (k *settingsKeys) run(ctx context.Context) {
	delay := k.retryFirst
	for !k.openPending(ctx) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, keysRetryLongest)
	}
}

// openPending is one round, and reports whether everything is open. The
// credentials live for this function and no longer. Go cannot wipe a string, so
// that means nothing refers to them any more, not that the bytes are gone.
func (k *settingsKeys) openPending(ctx context.Context) bool {
	pending := k.pending()
	if len(pending) == 0 {
		return true
	}
	env, err := k.credentials(ctx)
	if err != nil {
		log.Printf("settings: no credentials for sops: %v", err)
		for _, state := range pending {
			state.Failure = err.Error()
			k.record(state)
		}
		return false
	}
	allOpen := true
	for _, state := range pending {
		values, err := openSopsFile(ctx, state.Path, env)
		if err != nil {
			log.Printf("settings: %s did not open: %v", state.Name, err)
			state.Failure = err.Error()
			allOpen = false
		} else {
			log.Printf("settings: opened %s", state.Name)
			state.Values, state.OpenedAt, state.Failure = values, time.Now(), ""
		}
		k.record(state)
	}
	return allOpen
}

func (k *settingsKeys) pending() []keyState {
	var pending []keyState
	for _, state := range k.snapshot() {
		if !state.opened() {
			pending = append(pending, state)
		}
	}
	return pending
}

func (k *settingsKeys) record(state keyState) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.states[state.Name] = state
}

// snapshot is every file, by name. A state's Values is never changed once set,
// so sharing the map is safe.
func (k *settingsKeys) snapshot() []keyState {
	k.mu.RLock()
	defer k.mu.RUnlock()
	states := make([]keyState, 0, len(k.states))
	for _, state := range k.states {
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Name < states[j].Name })
	return states
}
