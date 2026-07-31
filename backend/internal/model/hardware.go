package model

import "time"

// HardwareCommand is a durable command sent to a printer, gate, scanner, or
// identity reader. A command is never considered successful merely because a
// browser requested it; the device must acknowledge it with the same token.
type HardwareCommand struct {
	Base
	TenantID     uint       `gorm:"index;not null" json:"tenant_id"`
	ScenicAreaID uint       `gorm:"index;not null" json:"scenic_area_id"`
	DeviceID     uint       `gorm:"index;not null" json:"device_id"`
	CommandNo    string     `gorm:"size:50;uniqueIndex;not null" json:"command_no"`
	Kind         string     `gorm:"size:30;not null" json:"kind"` // print, verify, open_gate, read_identity
	PayloadJSON  string     `gorm:"type:text" json:"payload_json"`
	Status       string     `gorm:"size:20;not null;default:'queued';index" json:"status"` // queued, delivered, acknowledged, failed, expired
	AttemptCount int        `json:"attempt_count"`
	LastError    string     `gorm:"size:500" json:"last_error,omitempty"`
	AckToken     string     `gorm:"size:100;not null" json:"-"`
	QueuedAt     time.Time  `json:"queued_at"`
	DeliveredAt  *time.Time `json:"delivered_at,omitempty"`
	AckedAt      *time.Time `json:"acked_at,omitempty"`
	ExpiresAt    time.Time  `json:"expires_at"`
}

// HardwareEvent is the immutable device-side acknowledgement/audit fact.
type HardwareEvent struct {
	Base
	TenantID  uint   `gorm:"index;not null" json:"tenant_id"`
	DeviceID  uint   `gorm:"index;not null" json:"device_id"`
	CommandNo string `gorm:"size:50;index;not null" json:"command_no"`
	EventType string `gorm:"size:30;not null" json:"event_type"`
	Payload   string `gorm:"type:text" json:"payload,omitempty"`
}
