package model

import "time"

// MiniappCustomer is an end-customer identity scoped to one channel account.
// Official platform identifiers and session keys remain encrypted at rest;
// only hashes are used for lookup and bearer-session authentication.
type MiniappCustomer struct {
	Base
	TenantID         uint `gorm:"index;not null" json:"-"`
	ChannelAccountID uint `gorm:"uniqueIndex:idx_miniapp_customer_openid,priority:1;index;not null" json:"-"`
	// MemberID is populated only after the authenticated self-hosted identity
	// has been resolved by the tenant member service. Existing channel
	// customer/session fields remain the order-access authority.
	MemberID *uint `gorm:"index" json:"-"`
	// OrderMemberID is an in-memory authorization projection. A withdrawn or
	// anonymized member may still be shown their own profile/history, but must
	// not be written onto a new order. It is never persisted.
	OrderMemberID        *uint      `gorm:"-" json:"-"`
	OpenIDHash           string     `gorm:"size:64;uniqueIndex:idx_miniapp_customer_openid,priority:2;not null" json:"-"`
	OpenIDCiphertext     string     `gorm:"type:text;not null" json:"-"`
	SessionKeyCiphertext string     `gorm:"type:text;not null" json:"-"`
	SessionTokenHash     string     `gorm:"size:64;uniqueIndex;not null" json:"-"`
	SessionExpiresAt     time.Time  `gorm:"index;not null" json:"-"`
	Status               string     `gorm:"size:20;not null;default:'active';index" json:"-"`
	LastLoginAt          time.Time  `gorm:"not null" json:"-"`
	LastSeenAt           *time.Time `json:"-"`
}
