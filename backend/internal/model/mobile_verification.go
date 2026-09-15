package model

import "time"

// MobileVerificationSession binds a short-lived browser session to one
// authenticated operator, checkpoint, and handheld device. The raw token is
// never persisted; only its SHA-256 digest is stored.
type MobileVerificationSession struct {
	Base
	TenantID     uint       `gorm:"index;not null" json:"tenant_id"`
	StaffID      uint       `gorm:"index;not null" json:"staff_id"`
	DeviceID     uint       `gorm:"index;not null" json:"device_id"`
	CheckPointID uint       `gorm:"index;not null" json:"check_point_id"`
	ScenicAreaID uint       `gorm:"index;not null" json:"scenic_area_id"`
	TokenHash    string     `gorm:"size:64;uniqueIndex;not null" json:"-"`
	Status       string     `gorm:"size:20;not null;default:'active';index" json:"status"` // active, revoked, expired
	ExpiresAt    time.Time  `gorm:"index;not null" json:"expires_at"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
}

// MobileVerificationPreview is a short-lived, server-generated description of
// a ticket that an operator may explicitly confirm. It never consumes a ticket.
type MobileVerificationPreview struct {
	Base
	PreviewID                  string    `gorm:"size:64;uniqueIndex;not null" json:"preview_id"`
	TenantID                   uint      `gorm:"index;not null" json:"tenant_id"`
	StaffID                    uint      `gorm:"index;not null" json:"staff_id"`
	SessionID                  uint      `gorm:"index;not null" json:"session_id"`
	DeviceID                   uint      `gorm:"index;not null" json:"device_id"`
	CheckPointID               uint      `gorm:"index;not null" json:"check_point_id"`
	ScenicAreaID               uint      `gorm:"index;not null" json:"scenic_area_id"`
	TicketCode                 string    `gorm:"size:100;not null" json:"-"`
	ProductName                string    `gorm:"size:100;not null" json:"product_name"`
	CodeMode                   string    `gorm:"size:20;not null" json:"code_mode"`
	BatchAllowed               bool      `gorm:"not null;default:false" json:"batch_allowed"`
	MaxQuantity                int       `gorm:"not null;default:1" json:"max_quantity"`
	PointUsed                  int       `gorm:"not null;default:0" json:"point_used"`
	PointRemaining             int       `gorm:"not null;default:0" json:"point_remaining"`
	RequiresRepeatConfirmation bool      `gorm:"not null;default:false" json:"requires_repeat_confirmation"`
	OperationID                string    `gorm:"size:100;index" json:"operation_id,omitempty"`
	ExpiresAt                  time.Time `gorm:"index;not null" json:"expires_at"`
	Status                     string    `gorm:"size:20;index;not null;default:'active'" json:"status"` // active, consuming, consumed, expired
}

// MobileVerificationOperation is the durable idempotency and recovery record
// for an explicit mobile admission confirmation.
type MobileVerificationOperation struct {
	Base
	OperationID    string     `gorm:"size:100;uniqueIndex;not null" json:"operation_id"`
	TenantID       uint       `gorm:"index;not null" json:"tenant_id"`
	StaffID        uint       `gorm:"index;not null" json:"staff_id"`
	SessionID      uint       `gorm:"index;not null" json:"session_id"`
	DeviceID       uint       `gorm:"index;not null" json:"device_id"`
	CheckPointID   uint       `gorm:"index;not null" json:"check_point_id"`
	ScenicAreaID   uint       `gorm:"index;not null" json:"scenic_area_id"`
	TicketCode     string     `gorm:"size:100;index;not null" json:"-"`
	Quantity       int        `gorm:"not null" json:"quantity"`
	PointRemaining int        `gorm:"not null;default:0" json:"point_remaining"`
	ContinuationOf string     `gorm:"size:100" json:"continuation_of,omitempty"`
	RequestHash    string     `gorm:"size:64;not null" json:"-"`
	Status         string     `gorm:"size:20;index;not null;default:'processing'" json:"status"` // processing, completed, denied
	Result         string     `gorm:"size:20" json:"result"`
	ReasonCode     string     `gorm:"size:50" json:"reason_code,omitempty"`
	DisplayText    string     `gorm:"size:255" json:"display_text,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}
