package main

import (
	"fmt"
	"sync"
	"time"
)

type coffeeRuntime struct {
	mu           sync.Mutex
	nextOrderID  int
	orders       []coffeeOrderRecord
	voucherUsage map[string]int
	subscribers  map[int]chan coffeeOrderRecord
	nextSubID    int
}

func newCoffeeRuntime() *coffeeRuntime {
	return &coffeeRuntime{
		nextOrderID:  1,
		voucherUsage: map[string]int{},
		subscribers:  map[int]chan coffeeOrderRecord{},
	}
}

func (r *coffeeRuntime) submit(prepared preparedCoffeeOrder, validationFailure *coffeeOrderFailure) coffeeOrderRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	record := coffeeOrderRecord{
		OrderID:         fmt.Sprintf("coffee-%06d", r.nextOrderID),
		SubmittedAt:     time.Now().UTC().Format(time.RFC3339),
		VoucherCode:     prepared.VoucherCode,
		Items:           append([]coffeeOrderLine(nil), prepared.Items...),
		Source:          cloneStringMap(prepared.Source),
		Currency:        prepared.Currency,
		TotalPriceCents: prepared.TotalPriceCents,
		Status:          coffeeOrderStatusPlaced,
	}
	r.nextOrderID++

	failure := validationFailure
	if failure == nil && prepared.Voucher != nil && prepared.Voucher.MaximumUsage > 0 {
		usage := r.voucherUsage[normalizeVoucherCode(prepared.Voucher.Code)]
		if usage >= prepared.Voucher.MaximumUsage {
			failure = &coffeeOrderFailure{
				Code:    coffeeFailureVoucherDepleted,
				Message: "Free coffee is no longer available for that voucher.",
			}
		}
	}

	if failure != nil {
		record.Status = coffeeOrderStatusRejected
		record.FailureCode = failure.Code
		record.FailureMessage = failure.Message
		r.appendRecordLocked(record)
		return record
	}

	if prepared.Voucher != nil {
		key := normalizeVoucherCode(prepared.Voucher.Code)
		r.voucherUsage[key]++
	}
	r.appendRecordLocked(record)
	return record
}

func (r *coffeeRuntime) appendRecordLocked(record coffeeOrderRecord) {
	r.orders = append(r.orders, record)
	for _, ch := range r.subscribers {
		select {
		case ch <- record:
		default:
		}
	}
}

func (r *coffeeRuntime) snapshot() coffeeOrdersSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	orders := make([]coffeeOrderRecord, len(r.orders))
	copy(orders, r.orders)
	usage := make(map[string]int, len(r.voucherUsage))
	for key, value := range r.voucherUsage {
		usage[key] = value
	}
	return coffeeOrdersSnapshot{
		Orders:       orders,
		VoucherUsage: usage,
	}
}

func (r *coffeeRuntime) subscribe(buffer int) (int, <-chan coffeeOrderRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextSubID++
	id := r.nextSubID
	ch := make(chan coffeeOrderRecord, buffer)
	r.subscribers[id] = ch
	return id, ch
}

func (r *coffeeRuntime) unsubscribe(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ch, ok := r.subscribers[id]
	if !ok {
		return
	}
	delete(r.subscribers, id)
	close(ch)
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
