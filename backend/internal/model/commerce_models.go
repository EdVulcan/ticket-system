package model

import "time"

// TenantBusinessCapability enables an independent commercial business domain
// for a tenant. It is deliberately separate from market capabilities such as
// supplier/distributor/travel agency.
type TenantBusinessCapability struct {
	Base
	TenantID     uint       `gorm:"not null;uniqueIndex:idx_tenant_business_capability" json:"tenant_id"`
	BusinessType string     `gorm:"size:20;not null;uniqueIndex:idx_tenant_business_capability;check:chk_tenant_business_capability_type,business_type IN ('restaurant','retail')" json:"business_type"`
	Status       string     `gorm:"size:20;not null;default:'active';index;check:chk_tenant_business_capability_status,status IN ('active','suspended')" json:"status"`
	EnabledAt    *time.Time `json:"enabled_at,omitempty"`
	SuspendedAt  *time.Time `json:"suspended_at,omitempty"`
}

// CommerceProduct is the tenant-owned product aggregate for restaurant and
// retail domains. Ticket products remain represented by Product.
type CommerceProduct struct {
	Base
	TenantID       uint                  `gorm:"not null;index:idx_commerce_products_tenant_domain" json:"tenant_id"`
	BusinessType   string                `gorm:"size:20;not null;index:idx_commerce_products_tenant_domain;check:chk_commerce_products_business_type,business_type IN ('restaurant','retail')" json:"business_type"`
	Name           string                `gorm:"size:160;not null" json:"name"`
	ShortTitle     string                `gorm:"size:80" json:"short_title,omitempty"`
	Description    string                `gorm:"type:text" json:"description,omitempty"`
	CategoryName   string                `gorm:"size:80;index" json:"category_name,omitempty"`
	Status         string                `gorm:"size:20;not null;default:'draft';index;check:chk_commerce_products_status,status IN ('draft','online','offline')" json:"status"`
	SaleStartsAt   *time.Time            `json:"sale_starts_at,omitempty"`
	SaleEndsAt     *time.Time            `json:"sale_ends_at,omitempty"`
	CurrentVersion int                   `gorm:"not null;default:1" json:"current_version"`
	SKUs           []CommerceSKU         `gorm:"foreignKey:ProductID" json:"skus,omitempty"`
	OptionGroups   []CommerceOptionGroup `gorm:"foreignKey:ProductID" json:"option_groups,omitempty"`
}

// CommerceSKU is the price and inventory unit. All monetary values are cents.
type CommerceSKU struct {
	Base
	TenantID           uint   `gorm:"not null;uniqueIndex:idx_commerce_skus_tenant_code,priority:1;index:idx_commerce_skus_tenant_product" json:"tenant_id"`
	ProductID          uint   `gorm:"not null;index:idx_commerce_skus_tenant_product" json:"product_id"`
	SkuCode            string `gorm:"size:80;not null;uniqueIndex:idx_commerce_skus_tenant_code,priority:2" json:"sku_code"`
	Name               string `gorm:"size:160;not null" json:"name"`
	OriginalPriceCents int64  `gorm:"not null;default:0;check:chk_commerce_skus_prices,original_price_cents >= 0 AND price_cents >= 0" json:"original_price_cents"`
	PriceCents         int64  `gorm:"not null;default:0;check:chk_commerce_skus_price_nonnegative,price_cents >= 0" json:"price_cents"`
	Status             string `gorm:"size:20;not null;default:'active';index;check:chk_commerce_skus_status,status IN ('active','inactive')" json:"status"`
	AttributesJSON     string `gorm:"type:text" json:"attributes,omitempty"`
}

// CommerceOptionGroup and CommerceOption represent constrained product
// choices (taste, size, add-ons, weight, colour, etc.).
type CommerceOptionGroup struct {
	Base
	TenantID      uint             `gorm:"not null;index:idx_commerce_option_groups_tenant_product" json:"tenant_id"`
	ProductID     uint             `gorm:"not null;index:idx_commerce_option_groups_tenant_product" json:"product_id"`
	Name          string           `gorm:"size:80;not null" json:"name"`
	Required      bool             `gorm:"not null;default:false" json:"required"`
	MinSelections int              `gorm:"not null;default:0;check:chk_commerce_option_groups_selection,min_selections >= 0 AND max_selections >= min_selections" json:"min_selections"`
	MaxSelections int              `gorm:"not null;default:1" json:"max_selections"`
	Options       []CommerceOption `gorm:"foreignKey:OptionGroupID" json:"options,omitempty"`
}

type CommerceOption struct {
	Base
	TenantID        uint   `gorm:"not null;index:idx_commerce_options_tenant_group" json:"tenant_id"`
	OptionGroupID   uint   `gorm:"not null;index:idx_commerce_options_tenant_group" json:"option_group_id"`
	Name            string `gorm:"size:80;not null" json:"name"`
	PriceDeltaCents int64  `gorm:"not null;default:0" json:"price_delta_cents"`
	Status          string `gorm:"size:20;not null;default:'active';check:chk_commerce_options_status,status IN ('active','inactive')" json:"status"`
}

// CommerceFulfillmentLocation is a restaurant store, pickup point, or retail
// warehouse. Orders snapshot the selected location identity.
type CommerceFulfillmentLocation struct {
	Base
	TenantID     uint   `gorm:"not null;index:idx_commerce_locations_tenant_domain" json:"tenant_id"`
	BusinessType string `gorm:"size:20;not null;index:idx_commerce_locations_tenant_domain;check:chk_commerce_locations_business_type,business_type IN ('restaurant','retail')" json:"business_type"`
	Name         string `gorm:"size:120;not null" json:"name"`
	LocationType string `gorm:"size:20;not null;default:'store';check:chk_commerce_locations_type,location_type IN ('store','warehouse','pickup')" json:"location_type"`
	Status       string `gorm:"size:20;not null;default:'active';check:chk_commerce_locations_status,status IN ('active','inactive')" json:"status"`
}

type CommerceInventory struct {
	Base
	TenantID     uint  `gorm:"not null;uniqueIndex:idx_commerce_inventory_scope" json:"tenant_id"`
	SkuID        uint  `gorm:"not null;uniqueIndex:idx_commerce_inventory_scope" json:"sku_id"`
	LocationID   uint  `gorm:"not null;uniqueIndex:idx_commerce_inventory_scope" json:"location_id"`
	AvailableQty int   `gorm:"not null;default:0;check:chk_commerce_inventory_nonnegative,available_qty >= 0 AND reserved_qty >= 0 AND sold_qty >= 0 AND released_qty >= 0" json:"available_qty"`
	ReservedQty  int   `gorm:"not null;default:0" json:"reserved_qty"`
	SoldQty      int   `gorm:"not null;default:0" json:"sold_qty"`
	ReleasedQty  int   `gorm:"not null;default:0" json:"released_qty"`
	Version      int64 `gorm:"not null;default:1" json:"version"`
}

type CommerceCart struct {
	Base
	TenantID          uint               `gorm:"not null;index:idx_commerce_carts_scope" json:"tenant_id"`
	BusinessType      string             `gorm:"size:20;not null;index:idx_commerce_carts_scope;check:chk_commerce_carts_business_type,business_type IN ('restaurant','retail')" json:"business_type"`
	CustomerID        string             `gorm:"size:100;not null;index:idx_commerce_carts_customer" json:"customer_id"`
	ChannelAccountID  uint               `gorm:"index" json:"channel_account_id,omitempty"`
	LocationID        uint               `gorm:"not null;index:idx_commerce_carts_scope" json:"location_id"`
	Status            string             `gorm:"size:20;not null;default:'active';index;check:chk_commerce_carts_status,status IN ('active','checked_out','abandoned')" json:"status"`
	CheckedOutOrderID uint               `gorm:"index" json:"checked_out_order_id,omitempty"`
	Items             []CommerceCartItem `gorm:"foreignKey:CartID" json:"items,omitempty"`
}

type CommerceCartItem struct {
	Base
	TenantID            uint   `gorm:"not null;index:idx_commerce_cart_items_cart" json:"tenant_id"`
	CartID              uint   `gorm:"not null;index:idx_commerce_cart_items_cart" json:"cart_id"`
	ProductID           uint   `gorm:"not null" json:"product_id"`
	SkuID               uint   `gorm:"not null" json:"sku_id"`
	Quantity            int    `gorm:"not null;check:chk_commerce_cart_items_quantity,quantity > 0" json:"quantity"`
	OptionsSnapshotJSON string `gorm:"type:text" json:"options_snapshot,omitempty"`
}

type CommerceOrder struct {
	Base
	TenantID              uint                       `gorm:"not null;index:idx_commerce_orders_scope;uniqueIndex:idx_commerce_orders_payment_reference,priority:1" json:"tenant_id"`
	OrderNo               string                     `gorm:"size:50;not null;uniqueIndex" json:"order_no"`
	IdempotencyKey        string                     `gorm:"size:100;not null;default:''" json:"-"`
	BusinessType          string                     `gorm:"size:20;not null;index:idx_commerce_orders_scope;check:chk_commerce_orders_business_type,business_type IN ('restaurant','retail')" json:"business_type"`
	Channel               string                     `gorm:"size:50;not null;default:'direct'" json:"channel"`
	CustomerID            string                     `gorm:"size:100;index" json:"customer_id"`
	LocationID            uint                       `gorm:"not null" json:"location_id"`
	OriginalAmountCents   int64                      `gorm:"not null;default:0;check:chk_commerce_orders_amounts,original_amount_cents >= 0 AND discount_cents >= 0 AND total_amount_cents >= 0" json:"original_amount_cents"`
	DiscountCents         int64                      `gorm:"not null;default:0" json:"discount_cents"`
	TotalAmountCents      int64                      `gorm:"not null;default:0" json:"total_amount_cents"`
	PaymentStatus         string                     `gorm:"size:20;not null;default:'unpaid';index;check:chk_commerce_orders_payment_status,payment_status IN ('unpaid','pending','paid','failed','refunded')" json:"payment_status"`
	FulfillmentStatus     string                     `gorm:"size:20;not null;default:'pending';index" json:"fulfillment_status"`
	RefundStatus          string                     `gorm:"size:20;not null;default:'none';index;check:chk_commerce_orders_refund_status,refund_status IN ('none','requested','processing','partial','refunded','rejected')" json:"refund_status"`
	ExpiresAt             *time.Time                 `json:"expires_at,omitempty"`
	PaidAt                *time.Time                 `json:"paid_at,omitempty"`
	PaymentReference      string                     `gorm:"size:120;uniqueIndex:idx_commerce_orders_payment_reference,priority:2" json:"payment_reference,omitempty"`
	ContactName           string                     `gorm:"size:80" json:"contact_name,omitempty"`
	ContactPhone          string                     `gorm:"size:30" json:"contact_phone,omitempty"`
	ShippingAddressJSON   string                     `gorm:"type:text" json:"shipping_address,omitempty"`
	Items                 []CommerceOrderItem        `gorm:"foreignKey:OrderID" json:"items,omitempty"`
	AfterSales            []CommerceAfterSaleRequest `gorm:"foreignKey:OrderID" json:"after_sales,omitempty"`
	RestaurantFulfillment *RestaurantFulfillment     `gorm:"foreignKey:OrderID" json:"restaurant_fulfillment,omitempty"`
	RetailFulfillment     *RetailFulfillment         `gorm:"foreignKey:OrderID" json:"retail_fulfillment,omitempty"`
}

// CommercePaymentReconciliationTask persists an ambiguous external payment
// attempt. The task is intentionally separate from the order payment status:
// a local timeout never proves that a provider payment failed, so inventory
// stays reserved until a provider-authoritative result is applied.
type CommercePaymentReconciliationTask struct {
	Base
	TenantID            uint       `gorm:"not null;uniqueIndex:idx_commerce_payment_reconciliation_order,priority:1;index:idx_commerce_payment_reconciliation_scope" json:"tenant_id"`
	OrderID             uint       `gorm:"not null;uniqueIndex:idx_commerce_payment_reconciliation_order,priority:2;index:idx_commerce_payment_reconciliation_scope" json:"order_id"`
	PaymentReference    string     `gorm:"size:120" json:"payment_reference,omitempty"`
	ProviderReference   string     `gorm:"size:120" json:"provider_reference,omitempty"`
	ProviderPaidAt      *time.Time `json:"provider_paid_at,omitempty"`
	ProviderAmountCents int64      `gorm:"not null;default:0;check:chk_commerce_payment_reconciliation_provider_amount,provider_amount_cents >= 0" json:"provider_amount_cents"`
	Status              string     `gorm:"size:20;not null;default:'pending';index;check:chk_commerce_payment_reconciliation_status,status IN ('pending','failed','completed','manual_review')" json:"status"`
	Attempts            int        `gorm:"not null;default:0;check:chk_commerce_payment_reconciliation_attempts,attempts >= 0" json:"attempts"`
	NextAttemptAt       *time.Time `json:"next_attempt_at,omitempty"`
	LastAttemptAt       *time.Time `json:"last_attempt_at,omitempty"`
	LastProviderState   string     `gorm:"size:40" json:"last_provider_state,omitempty"`
	LastError           string     `gorm:"size:500" json:"last_error,omitempty"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
}

// CommercePaymentAttempt is the immutable provider-attempt identity for a
// commercial order. It is deliberately separate from CommerceOrder's final
// payment projection: a request can time out after the provider accepted it,
// and a later callback or query must still find the same attempt.
type CommercePaymentAttempt struct {
	Base
	TenantID           uint       `gorm:"not null;uniqueIndex:idx_commerce_payment_attempt_request,priority:1;index:idx_commerce_payment_attempt_scope" json:"tenant_id"`
	OrderID            uint       `gorm:"not null;index:idx_commerce_payment_attempt_scope" json:"order_id"`
	ChannelAccountID   uint       `gorm:"not null;index:idx_commerce_payment_attempt_scope" json:"channel_account_id"`
	Provider           string     `gorm:"size:20;not null;default:'wechat'" json:"provider"`
	ClientRequestID    string     `gorm:"size:100;not null;uniqueIndex:idx_commerce_payment_attempt_request,priority:2" json:"-"`
	RequestFingerprint string     `gorm:"size:64;not null" json:"-"`
	OutTradeNo         string     `gorm:"size:64;not null;uniqueIndex" json:"out_trade_no"`
	AppID              string     `gorm:"size:120;not null" json:"app_id"`
	MchID              string     `gorm:"size:100;not null" json:"mch_id"`
	AmountCents        int64      `gorm:"not null;check:chk_commerce_payment_attempt_amount,amount_cents > 0" json:"amount_cents"`
	Currency           string     `gorm:"size:8;not null;default:'CNY'" json:"currency"`
	PayerSubjectHash   string     `gorm:"size:64;not null" json:"-"`
	PrepayID           string     `gorm:"size:160" json:"-"`
	ProviderReference  string     `gorm:"size:120" json:"provider_reference,omitempty"`
	ProviderState      string     `gorm:"size:40" json:"provider_state,omitempty"`
	Status             string     `gorm:"size:20;not null;default:'pending';index;check:chk_commerce_payment_attempt_status,status IN ('pending','paid','failed','unknown','manual_review')" json:"status"`
	LastError          string     `gorm:"size:500" json:"last_error,omitempty"`
	LastQueriedAt      *time.Time `json:"last_queried_at,omitempty"`
	NextQueryAt        *time.Time `gorm:"index" json:"next_query_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
}

// CommerceRefundAttempt tracks the provider refund transaction separately
// from the customer-facing after-sale request. Provider acceptance is not a
// local refund success; only a confirmed callback or query may complete it.
type CommerceRefundAttempt struct {
	Base
	TenantID         uint       `gorm:"not null;uniqueIndex:idx_commerce_refund_attempt_request,priority:1;index:idx_commerce_refund_attempt_scope" json:"tenant_id"`
	RequestID        uint       `gorm:"not null;uniqueIndex:idx_commerce_refund_attempt_request,priority:2;index:idx_commerce_refund_attempt_scope" json:"request_id"`
	OrderID          uint       `gorm:"not null;index:idx_commerce_refund_attempt_scope" json:"order_id"`
	Provider         string     `gorm:"size:20;not null;default:'wechat'" json:"provider"`
	OutRefundNo      string     `gorm:"size:64;not null;uniqueIndex" json:"out_refund_no"`
	ProviderRefundID string     `gorm:"size:120;uniqueIndex" json:"provider_refund_id,omitempty"`
	AmountCents      int64      `gorm:"not null;check:chk_commerce_refund_attempt_amount,amount_cents > 0" json:"amount_cents"`
	Status           string     `gorm:"size:20;not null;default:'processing';index;check:chk_commerce_refund_attempt_status,status IN ('processing','succeeded','failed','unknown','manual_review')" json:"status"`
	ProviderState    string     `gorm:"size:40" json:"provider_state,omitempty"`
	LastError        string     `gorm:"size:500" json:"last_error,omitempty"`
	LastQueriedAt    *time.Time `json:"last_queried_at,omitempty"`
	NextQueryAt      *time.Time `gorm:"index" json:"next_query_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

// CommercePaymentProviderEvent is the idempotency inbox for provider
// notifications. The provider event ID is unique per tenant/provider and the
// payload is retained for audit and retry diagnostics.
type CommercePaymentProviderEvent struct {
	Base
	TenantID    uint       `gorm:"not null;uniqueIndex:idx_commerce_payment_event_identity,priority:1;index" json:"tenant_id"`
	Provider    string     `gorm:"size:20;not null;uniqueIndex:idx_commerce_payment_event_identity,priority:2" json:"provider"`
	EventID     string     `gorm:"size:160;not null;uniqueIndex:idx_commerce_payment_event_identity,priority:3" json:"event_id"`
	EventType   string     `gorm:"size:80;not null" json:"event_type"`
	OutTradeNo  string     `gorm:"size:64" json:"out_trade_no,omitempty"`
	OutRefundNo string     `gorm:"size:64" json:"out_refund_no,omitempty"`
	PayloadJSON string     `gorm:"type:text;not null" json:"-"`
	Status      string     `gorm:"size:20;not null;default:'received';index;check:chk_commerce_payment_event_status,status IN ('received','processed','failed')" json:"status"`
	LastError   string     `gorm:"size:500" json:"last_error,omitempty"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

type CommerceOrderItem struct {
	Base
	TenantID               uint       `gorm:"not null;index:idx_commerce_order_items_order" json:"tenant_id"`
	OrderID                uint       `gorm:"not null;index:idx_commerce_order_items_order" json:"order_id"`
	ProductID              uint       `gorm:"not null" json:"product_id"`
	SkuID                  uint       `gorm:"not null" json:"sku_id"`
	ProductNameSnapshot    string     `gorm:"size:160;not null" json:"product_name"`
	SkuNameSnapshot        string     `gorm:"size:160;not null" json:"sku_name"`
	DescriptionSnapshot    string     `gorm:"type:text" json:"description_snapshot,omitempty"`
	OptionsSnapshotJSON    string     `gorm:"type:text" json:"options_snapshot,omitempty"`
	Quantity               int        `gorm:"not null;check:chk_commerce_order_items_quantity,quantity > 0" json:"quantity"`
	OriginalUnitPriceCents int64      `gorm:"not null;default:0" json:"original_unit_price_cents"`
	UnitPriceCents         int64      `gorm:"not null;default:0" json:"unit_price_cents"`
	DiscountCents          int64      `gorm:"not null;default:0" json:"discount_cents"`
	LineAmountCents        int64      `gorm:"not null;default:0" json:"line_amount_cents"`
	ReservationStatus      string     `gorm:"size:20;not null;default:'released';check:chk_commerce_order_items_reservation_status,reservation_status IN ('reserved','sold','released','refunded')" json:"reservation_status"`
	ReleasedAt             *time.Time `json:"released_at,omitempty"`
}

type RestaurantFulfillment struct {
	Base
	TenantID        uint       `gorm:"not null;index:idx_restaurant_fulfillment_scope" json:"tenant_id"`
	OrderID         uint       `gorm:"not null;uniqueIndex:idx_restaurant_fulfillment_order" json:"order_id"`
	LocationID      uint       `gorm:"not null;index:idx_restaurant_fulfillment_scope" json:"location_id"`
	Method          string     `gorm:"size:20;not null;check:chk_restaurant_fulfillment_method,method IN ('pickup','delivery')" json:"method"`
	Status          string     `gorm:"size:30;not null;default:'pending_acceptance';index;check:chk_restaurant_fulfillment_status,status IN ('pending_acceptance','accepted','preparing','ready','delivering','completed','cancelled')" json:"status"`
	AcceptedAt      *time.Time `json:"accepted_at,omitempty"`
	ReadyAt         *time.Time `json:"ready_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	AddressSnapshot string     `gorm:"type:text" json:"address_snapshot,omitempty"`
}

type RetailFulfillment struct {
	Base
	TenantID    uint       `gorm:"not null;index:idx_retail_fulfillment_scope" json:"tenant_id"`
	OrderID     uint       `gorm:"not null;uniqueIndex:idx_retail_fulfillment_order" json:"order_id"`
	LocationID  uint       `gorm:"not null;index:idx_retail_fulfillment_scope" json:"location_id"`
	Status      string     `gorm:"size:30;not null;default:'pending_shipment';index;check:chk_retail_fulfillment_status,status IN ('pending_shipment','shipped','in_transit','delivered','completed','cancelled')" json:"status"`
	Carrier     string     `gorm:"size:80" json:"carrier,omitempty"`
	TrackingNo  string     `gorm:"size:120" json:"tracking_no,omitempty"`
	ShippedAt   *time.Time `json:"shipped_at,omitempty"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
}

type CommerceAddress struct {
	Base
	TenantID      uint   `gorm:"not null;index:idx_commerce_addresses_customer" json:"tenant_id"`
	CustomerID    string `gorm:"size:100;not null;index:idx_commerce_addresses_customer" json:"customer_id"`
	AddressType   string `gorm:"size:20;not null;default:'SHIPPING';index:idx_commerce_addresses_customer;check:chk_commerce_addresses_type,address_type IN ('CAMPUS','SHIPPING')" json:"address_type"`
	RecipientName string `gorm:"size:80;not null" json:"recipient_name"`
	Phone         string `gorm:"size:30;not null" json:"phone"`
	Province      string `gorm:"size:40" json:"province,omitempty"`
	City          string `gorm:"size:40" json:"city,omitempty"`
	District      string `gorm:"size:40" json:"district,omitempty"`
	CampusName    string `gorm:"size:80" json:"campus_name,omitempty"`
	ZoneName      string `gorm:"size:80" json:"zone_name,omitempty"`
	Building      string `gorm:"size:80" json:"building,omitempty"`
	Room          string `gorm:"size:80" json:"room,omitempty"`
	Detail        string `gorm:"size:255;not null" json:"detail"`
	IsDefault     bool   `gorm:"not null;default:false" json:"is_default"`
}

type CommerceAfterSaleRequest struct {
	Base
	TenantID       uint   `gorm:"not null;uniqueIndex:idx_commerce_after_sales_idempotency,priority:1;uniqueIndex:idx_commerce_after_sales_provider_reference,priority:1;index:idx_commerce_after_sales_scope" json:"tenant_id"`
	OrderID        uint   `gorm:"not null;index:idx_commerce_after_sales_scope" json:"order_id"`
	OrderItemID    uint   `gorm:"index" json:"order_item_id,omitempty"`
	RequestNo      string `gorm:"size:60;not null;uniqueIndex:idx_commerce_after_sales_request_no" json:"request_no"`
	IdempotencyKey string `gorm:"size:100;not null;uniqueIndex:idx_commerce_after_sales_idempotency,priority:2" json:"-"`
	Type           string `gorm:"size:20;not null;default:'refund';check:chk_commerce_after_sales_type,type IN ('refund')" json:"type"`
	Status         string `gorm:"size:20;not null;default:'requested';index;check:chk_commerce_after_sales_status,status IN ('requested','approved','rejected','processing','completed','failed','cancelled')" json:"status"`
	AmountCents    int64  `gorm:"not null;default:0;check:chk_commerce_after_sales_amount,amount_cents > 0" json:"amount_cents"`
	// Provider refund facts are distinct from CommerceOrder.PaymentReference,
	// which identifies the original payment attempt. They are written only by
	// the trusted provider-confirmation boundary.
	ProviderRefundReference   string                   `gorm:"size:120;not null;default:'';uniqueIndex:idx_commerce_after_sales_provider_reference,priority:2" json:"provider_refund_reference,omitempty"`
	ProviderRefundAmountCents int64                    `gorm:"not null;default:0;check:chk_commerce_after_sales_provider_amount,provider_refund_amount_cents >= 0" json:"provider_refund_amount_cents,omitempty"`
	Reason                    string                   `gorm:"size:255" json:"reason,omitempty"`
	ProcessedAt               *time.Time               `json:"processed_at,omitempty"`
	Events                    []CommerceAfterSaleEvent `gorm:"foreignKey:RequestID" json:"events,omitempty"`
}

type CommerceAfterSaleEvent struct {
	Base
	TenantID    uint   `gorm:"not null;index:idx_commerce_after_sale_events_request" json:"tenant_id"`
	RequestID   uint   `gorm:"not null;index:idx_commerce_after_sale_events_request" json:"request_id"`
	EventType   string `gorm:"size:40;not null" json:"event_type"`
	PayloadJSON string `gorm:"type:text" json:"payload,omitempty"`
}
