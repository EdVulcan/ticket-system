package model

// PaymentConfig 租户支付配置
type PaymentConfig struct {
	Base
	TenantID uint   `gorm:"uniqueIndex:idx_payment_provider,priority:1" json:"tenant_id"`
	Provider string `gorm:"size:20;not null;uniqueIndex:idx_payment_provider,priority:2" json:"provider"` // wechat, alipay
	AppID    string `gorm:"size:100;not null" json:"app_id"`
	MchID    string `gorm:"size:100" json:"mch_id"` // WeChat Only

	// Keys & Certs
	Key        string `gorm:"size:255" json:"key"`          // WeChat: ApiV3Key
	PrivateKey string `gorm:"type:text" json:"private_key"` // WeChat: PrivateKey PEM; Alipay: PrivateKey
	PublicKey  string `gorm:"type:text" json:"public_key"`  // Alipay: PublicKey
	SerialNo   string `gorm:"size:100" json:"serial_no"`    // WeChat: SerialNo

	NotifyURL string `gorm:"size:255" json:"notify_url"`
	Status    bool   `gorm:"default:true" json:"status"`
}

// Payment 支付流水
type Payment struct {
	Base
	TenantID      uint    `json:"tenant_id"`
	PaymentNo     string  `gorm:"size:50;uniqueIndex;not null" json:"payment_no"`
	OrderNo       string  `gorm:"size:50;index;not null" json:"order_no"` // 关联订单
	Amount        float64 `gorm:"type:decimal(10,2)" json:"amount"`
	Method        string  `gorm:"size:20" json:"method"`                   // cash, wechat, alipay
	Status        string  `gorm:"size:20;default:'pending'" json:"status"` // pending, paid, failed, refunded
	TransactionID string  `gorm:"size:100" json:"transaction_id"`          // 第三方流水号
	CodeURL       string  `gorm:"type:text" json:"code_url,omitempty"`
	PayType       string  `gorm:"size:20" json:"pay_type"` // bscanc (被扫), cscanb (主扫), jsapi
	AuthCode      string  `gorm:"size:100" json:"-"`       // 付款码 (For BScanC, not saved usually)
	ErrorMessage  string  `gorm:"size:255" json:"error_message"`
}
