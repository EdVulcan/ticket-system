package model

import "time"

// ChannelAccount is an independently managed external sales connection.
// Secrets are encrypted at rest by the channel service and are never exposed
// in API responses.
type ChannelAccount struct {
	Base
	TenantID            uint       `gorm:"index;not null" json:"tenant_id"`
	Code                string     `gorm:"size:80;uniqueIndex;not null" json:"code"`
	Type                string     `gorm:"size:30;not null" json:"type"`
	AppID               string     `gorm:"size:120" json:"app_id"`
	SecretCiphertext    string     `gorm:"type:text" json:"-"`
	VerifyKeyCiphertext string     `gorm:"type:text" json:"-"`
	SignAlgorithm       string     `gorm:"size:20;not null;default:'hmac-sha256'" json:"sign_algorithm"`
	PermissionsJSON     string     `gorm:"type:text" json:"permissions_json"`
	CallbackURL         string     `gorm:"size:255" json:"callback_url"`
	Status              string     `gorm:"size:20;not null;default:'active';index" json:"status"` // active, disabled, sandbox
	KeyVersion          int        `gorm:"not null;default:1" json:"key_version"`
	LastUsedAt          *time.Time `json:"last_used_at,omitempty"`
	RateLimitPerMin     int        `gorm:"not null;default:600" json:"rate_limit_per_min"`
	AllowedIPsJSON      string     `gorm:"type:text" json:"allowed_ips_json,omitempty"`
}

// ChannelProductMapping maps a channel product identifier to a seller-owned
// product/listing. Fulfillment authority is still resolved from ProductOffer.
type ChannelProductMapping struct {
	Base
	ChannelAccountID uint   `gorm:"uniqueIndex:idx_channel_product,priority:1;uniqueIndex:idx_channel_external_product,priority:1;not null" json:"channel_account_id"`
	ProductID        uint   `gorm:"uniqueIndex:idx_channel_product,priority:2;not null" json:"product_id"`
	ExternalCode     string `gorm:"size:120;uniqueIndex:idx_channel_external_product,priority:2;not null" json:"external_code"`
	Status           string `gorm:"size:20;not null;default:'active'" json:"status"`
	DisplayName      string `gorm:"size:120" json:"display_name"`
}

// ChannelRequest records the first body hash for an external request id. A
// retry with the same id but different content is rejected explicitly.
type ChannelRequest struct {
	Base
	ChannelAccountID uint       `gorm:"uniqueIndex:idx_channel_request,priority:1;not null" json:"channel_account_id"`
	RequestID        string     `gorm:"size:120;uniqueIndex:idx_channel_request,priority:2;not null" json:"request_id"`
	Endpoint         string     `gorm:"size:120;not null" json:"endpoint"`
	BodyHash         string     `gorm:"size:64;not null" json:"body_hash"`
	ResponseJSON     string     `gorm:"type:text" json:"response_json,omitempty"`
	Status           string     `gorm:"size:20;not null;default:'processing'" json:"status"` // processing, completed, failed, retryable
	ResponseStatus   int        `gorm:"not null;default:200" json:"response_status"`
	RemoteIP         string     `gorm:"size:64" json:"remote_ip,omitempty"`
	AttemptCount     int        `gorm:"not null;default:1" json:"attempt_count"`
	LastAttemptAt    *time.Time `gorm:"index" json:"last_attempt_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	LockedAt         *time.Time `gorm:"index" json:"-"`
}
