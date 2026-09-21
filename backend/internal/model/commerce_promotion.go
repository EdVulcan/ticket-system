package model

import "time"

// CommerceCouponTemplate is a versioned reward definition. Active reward
// terms are immutable through the promotion service; the version is retained
// so future edits can create a new template instead of rewriting issued value.
type CommerceCouponTemplate struct {
	Base
	TenantID              uint       `gorm:"not null;uniqueIndex:idx_commerce_coupon_templates_scope_name,priority:1;index:idx_commerce_coupon_templates_scope" json:"tenant_id"`
	ChannelAccountID      uint       `gorm:"not null;uniqueIndex:idx_commerce_coupon_templates_scope_name,priority:2;index:idx_commerce_coupon_templates_scope" json:"channel_account_id"`
	BusinessType          string     `gorm:"size:20;not null;uniqueIndex:idx_commerce_coupon_templates_scope_name,priority:3;index:idx_commerce_coupon_templates_scope;check:chk_commerce_coupon_templates_business_type,business_type IN ('restaurant','retail')" json:"business_type"`
	Name                  string     `gorm:"size:120;not null;uniqueIndex:idx_commerce_coupon_templates_scope_name,priority:4" json:"name"`
	Version               int        `gorm:"not null;default:1" json:"version"`
	DiscountCents         int64      `gorm:"not null;check:chk_commerce_coupon_templates_discount,discount_cents > 0" json:"discount_cents"`
	MinGoodsSubtotalCents int64      `gorm:"not null;default:0;check:chk_commerce_coupon_templates_minimum,min_goods_subtotal_cents >= 0" json:"min_goods_subtotal_cents"`
	ValidDays             int        `gorm:"not null;default:0;check:chk_commerce_coupon_templates_valid_days,valid_days >= 0" json:"valid_days"`
	StartsAt              *time.Time `gorm:"index" json:"starts_at,omitempty"`
	EndsAt                *time.Time `gorm:"index" json:"ends_at,omitempty"`
	Status                string     `gorm:"size:20;not null;default:'draft';index;check:chk_commerce_coupon_templates_status,status IN ('draft','active','inactive')" json:"status"`
	IssuanceCap           int        `gorm:"not null;default:0;check:chk_commerce_coupon_templates_issuance_cap,issuance_cap >= 0" json:"issuance_cap"`
	PerCustomerCap        int        `gorm:"not null;default:1;check:chk_commerce_coupon_templates_customer_cap,per_customer_cap > 0" json:"per_customer_cap"`
	RefundReturnPolicy    string     `gorm:"size:60;not null;default:'unfulfilled_full_refund_if_valid'" json:"refund_return_policy"`
}

// CommerceCouponGrant is a customer-specific coupon entitlement. Reward
// terms are copied from the template at issuance time so later versions do
// not rewrite a customer entitlement.
type CommerceCouponGrant struct {
	Base
	TenantID              uint       `gorm:"not null;uniqueIndex:idx_commerce_coupon_grants_issue,priority:1;index:idx_commerce_coupon_grants_scope" json:"tenant_id"`
	TemplateID            uint       `gorm:"not null;uniqueIndex:idx_commerce_coupon_grants_issue,priority:2;index:idx_commerce_coupon_grants_scope" json:"template_id"`
	ChannelAccountID      uint       `gorm:"not null;index:idx_commerce_coupon_grants_scope" json:"channel_account_id"`
	BusinessType          string     `gorm:"size:20;not null;index:idx_commerce_coupon_grants_scope;check:chk_commerce_coupon_grants_business_type,business_type IN ('restaurant','retail')" json:"business_type"`
	CustomerID            string     `gorm:"size:120;not null;uniqueIndex:idx_commerce_coupon_grants_issue,priority:5;index:idx_commerce_coupon_grants_customer" json:"customer_id"`
	Source                string     `gorm:"size:30;not null;uniqueIndex:idx_commerce_coupon_grants_issue,priority:3;check:chk_commerce_coupon_grants_source,source IN ('manual','assist_starter','assist_helper')" json:"source"`
	SourceIdentity        string     `gorm:"size:180;not null;uniqueIndex:idx_commerce_coupon_grants_issue,priority:4" json:"source_identity"`
	DiscountCents         int64      `gorm:"not null;check:chk_commerce_coupon_grants_discount,discount_cents > 0" json:"discount_cents"`
	MinGoodsSubtotalCents int64      `gorm:"not null;default:0;check:chk_commerce_coupon_grants_minimum,min_goods_subtotal_cents >= 0" json:"min_goods_subtotal_cents"`
	RefundReturnPolicy    string     `gorm:"size:60;not null;default:'unfulfilled_full_refund_if_valid'" json:"refund_return_policy"`
	Status                string     `gorm:"size:20;not null;default:'available';index;check:chk_commerce_coupon_grants_status,status IN ('available','reserved','used','expired','cancelled')" json:"status"`
	ExpiresAt             time.Time  `gorm:"not null;index" json:"expires_at"`
	ReservedOrderID       uint       `gorm:"index" json:"reserved_order_id,omitempty"`
	UsedOrderID           uint       `gorm:"index" json:"used_order_id,omitempty"`
	ReservedAt            *time.Time `json:"reserved_at,omitempty"`
	UsedAt                *time.Time `json:"used_at,omitempty"`
}

// CommerceAssistCampaign controls a share-and-help promotion for one
// tenant/channel/business scope. Starter and helper rewards are ordinary
// coupon templates, so checkout remains the single discount authority.
type CommerceAssistCampaign struct {
	Base
	TenantID                uint      `gorm:"not null;uniqueIndex:idx_commerce_assist_campaigns_scope_name,priority:1;index:idx_commerce_assist_campaigns_scope" json:"tenant_id"`
	ChannelAccountID        uint      `gorm:"not null;uniqueIndex:idx_commerce_assist_campaigns_scope_name,priority:2;index:idx_commerce_assist_campaigns_scope" json:"channel_account_id"`
	BusinessType            string    `gorm:"size:20;not null;uniqueIndex:idx_commerce_assist_campaigns_scope_name,priority:3;index:idx_commerce_assist_campaigns_scope;check:chk_commerce_assist_campaigns_business_type,business_type IN ('restaurant','retail')" json:"business_type"`
	Title                   string    `gorm:"size:160;not null;uniqueIndex:idx_commerce_assist_campaigns_scope_name,priority:4" json:"title"`
	StarterCouponTemplateID uint      `gorm:"not null" json:"starter_coupon_template_id"`
	HelperCouponTemplateID  uint      `gorm:"not null" json:"helper_coupon_template_id"`
	RequiredUniqueHelpers   int       `gorm:"not null;check:chk_commerce_assist_campaigns_required_helpers,required_unique_helpers > 0" json:"required_unique_helpers"`
	PerStarterSessionLimit  int       `gorm:"not null;default:1;check:chk_commerce_assist_campaigns_session_limit,per_starter_session_limit > 0" json:"per_starter_session_limit"`
	StartsAt                time.Time `gorm:"not null;index" json:"starts_at"`
	EndsAt                  time.Time `gorm:"not null;index" json:"ends_at"`
	Status                  string    `gorm:"size:20;not null;default:'draft';index;check:chk_commerce_assist_campaigns_status,status IN ('draft','active','inactive')" json:"status"`
}

// CommerceAssistSession uses the hash for lookup and keeps the raw share token
// encrypted so an idempotent retry can return the same usable share link after
// a lost response. The ciphertext is never serialized to clients.
type CommerceAssistSession struct {
	Base
	TenantID             uint       `gorm:"not null;uniqueIndex:idx_commerce_assist_sessions_idempotency,priority:1;index:idx_commerce_assist_sessions_scope" json:"tenant_id"`
	CampaignID           uint       `gorm:"not null;uniqueIndex:idx_commerce_assist_sessions_idempotency,priority:2;index:idx_commerce_assist_sessions_scope" json:"campaign_id"`
	StarterCustomerID    string     `gorm:"size:120;not null;uniqueIndex:idx_commerce_assist_sessions_idempotency,priority:3;index:idx_commerce_assist_sessions_starter" json:"starter_customer_id"`
	IdempotencyKey       string     `gorm:"size:120;not null;uniqueIndex:idx_commerce_assist_sessions_idempotency,priority:4" json:"-"`
	ShareTokenHash       string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	ShareTokenCiphertext string     `gorm:"type:text;not null" json:"-"`
	Status               string     `gorm:"size:20;not null;default:'active';index;check:chk_commerce_assist_sessions_status,status IN ('active','succeeded','expired','cancelled')" json:"status"`
	HelperCount          int        `gorm:"not null;default:0;check:chk_commerce_assist_sessions_helper_count,helper_count >= 0" json:"helper_count"`
	ExpiresAt            time.Time  `gorm:"not null;index" json:"expires_at"`
	SucceededAt          *time.Time `json:"succeeded_at,omitempty"`
}

type CommerceAssistRecord struct {
	Base
	TenantID         uint   `gorm:"not null;index:idx_commerce_assist_records_scope;uniqueIndex:idx_commerce_assist_records_session_helper,priority:1" json:"tenant_id"`
	CampaignID       uint   `gorm:"not null;index:idx_commerce_assist_records_scope;uniqueIndex:idx_commerce_assist_records_session_helper,priority:2" json:"campaign_id"`
	SessionID        uint   `gorm:"not null;index:idx_commerce_assist_records_scope;uniqueIndex:idx_commerce_assist_records_session_helper,priority:3" json:"session_id"`
	HelperCustomerID string `gorm:"size:120;not null;uniqueIndex:idx_commerce_assist_records_session_helper,priority:4;index" json:"helper_customer_id"`
	RewardGrantID    uint   `gorm:"index" json:"reward_grant_id,omitempty"`
	StarterGrantID   uint   `gorm:"index" json:"starter_grant_id,omitempty"`
}
