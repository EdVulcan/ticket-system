package model

import "time"

type SettlementStatement struct {
	Base
	SupplierTenantID       uint                   `gorm:"index;not null" json:"supplier_tenant_id"`
	DistributorTenantID    uint                   `gorm:"index;not null" json:"distributor_tenant_id"`
	StatementNo            string                 `gorm:"size:80;uniqueIndex;not null" json:"statement_no"`
	IdempotencyKey         string                 `gorm:"size:160;index" json:"idempotency_key"`
	PeriodStart            time.Time              `gorm:"index;not null" json:"period_start"`
	PeriodEnd              time.Time              `gorm:"index;not null" json:"period_end"`
	GrossCents             int64                  `gorm:"not null" json:"gross_cents"`
	RefundCents            int64                  `gorm:"not null" json:"refund_cents"`
	CommissionCents        int64                  `gorm:"not null" json:"commission_cents"`
	NetCents               int64                  `gorm:"not null" json:"net_cents"`
	AdjustmentCents        int64                  `gorm:"not null;default:0" json:"adjustment_cents"`
	Status                 string                 `gorm:"size:30;not null;index" json:"status"`
	DueAt                  *time.Time             `json:"due_at,omitempty"`
	PaidAt                 *time.Time             `json:"paid_at,omitempty"`
	ConfirmedAt            *time.Time             `json:"confirmed_at,omitempty"`
	SupplierConfirmedAt    *time.Time             `json:"supplier_confirmed_at,omitempty"`
	DistributorConfirmedAt *time.Time             `json:"distributor_confirmed_at,omitempty"`
	PaymentProof           string                 `gorm:"size:255" json:"payment_proof,omitempty"`
	DisputeReason          string                 `gorm:"size:255" json:"dispute_reason,omitempty"`
	Lines                  []SettlementLine       `gorm:"foreignKey:StatementID" json:"lines,omitempty"`
	Adjustments            []SettlementAdjustment `gorm:"foreignKey:StatementID" json:"adjustments,omitempty"`
}

// SettlementAdjustment is an append-only correction to a disputed statement.
// The immutable fulfillment lines and base NetCents remain unchanged.
type SettlementAdjustment struct {
	Base
	StatementID             uint   `gorm:"uniqueIndex:idx_settlement_adjustment_sequence,priority:1;not null" json:"statement_id"`
	Sequence                int    `gorm:"uniqueIndex:idx_settlement_adjustment_sequence,priority:2;not null" json:"sequence"`
	ActorTenantID           uint   `gorm:"index;not null" json:"actor_tenant_id"`
	ActorUserID             uint   `gorm:"index" json:"actor_user_id"`
	AmountCents             int64  `gorm:"not null" json:"amount_cents"`
	PreviousAdjustmentCents int64  `gorm:"not null" json:"previous_adjustment_cents"`
	NewAdjustmentCents      int64  `gorm:"not null" json:"new_adjustment_cents"`
	Reason                  string `gorm:"size:255;not null" json:"reason"`
}

type SettlementLine struct {
	Base
	StatementID        uint   `gorm:"index;not null" json:"statement_id"`
	FulfillmentOrderID uint   `gorm:"uniqueIndex;not null" json:"fulfillment_order_id"`
	GrossCents         int64  `gorm:"not null" json:"gross_cents"`
	RefundCents        int64  `gorm:"not null" json:"refund_cents"`
	CommissionCents    int64  `gorm:"not null" json:"commission_cents"`
	NetCents           int64  `gorm:"not null" json:"net_cents"`
	Status             string `gorm:"size:20;not null" json:"status"`
}
