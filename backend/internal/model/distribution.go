package model

// DistributorRelationship 分销关系表
// 记录 "分销商 (Agent)" 与 "供应商 (Supplier)" 之间的合作关系
type DistributorRelationship struct {
	Base
	AgentTenantID    uint   `json:"agent_tenant_id" gorm:"index"`                  // 代理商(分销商)主体ID
	SupplierTenantID uint   `json:"supplier_tenant_id" gorm:"index"`               // 供应商(货源)主体ID
	Status           string `json:"status" gorm:"size:20;default:'none'"`          // 分销合作: none, pending, active, rejected, suspended
	TravelStatus     string `json:"travel_status" gorm:"size:20;default:'none'"`   // 团队合作: none, pending, active, rejected, suspended
	AgentLevel       string `json:"agent_level" gorm:"size:20;default:'standard'"` // 分销等级: standard(标准), core(核心), diamond(钻金)
	Memo             string `json:"memo" gorm:"size:255"`                          // 备注说明

	// GORM Relations (Preload usage)
	AgentTenant    Tenant `json:"agent_tenant" gorm:"foreignKey:AgentTenantID"`
	SupplierTenant Tenant `json:"supplier_tenant" gorm:"foreignKey:SupplierTenantID"`
}
