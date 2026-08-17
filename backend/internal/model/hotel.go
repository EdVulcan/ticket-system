package model

import "time"

// HotelProperty is a supplier-owned accommodation property. It is deliberately
// separate from ScenicArea so hotel inventory can never be consumed as tickets.
type HotelProperty struct {
	Base
	TenantID     uint   `gorm:"not null;index" json:"tenant_id"`
	Code         string `gorm:"size:50;not null" json:"code"`
	Name         string `gorm:"size:120;not null" json:"name"`
	Address      string `gorm:"size:255" json:"address"`
	ContactName  string `gorm:"size:50" json:"contact_name"`
	ContactPhone string `gorm:"size:30" json:"contact_phone"`
	CheckInTime  string `gorm:"size:5;not null;default:'14:00'" json:"check_in_time"`
	CheckOutTime string `gorm:"size:5;not null;default:'12:00'" json:"check_out_time"`
	Status       string `gorm:"size:20;not null;default:'active';index;check:chk_hotel_properties_status,status IN ('active','suspended')" json:"status"`
}

// HotelRoomType is the physical inventory pool. All rate plans of the same
// room type share one daily capacity, which prevents cross-rate overselling.
type HotelRoomType struct {
	Base
	TenantID    uint   `gorm:"not null;index" json:"tenant_id"`
	HotelID     uint   `gorm:"not null;index" json:"hotel_id"`
	Code        string `gorm:"size:50;not null" json:"code"`
	Name        string `gorm:"size:100;not null" json:"name"`
	MaxGuests   int    `gorm:"not null;default:2" json:"max_guests"`
	BedType     string `gorm:"size:100" json:"bed_type"`
	Description string `gorm:"size:500" json:"description"`
	Status      string `gorm:"size:20;not null;default:'active';index;check:chk_hotel_room_types_status,status IN ('active','suspended')" json:"status"`
}

// HotelRatePlan carries the sell and settlement price for a room type. Capacity
// remains on HotelRoomInventory, not here.
type HotelRatePlan struct {
	Base
	TenantID             uint   `gorm:"not null;index" json:"tenant_id"`
	HotelID              uint   `gorm:"not null;index" json:"hotel_id"`
	RoomTypeID           uint   `gorm:"not null;index" json:"room_type_id"`
	Code                 string `gorm:"size:50;not null" json:"code"`
	Name                 string `gorm:"size:100;not null" json:"name"`
	RetailPriceCents     int64  `gorm:"not null" json:"retail_price_cents"`
	SettlementPriceCents int64  `gorm:"not null" json:"settlement_price_cents"`
	BreakfastCount       int    `gorm:"not null;default:0" json:"breakfast_count"`
	CancellationPolicy   string `gorm:"size:500" json:"cancellation_policy"`
	Status               string `gorm:"size:20;not null;default:'active';index;check:chk_hotel_rate_plans_status,status IN ('active','suspended')" json:"status"`
}

// HotelRatePlanPrice stores an optional stay-date override for a rate plan.
// An absent row means the rate plan's base prices apply for that date.
type HotelRatePlanPrice struct {
	Base
	TenantID             uint      `gorm:"uniqueIndex:idx_hotel_rate_plan_prices_scope,priority:1;not null;index" json:"tenant_id"`
	HotelID              uint      `gorm:"uniqueIndex:idx_hotel_rate_plan_prices_scope,priority:2;not null;index" json:"hotel_id"`
	RoomTypeID           uint      `gorm:"uniqueIndex:idx_hotel_rate_plan_prices_scope,priority:3;not null;index" json:"room_type_id"`
	RatePlanID           uint      `gorm:"uniqueIndex:idx_hotel_rate_plan_prices_scope,priority:4;not null;index" json:"rate_plan_id"`
	StayDate             time.Time `gorm:"type:date;uniqueIndex:idx_hotel_rate_plan_prices_scope,priority:5;not null;index" json:"stay_date"`
	RetailPriceCents     int64     `gorm:"not null" json:"retail_price_cents"`
	SettlementPriceCents int64     `gorm:"not null" json:"settlement_price_cents"`
}

// HotelRoomInventory stores availability for one room type and one stay date.
// Reserved is held inventory; Sold is confirmed inventory.
type HotelRoomInventory struct {
	Base
	TenantID   uint      `gorm:"uniqueIndex:idx_hotel_room_inventory,priority:1;not null;index" json:"tenant_id"`
	HotelID    uint      `gorm:"uniqueIndex:idx_hotel_room_inventory,priority:2;not null;index" json:"hotel_id"`
	RoomTypeID uint      `gorm:"uniqueIndex:idx_hotel_room_inventory,priority:3;not null;index" json:"room_type_id"`
	StayDate   time.Time `gorm:"type:date;uniqueIndex:idx_hotel_room_inventory,priority:4;not null;index" json:"stay_date"`
	Capacity   int       `gorm:"not null" json:"capacity"`
	Reserved   int       `gorm:"not null;default:0" json:"reserved"`
	Sold       int       `gorm:"not null;default:0" json:"sold"`
	Closed     bool      `gorm:"not null;default:false" json:"closed"`
}
