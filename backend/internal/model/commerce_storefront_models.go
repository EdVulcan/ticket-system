package model

import "time"

// CommerceCustomerSession is a bearer session for a commercial storefront
// customer. It is bound to one tenant and one channel account; it never
// creates or aliases a tenant user. A single session can access every
// business domain published by that channel account; each request resolves
// its business selector against the account's active bindings.
//
// The bearer token and lookup subject are represented by one-way hashes. The
// app-scoped WeChat openid is additionally encrypted so a phone authorization
// response can be bound to the same platform user instead of merely to a
// bearer token. Raw provider values are never returned to the storefront.
type CommerceCustomerSession struct {
	Base
	TenantID         uint `gorm:"not null;index:idx_commerce_customer_sessions_scope" json:"-"`
	ChannelAccountID uint `gorm:"not null;index:idx_commerce_customer_sessions_scope;uniqueIndex:idx_commerce_customer_sessions_subject,priority:1" json:"-"`
	// MemberID is a server-resolved, optional tenant customer association. It
	// is never accepted from storefront requests and is not used as an order
	// access credential.
	MemberID *uint `gorm:"index" json:"-"`
	// OrderMemberID is the safe projection for new order attribution. Keep the
	// profile association separate so a withdrawn/anonymized member can still
	// read their own status and history without being attached to new orders.
	OrderMemberID    *uint      `gorm:"-" json:"-"`
	SubjectHash      string     `gorm:"size:64;not null;uniqueIndex:idx_commerce_customer_sessions_subject,priority:2;index" json:"-"`
	OpenIDCiphertext string     `gorm:"type:text;not null;default:''" json:"-"`
	TokenHash        string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	ExpiresAt        time.Time  `gorm:"not null;index" json:"-"`
	Status           string     `gorm:"size:20;not null;default:'active';index;check:chk_commerce_customer_sessions_status,status IN ('active','revoked','expired')" json:"-"`
	RevokedAt        *time.Time `json:"-"`
	LastSeenAt       *time.Time `json:"-"`
}

// CommerceStorefrontBinding publishes one commercial business and fulfillment
// location through a channel account. One WeChat account may publish both
// commercial domains for the same tenant, but only once per domain. The
// account is the integration anchor; this row is configuration only and is
// never used as a customer identity.
type CommerceStorefrontBinding struct {
	Base
	TenantID         uint   `gorm:"not null;uniqueIndex:idx_commerce_storefront_bindings_account_domain,priority:1;index" json:"-"`
	ChannelAccountID uint   `gorm:"not null;uniqueIndex:idx_commerce_storefront_bindings_account_domain,priority:2" json:"channel_account_id"`
	BusinessType     string `gorm:"size:20;not null;uniqueIndex:idx_commerce_storefront_bindings_account_domain,priority:3;check:chk_commerce_storefront_bindings_business_type,business_type IN ('restaurant','retail')" json:"business_type"`
	LocationID       uint   `gorm:"not null" json:"location_id"`
	Status           string `gorm:"size:20;not null;default:'active';index;check:chk_commerce_storefront_bindings_status,status IN ('active','disabled')" json:"status"`
}

// CommerceStorefrontConfig is kept as a descriptive alias for callers that
// treat the published binding as storefront configuration.
type CommerceStorefrontConfig = CommerceStorefrontBinding
