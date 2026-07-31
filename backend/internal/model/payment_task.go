package model

import "time"

// PaymentReconciliationTask persists provider queries that must survive a
// process restart. A payment has at most one task; retries advance the same
// row instead of creating duplicate provider requests.
type PaymentReconciliationTask struct {
	Base
	TenantID  uint       `gorm:"index;not null" json:"tenant_id"`
	PaymentID uint       `gorm:"uniqueIndex;not null" json:"payment_id"`
	PaymentNo string     `gorm:"size:50;index;not null" json:"payment_no"`
	Status    string     `gorm:"size:20;index;default:'pending'" json:"status"` // pending, processing, completed
	Attempts  int        `gorm:"not null;default:0" json:"attempts"`
	NextRunAt time.Time  `gorm:"index;not null" json:"next_run_at"`
	LockedAt  *time.Time `json:"locked_at,omitempty"`
	LastError string     `gorm:"size:255" json:"last_error,omitempty"`
}
