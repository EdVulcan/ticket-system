package model

// ScenicArea is the physical fulfillment and verification boundary. It is
// deliberately separate from Tenant because one supplier may operate several
// parks or zones in the future.
type ScenicArea struct {
	Base
	TenantID uint   `gorm:"index;not null;uniqueIndex:idx_scenic_tenant_code,priority:1" json:"tenant_id"`
	Code     string `gorm:"size:50;not null;uniqueIndex:idx_scenic_tenant_code,priority:2" json:"code"`
	Name     string `gorm:"size:100;not null" json:"name"`
	Status   string `gorm:"size:20;not null;default:'active';index" json:"status"` // active, frozen, closed
}
