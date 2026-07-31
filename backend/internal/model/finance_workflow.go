package model

import "time"

// FinancialDocument records evidence that cannot be represented by a balance
// column alone: invoices, payout vouchers, bank receipts, and reconciliation
// differences. Amounts are integer cents to keep the financial invariant
// independent from legacy float columns.
type FinancialDocument struct {
	Base
	TenantID             uint       `gorm:"index;uniqueIndex:idx_financial_document_idempotency,priority:1;not null" json:"tenant_id"`
	DocumentNo           string     `gorm:"size:50;uniqueIndex;not null" json:"document_no"`
	IdempotencyKey       string     `gorm:"size:100;uniqueIndex:idx_financial_document_idempotency,priority:2" json:"idempotency_key,omitempty"`
	Type                 string     `gorm:"size:30;not null;index" json:"type"`             // invoice, payout, receipt, reconciliation_difference
	Status               string     `gorm:"size:20;not null;default:'draft'" json:"status"` // draft, submitted, approved, rejected, settled
	AmountCents          int64      `json:"amount_cents"`
	OrderNo              string     `gorm:"size:50;index" json:"order_no,omitempty"`
	CounterpartyTenantID uint       `gorm:"index" json:"counterparty_tenant_id,omitempty"`
	ExternalRef          string     `gorm:"size:100;index" json:"external_ref,omitempty"`
	Description          string     `gorm:"size:500" json:"description"`
	EvidenceJSON         string     `gorm:"type:text" json:"evidence_json,omitempty"`
	ApprovedBy           uint       `json:"approved_by,omitempty"`
	ApprovedAt           *time.Time `json:"approved_at,omitempty"`
}
