package model

import "time"

// HotelProduct is a hotel product's catalog profile. It reuses Product as the
// stable sale and channel identity, while keeping accommodation fulfillment
// separate from tickets, ticket entitlements, and scenic-area verification.
type HotelProduct struct {
	Base
	ProductID                uint   `gorm:"not null;index" json:"product_id"`
	TenantID                 uint   `gorm:"not null;index" json:"tenant_id"`
	HotelID                  uint   `gorm:"not null;index" json:"hotel_id"`
	RoomTypeID               uint   `gorm:"not null;index" json:"room_type_id"`
	RatePlanID               uint   `gorm:"not null;index" json:"rate_plan_id"`
	SaleMode                 string `gorm:"size:30;not null;default:'calendar_room';index;check:chk_hotel_products_sale_mode,sale_mode IN ('calendar_room','presale_room')" json:"sale_mode"`
	BaseRetailPriceCents     int64  `gorm:"not null" json:"base_retail_price_cents"`
	BaseSettlementPriceCents int64  `gorm:"not null" json:"base_settlement_price_cents"`
	Nights                   int    `gorm:"not null;default:1" json:"nights"`
	RoomsPerPackage          int    `gorm:"not null;default:1" json:"rooms_per_package"`
	VoucherValidityDays      int    `gorm:"not null;default:0" json:"voucher_validity_days"`
	MinAdvanceDays           int    `gorm:"not null;default:0" json:"min_advance_days"`
	MaxReschedules           int    `gorm:"not null;default:0" json:"max_reschedules"`
	Status                   string `gorm:"size:20;not null;default:'offline';index;check:chk_hotel_products_status,status IN ('online','offline')" json:"status"`
	CurrentRevisionID        uint   `gorm:"index" json:"current_revision_id"`
}

// HotelProductRevision freezes the accommodation resource and money facts
// sold by a hotel product. Calendar prices are revision-bound so later edits
// cannot rewrite a completed order's price source.
type HotelProductRevision struct {
	Base
	HotelProductID           uint   `gorm:"uniqueIndex:idx_hotel_product_revision,priority:1;not null;index" json:"hotel_product_id"`
	TenantID                 uint   `gorm:"not null;index" json:"tenant_id"`
	ProductID                uint   `gorm:"not null;index" json:"product_id"`
	Version                  int    `gorm:"uniqueIndex:idx_hotel_product_revision,priority:2;not null" json:"version"`
	HotelID                  uint   `gorm:"not null;index" json:"hotel_id"`
	RoomTypeID               uint   `gorm:"not null;index" json:"room_type_id"`
	RatePlanID               uint   `gorm:"not null;index" json:"rate_plan_id"`
	SaleMode                 string `gorm:"size:30;not null;check:chk_hotel_product_revisions_sale_mode,sale_mode IN ('calendar_room','presale_room')" json:"sale_mode"`
	BaseRetailPriceCents     int64  `gorm:"not null" json:"base_retail_price_cents"`
	BaseSettlementPriceCents int64  `gorm:"not null" json:"base_settlement_price_cents"`
	Nights                   int    `gorm:"not null" json:"nights"`
	RoomsPerPackage          int    `gorm:"not null" json:"rooms_per_package"`
	VoucherValidityDays      int    `gorm:"not null" json:"voucher_validity_days"`
	MinAdvanceDays           int    `gorm:"not null" json:"min_advance_days"`
	MaxReschedules           int    `gorm:"not null" json:"max_reschedules"`
}

// HotelProductCalendarPrice is a product-sale price, not a shared hotel rate
// plan price. It is only valid for a calendar-room revision. No row means the
// revision's base price applies for the stay date.
type HotelProductCalendarPrice struct {
	Base
	TenantID               uint      `gorm:"not null;index" json:"tenant_id"`
	HotelProductID         uint      `gorm:"not null;index" json:"hotel_product_id"`
	HotelProductRevisionID uint      `gorm:"not null;index" json:"hotel_product_revision_id"`
	StayDate               time.Time `gorm:"type:date;not null;index" json:"stay_date"`
	RetailPriceCents       int64     `gorm:"not null" json:"retail_price_cents"`
	SettlementPriceCents   int64     `gorm:"not null" json:"settlement_price_cents"`
}

// HotelProductEntitlement is one independently fulfillable accommodation unit
// sold by a hotel product. It intentionally has no TicketID: accommodation
// fulfillment must not manufacture scenic ticket or verification facts.
type HotelProductEntitlement struct {
	Base
	EntitlementNo          string     `gorm:"size:50;uniqueIndex;not null" json:"entitlement_no"`
	SalesTenantID          uint       `gorm:"not null;index" json:"sales_tenant_id"`
	SupplierTenantID       uint       `gorm:"not null;index" json:"supplier_tenant_id"`
	OrderID                uint       `gorm:"not null;index" json:"order_id"`
	OrderItemID            uint       `gorm:"not null;index" json:"order_item_id"`
	HotelProductID         uint       `gorm:"not null;index" json:"hotel_product_id"`
	HotelProductRevisionID uint       `gorm:"not null;index" json:"hotel_product_revision_id"`
	ReservationID          uint       `gorm:"not null;default:0;index" json:"reservation_id,omitempty"`
	CheckInDate            *time.Time `gorm:"type:date" json:"check_in_date,omitempty"`
	CheckOutDate           *time.Time `gorm:"type:date" json:"check_out_date,omitempty"`
	Rooms                  int        `gorm:"not null;default:1" json:"rooms"`
	HotelName              string     `gorm:"size:120" json:"hotel_name"`
	RoomTypeName           string     `gorm:"size:100" json:"room_type_name"`
	RatePlanName           string     `gorm:"size:100" json:"rate_plan_name"`
	GuestName              string     `gorm:"size:100" json:"guest_name"`
	ContactPhone           string     `gorm:"size:30" json:"contact_phone"`
	RetailPriceCents       int64      `gorm:"not null;default:0" json:"retail_price_cents"`
	SettlementPriceCents   int64      `gorm:"not null;default:0" json:"settlement_price_cents"`
	PriceSource            string     `gorm:"size:30" json:"price_source,omitempty"`
	Status                 string     `gorm:"size:30;not null;default:'pending_booking';index;check:chk_hotel_product_entitlements_status,status IN ('pending_booking','booking_pending','booked','cancel_pending','cancelled','refunded','expired')" json:"status"`
	ValidFrom              time.Time  `gorm:"not null;index" json:"valid_from"`
	ValidUntil             time.Time  `gorm:"not null;index" json:"valid_until"`
	RescheduleCount        int        `gorm:"not null;default:0" json:"reschedule_count"`
	ClientRequestID        string     `gorm:"size:100;index" json:"client_request_id,omitempty"`
	ExternalBookOrderID    string     `gorm:"size:100;index" json:"external_book_order_id,omitempty"`
	PlatformBookID         string     `gorm:"size:100;index" json:"platform_book_id,omitempty"`
	BookedAt               *time.Time `json:"booked_at,omitempty"`
	BookingCancelledAt     *time.Time `json:"booking_cancelled_at,omitempty"`
}

// HotelProductReservation is the immutable accommodation reservation snapshot
// for one hotel-product entitlement. It remains intentionally below PMS scope:
// no room assignment, guest-profile management, front-desk or room-state data.
type HotelProductReservation struct {
	Base
	ReservationNo          string     `gorm:"size:50;uniqueIndex;not null" json:"reservation_no"`
	SalesTenantID          uint       `gorm:"not null;index" json:"sales_tenant_id"`
	SupplierTenantID       uint       `gorm:"not null;index" json:"supplier_tenant_id"`
	OrderID                uint       `gorm:"not null;index" json:"order_id"`
	OrderItemID            uint       `gorm:"not null;index" json:"order_item_id"`
	EntitlementID          uint       `gorm:"not null;index" json:"entitlement_id"`
	HotelProductID         uint       `gorm:"not null;index" json:"hotel_product_id"`
	HotelProductRevisionID uint       `gorm:"not null;index" json:"hotel_product_revision_id"`
	HotelID                uint       `gorm:"not null;index" json:"hotel_id"`
	RoomTypeID             uint       `gorm:"not null;index" json:"room_type_id"`
	RatePlanID             uint       `gorm:"not null;index" json:"rate_plan_id"`
	HotelName              string     `gorm:"size:120;not null" json:"hotel_name"`
	RoomTypeName           string     `gorm:"size:100;not null" json:"room_type_name"`
	RatePlanName           string     `gorm:"size:100;not null" json:"rate_plan_name"`
	CheckInDate            time.Time  `gorm:"type:date;not null;index" json:"check_in_date"`
	CheckOutDate           time.Time  `gorm:"type:date;not null" json:"check_out_date"`
	Rooms                  int        `gorm:"not null" json:"rooms"`
	GuestName              string     `gorm:"size:100" json:"guest_name"`
	ContactPhone           string     `gorm:"size:30" json:"contact_phone"`
	RetailPriceCents       int64      `gorm:"not null" json:"retail_price_cents"`
	SettlementPriceCents   int64      `gorm:"not null" json:"settlement_price_cents"`
	PriceSource            string     `gorm:"size:30;not null;check:chk_hotel_product_reservations_price_source,price_source IN ('base','calendar')" json:"price_source"`
	ExternalBookOrderID    string     `gorm:"size:100;index" json:"external_book_order_id,omitempty"`
	PlatformBookID         string     `gorm:"size:100;index" json:"platform_book_id,omitempty"`
	Status                 string     `gorm:"size:20;not null;default:'reserved';index;check:chk_hotel_product_reservations_status,status IN ('reserved','confirmed','checked_in','checked_out','no_show','cancelled','refunded')" json:"status"`
	CheckedInAt            *time.Time `json:"checked_in_at,omitempty"`
	CheckedOutAt           *time.Time `json:"checked_out_at,omitempty"`
	NoShowAt               *time.Time `json:"no_show_at,omitempty"`
}
