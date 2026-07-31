package model

// PaymentConfig 租户支付配置
type PaymentConfig struct {
	Base
	TenantID uint   `gorm:"uniqueIndex:idx_payment_provider,priority:1" json:"tenant_id"`
	Provider string `gorm:"size:20;not null;uniqueIndex:idx_payment_provider,priority:2" json:"provider"` // wechat, alipay
	AppID    string `gorm:"size:100;not null" json:"app_id"`
	MchID    string `gorm:"size:100" json:"mch_id"` // WeChat Only

	// Keys & Certs
	Key                 string `gorm:"size:255" json:"key"`                    // WeChat: ApiV3Key
	PrivateKey          string `gorm:"type:text" json:"private_key"`           // WeChat: PrivateKey PEM; Alipay: PrivateKey
	PublicKey           string `gorm:"type:text" json:"public_key"`            // Alipay: PublicKey
	SerialNo            string `gorm:"size:100" json:"serial_no"`              // WeChat: SerialNo
	PlatformPublicKey   string `gorm:"type:text" json:"platform_public_key"`   // WeChat V3 platform public key/certificate
	PlatformPublicKeyID string `gorm:"size:100" json:"platform_public_key_id"` // WeChat V3 platform key serial

	NotifyURL string `gorm:"size:255" json:"notify_url"`
	Status    bool   `gorm:"default:true" json:"status"`
}

// Payment 支付流水
type Payment struct {
	Base
	TenantID       uint    `json:"tenant_id"`
	PaymentNo      string  `gorm:"size:50;uniqueIndex;not null" json:"payment_no"`
	OrderNo        string  `gorm:"size:50;index;not null" json:"order_no"` // 关联订单
	Amount         float64 `gorm:"type:decimal(10,2)" json:"amount"`
	RefundedAmount float64 `gorm:"type:decimal(10,2);default:0" json:"refunded_amount"`
	Method         string  `gorm:"size:20" json:"method"`                   // cash, wechat, alipay
	Status         string  `gorm:"size:20;default:'pending'" json:"status"` // pending, paid, failed, refunded
	TransactionID  string  `gorm:"size:100" json:"transaction_id"`          // 第三方流水号
	CodeURL        string  `gorm:"type:text" json:"code_url,omitempty"`
	PayType        string  `gorm:"size:20" json:"pay_type"` // bscanc (被扫), cscanb (主扫), jsapi
	AuthCode       string  `gorm:"size:100" json:"-"`       // 付款码 (For BScanC, not saved usually)
	ErrorMessage   string  `gorm:"size:255" json:"error_message"`
	ShiftID        uint    `gorm:"index" json:"shift_id,omitempty"`
	DeviceID       uint    `gorm:"index" json:"device_id,omitempty"`
	OperatorID     uint    `gorm:"index" json:"operator_id,omitempty"`
}

// Refund is an immutable business request/result for a payment refund. The
// idempotency key prevents double refunds when a cashier retries a request.
type Refund struct {
	Base
	TenantID         uint    `gorm:"index;not null;uniqueIndex:idx_refund_idempotency,priority:1" json:"tenant_id"`
	RefundNo         string  `gorm:"size:50;uniqueIndex;not null" json:"refund_no"`
	IdempotencyKey   string  `gorm:"size:100;uniqueIndex:idx_refund_idempotency,priority:2;not null" json:"idempotency_key"`
	OrderNo          string  `gorm:"size:50;index;not null" json:"order_no"`
	PaymentID        uint    `gorm:"index;not null" json:"payment_id"`
	Amount           float64 `gorm:"type:decimal(10,2);not null" json:"amount"`
	Method           string  `gorm:"size:20;not null" json:"method"`
	Status           string  `gorm:"size:20;not null;default:'succeeded'" json:"status"` // succeeded, pending, failed
	Reason           string  `gorm:"size:255" json:"reason"`
	TicketCodesJSON  string  `gorm:"type:text" json:"ticket_codes,omitempty"`
	ProviderRefundID string  `gorm:"size:100" json:"provider_refund_id,omitempty"`
}
