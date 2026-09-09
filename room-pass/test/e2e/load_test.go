package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	api "github.com/sunib/voter/room-pass/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSharedNATEnrollment(t *testing.T) {
	if os.Getenv("ROOM_PASS_LOAD") != "1" {
		t.Skip("run task load; temporarily creates 300 Participants")
	}
	ctx := context.Background()
	cfg, e := clientcmd.BuildConfigFromFlags("", "../../.local/kubeconfig")
	if e != nil {
		t.Fatal(e)
	}
	cfg.QPS = 100
	cfg.Burst = 200
	scheme := runtime.NewScheme()
	_ = api.AddToScheme(scheme)
	db, e := client.New(cfg, client.Options{Scheme: scheme})
	if e != nil {
		t.Fatal(e)
	}
	key := client.ObjectKey{Namespace: "room-pass", Name: "demo"}
	room := &api.Room{}
	if e = db.Get(ctx, key, room); e != nil {
		t.Fatal(e)
	}
	ps := &api.ParticipantList{}
	if e = db.List(ctx, ps, client.InNamespace("room-pass")); e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, p := range ps.Items {
		if p.Spec.RoomRef.UID == string(room.UID) {
			count++
		}
	}
	prefix := fmt.Sprintf("Load %d ", time.Now().UnixNano())
	original := room.Spec.MaxParticipants
	room.Spec.MaxParticipants = count + 300
	if e = db.Update(ctx, room); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		records := &api.ParticipantList{}
		if db.List(context.Background(), records, client.InNamespace("room-pass")) == nil {
			for i := range records.Items {
				p := &records.Items[i]
				if strings.HasPrefix(p.Spec.DisplayName, prefix) {
					_ = db.Delete(context.Background(), p)
				}
			}
		}
		r := &api.Room{}
		if db.Get(ctx, key, r) == nil {
			r.Spec.MaxParticipants = original
			_ = db.Update(ctx, r)
		}
	})
	ca, _ := os.ReadFile("../../.local/tls.crt")
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	gateway := os.Getenv("ROOM_PASS_EDGE_IP")
	if gateway == "" {
		gateway = "127.0.0.1"
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(gateway, "18443"))
	}, MaxIdleConnsPerHost: 100}
	defer tr.CloseIdleConnections()
	fields := regexp.MustCompile(`name="([^"]+)" value="([^"]*)"`)
	durations := []time.Duration{}
	var mu sync.Mutex
	for batch := 0; batch < 3; batch++ {
		// Wait for observed state; no handler accepts a stale generation's codes.
		for deadline := time.Now().Add(15 * time.Second); ; {
			_ = db.Get(ctx, key, room)
			if room.Status.ObservedGeneration == room.Generation && room.Status.JoinCode != nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("Room not reconciled")
			}
			time.Sleep(time.Second)
		}
		code := room.Status.JoinCode.Code
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				start := time.Now()
				jar, _ := cookiejar.New(nil)
				browser := &http.Client{Transport: tr, Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
				resp, err := browser.Get(join + "/join")
				if err != nil {
					t.Error(err)
					return
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 {
					t.Errorf("form status %d", resp.StatusCode)
					return
				}
				form := url.Values{"code": {code}, "name": {fmt.Sprintf("%s%d", prefix, i)}}
				for _, m := range fields.FindAllStringSubmatch(string(body), -1) {
					form.Set(m[1], html.UnescapeString(m[2]))
				}
				req, _ := http.NewRequest("POST", join+"/join", strings.NewReader(form.Encode()))
				req.Header.Set("Origin", join)
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				resp, err = browser.Do(req)
				if err != nil {
					t.Error(err)
					return
				}
				body, _ = io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 303 {
					t.Errorf("enrollment status %d: %s", resp.StatusCode, body)
					return
				}
				mu.Lock()
				durations = append(durations, time.Since(start))
				mu.Unlock()
			}(batch*100 + i)
		}
		wg.Wait()
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	if len(durations) != 300 {
		t.Fatalf("successful enrollments %d/300", len(durations))
	}
	if e = db.List(ctx, ps, client.InNamespace("room-pass")); e != nil {
		t.Fatal(e)
	}
	after := 0
	for _, p := range ps.Items {
		if p.Spec.RoomRef.UID == string(room.UID) {
			after++
		}
	}
	if after-count != 300 {
		t.Fatalf("persisted %d, expected 300", after-count)
	}
	t.Logf("300 real enrollments, batches of 100 from one source IP; p50=%s p95=%s max=%s", durations[149].Round(time.Millisecond), durations[284].Round(time.Millisecond), durations[299].Round(time.Millisecond))
}
