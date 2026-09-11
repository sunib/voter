// Command room-qr is the presenter's screen.
//
// It watches a Room's rotating join code and draws, in the terminal, a QR code
// that a participant can scan to join and land straight on a chosen page of
// the application. Point a projector at it.
//
// Why a terminal tool and not a page in the application: the join code is
// operator-only credential material. It lives in the Room's status, which
// ordinary participants cannot read, and rotation is what bounds the damage
// when somebody photographs the screen. Serving it from a web page would mean
// building an operator login for that page and getting it exactly right; a
// tool that runs under the operator's own kubeconfig inherits the answer
// Kubernetes already gives. If you cannot read rooms/status, you get nothing.
//
//	go run ./cmd/room-qr --room demo --namespace voter \
//	  --base https://voter.koudijs.dev --next /answer/round-1
//
// The QR encodes the application's login URL with two parameters: the code, so
// the participant never types it, and the destination, so they arrive at the
// questionnaire rather than the front page. Everything else -- Dex, the
// enrollment, the token exchange -- is the ordinary flow.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	api "github.com/sunib/voter/room-pass/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func main() {
	if e := run(); e != nil && !errors.Is(e, context.Canceled) {
		fmt.Fprintln(os.Stderr, "room-qr:", e)
		os.Exit(1)
	}
}

type options struct {
	room, namespace string
	base, next      string
	interval        time.Duration
	once            bool
}

func run() error {
	var o options
	flag.StringVar(&o.room, "room", "demo", "Room name")
	flag.StringVar(&o.namespace, "namespace", "voter", "Room namespace")
	flag.StringVar(&o.base, "base", "", "Application origin, e.g. https://voter.koudijs.dev (required)")
	flag.StringVar(&o.next, "next", "/", "Absolute path within the application to land on after login")
	flag.DurationVar(&o.interval, "interval", time.Second, "How often to re-read the Room")
	flag.BoolVar(&o.once, "once", false, "Print one QR code and exit, instead of following rotation")
	flag.Parse()

	if err := o.validate(); err != nil {
		return err
	}

	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		return err
	}
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("no Kubernetes configuration: %w", err)
	}
	cfg.Timeout = 8 * time.Second
	db, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	key := client.ObjectKey{Namespace: o.namespace, Name: o.room}
	// A plain poll, not a watch. The screen has to redraw on a clock anyway --
	// the countdown moves whether or not the object changes -- and a poll of a
	// single object once a second is cheaper to reason about than a watch that
	// has to be re-established every time the connection drops mid-talk.
	shown := ""
	for {
		room := &api.Room{}
		switch err := db.Get(ctx, key, room); {
		case err != nil && ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			// Do not clear a working QR code because one read failed. The
			// room is mid-talk; a blank projector is worse than a stale one.
			fmt.Fprintf(os.Stderr, "\nroom-qr: could not read %s: %v\n", key, err)
		default:
			code := ""
			if room.Status.JoinCode != nil {
				code = room.Status.JoinCode.Code
			}
			if code == "" {
				if shown != "" || o.once {
					fmt.Print(clearScreen)
					fmt.Printf("  %s\n\n  No join code is being published.\n  Enrollment is %s and the Room %s.\n",
						room.Spec.Title, strings.ToLower(room.Spec.Enrollment), activity(room))
					shown = ""
				}
				if o.once {
					return errors.New("the Room is publishing no join code")
				}
				break
			}
			if code != shown {
				if err := draw(room, code, o); err != nil {
					return err
				}
				shown = code
			}
			if o.once {
				return nil
			}
			if room.Status.JoinCode != nil {
				countdown(room.Status.JoinCode.ExpiresAt.Time)
			}
		}
		select {
		case <-ctx.Done():
			fmt.Println()
			return nil
		case <-time.After(o.interval):
		}
	}
}

func (o *options) validate() error {
	u, err := url.Parse(o.base)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.RawQuery != "" {
		return errors.New("--base must be a bare origin such as https://voter.koudijs.dev")
	}
	if u.Scheme != "https" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		// A camera app follows whatever the QR code says. Handing the room an
		// http:// URL means handing it a session cookie over plaintext.
		return errors.New("--base must be https (except for localhost)")
	}
	if !strings.HasPrefix(o.next, "/") || strings.HasPrefix(o.next, "//") {
		return errors.New("--next must be an absolute path within the application, such as /answer/round-1")
	}
	if o.interval < 100*time.Millisecond {
		return errors.New("--interval is too short")
	}
	return nil
}

// joinURL is what the QR code carries. Kept as short as it can honestly be:
// every character is another module in the symbol, and the symbol has to
// survive being photographed from the back of the room.
func (o *options) joinURL(code string) string {
	q := url.Values{"code": {code}}
	if o.next != "/" {
		q.Set("return", o.next)
	}
	return o.base + "/auth/login?" + q.Encode()
}

const clearScreen = "\033[H\033[2J"

func draw(room *api.Room, code string, o options) error {
	target := o.joinURL(code)
	// Low recovery on purpose. A screen is not a coffee cup: there is nothing
	// to scratch the symbol, and the lowest level gives the fewest modules,
	// which means the largest ones at a fixed terminal size.
	q, err := qrcode.New(target, qrcode.Low)
	if err != nil {
		return fmt.Errorf("could not encode %q: %w", target, err)
	}
	fmt.Print(clearScreen)
	fmt.Printf("  %s\n\n", room.Spec.Title)
	fmt.Print(q.ToSmallString(false))
	fmt.Printf("\n  Scan to join, or go to %s/join and enter\n\n      %s\n\n", o.base, spaced(code))
	return nil
}

// spaced puts the code in groups of three, which is how people read it aloud
// and how Room Pass's Normalize already expects to receive it back.
func spaced(code string) string {
	var parts []string
	for i := 0; i < len(code); i += 3 {
		parts = append(parts, code[i:min(i+3, len(code))])
	}
	return strings.Join(parts, " ")
}

// countdown rewrites a single line in place, so the QR code above it does not
// flicker between rotations.
func countdown(expires time.Time) {
	left := time.Until(expires).Round(time.Second)
	if left < 0 {
		left = 0
	}
	fmt.Printf("\r  This code changes in %2ds. The one on screen is always current.   ", int(left.Seconds()))
}

func activity(room *api.Room) string {
	switch {
	case room.Spec.Stopped:
		return "has been stopped"
	case time.Now().After(room.Spec.EndsAt.Time):
		return "has ended"
	default:
		return "is not ready yet"
	}
}
