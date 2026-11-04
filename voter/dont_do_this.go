package main

// GET /dont-do-this: a page that shows the opened keys, and where they are.
//
// A workload would normally never do this. It exists only behind DONT_DO_THIS,
// to make a point on stage about values that are made up and already public, and
// it must come out of the Deployment before a real key ever goes into the folder.
//
// It is public, like /decrypted/ on coffee-settings: the same values from the
// same image are readable there, so a login would protect nothing. The values
// are whatever somebody typed on /room and this page shares an origin with the
// login session, which is why it is html/template and nothing else.

import (
	"bytes"
	"html/template"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

const (
	serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

	// A shorter value would match by accident somewhere in the environment, so
	// it only counts as found when a variable holds exactly that.
	environmentScanMinLen = 8

	ciphertextShownLen = 32
)

var dontDoThisTemplate = template.Must(template.New("dont-do-this").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Don't do this</title>
<style>
:root { color-scheme: light dark; }
body { font: 1rem/1.5 system-ui, sans-serif; margin: 0 auto; max-width: 46rem; padding: 1rem; }
code, dd { font-family: ui-monospace, monospace; overflow-wrap: anywhere; }
dt { font-weight: 600; margin-top: 1rem; }
dd { margin: 0; }
.quiet { opacity: .7; }
</style>
</head>
<body>
<h1>Don't do this.</h1>
<p>Normally this page would not exist. A workload opens a key, uses it, and never shows it to anybody.</p>
<p>These values are made up, and they are already public on coffee-settings.k8s.koudijs.dev/decrypted/.</p>

<h2>The keys</h2>
<dl>
{{- range .Keys}}
<dt>{{.Name}}</dt>
{{- if .Failure}}
<dd>unavailable: {{.Failure}}</dd>
{{- else if not .Opened}}
<dd>opening…</dd>
{{- end}}
{{- range .Values}}
<dd>{{.Name}} = {{.Value}}</dd>
{{- end}}
<dd class="quiet">on disk: {{.Path}}</dd>
<dd class="quiet">{{.Ciphertext}}</dd>
{{- else}}
<dd>There are no encrypted Secrets in this folder.</dd>
{{- end}}
</dl>

<h2>Where these values are, checked just now by this process</h2>
<dl>
<dt>Disk</dt><dd>{{.Disk}}</dd>
<dt>Environment</dt><dd>{{.Environment}}</dd>
<dt>Kubernetes</dt><dd>{{.Kubernetes}}</dd>
<dt>This process</dt><dd>Here, and only here. Gone when the pod goes.</dd>
<dt>The age key</dt><dd>Fetched from OpenBao, handed to sops, not kept.</dd>
</dl>

{{- if .Opened}}
<p class="quiet">Opened {{.Opened}}{{if .Reference}} from {{.Reference}}{{end}}</p>
{{- else if .Reference}}
<p class="quiet">From {{.Reference}}</p>
{{- end}}
</body>
</html>
`))

type dontDoThisValue struct {
	Name  string
	Value string
}

type dontDoThisKey struct {
	Name       string
	Path       string
	Ciphertext string
	Failure    string
	Opened     bool
	Values     []dontDoThisValue
}

type dontDoThisPage struct {
	Keys        []dontDoThisKey
	Disk        string
	Environment string
	Kubernetes  string
	Opened      string
	Reference   string
}

func registerDontDoThis(mux *http.ServeMux, deps handlerDeps) {
	// Not left to the SPA fallback, which would answer with a 200: where this
	// page is off, it does not exist.
	if !deps.cfg.DontDoThis || deps.settings == nil || deps.settings.keys == nil {
		mux.Handle("/dont-do-this", http.NotFoundHandler())
		return
	}
	mux.HandleFunc("/dont-do-this", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		page := buildDontDoThisPage(deps.settings, os.Environ(), serviceAccountTokenPath)
		var body bytes.Buffer
		if err := dontDoThisTemplate.Execute(&body, page); err != nil {
			log.Printf("dont-do-this: %v", err)
			http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
			return
		}
		noStore(w)
		w.Header().Set("X-Robots-Tag", "noindex")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body.Bytes())
	})
}

// buildDontDoThisPage does the checks at render time, so what the page says is
// what is true now and not what the code intends.
func buildDontDoThisPage(s *settings, environ []string, tokenPath string) dontDoThisPage {
	page := dontDoThisPage{Reference: s.reference}
	var latest time.Time
	var plain []string
	diskClean, diskRead := true, false

	for _, state := range s.keys.snapshot() {
		key := dontDoThisKey{Name: state.Name, Path: state.Path, Failure: state.Failure, Opened: state.opened()}
		for name, value := range state.Values {
			key.Values = append(key.Values, dontDoThisValue{Name: name, Value: value.reveal()})
			plain = append(plain, value.reveal())
		}
		sort.Slice(key.Values, func(i, j int) bool { return key.Values[i].Name < key.Values[j].Name })
		if state.OpenedAt.After(latest) {
			latest = state.OpenedAt
		}
		ciphertext, encrypted, err := readCiphertext(state.Path)
		switch {
		case err != nil:
			key.Ciphertext = "could not be read just now"
		case !encrypted:
			key.Ciphertext = "NOT ENCRYPTED"
			diskClean, diskRead = false, true
		default:
			key.Ciphertext = ciphertext
			diskRead = true
		}
		page.Keys = append(page.Keys, key)
	}

	switch {
	case !diskRead:
		page.Disk = "Nothing could be read just now."
	case diskClean:
		page.Disk = "The folder holds ciphertext only (read just now, above)."
	default:
		page.Disk = "A file in the folder is NOT encrypted."
	}

	page.Environment = "Not in this process's environment."
	if inEnvironment(environ, plain) {
		page.Environment = "FOUND in this process's environment."
	}

	page.Kubernetes = "No ServiceAccount token in this pod, so it could not read a Secret anyway."
	if _, err := os.Stat(tokenPath); err == nil {
		page.Kubernetes = "This pod HAS a ServiceAccount token."
	}

	if !latest.IsZero() {
		page.Opened = latest.Format("15:04:05")
	}
	return page
}

// readCiphertext reads an encrypted file again and returns the start of its
// first value, and whether every value in data is ciphertext.
func readCiphertext(path string) (string, bool, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- from the configured folder
	if err != nil {
		return "", false, err
	}
	var file struct {
		Data map[string]string `json:"data"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return "", false, err
	}
	names := make([]string, 0, len(file.Data))
	for name := range file.Data {
		names = append(names, name)
	}
	sort.Strings(names)
	shown := ""
	for _, name := range names {
		value := file.Data[name]
		if !strings.HasPrefix(value, "ENC[") {
			return "", false, nil
		}
		if shown == "" {
			shown = value
		}
	}
	if len(shown) > ciphertextShownLen {
		shown = shown[:ciphertextShownLen] + "…"
	}
	return shown, true, nil
}

func inEnvironment(environ, values []string) bool {
	for _, variable := range environ {
		_, content, _ := strings.Cut(variable, "=")
		for _, value := range values {
			if value == "" {
				continue
			}
			if content == value || (len(value) >= environmentScanMinLen && strings.Contains(content, value)) {
				return true
			}
		}
	}
	return false
}
