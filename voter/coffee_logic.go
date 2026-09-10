package main

import "strings"

const (
	coffeeOrderStatusPlaced   = "placed"
	coffeeOrderStatusRejected = "rejected"

	coffeeFailureVoucherDepleted      = "VoucherDepleted"
	coffeeFailureVoucherInvalid       = "VoucherInvalid"
	coffeeFailureVoucherNotApplicable = "VoucherNotApplicable"
	coffeeFailureProductUnavailable   = "ProductUnavailable"
	coffeeFailureEmptyOrder           = "EmptyOrder"
)

type preparedCoffeeOrder struct {
	VoucherCode     string
	Voucher         *coffeeVoucherSpec
	Items           []coffeeOrderLine
	Source          map[string]string
	Currency        string
	TotalPriceCents int
}

func buildStorefront(cfg coffeeConfig, voucherCode string) storefrontResponse {
	voucherCode = strings.TrimSpace(voucherCode)
	voucher, voucherState := resolveVoucherForDisplay(cfg.Spec, voucherCode)
	products := make([]storefrontProduct, 0, len(cfg.Spec.Products))

	for _, product := range cfg.Spec.Products {
		if !product.Enabled {
			continue
		}

		displayPrice := product.PriceCents
		lineVoucherState := "not-applied"
		if voucher != nil && voucherAppliesToProduct(*voucher, product.SKU) {
			displayPrice = discountedUnitPrice(product.PriceCents, *voucher)
			lineVoucherState = voucherState
		}

		products = append(products, storefrontProduct{
			SKU:               product.SKU,
			Name:              product.Name,
			Description:       product.Description,
			Enabled:           product.Enabled,
			BasePriceCents:    product.PriceCents,
			DisplayPriceCents: displayPrice,
			VoucherState:      lineVoucherState,
		})
	}

	displayMessage := ""
	if voucher != nil {
		displayMessage = voucher.DisplayMessage
	}

	return storefrontResponse{
		Shop: storefrontShop{
			Name:       cfg.Spec.ShopName,
			BannerText: cfg.Spec.BannerText,
			Currency:   cfg.Spec.Currency,
		},
		Voucher: storefrontVoucher{
			Code:           voucherCode,
			PresentInURL:   voucherCode != "",
			DisplayMessage: displayMessage,
			State:          voucherState,
		},
		Products: products,
	}
}

func resolveVoucherForDisplay(spec coffeeConfigSpec, voucherCode string) (*coffeeVoucherSpec, string) {
	if voucherCode == "" {
		return nil, "not-present"
	}

	voucher := findVoucher(spec.Vouchers, voucherCode)
	if voucher == nil || !voucher.Enabled {
		return nil, "invalid"
	}
	if !voucherAppliesToAnyEnabledProduct(*voucher, spec.Products) {
		return voucher, "not-applicable"
	}
	return voucher, "assumed-applied"
}

func prepareCoffeeOrder(cfg coffeeConfig, req coffeeOrderRequest) (preparedCoffeeOrder, *coffeeOrderFailure) {
	var prepared preparedCoffeeOrder
	prepared.VoucherCode = strings.TrimSpace(req.VoucherCode)
	prepared.Source = req.Source
	prepared.Currency = cfg.Spec.Currency

	quantities := map[string]int{}
	totalQuantity := 0
	for _, item := range req.Items {
		sku := strings.TrimSpace(item.SKU)
		if sku == "" || item.Quantity <= 0 {
			continue
		}
		quantities[sku] += item.Quantity
		totalQuantity += item.Quantity
	}
	if totalQuantity == 0 {
		return prepared, &coffeeOrderFailure{
			Code:    coffeeFailureEmptyOrder,
			Message: "Choose at least one coffee before ordering.",
		}
	}

	productsBySKU := map[string]coffeeProductSpec{}
	for _, product := range cfg.Spec.Products {
		productsBySKU[product.SKU] = product
	}

	if prepared.VoucherCode != "" {
		prepared.Voucher = findVoucher(cfg.Spec.Vouchers, prepared.VoucherCode)
		if prepared.Voucher == nil || !prepared.Voucher.Enabled {
			return prepared, &coffeeOrderFailure{
				Code:    coffeeFailureVoucherInvalid,
				Message: "That voucher could not be applied.",
			}
		}
	}

	applicableVoucher := false
	prepared.Items = make([]coffeeOrderLine, 0, len(quantities))
	for _, product := range cfg.Spec.Products {
		quantity := quantities[product.SKU]
		if quantity == 0 {
			continue
		}
		if !product.Enabled {
			return prepared, &coffeeOrderFailure{
				Code:    coffeeFailureProductUnavailable,
				Message: "One of the selected coffees is no longer available.",
			}
		}

		unitPrice := product.PriceCents
		voucherApplied := false
		if prepared.Voucher != nil && voucherAppliesToProduct(*prepared.Voucher, product.SKU) {
			unitPrice = discountedUnitPrice(product.PriceCents, *prepared.Voucher)
			voucherApplied = true
			applicableVoucher = true
		}

		lineTotal := unitPrice * quantity
		prepared.TotalPriceCents += lineTotal
		prepared.Items = append(prepared.Items, coffeeOrderLine{
			SKU:            product.SKU,
			Name:           product.Name,
			Quantity:       quantity,
			UnitPriceCents: unitPrice,
			LineTotalCents: lineTotal,
			VoucherApplied: voucherApplied,
		})
		delete(quantities, product.SKU)
	}

	if len(quantities) > 0 {
		return prepared, &coffeeOrderFailure{
			Code:    coffeeFailureProductUnavailable,
			Message: "One of the selected coffees is no longer available.",
		}
	}

	if prepared.Voucher != nil && !applicableVoucher {
		return prepared, &coffeeOrderFailure{
			Code:    coffeeFailureVoucherNotApplicable,
			Message: "That voucher does not apply to the selected coffees.",
		}
	}

	return prepared, nil
}

func findVoucher(vouchers []coffeeVoucherSpec, code string) *coffeeVoucherSpec {
	needle := normalizeVoucherCode(code)
	if needle == "" {
		return nil
	}
	for i := range vouchers {
		if normalizeVoucherCode(vouchers[i].Code) == needle {
			return &vouchers[i]
		}
	}
	return nil
}

func normalizeVoucherCode(code string) string {
	return strings.ToLower(strings.TrimSpace(code))
}

func voucherAppliesToProduct(voucher coffeeVoucherSpec, sku string) bool {
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return false
	}
	if len(voucher.AppliesToProducts) == 0 {
		return true
	}
	for _, candidate := range voucher.AppliesToProducts {
		if strings.TrimSpace(candidate) == sku {
			return true
		}
	}
	return false
}

func voucherAppliesToAnyEnabledProduct(voucher coffeeVoucherSpec, products []coffeeProductSpec) bool {
	for _, product := range products {
		if product.Enabled && voucherAppliesToProduct(voucher, product.SKU) {
			return true
		}
	}
	return false
}

func discountedUnitPrice(priceCents int, voucher coffeeVoucherSpec) int {
	switch strings.TrimSpace(voucher.DiscountType) {
	case "fixed":
		discounted := priceCents - voucher.DiscountValue
		if discounted < 0 {
			return 0
		}
		return discounted
	default:
		discounted := priceCents - ((priceCents * voucher.DiscountValue) / 100)
		if discounted < 0 {
			return 0
		}
		return discounted
	}
}
