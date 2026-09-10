package main

import (
	"sync"
	"testing"
)

func TestVoucherLedgerEnforcesTheLimit(t *testing.T) {
	l := newVoucherLedger()

	for i := 1; i <= 3; i++ {
		used, ok := l.redeem("TESTNET", 3)
		if !ok {
			t.Fatalf("redemption %d refused, want allowed", i)
		}
		if used != i {
			t.Fatalf("redemption %d reported used=%d, want %d", i, used, i)
		}
	}

	used, ok := l.redeem("TESTNET", 3)
	if ok {
		t.Fatal("the fourth redemption was allowed past a limit of 3")
	}
	if used != 3 {
		t.Fatalf("refused redemption reported used=%d, want the unchanged 3", used)
	}
}

// The code is the identity of the allowance. If casing or padding created a
// separate tally, the limit would be trivially bypassable by typing TESTNET
// instead of testnet.
func TestVoucherLedgerNormalizesTheCode(t *testing.T) {
	l := newVoucherLedger()

	if _, ok := l.redeem("testnet", 2); !ok {
		t.Fatal("first redemption refused")
	}
	if _, ok := l.redeem("  TESTNET  ", 2); !ok {
		t.Fatal("second redemption refused")
	}
	if _, ok := l.redeem("TestNet", 2); ok {
		t.Fatal("a third redemption was allowed: casing created a second allowance")
	}
	if got := l.used("testnet"); got != 2 {
		t.Fatalf("used = %d, want 2 across all spellings", got)
	}
}

// A voucher with no maximumUsage configured is unlimited, not unusable.
// Getting this backwards would deplete every voucher on its first order.
func TestVoucherLedgerTreatsZeroLimitAsUnlimited(t *testing.T) {
	l := newVoucherLedger()
	for i := range 50 {
		if _, ok := l.redeem("free", 0); !ok {
			t.Fatalf("redemption %d refused under an unset limit", i)
		}
	}
	if _, ok := l.redeem("free", -1); !ok {
		t.Fatal("a negative limit refused a redemption; it should also mean unlimited")
	}
}

func TestVoucherLedgerIgnoresAnEmptyCode(t *testing.T) {
	l := newVoucherLedger()
	// An order with no voucher must not consume anything, and must not be
	// refused by a limit it was never subject to.
	if _, ok := l.redeem("", 1); !ok {
		t.Fatal("an order without a voucher was refused")
	}
	if _, ok := l.redeem("   ", 1); !ok {
		t.Fatal("a blank voucher code was refused")
	}
	if got := l.used(""); got != 0 {
		t.Fatalf("used = %d for the empty code, want 0", got)
	}
}

func TestVoucherLedgerReleaseGivesTheAllowanceBack(t *testing.T) {
	l := newVoucherLedger()
	if _, ok := l.redeem("testnet", 1); !ok {
		t.Fatal("first redemption refused")
	}
	if _, ok := l.redeem("testnet", 1); ok {
		t.Fatal("the limit was not enforced before release")
	}

	l.release("testnet")
	if got := l.used("testnet"); got != 0 {
		t.Fatalf("used = %d after release, want 0", got)
	}
	if _, ok := l.redeem("testnet", 1); !ok {
		t.Fatal("the released allowance was not usable again")
	}
}

// Releasing more than was taken must not produce a negative count, which would
// hand out free redemptions.
func TestVoucherLedgerReleaseDoesNotGoNegative(t *testing.T) {
	l := newVoucherLedger()
	l.release("testnet")
	l.release("testnet")
	if got := l.used("testnet"); got != 0 {
		t.Fatalf("used = %d after releasing an unused voucher, want 0", got)
	}
	if _, ok := l.redeem("testnet", 1); !ok {
		t.Fatal("first redemption refused")
	}
	if _, ok := l.redeem("testnet", 1); ok {
		t.Fatal("over-releasing created an extra allowance")
	}
}

// The whole point of holding the lock across check-and-increment. With a
// separate read and write, a room full of people submitting at once would
// overshoot the limit -- which in this demo means the bug fails to reproduce.
func TestVoucherLedgerIsSafeUnderConcurrentOrders(t *testing.T) {
	const limit = 25
	const attempts = 500

	l := newVoucherLedger()
	var wg sync.WaitGroup
	granted := make([]bool, attempts)

	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok := l.redeem("testnet", limit)
			granted[i] = ok
		}()
	}
	wg.Wait()

	count := 0
	for _, ok := range granted {
		if ok {
			count++
		}
	}
	if count != limit {
		t.Fatalf("%d of %d concurrent orders were granted, want exactly %d", count, attempts, limit)
	}
	if got := l.used("testnet"); got != limit {
		t.Fatalf("used = %d, want %d", got, limit)
	}
}
