package model

import "time"

// ScenicHotelPackage attaches one fixed hotel stay to an existing online
// ticket product. One product unit is one package unit; the ticket product can
// itself represent a family/double ticket through its normal admission rule.
type ScenicHotelPackage struct {
	Base
	TenantID                  uint   `gorm:"not null;index" json:"tenant_id"`
	ProductID                 uint   `gorm:"not null" json:"product_id"`
	HotelID                   uint   `gorm:"not null;index" json:"hotel_id"`
	RoomTypeID                uint   `gorm:"not null;index" json:"room_type_id"`
	RatePlanID                uint   `gorm:"not null;index" json:"rate_plan_id"`
	Nights                    int    `gorm:"not null;default:1" json:"nights"`
	RoomsPerPackage           int    `gorm:"not null;default:1" json:"rooms_per_package"`
	HotelSettlementPriceCents int64  `gorm:"not null" json:"hotel_settlement_price_cents"`
	Status                    string `gorm:"size:20;not null;default:'offline';index;check:chk_scenic_hotel_packages_status,status IN ('online','offline')" json:"status"`
}

// HotelReservation is the accommodation fulfillment fact for one sold
// package unit. TicketID gives refund operations an exact one-to-one handle.
type HotelReservation struct {
	Base
	ReservationNo        string     `gorm:"size:50;uniqueIndex;not null" json:"reservation_no"`
	SalesTenantID        uint       `gorm:"not null;index" json:"sales_tenant_id"`
	SupplierTenantID     uint       `gorm:"not null;index" json:"supplier_tenant_id"`
	OrderID              uint       `gorm:"not null;index" json:"order_id"`
	OrderItemID          uint       `gorm:"not null;index" json:"order_item_id"`
	TicketID             uint       `gorm:"not null;uniqueIndex;index" json:"ticket_id"`
	PackageID            uint       `gorm:"not null;index" json:"package_id"`
	HotelID              uint       `gorm:"not null;index" json:"hotel_id"`
	RoomTypeID           uint       `gorm:"not null;index" json:"room_type_id"`
	RatePlanID           uint       `gorm:"not null;index" json:"rate_plan_id"`
	HotelName            string     `gorm:"size:120;not null" json:"hotel_name"`
	RoomTypeName         string     `gorm:"size:100;not null" json:"room_type_name"`
	RatePlanName         string     `gorm:"size:100;not null" json:"rate_plan_name"`
	CheckInDate          time.Time  `gorm:"type:date;not null;index" json:"check_in_date"`
	CheckOutDate         time.Time  `gorm:"type:date;not null" json:"check_out_date"`
	Rooms                int        `gorm:"not null" json:"rooms"`
	SettlementPriceCents int64      `gorm:"not null" json:"settlement_price_cents"`
	Status               string     `gorm:"size:20;not null;default:'reserved';index;check:chk_hotel_reservations_status,status IN ('reserved','confirmed','checked_in','checked_out','no_show','cancelled','refunded')" json:"status"`
	CheckedInAt          *time.Time `json:"checked_in_at,omitempty"`
	CheckedOutAt         *time.Time `json:"checked_out_at,omitempty"`
	NoShowAt             *time.Time `json:"no_show_at,omitempty"`
}
