package main

// Voucher redemption counting.
//
// The demo turns on one bug: the TestNet voucher is depleted because
// `maximumUsage` is set far too low, and -- this is the part that makes it a
// good demo -- the depletion is invisible until someone actually submits an
// order. The storefront happily shows the discounted price right up to the
// moment it fails.
//
// So something has to count redemptions. The honest options were:
//
//   - A status subresource on CoffeeConfig. There isn't one, and adding it
//     means owning a controller.
//   - An Order custom resource per submission. There is no such CRD in the
//     cluster, and participants have no permission to create one.
//   - Count here, in the process.
//
// This is the third, and its cost is stated rather than hidden: the count is
// per-process and in memory. Restarting the pod forgives every redemption, and
// a second replica would keep its own tally, so the limit would effectively
// double. That is acceptable for a single-replica conference demo and NOT
// acceptable for anything else -- PLAN.md tracks the decision that has to
// happen before this is scaled.
//
// The limit itself is never cached: it is read from the CoffeeConfig on every
// order. That is what makes the demo's punchline work -- an operator raises
// `maximumUsage` in Git, Flux applies it, and the very next order succeeds
// with no restart.

import "sync"

// voucherLedger counts successful redemptions per voucher code.
//
// Keyed by the NORMALIZED code, so "TESTNET" and " testnet " are one voucher
// and not two independent allowances.
type voucherLedger struct {
	mu    sync.Mutex
	count map[string]int
}

func newVoucherLedger() *voucherLedger {
	return &voucherLedger{count: map[string]int{}}
}

// redeem records one use of code if the limit allows it, and reports whether it
// did. The check and the increment happen under one lock, so two concurrent
// orders cannot both pass a limit with one remaining.
//
// A limit of zero or less means unlimited: a voucher with no maximumUsage set
// is not a voucher that can never be used. Depletion has to be configured
// deliberately, which is exactly what the demo does.
func (l *voucherLedger) redeem(code string, limit int) (used int, ok bool) {
	key := normalizeVoucherCode(code)
	if key == "" {
		return 0, true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	current := l.count[key]
	if limit > 0 && current >= limit {
		return current, false
	}
	l.count[key] = current + 1
	return l.count[key], true
}

// used reports the redemption count without changing it, for the storefront's
// "assumed-applied" hint and for tests.
func (l *voucherLedger) used(code string) int {
	key := normalizeVoucherCode(code)
	if key == "" {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.count[key]
}

// release gives a redemption back. It exists for one case: the order was
// counted, and then a later step of the same request failed, so the
// participant never got the coffee. Without it a failed write would silently
// consume somebody else's allowance.
func (l *voucherLedger) release(code string) {
	key := normalizeVoucherCode(code)
	if key == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.count[key] > 0 {
		l.count[key]--
	}
}
