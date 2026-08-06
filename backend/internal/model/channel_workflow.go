package model

import "time"

// ChannelReservation models the channel-facing pre-order lock separately
// from the sales order. It prevents an OTA timeout from being mistaken for a
// customer cancellation and gives operators a durable compensation record.
type ChannelReservation struct {
	Base
	TenantID         uint       `gorm:"index;not null" json:"tenant_id"`
	ChannelAccountID uint       `gorm:"index;uniqueIndex:idx_channel_reservation_external,priority:1;not null" json:"channel_account_id"`
	ExternalNo       string     `gorm:"size:100;uniqueIndex:idx_channel_reservation_external,priority:2;not null" json:"external_no"`
	ProductID        uint       `gorm:"index;not null" json:"product_id"`
	UseDate          *time.Time `gorm:"type:date" json:"use_date,omitempty"`
	StockSlot        string     `gorm:"size:50" json:"stock_slot,omitempty"`
	Quantity         int        `gorm:"not null" json:"quantity"`
	Status           string     `gorm:"size:20;not null;default:'held';index" json:"status"` // held, converted, released, expired
	Environment      string     `gorm:"size:20;not null;default:'production';index" json:"environment"`
	ExpiresAt        time.Time  `gorm:"index;not null" json:"expires_at"`
	OrderNo          string     `gorm:"size:50;index" json:"order_no,omitempty"`
}
