package model

import "time"

type POSHoldLine struct {
	ProductID uint `json:"product_id"`
	Quantity  int  `json:"quantity"`
}

// POSHold is a durable cashier draft. It intentionally stores the selected
// product identifiers and quantities, not a client-controlled price snapshot.
// Prices, availability and tenant ownership are revalidated when the hold is
// resumed into a real order.
type POSHold struct {
	Base
	TenantID       uint       `gorm:"index;not null" json:"tenant_id"`
	DeviceID       uint       `gorm:"index;not null" json:"device_id"`
	OperatorID     uint       `gorm:"index;not null" json:"operator_id"`
	HoldNo         string     `gorm:"size:80;uniqueIndex;not null" json:"hold_no"`
	Status         string     `gorm:"size:20;not null;default:'held';index" json:"status"` // held, resumed, cancelled, expired
	ItemsJSON      string     `gorm:"type:text;not null" json:"items_json"`
	ContactName    string     `gorm:"size:50" json:"contact_name"`
	ContactPhone   string     `gorm:"size:20" json:"contact_phone"`
	TotalCents     int64      `gorm:"not null;default:0" json:"total_cents"`
	ExpiresAt      time.Time  `gorm:"index;not null" json:"expires_at"`
	ResumedOrderNo string     `gorm:"size:80;index" json:"resumed_order_no,omitempty"`
	CancelledAt    *time.Time `json:"cancelled_at,omitempty"`
	Notes          string     `gorm:"size:255" json:"notes,omitempty"`
}
