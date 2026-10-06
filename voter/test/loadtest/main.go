// voteload drives the REAL participant path at room scale: krm-foyer's login
// through Dex and the Room Pass connector, a live stream of the coffee menu
// held open, and a ballot cast with that participant's own token. No browser,
// no shortcut, no seeded session -- every user in this harness enrols, watches
// and votes the way a phone in the room does, which is the only way the
// numbers mean anything.
//
// The chain per user:
//
//	GET  {app}/auth/login            -> krm-foyer -> Dex -> room-pass -> /join form
//	POST {app}/join                  -> Dex -> {app}/auth/callback -> krm-foyer's session cookie
//	GET  {app}/auth/session          -> displayName, connector, csrfToken, csrfHeader
//	GET  {app}/stream/v1?...         -> the CoffeeConfig, through krm-foyer's shared watch,
//	                                    held open until the run ends (-stream)
//	GET  {app}/k8s/.../quizsessions/{r}    -> the round's uid, questions and digest
//	POST {app}/k8s/.../quizsubmissions     -> 201, a ballot created AS THAT PERSON,
//	                                          through Voter's admission policy
//
// The join code rotates (~15s), so it is re-read from Room status in the
// background rather than captured once at start -- a 60s run outlives several
// codes and a stale one is refused by design.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"net"
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
	namespace = flag.String("namespace", "voter", "where the round, ballots and CoffeeConfig live")
	coffee    = flag.String("coffee", "demo-coffee", "the CoffeeConfig every participant streams")
	stream    = flag.Bool("stream", true, "hold a live stream of the CoffeeConfig open for the whole run")
	dial      = flag.String("dial", "", "connect every request to this address's IP instead of resolving the host (the e2e fixture's Docker gateway)")
	verbose   = flag.Bool("v", false, "log every failure as it happens")
)

// Hidden inputs on the join form: csrf, handoff, return. Scraped rather than
// guessed, because a handoff is single-use and bound to this browser.
var hiddenField = regexp.MustCompile(`<input type="hidden" name="([^"]+)" value="([^"]*)"`)
var formAction = regexp.MustCompile(`<form method="post" action="([^"]+)"`)

// runCtx is the whole run's context: the streams are held open on it, so
// they outlive each participant's own deadline and close together at the end.
var runCtx context.Context

type outcome struct {
	user      int
	phase     string // where it stopped: "" means it completed
	err       string
	login     time.Duration
	synced    time.Duration // until the stream's first snapshot arrived
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
	runCtx = ctx

	// One shared transport: connection reuse is what a real room does NOT get
	// (250 separate phones), so this is deliberately generous and the numbers
	// below are therefore an optimistic floor on the server side, not a
	// simulation of 250 distinct TCP stacks.
	tr := &http.Transport{
		DialContext:         dialer(*dial),
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
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		o.phase, o.status = "post-join", resp.StatusCode
		o.err = fmt.Sprintf("landed on %s: %s", resp.Request.URL, firstLine(string(joinBody)))
		return o
	}

	// 3. The session the browser would now hold: krm-foyer's, with the CSRF
	//    proof every write needs and the connector admission keys on.
	var sess struct {
		Authenticated bool   `json:"authenticated"`
		DisplayName   string `json:"displayName"`
		Connector     string `json:"connector"`
		CSRF          string `json:"csrfToken"`
		CSRFHeader    string `json:"csrfHeader"`
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

	// 4. The coffee menu, live, as every phone on /coffee watches it: one
	//    stream per participant, all served from krm-foyer's one shared watch.
	//    Held open in the background until the run's deadline.
	if *stream {
		syncedAt, err := openStream(ctx, client)
		if err != nil {
			o.phase, o.err = "stream", err.Error()
			return o
		}
		o.synced = syncedAt
	}
	if !*vote {
		return o
	}
	if sess.Connector != "room-pass" {
		o.phase, o.err = "auth-session", fmt.Sprintf("connector %q is not room-pass: admission would refuse the ballot", sess.Connector)
		return o
	}

	// 5. The round: its uid and questions digest are the ballot's pins, and its
	//    questions decide the answers.
	base := *app + "/k8s/apis/examples.configbutler.ai/v1alpha1/namespaces/" + *namespace
	var rd struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
		Spec struct {
			Questions []struct {
				ID      string   `json:"id"`
				Type    string   `json:"type"`
				Choices []string `json:"choices"`
				Min     *float64 `json:"min"`
			} `json:"questions"`
		} `json:"spec"`
		Status struct {
			QuestionsDigest string `json:"questionsDigest"`
		} `json:"status"`
	}
	// A round opened moments ago has no digest until Voter's reconciler
	// publishes it with the first tally; the page waits for it, and so does this.
	for {
		if err := getJSON(ctx, client, base+"/quizsessions/"+*round, &rd); err != nil {
			o.phase, o.err = "get-round", err.Error()
			return o
		}
		if rd.Status.QuestionsDigest != "" {
			break
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			o.phase, o.err = "get-round", "the round never published its questions digest"
			return o
		}
	}

	// 6. The ballot: created with this participant's OWN token through /k8s,
	//    which is what the audit webhook attributes and the reverser commits,
	//    named and labelled as admission requires.
	answers := make([]any, 0, len(rd.Spec.Questions))
	for _, q := range rd.Spec.Questions {
		a := map[string]any{"questionId": q.ID}
		switch q.Type {
		case "singleChoice":
			a["singleChoice"] = q.Choices[i%len(q.Choices)]
		case "multiChoice":
			a["multiChoice"] = []string{q.Choices[i%len(q.Choices)]}
		case "scale0to10":
			a["number"] = i % 11
		case "number":
			if q.Min != nil {
				a["number"] = *q.Min
			} else {
				a["number"] = 0
			}
		default:
			a["freeText"] = fmt.Sprintf("Load rehearsal ballot %d.", i+1)
		}
		answers = append(answers, a)
	}
	ballotStart := time.Now()
	payload, _ := json.Marshal(map[string]any{
		"apiVersion": "examples.configbutler.ai/v1alpha1",
		"kind":       "QuizSubmission",
		"metadata": map[string]any{
			"name": *round + "-" + strings.ToLower(sess.DisplayName),
			"labels": map[string]any{
				"voter.configbutler.ai/round":     *round,
				"voter.configbutler.ai/submitter": sess.DisplayName,
			},
		},
		"spec": map[string]any{
			"sessionRef":      map[string]any{"group": "examples.configbutler.ai", "kind": "QuizSession", "name": *round},
			"roundUID":        rd.Metadata.UID,
			"questionsDigest": rd.Status.QuestionsDigest,
			"submittedAt":     time.Now().UTC().Format(time.RFC3339),
			"answers":         answers,
		},
	})
	vreq, _ := http.NewRequestWithContext(ctx, "POST", base+"/quizsubmissions?fieldManager=voter", bytes.NewReader(payload))
	vreq.Header.Set("Content-Type", "application/json")
	vreq.Header.Set(sess.CSRFHeader, sess.CSRF)
	vreq.Header.Set("Origin", *app)
	vresp, err := client.Do(vreq)
	if err != nil {
		o.phase, o.err = "post-vote", err.Error()
		return o
	}
	vbody, _ := io.ReadAll(io.LimitReader(vresp.Body, 16<<10))
	_ = vresp.Body.Close()
	o.ballot = time.Since(ballotStart)
	o.status = vresp.StatusCode
	if vresp.StatusCode != 201 {
		o.phase, o.err = "post-vote", fmt.Sprintf("%d %s", vresp.StatusCode, firstLine(string(vbody)))
		return o
	}
	o.votedAt = time.Now()
	return o
}

// dialer connects to the given IP, keeping the port and the TLS server name of
// the URL, so the fixture's *.voter.test hosts work without /etc/hosts. Empty
// resolves names as usual.
func dialer(ip string) func(context.Context, string, string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if ip == "" {
		return d.DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		return d.DialContext(ctx, network, net.JoinHostPort(ip, port))
	}
}

// openStream opens krm-foyer's stream of the CoffeeConfig, waits for its
// first snapshot (the "synced" event), and keeps reading in the background
// until the run's context ends -- a phone that stays on the page. It returns
// how long the snapshot took.
func openStream(ctx context.Context, client *http.Client) (time.Duration, error) {
	started := time.Now()
	q := url.Values{
		"group": {"examples.configbutler.ai"}, "version": {"v1alpha1"}, "resource": {"coffeeconfigs"},
		"namespace": {*namespace}, "name": {*coffee},
	}
	// The run's context, not this user's: the stream outlives the vote.
	req, _ := http.NewRequestWithContext(runCtx, "GET", *app+"/stream/v1?"+q.Encode(), nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != 200 {
		_ = resp.Body.Close()
		return 0, fmt.Errorf("stream answered %d", resp.StatusCode)
	}
	synced := make(chan error, 1)
	go func() {
		defer func() { _ = resp.Body.Close() }()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		reported := false
		for sc.Scan() {
			line := sc.Text()
			if reported || !strings.HasPrefix(line, "data:") {
				continue
			}
			switch {
			case strings.Contains(line, `"type":"synced"`):
				synced <- nil
				reported = true
			case strings.Contains(line, `"type":"error"`):
				synced <- fmt.Errorf("stream error: %s", firstLine(strings.TrimPrefix(line, "data:")))
				reported = true
			}
		}
		if !reported {
			synced <- fmt.Errorf("stream ended before its snapshot: %v", sc.Err())
		}
	}()
	select {
	case err := <-synced:
		return time.Since(started), err
	case <-ctx.Done():
		return 0, fmt.Errorf("no snapshot before the deadline")
	}
}

func get(ctx context.Context, c *http.Client, u string) (string, *url.URL, int, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, err := c.Do(req)
	if err != nil {
		return "", nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	return string(b), resp.Request.URL, resp.StatusCode, nil
}

func getJSON(ctx context.Context, c *http.Client, u string, into any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
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
		args = append(args, "-n", *roomNS, "get", "rooms.room-pass.koudijs.dev", *roomName, "-o", "jsonpath={.status.joinCode.code}")
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
	return fmt.Errorf("room %s/%s has no status.joinCode", *roomNS, *roomName)
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
	synced := collect(func(o outcome) time.Duration { return o.synced })
	ballot := collect(func(o outcome) time.Duration { return o.ballot })
	total := collect(func(o outcome) time.Duration { return o.total })
	fmt.Printf("\n%-10s %10s %10s %10s %10s\n", "phase", "p50", "p95", "p99", "max")
	for _, row := range []struct {
		name string
		ds   []time.Duration
	}{{"login", login}, {"stream", synced}, {"ballot", ballot}, {"end-to-end", total}} {
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
