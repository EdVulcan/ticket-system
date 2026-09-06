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
