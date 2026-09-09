package model

import "time"

// MiniappInstantDiscountActivity is the current account-scoped random discount
// policy. It is deliberately separate from channel product and settlement
// configuration: it can only reduce a customer's checkout total.
type MiniappInstantDiscountActivity struct {
	Base
	TenantID         uint                                    `gorm:"uniqueIndex:idx_miniapp_discount_account,priority:1;index;not null" json:"-"`
	ChannelAccountID uint                                    `gorm:"uniqueIndex:idx_miniapp_discount_account,priority:2;index;not null" json:"channel_account_id"`
	Enabled          bool                                    `gorm:"not null;default:false" json:"enabled"`
	MinDiscountCents int64                                   `gorm:"not null;default:0" json:"min_discount_cents"`
	MaxDiscountCents int64                                   `gorm:"not null;default:0" json:"max_discount_cents"`
	ValidityMinutes  int                                     `gorm:"not null;default:0" json:"validity_minutes"`
	CooldownDays     int                                     `gorm:"not null;default:0" json:"cooldown_days"`
	Mappings         []MiniappInstantDiscountActivityMapping `gorm:"foreignKey:ActivityID" json:"-"`
}

// MiniappInstantDiscountActivityMapping scopes an activity to an account-owned
// Xiaohongshu ticket mapping. It never copies product, fulfillment, or price.
type MiniappInstantDiscountActivityMapping struct {
	Base
	ActivityID              uint `gorm:"index;not null" json:"-"`
	ChannelProductMappingID uint `gorm:"index;not null" json:"mapping_id"`
}

// MiniappInstantDiscountGrant is append-only customer promotion history. The
// discount and eligibility times are snapshots; later activity edits never
// redraw or reset the customer's cooldown.
type MiniappInstantDiscountGrant struct {
	Base
	TenantID          uint       `gorm:"index;not null" json:"-"`
	ChannelAccountID  uint       `gorm:"index;not null" json:"-"`
	MiniappCustomerID uint       `gorm:"index;not null" json:"-"`
	ActivityID        uint       `gorm:"index;not null" json:"-"`
	DiscountCents     int64      `gorm:"not null" json:"discount_cents"`
	ObtainedAt        time.Time  `gorm:"not null" json:"obtained_at"`
	ExpiresAt         time.Time  `gorm:"not null;index" json:"expires_at"`
	NextEligibleAt    time.Time  `gorm:"not null;index" json:"next_eligible_at"`
	ReservedOrderID   uint       `gorm:"index;not null;default:0" json:"-"`
	ReservedAt        *time.Time `json:"-"`
	ConsumedAt        *time.Time `json:"-"`
}
