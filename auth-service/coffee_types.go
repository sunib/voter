package main

type kubeObjectMeta struct {
	Name            string            `json:"name,omitempty"`
	Namespace       string            `json:"namespace,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
}

type secretKeyRef struct {
	Name string `json:"name,omitempty"`
	Key  string `json:"key,omitempty"`
}

type coffeeProductSpec struct {
	SKU         string `json:"sku,omitempty"`
	Name        string `json:"name,omitempty"`
	PriceCents  int    `json:"priceCents,omitempty"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled,omitempty"`
}

type coffeeVoucherSpec struct {
	Code              string   `json:"code,omitempty"`
	Enabled           bool     `json:"enabled,omitempty"`
	DiscountType      string   `json:"discountType,omitempty"`
	DiscountValue     int      `json:"discountValue,omitempty"`
	MaximumUsage      int      `json:"maximumUsage,omitempty"`
	AppliesToProducts []string `json:"appliesToProducts,omitempty"`
	DisplayMessage    string   `json:"displayMessage,omitempty"`
}

type coffeeMailSpec struct {
	Provider                  string        `json:"provider,omitempty"`
	APIKeySecretRef           *secretKeyRef `json:"apiKeySecretRef,omitempty"`
	OrderConfirmationTemplate string        `json:"orderConfirmationTemplate,omitempty"`
	FromAddress               string        `json:"fromAddress,omitempty"`
}

type coffeePaymentsSpec struct {
	Provider                  string        `json:"provider,omitempty"`
	APIKeySecretRef           *secretKeyRef `json:"apiKeySecretRef,omitempty"`
	Mode                      string        `json:"mode,omitempty"`
	ZeroAmountCheckoutAllowed bool          `json:"zeroAmountCheckoutAllowed,omitempty"`
}

type coffeeConfigSpec struct {
	ShopName   string              `json:"shopName,omitempty"`
	BannerText string              `json:"bannerText,omitempty"`
	QRURL      string              `json:"qrUrl,omitempty"`
	Currency   string              `json:"currency,omitempty"`
	Products   []coffeeProductSpec `json:"products,omitempty"`
	Vouchers   []coffeeVoucherSpec `json:"vouchers,omitempty"`
	Mail       coffeeMailSpec      `json:"mail,omitempty"`
	Payments   coffeePaymentsSpec  `json:"payments,omitempty"`
}

type coffeeConfig struct {
	APIVersion string           `json:"apiVersion,omitempty"`
	Kind       string           `json:"kind,omitempty"`
	Metadata   kubeObjectMeta   `json:"metadata,omitempty"`
	Spec       coffeeConfigSpec `json:"spec,omitempty"`
}

type storefrontResponse struct {
	Shop     storefrontShop      `json:"shop"`
	Voucher  storefrontVoucher   `json:"voucher"`
	Products []storefrontProduct `json:"products"`
}

type storefrontShop struct {
	Name       string `json:"name"`
	BannerText string `json:"bannerText"`
	Currency   string `json:"currency"`
}

type storefrontVoucher struct {
	Code           string `json:"code"`
	PresentInURL   bool   `json:"presentInUrl"`
	DisplayMessage string `json:"displayMessage"`
	State          string `json:"state"`
}

type storefrontProduct struct {
	SKU               string `json:"sku"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Enabled           bool   `json:"enabled"`
	BasePriceCents    int    `json:"basePriceCents"`
	DisplayPriceCents int    `json:"displayPriceCents"`
	VoucherState      string `json:"voucherState"`
}

type coffeeOrderRequest struct {
	VoucherCode string                   `json:"voucherCode"`
	Source      map[string]string        `json:"source"`
	Items       []coffeeOrderItemRequest `json:"items"`
}

type coffeeOrderItemRequest struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type coffeeOrderLine struct {
	SKU            string `json:"sku"`
	Name           string `json:"name"`
	Quantity       int    `json:"quantity"`
	UnitPriceCents int    `json:"unitPriceCents"`
	LineTotalCents int    `json:"lineTotalCents"`
	VoucherApplied bool   `json:"voucherApplied"`
}

type coffeeOrderRecord struct {
	OrderID         string            `json:"orderId"`
	SubmittedAt     string            `json:"submittedAt"`
	VoucherCode     string            `json:"voucherCode,omitempty"`
	Items           []coffeeOrderLine `json:"items"`
	Source          map[string]string `json:"source,omitempty"`
	Currency        string            `json:"currency"`
	TotalPriceCents int               `json:"totalPriceCents"`
	Status          string            `json:"status"`
	FailureCode     string            `json:"failureCode,omitempty"`
	FailureMessage  string            `json:"failureMessage,omitempty"`
}

type coffeeOrdersSnapshot struct {
	Orders       []coffeeOrderRecord `json:"orders"`
	VoucherUsage map[string]int      `json:"voucherUsage"`
}

type coffeeConfigWatchEvent struct {
	Type   string       `json:"type"`
	Object coffeeConfig `json:"object"`
}

type coffeeOrderFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type coffeeOrderResponse struct {
	OrderID         string              `json:"orderId"`
	Status          string              `json:"status"`
	Currency        string              `json:"currency"`
	TotalPriceCents int                 `json:"totalPriceCents"`
	Failure         *coffeeOrderFailure `json:"failure,omitempty"`
}
