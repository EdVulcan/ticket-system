package model

import "time"

// CommerceCustomerSession is a bearer session for a commercial storefront
// customer. It is bound to one tenant and one channel account; it never
// creates or aliases a tenant user.
//
// The WeChat subject and bearer token are represented by one-way hashes. The
// raw values are only held by the login adapter for the duration of a request.
type CommerceCustomerSession struct {
	Base
	TenantID         uint       `gorm:"not null;index:idx_commerce_customer_sessions_scope" json:"-"`
	ChannelAccountID uint       `gorm:"not null;index:idx_commerce_customer_sessions_scope;uniqueIndex:idx_commerce_customer_sessions_subject,priority:1" json:"-"`
	SubjectHash      string     `gorm:"size:64;not null;uniqueIndex:idx_commerce_customer_sessions_subject,priority:2;index" json:"-"`
	TokenHash        string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	ExpiresAt        time.Time  `gorm:"not null;index" json:"-"`
	Status           string     `gorm:"size:20;not null;default:'active';index;check:chk_commerce_customer_sessions_status,status IN ('active','revoked','expired')" json:"-"`
	RevokedAt        *time.Time `json:"-"`
	LastSeenAt       *time.Time `json:"-"`
}

// CommerceStorefrontBinding publishes one commercial business and fulfillment
// location through a channel account. The account is the integration anchor;
// this row is configuration only and is never used as a customer identity.
type CommerceStorefrontBinding struct {
	Base
	TenantID         uint   `gorm:"not null;uniqueIndex:idx_commerce_storefront_bindings_account;index" json:"-"`
	ChannelAccountID uint   `gorm:"not null;uniqueIndex:idx_commerce_storefront_bindings_account" json:"channel_account_id"`
	BusinessType     string `gorm:"size:20;not null;check:chk_commerce_storefront_bindings_business_type,business_type IN ('restaurant','retail')" json:"business_type"`
	LocationID       uint   `gorm:"not null" json:"location_id"`
	Status           string `gorm:"size:20;not null;default:'active';index;check:chk_commerce_storefront_bindings_status,status IN ('active','disabled')" json:"status"`
}

// CommerceStorefrontConfig is kept as a descriptive alias for callers that
// treat the published binding as storefront configuration.
type CommerceStorefrontConfig = CommerceStorefrontBinding
