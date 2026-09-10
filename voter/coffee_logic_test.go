package main

import "testing"

func TestBuildStorefrontAssumesVoucherBeforeDepletion(t *testing.T) {
	cfg := coffeeConfig{
		Spec: coffeeConfigSpec{
			ShopName:   "TestNet Coffee",
			BannerText: "Free coffee",
			Currency:   "EUR",
			Products: []coffeeProductSpec{
				{
					SKU:        "flat-white",
					Name:       "Flat White",
					PriceCents: 395,
					Enabled:    true,
				},
			},
			Vouchers: []coffeeVoucherSpec{
				{
					Code:              "testnet2026",
					Enabled:           true,
					DiscountType:      "percentage",
					DiscountValue:     100,
					MaximumUsage:      1,
					AppliesToProducts: []string{"flat-white"},
					DisplayMessage:    "Free coffee for TestNet visitors",
				},
			},
		},
	}

	storefront := buildStorefront(cfg, "testnet2026")
	if storefront.Voucher.State != "assumed-applied" {
		t.Fatalf("unexpected voucher state: %q", storefront.Voucher.State)
	}
	if len(storefront.Products) != 1 {
		t.Fatalf("unexpected product count: %d", len(storefront.Products))
	}
	if storefront.Products[0].DisplayPriceCents != 0 {
		t.Fatalf("expected free display price, got %d", storefront.Products[0].DisplayPriceCents)
	}
}

func TestPrepareCoffeeOrderRejectsInapplicableVoucher(t *testing.T) {
	cfg := coffeeConfig{
		Spec: coffeeConfigSpec{
			Currency: "EUR",
			Products: []coffeeProductSpec{
				{
					SKU:        "espresso",
					Name:       "Espresso",
					PriceCents: 275,
					Enabled:    true,
				},
			},
			Vouchers: []coffeeVoucherSpec{
				{
					Code:              "testnet2026",
					Enabled:           true,
					DiscountType:      "percentage",
					DiscountValue:     100,
					AppliesToProducts: []string{"flat-white"},
				},
			},
		},
	}

	_, failure := prepareCoffeeOrder(cfg, coffeeOrderRequest{
		VoucherCode: "testnet2026",
		Items: []coffeeOrderItemRequest{
			{SKU: "espresso", Quantity: 1},
		},
	})
	if failure == nil {
		t.Fatalf("expected voucher failure")
	}
	if failure.Code != coffeeFailureVoucherNotApplicable {
		t.Fatalf("unexpected failure code: %q", failure.Code)
	}
}
