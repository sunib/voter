// voteload drives the REAL participant path at room scale: Dex login through
// the Room Pass connector, then a ballot cast with that participant's own
// token. No browser, no shortcut, no seeded session -- every user in this
// harness enrols and votes exactly the way a phone in the room does, which is
// the only way the numbers mean anything.
//
// The chain per user:
//
//	GET  {app}/auth/login          -> Dex -> room-pass connector -> /join form
//	POST {issuer}/join             -> Dex -> {app}/auth/callback -> session cookie
//	GET  {app}/auth/session        -> csrfToken
//	GET  {app}/public/rounds/{r}   -> the round's resourceVersion
//	POST {app}/public/rounds/{r}   -> 201, a QuizSubmission created AS THAT PERSON
//
// The join code rotates (~15s), so it is re-read from Room status in the
// background rather than captured once at start -- a 60s run outlives several
// codes and a stale one is refused by design.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	app       = flag.String("app", "https://demo.koudijs.dev", "Voter base URL")
	round     = flag.String("round", "", "QuizSession name to vote in (required unless -vote=false)")
	users     = flag.Int("users", 200, "number of distinct participants")
	window    = flag.Duration("window", time.Minute, "spread user starts evenly across this window")
	vote      = flag.Bool("vote", true, "cast a ballot after logging in")
	prefix    = flag.String("prefix", "", "display-name prefix; defaults to Load<unix>, used for cleanup")
	timeout   = flag.Duration("timeout", 3*time.Minute, "overall deadline")
	perUser   = flag.Duration("per-user-timeout", 90*time.Second, "deadline for one participant's whole chain")
	kubeflags = flag.String("kubeconfig", "", "kubeconfig for reading the rotating join code")
	roomNS    = flag.String("room-namespace", "voter", "namespace holding the Room")
	roomName  = flag.String("room", "demo", "Room whose join code to read")
	insecure  = flag.Bool("insecure", false, "skip TLS verification (local fixtures only)")
	verbose   = flag.Bool("v", false, "log every failure as it happens")
)

// Hidden inputs on the join form: csrf, handoff, return. Scraped rather than
// guessed, because a handoff is single-use and bound to this browser.
var hiddenField = regexp.MustCompile(`<input type="hidden" name="([^"]+)" value="([^"]*)"`)
var formAction = regexp.MustCompile(`<form method="post" action="([^"]+)"`)

type outcome struct {
	user      int
	phase     string // where it stopped: "" means it completed
	err       string
	login     time.Duration
	ballot    time.Duration
	total     time.Duration
	votedAt   time.Time
	startedAt time.Time
	status    int
}

func main() {
	flag.Parse()
	if *vote && *round == "" {
		fmt.Fprintln(os.Stderr, "-round is required unless -vote=false")
		os.Exit(2)
	}
	if *prefix == "" {
		*prefix = fmt.Sprintf("Load%d", time.Now().Unix())
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// One shared transport: connection reuse is what a real room does NOT get
	// (250 separate phones), so this is deliberately generous and the numbers
	// below are therefore an optimistic floor on the server side, not a
	// simulation of 250 distinct TCP stacks.
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: *insecure, MinVersion: tls.VersionTLS12},
		MaxIdleConns:        512,
		MaxIdleConnsPerHost: 512,
		MaxConnsPerHost:     0,
	}
	defer tr.CloseIdleConnections()

	code := startCodeRefresher(ctx)
	if err := waitForCode(ctx, code); err != nil {
		fmt.Fprintln(os.Stderr, "could not read a join code:", err)
		os.Exit(1)
	}
	fmt.Printf("target      %s\nround       %s\nusers       %d over %s\nprefix      %s (display names %s-001 ...)\n\n",
		*app, *round, *users, *window, *prefix, *prefix)

	results := make([]outcome, *users)
	var wg sync.WaitGroup
	gap := time.Duration(0)
	if *users > 1 {
		gap = *window / time.Duration(*users)
	}
	begin := time.Now()
	for i := 0; i < *users; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case <-time.After(time.Duration(i) * gap):
			case <-ctx.Done():
				results[i] = outcome{user: i, phase: "never-started", err: "deadline before start"}
				return
			}
			uctx, ucancel := context.WithTimeout(ctx, *perUser)
			defer ucancel()
			results[i] = run(uctx, tr, i, code)
			if *verbose && results[i].phase != "" {
				fmt.Printf("  user %3d failed at %-14s %s\n", i+1, results[i].phase, results[i].err)
			}
		}(i)
	}
	wg.Wait()
	report(results, begin)
}

// run is one participant, start to finish.
func run(ctx context.Context, tr http.RoundTripper, i int, code *atomic.Value) (o outcome) {
	o = outcome{user: i, startedAt: time.Now()}
	name := fmt.Sprintf("%s %03d", *prefix, i+1)
	defer func() { o.total = time.Since(o.startedAt) }()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Transport: tr,
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 20 {
				return fmt.Errorf("stopped after 20 redirects")
			}
			return nil
		},
	}

	// 1. Start the OIDC flow. OIDC_CONNECTOR_ID=room-pass means Dex skips its
	//    chooser and this lands directly on the join form.
	loginStart := time.Now()
	body, finalURL, status, err := get(ctx, client, *app+"/auth/login")
	if err != nil {
		o.phase, o.err = "get-join-form", err.Error()
		return o
	}
	if status != 200 || !strings.Contains(body, `name="code"`) {
		o.phase, o.status = "get-join-form", status
		o.err = fmt.Sprintf("no join form at %s (status %d)", finalURL, status)
		return o
	}

	// 2. Submit the room code and a display name. The hidden handoff is
	//    single-use and bound to this cookie jar, so it must come from THIS
	//    response, not from a template.
	form := url.Values{"code": {code.Load().(string)}, "name": {name}}
	for _, m := range hiddenField.FindAllStringSubmatch(body, -1) {
		form.Set(m[1], html.UnescapeString(m[2]))
	}
	action := "/join"
	if m := formAction.FindStringSubmatch(body); m != nil {
		action = html.UnescapeString(m[1])
	}
	postURL, err := finalURL.Parse(action)
	if err != nil {
		o.phase, o.err = "resolve-form-action", err.Error()
		return o
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", postURL.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", postURL.Scheme+"://"+postURL.Host)
	resp, err := client.Do(req)
	if err != nil {
		o.phase, o.err = "post-join", err.Error()
		return o
	}
	joinBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode != 200 {
		o.phase, o.status = "post-join", resp.StatusCode
		o.err = fmt.Sprintf("landed on %s: %s", resp.Request.URL, firstLine(string(joinBody)))
		return o
	}

	// 3. The session the browser would now hold. csrfToken is required on the
	//    ballot, and canVote is the app's own connector check -- a login that
	//    did not come through Room Pass is refused later with a 403, so catch
	//    it here where the message is legible.
	var sess struct {
		Authenticated bool   `json:"authenticated"`
		CSRF          string `json:"csrfToken"`
		DisplayName   string `json:"displayName"`
		Username      string `json:"username"`
		CanVote       bool   `json:"canVote"`
	}
	if err := getJSON(ctx, client, *app+"/auth/session", &sess); err != nil {
		o.phase, o.err = "auth-session", err.Error()
		return o
	}
	if !sess.Authenticated {
		o.phase, o.err = "auth-session", "session not authenticated after join"
		return o
	}
	o.login = time.Since(loginStart)
	if !*vote {
		return o
	}
	if !sess.CanVote {
		o.phase, o.err = "auth-session", "canVote=false (wrong connector)"
		return o
	}

	// 4. The round, for its resourceVersion. The handler refuses a ballot whose
	//    resourceVersion does not match, so this cannot be cached across users.
	var rd struct {
		Round struct {
			Metadata struct {
				ResourceVersion string `json:"resourceVersion"`
			} `json:"metadata"`
		} `json:"round"`
		Voted bool `json:"voted"`
	}
	if err := getJSON(ctx, client, *app+"/public/rounds/"+*round, &rd); err != nil {
		o.phase, o.err = "get-round", err.Error()
		return o
	}

	// 5. The ballot. This is the write that becomes a QuizSubmission created
	//    with this participant's OWN token, which is what the audit webhook
	//    attributes and the reverser commits.
	ballotStart := time.Now()
	payload, _ := json.Marshal(map[string]any{
		"resourceVersion": rd.Round.Metadata.ResourceVersion,
		"answers": []any{
			map[string]any{"questionId": "approach", "singleChoice": choices[i%len(choices)]},
			map[string]any{"questionId": "feedback", "freeText": fmt.Sprintf("Load rehearsal ballot %d.", i+1)},
		},
	})
	vreq, _ := http.NewRequestWithContext(ctx, "POST", *app+"/public/rounds/"+*round, bytes.NewReader(payload))
	vreq.Header.Set("Content-Type", "application/json")
	vreq.Header.Set("X-CSRF-Token", sess.CSRF)
	vreq.Header.Set("Origin", *app)
	vresp, err := client.Do(vreq)
	if err != nil {
		o.phase, o.err = "post-vote", err.Error()
		return o
	}
	vbody, _ := io.ReadAll(io.LimitReader(vresp.Body, 16<<10))
	vresp.Body.Close()
	o.ballot = time.Since(ballotStart)
	o.status = vresp.StatusCode
	if vresp.StatusCode != 201 {
		o.phase, o.err = "post-vote", fmt.Sprintf("%d %s", vresp.StatusCode, firstLine(string(vbody)))
		return o
	}
	o.votedAt = time.Now()
	return o
}

// Spread across the round's four choices so the results screen looks like a
// room rather than a bot.
var choices = []string{"GitOps", "kubectl or a cluster UI", "A mix of both", "I am still exploring"}

func get(ctx context.Context, c *http.Client, u string) (string, *url.URL, int, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, err := c.Do(req)
	if err != nil {
		return "", nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	return string(b), resp.Request.URL, resp.StatusCode, nil
}

func getJSON(ctx context.Context, c *http.Client, u string, into any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("%d %s", resp.StatusCode, firstLine(string(b)))
	}
	return json.Unmarshal(b, into)
}

// startCodeRefresher keeps the rotating join code fresh for the whole run.
func startCodeRefresher(ctx context.Context) *atomic.Value {
	v := &atomic.Value{}
	read := func() {
		args := []string{}
		if *kubeflags != "" {
			args = append(args, "--kubeconfig", *kubeflags)
		}
		args = append(args, "-n", *roomNS, "get", "room", *roomName, "-o", "jsonpath={.status.joinCode.code}")
		out, err := exec.CommandContext(ctx, "kubectl", args...).Output()
		if c := strings.TrimSpace(string(out)); err == nil && c != "" {
			v.Store(c)
		}
	}
	read()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				read()
			}
		}
	}()
	return v
}

func waitForCode(ctx context.Context, v *atomic.Value) error {
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		if _, ok := v.Load().(string); ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("Room %s/%s has no status.joinCode", *roomNS, *roomName)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

func report(rs []outcome, begin time.Time) {
	var ok []outcome
	byPhase := map[string]int{}
	samples := map[string]string{}
	for _, r := range rs {
		if r.phase == "" {
			ok = append(ok, r)
			continue
		}
		byPhase[r.phase]++
		if _, seen := samples[r.phase]; !seen {
			samples[r.phase] = r.err
		}
	}
	fmt.Printf("\n=== %d/%d completed the full chain ===\n", len(ok), len(rs))
	if len(byPhase) > 0 {
		fmt.Println("\nfailures by phase:")
		phases := []string{}
		for p := range byPhase {
			phases = append(phases, p)
		}
		sort.Strings(phases)
		for _, p := range phases {
			fmt.Printf("  %-18s %4d   e.g. %s\n", p, byPhase[p], samples[p])
		}
	}
	if len(ok) == 0 {
		return
	}
	pct := func(ds []time.Duration, p float64) time.Duration {
		if len(ds) == 0 {
			return 0
		}
		i := int(p * float64(len(ds)-1))
		return ds[i].Round(time.Millisecond)
	}
	collect := func(f func(outcome) time.Duration) []time.Duration {
		ds := make([]time.Duration, 0, len(ok))
		for _, r := range ok {
			ds = append(ds, f(r))
		}
		sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
		return ds
	}
	login := collect(func(o outcome) time.Duration { return o.login })
	ballot := collect(func(o outcome) time.Duration { return o.ballot })
	total := collect(func(o outcome) time.Duration { return o.total })
	fmt.Printf("\n%-10s %10s %10s %10s %10s\n", "phase", "p50", "p95", "p99", "max")
	for _, row := range []struct {
		name string
		ds   []time.Duration
	}{{"login", login}, {"ballot", ballot}, {"end-to-end", total}} {
		fmt.Printf("%-10s %10s %10s %10s %10s\n", row.name,
			pct(row.ds, 0.50), pct(row.ds, 0.95), pct(row.ds, 0.99), pct(row.ds, 1.0))
	}
	// The question the talk actually asks: how long until the whole room has
	// voted, measured from the first request to the last accepted ballot.
	var first, last time.Time
	for _, r := range ok {
		if r.votedAt.IsZero() {
			continue
		}
		if first.IsZero() || r.votedAt.Before(first) {
			first = r.votedAt
		}
		if r.votedAt.After(last) {
			last = r.votedAt
		}
	}
	if !last.IsZero() {
		fmt.Printf("\nfirst ballot accepted  %s after start\n", first.Sub(begin).Round(time.Millisecond))
		fmt.Printf("last  ballot accepted  %s after start\n", last.Sub(begin).Round(time.Millisecond))
	}
}
