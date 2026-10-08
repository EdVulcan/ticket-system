package model

import "time"

// XiaohongshuSupplierVerification persists supplier-side ticket verification
// reports. The ticket identity is permanent so every external lifecycle maps
// to one durable row.
type XiaohongshuSupplierVerification struct {
	Base
	TenantID                 uint       `gorm:"not null;index:idx_xhs_supplier_verification_tenant_channel,priority:1" json:"-"`
	ChannelAccountID         uint       `gorm:"not null;index:idx_xhs_supplier_verification_tenant_channel,priority:2" json:"channel_account_id"`
	OrderID                  uint       `gorm:"not null;index" json:"order_id"`
	OrderItemID              uint       `gorm:"not null;index" json:"order_item_id"`
	SupplySnapshotID         uint       `gorm:"not null;index" json:"supply_snapshot_id"`
	TicketID                 uint       `gorm:"not null;uniqueIndex:idx_xhs_supplier_verification_ticket" json:"ticket_id"`
	State                    string     `gorm:"size:20;not null;default:'pending';index;check:chk_xhs_supplier_verification_state,state IN ('pending','in_flight','confirmed','unknown','manual_review','skipped')" json:"state"`
	VerifyID                 string     `gorm:"size:100;not null;default:''" json:"verify_id,omitempty"`
	AttemptCount             int        `gorm:"not null;default:0" json:"attempt_count"`
	NextAttemptAt            *time.Time `gorm:"index" json:"next_attempt_at,omitempty"`
	ExternalStartedAt        *time.Time `json:"external_started_at,omitempty"`
	ConfirmedAt              *time.Time `json:"confirmed_at,omitempty"`
	RequestPayloadCiphertext string     `gorm:"type:text;not null;default:''" json:"-"`
	RequestHash              string     `gorm:"size:64;not null;default:''" json:"-"`
	LastError                string     `gorm:"size:500;not null;default:''" json:"last_error,omitempty"`
	ReviewReason             string     `gorm:"size:60;not null;default:''" json:"review_reason,omitempty"`
}
