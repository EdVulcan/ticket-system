package model

import "time"

// ChannelAccount is an independently managed external sales connection.
// Secrets are encrypted at rest by the channel service and are never exposed
// in API responses.
type ChannelAccount struct {
	Base
	TenantID                 uint       `gorm:"index;not null" json:"tenant_id"`
	Code                     string     `gorm:"size:80;uniqueIndex;not null" json:"code"`
	Type                     string     `gorm:"size:30;not null" json:"type"`
	AppID                    string     `gorm:"size:120" json:"app_id"`
	SecretCiphertext         string     `gorm:"type:text" json:"-"`
	VerifyKeyCiphertext      string     `gorm:"type:text" json:"-"`
	ProtocolConfigCiphertext string     `gorm:"type:text" json:"-"`
	SignAlgorithm            string     `gorm:"size:20;not null;default:'hmac-sha256'" json:"sign_algorithm"`
	PermissionsJSON          string     `gorm:"type:text" json:"permissions_json"`
	CallbackURL              string     `gorm:"size:255" json:"callback_url"`
	Status                   string     `gorm:"size:20;not null;default:'active';index" json:"status"`    // active, disabled, sandbox
	Environment              string     `gorm:"size:20;not null;default:'production'" json:"environment"` // sandbox, production
	KeyVersion               int        `gorm:"not null;default:1" json:"key_version"`
	LastUsedAt               *time.Time `json:"last_used_at,omitempty"`
	RateLimitPerMin          int        `gorm:"not null;default:600" json:"rate_limit_per_min"`
	AllowedIPsJSON           string     `gorm:"type:text" json:"allowed_ips_json,omitempty"`
	ProtocolConfigured       bool       `gorm:"-" json:"protocol_configured"`
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
	ChannelSaleCents int64  `gorm:"not null;default:0" json:"channel_sale_cents"`
	ChannelCostCents int64  `gorm:"not null;default:0" json:"channel_cost_cents"`
}

// CtripOutboundTask persists price and inventory notifications before any
// network call. A failed call can be retried after a restart without changing
// the supplier product or inventory facts.
type CtripOutboundTask struct {
	Base
	TenantID                uint       `gorm:"index;not null" json:"tenant_id"`
	ChannelAccountID        uint       `gorm:"index;not null;uniqueIndex:idx_ctrip_outbound_payload,priority:1" json:"channel_account_id"`
	ChannelProductMappingID uint       `gorm:"index;not null" json:"channel_product_mapping_id"`
	Kind                    string     `gorm:"size:20;not null;uniqueIndex:idx_ctrip_outbound_payload,priority:2" json:"kind"`
	PayloadHash             string     `gorm:"size:64;not null;uniqueIndex:idx_ctrip_outbound_payload,priority:3" json:"payload_hash"`
	Endpoint                string     `gorm:"size:255;not null" json:"-"`
	PayloadJSON             string     `gorm:"type:text;not null" json:"-"`
	Status                  string     `gorm:"size:20;not null;default:'pending';index" json:"status"`
	AttemptCount            int        `gorm:"not null;default:0" json:"attempt_count"`
	NextAttemptAt           *time.Time `gorm:"index" json:"next_attempt_at,omitempty"`
	LockedAt                *time.Time `gorm:"index" json:"-"`
	ResultCode              string     `gorm:"size:30" json:"result_code,omitempty"`
	ResultMessage           string     `gorm:"size:500" json:"result_message,omitempty"`
	LastError               string     `gorm:"size:500" json:"last_error,omitempty"`
	CompletedAt             *time.Time `json:"completed_at,omitempty"`
}

// ChannelRequest records the first body hash for an external request id. A
// retry with the same id but different content is rejected explicitly.
type ChannelRequest struct {
	Base
	ChannelAccountID uint       `gorm:"uniqueIndex:idx_channel_request,priority:1;not null" json:"channel_account_id"`
	RequestID        string     `gorm:"size:120;uniqueIndex:idx_channel_request,priority:2;not null" json:"request_id"`
	Nonce            string     `gorm:"size:120" json:"-"`
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

// ChannelNonce gives replay protection an independent database-enforced key.
// The request id is retained so an exact idempotent retry may reuse its nonce.
type ChannelNonce struct {
	Base
	ChannelAccountID uint      `gorm:"uniqueIndex:idx_channel_nonce,priority:1;not null" json:"-"`
	Nonce            string    `gorm:"size:120;uniqueIndex:idx_channel_nonce,priority:2;not null" json:"-"`
	RequestID        string    `gorm:"size:120;not null" json:"-"`
	ExpiresAt        time.Time `gorm:"index;not null" json:"-"`
}

// CtripOrderLink keeps Ctrip protocol identifiers outside the core order
// model. Business state remains authoritative on Order and Ticket.
type CtripOrderLink struct {
	Base
	TenantID         uint             `gorm:"index;not null;uniqueIndex:idx_ctrip_order_account_ota,priority:1" json:"tenant_id"`
	ChannelAccountID uint             `gorm:"index;not null;uniqueIndex:idx_ctrip_order_account_ota,priority:2" json:"channel_account_id"`
	OrderID          uint             `gorm:"uniqueIndex;not null" json:"order_id"`
	OTAOrderID       string           `gorm:"size:100;not null;uniqueIndex:idx_ctrip_order_account_ota,priority:3" json:"ota_order_id"`
	SupplierOrderID  string           `gorm:"size:50;not null;index" json:"supplier_order_id"`
	State            string           `gorm:"size:20;not null;default:'preordered'" json:"state"`
	Items            []CtripOrderItem `gorm:"foreignKey:CtripOrderLinkID" json:"items,omitempty"`
}

// CtripOrderItem maps one Ctrip item and its passenger identifiers to the
// corresponding local order item. Passenger data stays as an immutable JSON
// snapshot because Ctrip owns those identifiers.
type CtripOrderItem struct {
	Base
	CtripOrderLinkID uint   `gorm:"index;not null;uniqueIndex:idx_ctrip_link_item,priority:1" json:"ctrip_order_link_id"`
	OrderItemID      uint   `gorm:"uniqueIndex;not null" json:"order_item_id"`
	ExternalItemID   string `gorm:"size:100;not null;uniqueIndex:idx_ctrip_link_item,priority:2" json:"external_item_id"`
	PLU              string `gorm:"size:120;not null" json:"plu"`
	PassengerIDsJSON string `gorm:"type:text" json:"passenger_ids_json,omitempty"`
	GuestPriceCents  int64  `gorm:"not null;default:0" json:"guest_price_cents"`
	SalePriceCents   int64  `gorm:"not null;default:0" json:"sale_price_cents"`
	CostCents        int64  `gorm:"not null;default:0" json:"cost_cents"`
}
