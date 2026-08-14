package model

import "time"

// XiaohongshuBookingOperation is the durable boundary between local package
// fulfillment and Xiaohongshu's presale-booking API. The request payload is
// encrypted before persistence so retries survive process restarts without
// storing guest data in plaintext.
type XiaohongshuBookingOperation struct {
	Base
	TenantID         uint   `gorm:"not null;index" json:"tenant_id"`
	ChannelAccountID uint   `gorm:"not null;index" json:"channel_account_id"`
	OrderLinkID      uint   `gorm:"not null;index" json:"order_link_id"`
	EntitlementID    uint   `gorm:"not null;index" json:"entitlement_id"`
	OperationKey     string `gorm:"size:160;not null;uniqueIndex" json:"operation_key"`
	// refund_status_sync records only the presale-booking status=4 notification
	// after an independently completed local after-sale. It is not a payment
	// refund operation.
	Type                     string     `gorm:"size:30;not null;index;check:chk_xiaohongshu_booking_operations_type,type IN ('book','revoke','refund_status_sync')" json:"type"`
	Status                   string     `gorm:"size:30;not null;default:'pending';index;check:chk_xiaohongshu_booking_operations_status,status IN ('pending','remote_succeeded','confirm_pending','completed','compensation_pending','failed')" json:"status"`
	FailedFromStage          string     `gorm:"size:30;not null;default:''" json:"failed_from_stage,omitempty"`
	ExternalBookOrderID      string     `gorm:"size:100;index" json:"external_book_order_id,omitempty"`
	PlatformBookID           string     `gorm:"size:100;index" json:"platform_book_id,omitempty"`
	RequestPayloadCiphertext string     `gorm:"type:text;not null" json:"-"`
	Attempts                 int        `gorm:"not null;default:0" json:"attempts"`
	MaxAttempts              int        `gorm:"not null;default:20" json:"max_attempts"`
	NextAttemptAt            *time.Time `gorm:"index" json:"next_attempt_at,omitempty"`
	LastError                string     `gorm:"size:500" json:"last_error,omitempty"`
	CompletedAt              *time.Time `gorm:"check:chk_xiaohongshu_booking_operations_semantics,(((type = 'book' AND external_book_order_id <> '') OR (type IN ('revoke','refund_status_sync') AND external_book_order_id <> '' AND platform_book_id <> '')) AND (type = 'book' OR status IN ('pending','remote_succeeded','completed','failed')) AND (status NOT IN ('remote_succeeded','confirm_pending','compensation_pending','completed') OR platform_book_id <> '') AND (failed_from_stage = '' OR failed_from_stage IN ('pending','remote_succeeded','confirm_pending','compensation_pending')) AND (failed_from_stage = '' OR status = 'failed') AND ((status IN ('completed','failed')) = (completed_at IS NOT NULL)) AND ((status IN ('pending','remote_succeeded','confirm_pending','compensation_pending')) = (next_attempt_at IS NOT NULL)))" json:"completed_at,omitempty"`
}
