package model

import "time"

// XiaohongshuOrderOperation is the durable boundary around the ordinary
// Xiaohongshu order upsert. The encrypted request is committed before the
// remote call; a recorded remote success can therefore be finalized locally
// after a process restart without inventing a payment or cancelling the order.
type XiaohongshuOrderOperation struct {
	Base
	TenantID                 uint       `gorm:"index;not null" json:"tenant_id"`
	ChannelAccountID         uint       `gorm:"index;not null" json:"channel_account_id"`
	XiaohongshuOrderLinkID   uint       `gorm:"uniqueIndex;not null" json:"xiaohongshu_order_link_id"`
	RequestPayloadCiphertext string     `gorm:"type:text;not null" json:"-"`
	PlatformOrderID          string     `gorm:"size:100" json:"platform_order_id,omitempty"`
	PayTokenCiphertext       string     `gorm:"type:text" json:"-"`
	PayTokenExpiresAt        *time.Time `json:"pay_token_expires_at,omitempty"`
	Status                   string     `gorm:"size:30;not null;default:'pending';index" json:"status"`
	AttemptCount             int        `gorm:"not null;default:0" json:"attempt_count"`
	NextAttemptAt            *time.Time `gorm:"index" json:"next_attempt_at,omitempty"`
	LastError                string     `gorm:"size:500" json:"last_error,omitempty"`
	CompletedAt              *time.Time `json:"completed_at,omitempty"`
}
