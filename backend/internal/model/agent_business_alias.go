package model

// AgentBusinessAlias stores tenant-owned vocabulary for the restricted
// assistant. The canonical value is a business name, never a database ID;
// every use is resolved again against the current tenant catalog.
type AgentBusinessAlias struct {
	Base
	TenantID      uint   `gorm:"uniqueIndex:idx_agent_business_alias,priority:1;index;not null" json:"tenant_id"`
	Kind          string `gorm:"size:20;uniqueIndex:idx_agent_business_alias,priority:2;not null" json:"kind"`
	Alias         string `gorm:"size:100;uniqueIndex:idx_agent_business_alias,priority:3;not null" json:"alias"`
	CanonicalName string `gorm:"size:100;not null" json:"canonical_name"`
	UpdatedBy     uint   `gorm:"index" json:"updated_by"`
}
