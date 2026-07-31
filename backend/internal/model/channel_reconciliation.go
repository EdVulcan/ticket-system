package model

import "time"

// ChannelBillRecord is an immutable import of one external channel fact. It
// is kept separate from orders/payments so a later reconciliation can explain
// a missing, duplicate or amount-mismatched record without rewriting sales
// facts.
type ChannelBillRecord struct {
	Base
	TenantID            uint       `gorm:"index;not null" json:"tenant_id"`
	ChannelAccountID    uint       `gorm:"index;not null;uniqueIndex:idx_channel_bill_fact,priority:1" json:"channel_account_id"`
	ExternalNo          string     `gorm:"size:120;not null;uniqueIndex:idx_channel_bill_fact,priority:2" json:"external_no"`
	Operation           string     `gorm:"size:20;not null;uniqueIndex:idx_channel_bill_fact,priority:3" json:"operation"` // sale, payment, cancel, refund
	ExternalProductCode string     `gorm:"size:120" json:"external_product_code,omitempty"`
	AmountCents         int64      `gorm:"not null" json:"amount_cents"`
	Currency            string     `gorm:"size:8;not null;default:'CNY'" json:"currency"`
	ExternalOccurredAt  *time.Time `json:"external_occurred_at,omitempty"`
	Status              string     `gorm:"size:20;not null;default:'matched';index" json:"status"` // matched, mismatch, unmatched, duplicate
	MatchedOrderNo      string     `gorm:"size:80;index" json:"matched_order_no,omitempty"`
	MatchedPaymentNo    string     `gorm:"size:80;index" json:"matched_payment_no,omitempty"`
	MatchedRefundNo     string     `gorm:"size:80;index" json:"matched_refund_no,omitempty"`
	DifferenceCents     int64      `gorm:"not null;default:0" json:"difference_cents"`
	RawJSON             string     `gorm:"type:text" json:"raw_json,omitempty"`
}

// ChannelReconciliation is the auditable import result for one bill batch.
// It never mutates the external records or silently marks a mismatch as paid.
type ChannelReconciliation struct {
	Base
	TenantID         uint      `gorm:"index;not null" json:"tenant_id"`
	ChannelAccountID uint      `gorm:"index;not null" json:"channel_account_id"`
	IdempotencyKey   string    `gorm:"size:120;not null;uniqueIndex" json:"idempotency_key"`
	PeriodStart      time.Time `gorm:"not null" json:"period_start"`
	PeriodEnd        time.Time `gorm:"not null" json:"period_end"`
	Status           string    `gorm:"size:20;not null;index" json:"status"` // completed, needs_review
	RecordCount      int       `gorm:"not null;default:0" json:"record_count"`
	MatchedCount     int       `gorm:"not null;default:0" json:"matched_count"`
	DifferenceCents  int64     `gorm:"not null;default:0" json:"difference_cents"`
	SummaryJSON      string    `gorm:"type:text" json:"summary_json,omitempty"`
}
