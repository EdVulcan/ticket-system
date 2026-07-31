package model

import "time"

type POSShift struct {
	Base
	TenantID      uint       `gorm:"index;not null" json:"tenant_id"`
	ScenicAreaID  uint       `gorm:"index;not null" json:"scenic_area_id"`
	DeviceID      uint       `gorm:"index;not null" json:"device_id"`
	OperatorID    uint       `gorm:"index;not null" json:"operator_id"`
	ShiftNo       string     `gorm:"size:80;uniqueIndex;not null" json:"shift_no"`
	Status        string     `gorm:"size:20;not null;default:'open';index" json:"status"` // open, closed, reconciled
	OpeningCents  int64      `gorm:"not null;default:0" json:"opening_cents"`
	ClosingCents  int64      `gorm:"not null;default:0" json:"closing_cents"`
	ExpectedCents int64      `gorm:"not null;default:0" json:"expected_cents"`
	OpenedAt      time.Time  `gorm:"not null" json:"opened_at"`
	ClosedAt      *time.Time `json:"closed_at,omitempty"`
	ReconciledAt  *time.Time `json:"reconciled_at,omitempty"`
	Notes         string     `gorm:"size:255" json:"notes"`
}

type PrintJob struct {
	Base
	TenantID     uint       `gorm:"index;not null" json:"tenant_id"`
	DeviceID     uint       `gorm:"index;not null" json:"device_id"`
	OrderNo      string     `gorm:"size:80;index;not null" json:"order_no"`
	TicketCode   string     `gorm:"size:80;index" json:"ticket_code"`
	Status       string     `gorm:"size:20;not null;default:'queued';index" json:"status"` // queued, printing, printed, failed
	AttemptCount int        `gorm:"not null;default:0" json:"attempt_count"`
	LastError    string     `gorm:"size:255" json:"last_error"`
	PrintedAt    *time.Time `json:"printed_at,omitempty"`
	OperatorID   uint       `gorm:"index;not null" json:"operator_id"`
	ShiftID      uint       `gorm:"index;not null" json:"shift_id"`
}

type DeviceAlert struct {
	Base
	TenantID     uint       `gorm:"index;not null" json:"tenant_id"`
	ScenicAreaID uint       `gorm:"index" json:"scenic_area_id"`
	DeviceID     uint       `gorm:"index;not null" json:"device_id"`
	Type         string     `gorm:"size:30;not null" json:"type"` // offline, fault
	Status       string     `gorm:"size:20;not null;default:'open';index" json:"status"`
	Message      string     `gorm:"size:255" json:"message"`
	OpenedAt     time.Time  `gorm:"not null" json:"opened_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
}
