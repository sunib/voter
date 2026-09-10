package main

import (
	"testing"
	"time"
)

func TestNewCoffeeConfigChangeIncludesActorReasonAndFieldDiffs(t *testing.T) {
	now := time.Date(2026, 5, 5, 9, 30, 0, 0, time.UTC)
	before := coffeeConfig{
		Metadata: kubeObjectMeta{Generation: 7},
		Spec: coffeeConfigSpec{
			ShopName:   "TestNet Coffee",
			BannerText: "Before",
			Products: []coffeeProductSpec{{
				SKU:        "flat-white",
				Name:       "Flat White",
				PriceCents: 395,
				Enabled:    true,
			}},
		},
	}
	after := before
	after.Metadata.Generation = 8
	after.Spec.BannerText = "After"
	after.Spec.Products = append([]coffeeProductSpec(nil), before.Spec.Products...)
	after.Spec.Products[0].PriceCents = 425

	record := newCoffeeConfigChange(
		now,
		before,
		after,
		[]byte(`{"spec":{"bannerText":"After","products":[{"sku":"flat-white","name":"Flat White","priceCents":425,"enabled":true}]}}`),
		"Alice",
		"Raise price for the demo rush",
	)

	if record.Actor != "Alice" {
		t.Fatalf("unexpected actor: got %q want %q", record.Actor, "Alice")
	}
	if record.Reason != "Raise price for the demo rush" {
		t.Fatalf("unexpected reason: got %q", record.Reason)
	}
	if record.Generation != 8 {
		t.Fatalf("unexpected generation: got %d want %d", record.Generation, 8)
	}
	if len(record.Changes) != 2 {
		t.Fatalf("unexpected change count: got %d want %d", len(record.Changes), 2)
	}
	if record.Changes[0].Path != "spec.bannerText" {
		t.Fatalf("unexpected first change path: got %q", record.Changes[0].Path)
	}
	if record.Changes[1].Path != "spec.products" {
		t.Fatalf("unexpected second change path: got %q", record.Changes[1].Path)
	}
}

func TestCoffeeChangeRuntimeSnapshotNewestFirst(t *testing.T) {
	runtime := newCoffeeChangeRuntime(2)

	first := runtime.record(coffeeConfigChangeRecord{
		CreatedAt: time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Actor:     "Alice",
		Summary:   "Updated banner text",
		Changes: []coffeeConfigFieldChange{{
			Path: "spec.bannerText",
		}},
	})
	second := runtime.record(coffeeConfigChangeRecord{
		CreatedAt: time.Date(2026, 5, 5, 9, 1, 0, 0, time.UTC).Format(time.RFC3339),
		Actor:     "Bob",
		Summary:   "Updated currency",
		Changes: []coffeeConfigFieldChange{{
			Path: "spec.currency",
		}},
	})
	runtime.record(coffeeConfigChangeRecord{
		CreatedAt: time.Date(2026, 5, 5, 9, 2, 0, 0, time.UTC).Format(time.RFC3339),
		Actor:     "Cara",
		Summary:   "Updated mail provider",
		Changes: []coffeeConfigFieldChange{{
			Path: "spec.mail.provider",
		}},
	})

	snapshot := runtime.snapshot()
	if len(snapshot.Changes) != 2 {
		t.Fatalf("unexpected snapshot size: got %d want %d", len(snapshot.Changes), 2)
	}
	if snapshot.Changes[0].Actor != "Cara" {
		t.Fatalf("expected newest change first, got actor %q", snapshot.Changes[0].Actor)
	}
	if snapshot.Changes[1].ID != second.ID {
		t.Fatalf("expected second record to remain, got %q want %q", snapshot.Changes[1].ID, second.ID)
	}
	if snapshot.Changes[0].ID == first.ID {
		t.Fatalf("expected oldest record to be evicted")
	}
}
