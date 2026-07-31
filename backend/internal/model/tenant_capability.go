package model

import "time"

// TenantCapability keeps business abilities composable. A tenant may be both
// a scenic supplier and a distributor, so this must not be represented by a
// single mutually-exclusive tenant type.
type TenantCapability struct {
	Base
	TenantID   uint       `gorm:"uniqueIndex:idx_tenant_capability,priority:1;not null;index" json:"tenant_id"`
	Capability string     `gorm:"size:30;uniqueIndex:idx_tenant_capability,priority:2;not null" json:"capability"` // supplier, distributor, travel_agency
	Status     string     `gorm:"size:20;not null;default:'pending';index" json:"status"`                          // pending, active, suspended, rejected
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Reason     string     `gorm:"size:255" json:"reason,omitempty"`
}
