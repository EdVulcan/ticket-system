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

// XiaohongshuProductConfig contains the provider-specific fields required to
// publish an existing channel mapping. It does not own product price,
// inventory or fulfillment rights.
type XiaohongshuProductConfig struct {
	Base
	TenantID                uint       `gorm:"index;not null" json:"tenant_id"`
	ChannelAccountID        uint       `gorm:"index;not null" json:"channel_account_id"`
	ChannelProductMappingID uint       `gorm:"uniqueIndex;not null" json:"channel_product_mapping_id"`
	ExternalSKUID           string     `gorm:"column:external_sku_id;size:120;not null" json:"external_sku_id"`
	CategoryID              string     `gorm:"size:120;not null" json:"category_id"`
	POIIDsJSON              string     `gorm:"column:poi_ids_json;type:text" json:"poi_ids_json"`
	ImageURL                string     `gorm:"size:500;not null" json:"image_url"`
	Description             string     `gorm:"type:text;not null" json:"description"`
	ProductPath             string     `gorm:"size:255;not null" json:"product_path"`
	OrderPath               string     `gorm:"size:255;not null" json:"order_path"`
	ProductType             int        `gorm:"not null;default:1" json:"product_type"`
	SettleType              int        `gorm:"not null;default:1" json:"settle_type"`
	SyncStatus              string     `gorm:"size:20;not null;default:'pending';index" json:"sync_status"`
	LastSyncError           string     `gorm:"size:500" json:"last_sync_error,omitempty"`
	LastSyncedAt            *time.Time `json:"last_synced_at,omitempty"`
}

// XiaohongshuOrderLink keeps miniapp payment identifiers separate from the
// core order. Pay tokens are encrypted and a client request id is the
// idempotency boundary for repeated taps.
type XiaohongshuOrderLink struct {
	Base
	TenantID           uint       `gorm:"index;not null" json:"tenant_id"`
	ChannelAccountID   uint       `gorm:"index;not null;uniqueIndex:idx_xhs_order_external,priority:1" json:"channel_account_id"`
	MiniappCustomerID  uint       `gorm:"index;not null;uniqueIndex:idx_xhs_order_request,priority:1" json:"-"`
	OrderID            uint       `gorm:"uniqueIndex;not null" json:"order_id"`
	ClientRequestID    string     `gorm:"size:100;not null;uniqueIndex:idx_xhs_order_request,priority:2" json:"-"`
	ExternalOrderID    string     `gorm:"size:100;not null;uniqueIndex:idx_xhs_order_external,priority:2" json:"external_order_id"`
	PlatformOrderID    string     `gorm:"size:100;index" json:"platform_order_id,omitempty"`
	PayTokenCiphertext string     `gorm:"type:text" json:"-"`
	PayTokenExpiresAt  *time.Time `json:"pay_token_expires_at,omitempty"`
	State              string     `gorm:"size:20;not null;default:'creating';index" json:"state"`
	TradeNo            string     `gorm:"size:100;index" json:"trade_no,omitempty"`
	PayChannel         int        `gorm:"not null;default:0" json:"pay_channel,omitempty"`
	LastQueriedAt      *time.Time `json:"last_queried_at,omitempty"`
	LastError          string     `gorm:"size:500" json:"last_error,omitempty"`
}

// XiaohongshuVoucherLink maps provider vouchers to immutable local ticket
// entitlements. The raw provider code is encrypted at rest.
type XiaohongshuVoucherLink struct {
	Base
	TenantID               uint   `gorm:"index;not null" json:"tenant_id"`
	ChannelAccountID       uint   `gorm:"index;not null;uniqueIndex:idx_xhs_voucher_hash,priority:1" json:"channel_account_id"`
	XiaohongshuOrderLinkID uint   `gorm:"index;not null" json:"xiaohongshu_order_link_id"`
	TicketID               uint   `gorm:"uniqueIndex;not null" json:"ticket_id"`
	VoucherCodeHash        string `gorm:"size:64;not null;uniqueIndex:idx_xhs_voucher_hash,priority:2" json:"-"`
	VoucherCodeCiphertext  string `gorm:"type:text;not null" json:"-"`
	Status                 int    `gorm:"not null;default:1" json:"status"`
	VerifyID               string `gorm:"size:100" json:"verify_id,omitempty"`
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

// XiaohongshuWebhookEvent durably retains an authenticated callback before it
// is acknowledged. The decrypted payload remains encrypted at rest.
type XiaohongshuWebhookEvent struct {
	Base
	TenantID          uint   `gorm:"index;not null" json:"tenant_id"`
	ChannelAccountID  uint   `gorm:"not null;uniqueIndex:idx_xhs_webhook_payload,priority:1" json:"channel_account_id"`
	PayloadHash       string `gorm:"size:64;not null;uniqueIndex:idx_xhs_webhook_payload,priority:2" json:"payload_hash"`
	EventType         string `gorm:"size:80;not null;index" json:"event_type"`
	PayloadCiphertext string `gorm:"type:text;not null" json:"-"`
	// pending is retained for supported events. manual_review is fail-closed:
	// the callback was authenticated and persisted, but has no authorized local
	// business consumer.
	Status      string     `gorm:"size:20;not null;default:'pending';index" json:"status"`
	ReceivedAt  time.Time  `gorm:"not null;index" json:"received_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
	LastError   string     `gorm:"size:500" json:"last_error,omitempty"`
}

// ChannelRequest records the first body hash for an external endpoint and
// request id. A retry in the same endpoint with different content is rejected.
type ChannelRequest struct {
	Base
	ChannelAccountID uint       `gorm:"uniqueIndex:idx_channel_request,priority:1;not null" json:"channel_account_id"`
	RequestID        string     `gorm:"size:120;uniqueIndex:idx_channel_request,priority:3;not null" json:"request_id"`
	Nonce            string     `gorm:"size:120" json:"-"`
	Endpoint         string     `gorm:"size:120;uniqueIndex:idx_channel_request,priority:2;not null" json:"endpoint"`
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
