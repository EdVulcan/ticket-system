package model

type StaffResourceScope struct {
	Base
	TenantID     uint   `gorm:"not null;index;uniqueIndex:idx_staff_resource_scope,priority:1" json:"tenant_id"`
	StaffID      uint   `gorm:"not null;index;uniqueIndex:idx_staff_resource_scope,priority:2" json:"staff_id"`
	ResourceType string `gorm:"size:30;not null;uniqueIndex:idx_staff_resource_scope,priority:3" json:"resource_type"` // scenic_area, checkpoint, device
	ResourceID   uint   `gorm:"not null;index;uniqueIndex:idx_staff_resource_scope,priority:4" json:"resource_id"`
}
