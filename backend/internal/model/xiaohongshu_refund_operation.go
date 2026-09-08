package model

// XiaohongshuRefundOperation binds an immutable provider request to the
// existing Refund/DigitalRefundTask. It does not own financial finalization.
type XiaohongshuRefundOperation struct {
	Base
	TenantID                  uint   `gorm:"not null;index" json:"tenant_id"`
	ChannelAccountID          uint   `gorm:"not null;uniqueIndex:idx_xhs_refund_external,priority:1" json:"channel_account_id"`
	XiaohongshuOrderLinkID    uint   `gorm:"not null;index" json:"xiaohongshu_order_link_id"`
	RefundID                  uint   `gorm:"not null;uniqueIndex" json:"refund_id"`
	ExternalAfterSalesOrderID string `gorm:"size:100;not null;uniqueIndex:idx_xhs_refund_external,priority:2" json:"external_after_sales_order_id"`
	RequestPayloadCiphertext  string `gorm:"type:text;not null" json:"-"`
	// prepared -> querying is committed before add. A crash or lost reply can
	// only query the same identifier; it must never blindly send add again.
	State string `gorm:"size:20;not null;default:'prepared'" json:"state"`
}
