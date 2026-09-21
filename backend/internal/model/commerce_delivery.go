package model

import "time"

// CommerceLocationServiceConfig is the immutable-at-checkout configuration
// for how a commercial fulfillment location accepts orders. It is scoped by
// tenant, location and business domain; it never changes ticketing facts.
type CommerceLocationServiceConfig struct {
	Base
	TenantID                   uint   `gorm:"not null;uniqueIndex:idx_commerce_location_service_scope,priority:1;index" json:"tenant_id"`
	LocationID                 uint   `gorm:"not null;uniqueIndex:idx_commerce_location_service_scope,priority:2;index" json:"location_id"`
	BusinessType               string `gorm:"size:20;not null;uniqueIndex:idx_commerce_location_service_scope,priority:3;check:chk_commerce_location_service_business_type,business_type IN ('restaurant','retail')" json:"business_type"`
	PickupEnabled              bool   `gorm:"not null;default:false" json:"pickup_enabled"`
	DeliveryEnabled            bool   `gorm:"not null;default:false" json:"delivery_enabled"`
	ShippingEnabled            bool   `gorm:"not null;default:false" json:"shipping_enabled"`
	MinGoodsCents              int64  `gorm:"not null;default:0;check:chk_commerce_location_service_min_goods,min_goods_cents >= 0" json:"min_goods_cents"`
	PackagingFeeCents          int64  `gorm:"not null;default:0;check:chk_commerce_location_service_packaging_fee,packaging_fee_cents >= 0" json:"packaging_fee_cents"`
	ShippingFeeCents           int64  `gorm:"not null;default:0;check:chk_commerce_location_service_shipping_fee,shipping_fee_cents >= 0" json:"shipping_fee_cents"`
	FreeShippingThresholdCents int64  `gorm:"not null;default:0;check:chk_commerce_location_service_free_shipping,free_shipping_threshold_cents >= 0" json:"free_shipping_threshold_cents"`
	EstimatedMinutes           int    `gorm:"not null;default:0;check:chk_commerce_location_service_estimated,estimated_minutes >= 0" json:"estimated_minutes"`
	Status                     string `gorm:"size:20;not null;default:'active';index;check:chk_commerce_location_service_status,status IN ('active','inactive')" json:"status"`
	ContactName                string `gorm:"size:120" json:"contact_name,omitempty"`
	ContactPhone               string `gorm:"size:40" json:"contact_phone,omitempty"`
	Address                    string `gorm:"size:500" json:"address,omitempty"`
	ConfigVersion              int64  `gorm:"not null;default:1" json:"config_version"`
}

// CommerceDeliveryZone is an explicit merchant-configured service region.
// v1 intentionally does not infer fees from maps or geocoding.
type CommerceDeliveryZone struct {
	Base
	TenantID              uint   `gorm:"not null;index:idx_commerce_delivery_zone_scope,priority:1" json:"tenant_id"`
	LocationID            uint   `gorm:"not null;index:idx_commerce_delivery_zone_scope,priority:2" json:"location_id"`
	Name                  string `gorm:"size:120;not null" json:"name"`
	Province              string `gorm:"size:80;not null" json:"province"`
	City                  string `gorm:"size:80;not null" json:"city"`
	District              string `gorm:"size:80;not null" json:"district"`
	FeeCents              int64  `gorm:"not null;default:0;check:chk_commerce_delivery_zone_fee,fee_cents >= 0" json:"fee_cents"`
	MinGoodsCentsOverride *int64 `gorm:"check:chk_commerce_delivery_zone_min_goods,min_goods_cents_override IS NULL OR min_goods_cents_override >= 0" json:"min_goods_cents_override,omitempty"`
	EstimatedMinutes      int    `gorm:"not null;default:0;check:chk_commerce_delivery_zone_estimated,estimated_minutes >= 0" json:"estimated_minutes"`
	Status                string `gorm:"size:20;not null;default:'active';index;check:chk_commerce_delivery_zone_status,status IN ('active','inactive')" json:"status"`
	SortOrder             int    `gorm:"not null;default:0" json:"sort_order"`
}

// CommerceDeliverySlot describes a recurring weekday window. A selected
// calendar date is stored in a quote to turn this recurrence into a concrete
// service promise.
type CommerceDeliverySlot struct {
	Base
	TenantID           uint   `gorm:"not null;index:idx_commerce_delivery_slot_scope,priority:1" json:"tenant_id"`
	LocationID         uint   `gorm:"not null;index:idx_commerce_delivery_slot_scope,priority:2" json:"location_id"`
	ZoneID             *uint  `gorm:"index" json:"zone_id,omitempty"`
	DayOfWeek          int    `gorm:"not null;check:chk_commerce_delivery_slot_weekday,day_of_week BETWEEN 0 AND 6" json:"day_of_week"`
	StartMinute        int    `gorm:"not null;check:chk_commerce_delivery_slot_start,start_minute BETWEEN 0 AND 1439" json:"start_minute"`
	EndMinute          int    `gorm:"not null;check:chk_commerce_delivery_slot_end,end_minute BETWEEN 1 AND 1440" json:"end_minute"`
	OrderCutoffMinutes int    `gorm:"not null;default:0;check:chk_commerce_delivery_slot_cutoff,order_cutoff_minutes BETWEEN 0 AND 1440" json:"order_cutoff_minutes"`
	Capacity           int    `gorm:"not null;default:0;check:chk_commerce_delivery_slot_capacity,capacity >= 0" json:"capacity"`
	Status             string `gorm:"size:20;not null;default:'active';index;check:chk_commerce_delivery_slot_status,status IN ('active','inactive')" json:"status"`
}

// CommerceCheckoutQuote is a short-lived server quote. TokenHash is the only
// persisted token material; the raw token is returned once to the caller.
type CommerceCheckoutQuote struct {
	Base
	TenantID            uint       `gorm:"not null;index:idx_commerce_checkout_quote_scope" json:"tenant_id"`
	ChannelAccountID    uint       `gorm:"not null;index:idx_commerce_checkout_quote_scope" json:"channel_account_id"`
	BusinessType        string     `gorm:"size:20;not null;index:idx_commerce_checkout_quote_scope;check:chk_commerce_checkout_quote_business_type,business_type IN ('restaurant','retail')" json:"business_type"`
	CustomerHash        string     `gorm:"size:64;not null;index:idx_commerce_checkout_quote_scope" json:"-"`
	LocationID          uint       `gorm:"not null;index:idx_commerce_checkout_quote_scope" json:"location_id"`
	AddressSnapshotJSON string     `gorm:"type:text;not null;default:''" json:"-"`
	FulfillmentMethod   string     `gorm:"size:20;not null;check:chk_commerce_checkout_quote_method,fulfillment_method IN ('pickup','delivery','shipping')" json:"fulfillment_method"`
	ZoneID              *uint      `gorm:"index" json:"zone_id,omitempty"`
	SlotID              *uint      `gorm:"index" json:"slot_id,omitempty"`
	SlotDate            *time.Time `json:"slot_date,omitempty"`
	GoodsSubtotalCents  int64      `gorm:"not null;check:chk_commerce_checkout_quote_goods,goods_subtotal_cents >= 0" json:"goods_subtotal_cents"`
	PackagingFeeCents   int64      `gorm:"not null;check:chk_commerce_checkout_quote_packaging,packaging_fee_cents >= 0" json:"packaging_fee_cents"`
	DeliveryFeeCents    int64      `gorm:"not null;check:chk_commerce_checkout_quote_delivery,delivery_fee_cents >= 0" json:"delivery_fee_cents"`
	ShippingFeeCents    int64      `gorm:"not null;check:chk_commerce_checkout_quote_shipping,shipping_fee_cents >= 0" json:"shipping_fee_cents"`
	DiscountCents       int64      `gorm:"not null;default:0;check:chk_commerce_checkout_quote_discount,discount_cents >= 0" json:"discount_cents"`
	TotalCents          int64      `gorm:"not null;check:chk_commerce_checkout_quote_total,total_cents >= 0" json:"total_cents"`
	Status              string     `gorm:"size:20;not null;default:'active';index;check:chk_commerce_checkout_quote_status,status IN ('active','consumed','expired','cancelled')" json:"status"`
	TokenHash           string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	ExpiresAt           time.Time  `gorm:"not null;index" json:"expires_at"`
	ConsumedOrderID     *uint      `gorm:"index" json:"consumed_order_id,omitempty"`
	ConfigVersion       int64      `gorm:"not null;default:1" json:"config_version"`
}

// CommerceOrderAdjustment is an immutable order-time charge/discount fact.
type CommerceOrderAdjustment struct {
	Base
	TenantID     uint   `gorm:"not null;index:idx_commerce_order_adjustment_scope" json:"tenant_id"`
	OrderID      uint   `gorm:"not null;index:idx_commerce_order_adjustment_scope" json:"order_id"`
	Kind         string `gorm:"size:30;not null;check:chk_commerce_order_adjustment_kind,kind IN ('delivery_fee','packaging_fee','shipping_fee','coupon_discount')" json:"kind"`
	AmountCents  int64  `gorm:"not null;check:chk_commerce_order_adjustment_amount,amount_cents >= 0" json:"amount_cents"`
	Description  string `gorm:"size:255;not null" json:"description"`
	SnapshotJSON string `gorm:"type:text" json:"snapshot_json,omitempty"`
}
