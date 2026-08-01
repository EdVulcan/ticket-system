package model

import "time"

// AfterSaleRequest is the durable workflow record for changes made after an
// order was created. The request is intentionally separate from Order.Status:
// a refund, reschedule, reissue, or void may remain pending for approval or an
// external provider without losing the original sales fact.
type AfterSaleRequest struct {
	Base
	TenantID                    uint             `gorm:"uniqueIndex:idx_after_sale_idempotency,priority:1;not null;index" json:"tenant_id"`
	RequestNo                   string           `gorm:"size:50;uniqueIndex;not null" json:"request_no"`
	IdempotencyKey              string           `gorm:"size:100;uniqueIndex:idx_after_sale_idempotency,priority:2;not null" json:"idempotency_key"`
	OrderNo                     string           `gorm:"size:50;index;not null" json:"order_no"`
	Type                        string           `gorm:"size:20;not null;index" json:"type"`                     // refund, reschedule, exchange, void, reissue
	Status                      string           `gorm:"size:20;not null;default:'pending';index" json:"status"` // pending, approved, rejected, processing, completed, failed
	Reason                      string           `gorm:"size:255" json:"reason"`
	TicketCodesJSON             string           `gorm:"type:text" json:"ticket_codes,omitempty"`
	TargetDate                  *time.Time       `gorm:"type:date" json:"target_date,omitempty"`
	TargetSlot                  string           `gorm:"size:50" json:"target_slot,omitempty"`
	TargetProductID             uint             `json:"target_product_id,omitempty"`
	AmountCents                 int64            `json:"amount_cents"`
	PaymentMethod               string           `gorm:"size:20" json:"payment_method,omitempty"`
	RefundID                    uint             `gorm:"index" json:"refund_id,omitempty"`
	DifferenceCents             int64            `gorm:"not null;default:0" json:"difference_cents"`
	DifferenceStatus            string           `gorm:"size:30" json:"difference_status,omitempty"`
	DifferencePaymentID         uint             `gorm:"index" json:"difference_payment_id,omitempty"`
	DifferenceRefundID          uint             `gorm:"index" json:"difference_refund_id,omitempty"`
	SettlementExceptionApproved bool             `gorm:"not null;default:false" json:"settlement_exception_approved"`
	SettlementExceptionReason   string           `gorm:"size:255" json:"settlement_exception_reason,omitempty"`
	DeviceID                    uint             `json:"device_id,omitempty"`
	ShiftID                     uint             `json:"shift_id,omitempty"`
	OperatorID                  uint             `json:"operator_id"`
	ReviewerID                  uint             `json:"reviewer_id,omitempty"`
	ReviewedAt                  *time.Time       `json:"reviewed_at,omitempty"`
	CompletedAt                 *time.Time       `json:"completed_at,omitempty"`
	ErrorMessage                string           `gorm:"size:500" json:"error_message,omitempty"`
	Events                      []AfterSaleEvent `gorm:"foreignKey:RequestNo;references:RequestNo" json:"events,omitempty"`
}

// AfterSaleEvent is an append-only operational timeline for customer service
// and reconciliation. It remains useful even when the request itself changes
// status or an external payment provider is unavailable.
type AfterSaleEvent struct {
	Base
	TenantID   uint   `gorm:"index;not null" json:"tenant_id"`
	RequestNo  string `gorm:"size:50;index;not null" json:"request_no"`
	FromStatus string `gorm:"size:20" json:"from_status"`
	ToStatus   string `gorm:"size:20" json:"to_status"`
	Action     string `gorm:"size:30;not null" json:"action"`
	ActorID    uint   `json:"actor_id"`
	Reason     string `gorm:"size:255" json:"reason"`
}
