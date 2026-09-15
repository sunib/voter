package main

// The order feed, and why it is not a custom resource.
//
// Everything else the coffee half of this demo touches is a Kubernetes object:
// the menu, the prices, the vouchers, who may edit them. That is the argument
// the demo makes -- configuration belongs in the API server, under RBAC and
// admission, and from there into Git.
//
// Orders are the counter-example, and showing them beside the CoffeeConfig is
// the point. An order is an event, not configuration. Nobody reviews it, nobody
// reconciles toward it, and writing one into Git would produce a commit per
// coffee that no human will ever read. So orders live here: a bounded ring
// buffer in this process, fanned out to whoever is watching, and gone on the
// next restart. That loss is the honest cost of the classification, not an
// unfinished feature -- a real shop would put these in a queue or a ledger, and
// neither of those is a CRD either.
//
// See coffee_vouchers.go for the same decision taken about redemption counts.

import (
	"sync"
	"time"
)

// orderLogCapacity bounds what the feed remembers. A demo room produces tens of
// orders, and a viewer joining late wants the recent ones, not all of them.
const orderLogCapacity = 100

// orderLogMaxSubscribers bounds concurrent watchers. Each one costs a goroutine
// and a channel, and unlike the KRM stream there is no shared watch underneath
// to amortise them: this is a fan-out from memory. The cap exists so a room of
// phones that all open the feed degrades into a refusal a screen can render,
// rather than into unbounded growth.
const orderLogMaxSubscribers = 64

// coffeeOrderRecord is one submission as the feed reports it.
//
// It carries the participant's DISPLAY NAME and nothing else about them -- no
// subject, no email, no token. The feed is readable by everyone signed in, so
// it may only show what the room already sees on each other's badges.
type coffeeOrderRecord struct {
	// Seq is this process's own counter, and the only stable key a viewer has:
	// a rejected order never got an ID. It also serves as the SSE event id, so
	// a reconnecting browser can tell a replayed record from a new one.
	Seq             int64             `json:"seq"`
	OrderID         string            `json:"orderId,omitempty"`
	SubmittedAt     string            `json:"submittedAt"`
	Who             string            `json:"who,omitempty"`
	VoucherCode     string            `json:"voucherCode,omitempty"`
	Items           []coffeeOrderLine `json:"items,omitempty"`
	Currency        string            `json:"currency,omitempty"`
	TotalPriceCents int               `json:"totalPriceCents"`
	Status          string            `json:"status"`
	FailureCode     string            `json:"failureCode,omitempty"`
	FailureMessage  string            `json:"failureMessage,omitempty"`
}

// orderLog is the whole store. Oldest first, newest last, at most
// orderLogCapacity entries.
type orderLog struct {
	mu      sync.Mutex
	nextSeq int64
	records []coffeeOrderRecord

	subs      map[int64]chan coffeeOrderRecord
	nextSubID int64
}

func newOrderLog() *orderLog {
	return &orderLog{nextSeq: 1, subs: map[int64]chan coffeeOrderRecord{}}
}

// record stamps the submission and publishes it. It returns the stored copy so
// a caller can log or test against the sequence number it was given.
//
// Rejected orders are recorded too, deliberately. "Nine placed, one refused
// because the voucher is depleted" is the sentence the demo is building toward,
// and a feed that only showed successes could not say it.
func (l *orderLog) record(rec coffeeOrderRecord) coffeeOrderRecord {
	if l == nil {
		return rec
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	rec.Seq = l.nextSeq
	l.nextSeq++
	if rec.SubmittedAt == "" {
		rec.SubmittedAt = time.Now().UTC().Format(time.RFC3339)
	}

	l.records = append(l.records, rec)
	if len(l.records) > orderLogCapacity {
		// Re-slice into a fresh array rather than dropping the head in place,
		// so the backing array cannot grow without bound as the demo runs.
		kept := make([]coffeeOrderRecord, orderLogCapacity)
		copy(kept, l.records[len(l.records)-orderLogCapacity:])
		l.records = kept
	}

	for _, ch := range l.subs {
		select {
		case ch <- rec:
		default:
			// A watcher that cannot keep up loses this record rather than
			// stalling every other watcher behind it. It still holds the
			// snapshot it opened with, and a reload repairs the gap.
		}
	}
	return rec
}

// snapshot copies the remembered records, oldest first.
func (l *orderLog) snapshot() []coffeeOrderRecord {
	if l == nil {
		return []coffeeOrderRecord{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]coffeeOrderRecord, len(l.records))
	copy(out, l.records)
	return out
}

// subscribe registers a watcher, or reports false when the cap is reached.
//
// The buffer is deliberately larger than one: a watcher is briefly busy writing
// to its socket while the next order arrives, and a single slot would drop
// records during ordinary operation rather than only under real pressure.
func (l *orderLog) subscribe() (id int64, ch <-chan coffeeOrderRecord, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.subs) >= orderLogMaxSubscribers {
		return 0, nil, false
	}
	l.nextSubID++
	id = l.nextSubID
	events := make(chan coffeeOrderRecord, 32)
	l.subs[id] = events
	return id, events, true
}

func (l *orderLog) unsubscribe(id int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ch, found := l.subs[id]; found {
		delete(l.subs, id)
		close(ch)
	}
}

// subscribers reports the current watcher count, for the feed's own header and
// for tests that need to know a subscription was actually released.
func (l *orderLog) subscribers() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.subs)
}
