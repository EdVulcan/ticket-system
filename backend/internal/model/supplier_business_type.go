package model

import "time"

// SupplierBusinessType describes what a supplier tenant fulfills. It is
// intentionally separate from TenantCapability: supplier/distributor/travel
// agency are market roles, while scenic/hotel are composable supply verticals.
type SupplierBusinessType struct {
	Base
	TenantID     uint       `gorm:"uniqueIndex:idx_supplier_business_type,priority:1;not null;index" json:"tenant_id"`
	BusinessType string     `gorm:"size:30;uniqueIndex:idx_supplier_business_type,priority:2;not null;check:chk_supplier_business_types_business_type,business_type IN ('scenic','hotel')" json:"business_type"` // scenic, hotel
	Status       string     `gorm:"size:20;not null;default:'suspended';index;check:chk_supplier_business_types_status,status IN ('active','suspended')" json:"status"`                                          // active, suspended
	ActivatedAt  *time.Time `json:"activated_at,omitempty"`
	Reason       string     `gorm:"size:255" json:"reason,omitempty"`
}
