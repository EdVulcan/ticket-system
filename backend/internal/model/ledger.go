package model

import "time"

// LedgerEntry is an append-only money fact. AmountCents is signed: positive
// entries add available cash and negative entries consume it. The legacy
// CapitalAccount float fields remain as a compatibility projection while all
// new money-affecting workflows record a cent-based entry as well.
type LedgerEntry struct {
	Base
	AccountID          uint   `gorm:"index;not null" json:"account_id"`
	OwnerTenantID      uint   `gorm:"index;not null" json:"owner_tenant_id"`
	ManagerTenantID    uint   `gorm:"index;not null" json:"manager_tenant_id"`
	EntryType          string `gorm:"size:30;not null;index" json:"entry_type"`
	AmountCents        int64  `gorm:"not null" json:"amount_cents"`
	BalanceCents       int64  `gorm:"not null" json:"balance_cents"`
	UsedCreditCents    int64  `gorm:"not null;default:0" json:"used_credit_cents"`
	FrozenCents        int64  `gorm:"not null;default:0" json:"frozen_cents"`
	IdempotencyKey     string `gorm:"size:120;not null;uniqueIndex:idx_ledger_idempotency,priority:1" json:"idempotency_key"`
	RelatedOrderNo     string `gorm:"size:80;index" json:"related_order_no"`
	RelatedFulfillment string `gorm:"size:80;index" json:"related_fulfillment_no"`
	OperatorID         uint   `gorm:"index" json:"operator_id"`
	ReversalOfID       uint   `gorm:"index" json:"reversal_of_id"`
	Memo               string `gorm:"size:255" json:"memo"`
	MetadataJSON       string `gorm:"type:text" json:"metadata_json,omitempty"`
}

// DigitalRefundTask persists provider refund work. A restart must resume a
// pending refund instead of creating a second provider request.
type DigitalRefundTask struct {
	Base
	RefundID       uint       `gorm:"uniqueIndex;not null" json:"refund_id"`
	TenantID       uint       `gorm:"index;not null" json:"tenant_id"`
	Provider       string     `gorm:"size:20;not null" json:"provider"`
	PaymentNo      string     `gorm:"size:80;index;not null" json:"payment_no"`
	ProviderRefund string     `gorm:"size:120" json:"provider_refund_id"`
	Status         string     `gorm:"size:20;not null;index;default:'pending'" json:"status"` // pending, processing, submitted, succeeded, failed, manual_review
	AttemptCount   int        `gorm:"not null;default:0" json:"attempt_count"`
	MaxAttempts    int        `gorm:"not null;default:8" json:"max_attempts"`
	NextAttemptAt  *time.Time `gorm:"index" json:"next_attempt_at,omitempty"`
	LockedAt       *time.Time `gorm:"index" json:"-"`
	LastError      string     `gorm:"size:255" json:"last_error,omitempty"`
	FailureCode    string     `gorm:"size:40;index" json:"failure_code,omitempty"`
	ProviderStatus string     `gorm:"size:40" json:"provider_status,omitempty"`
	ManualReviewAt *time.Time `json:"manual_review_at,omitempty"`
}
