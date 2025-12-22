package model

// CapitalAccount 资金账户表
// 用于管理分销商在供应商处的 "预付款" 与 "授信额度"
type CapitalAccount struct {
	Base
	OwnerTenantID   uint `json:"owner_tenant_id" gorm:"index"`   // 账户所有者(分销商)
	ManagerTenantID uint `json:"manager_tenant_id" gorm:"index"` // 账户管理者(供应商/平台)

	Balance      float64 `json:"balance" gorm:"type:decimal(15,2);default:0"`       // 预付款余额 (Cash)
	CreditLine   float64 `json:"credit_line" gorm:"type:decimal(15,2);default:0"`   // 授信额度 (Credit Limit)
	UsedCredit   float64 `json:"used_credit" gorm:"type:decimal(15,2);default:0"`   // 已用授信
	FrozenAmount float64 `json:"frozen_amount" gorm:"type:decimal(15,2);default:0"` // 冻结金额 (下单后未核销/结算前冻结)

	Status string `json:"status" gorm:"size:20;default:'active'"` // active(正常), frozen(冻结)
}

// TransactionRecord 资金流水表
// 记录所有的充值、消费、退款、授信调整记录
type TransactionRecord struct {
	Base
	AccountID      uint    `json:"account_id" gorm:"index"`
	Type           string  `json:"type" gorm:"size:20"`              // recharge(充值), payment(支付), refund(退款), credit_adjust(授信调整)
	Amount         float64 `json:"amount" gorm:"type:decimal(15,2)"` // 变动金额 (+/-)
	BalanceAfter   float64 `json:"balance_after" gorm:"type:decimal(15,2)"`
	RelatedOrderNo string  `json:"related_order_no" gorm:"size:50;index"` // 关联单号
	Memo           string  `json:"memo" gorm:"size:255"`
	OperatorID     uint    `json:"operator_id"` // 操作人(User ID)
}
