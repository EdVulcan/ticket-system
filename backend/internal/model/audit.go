package model

// AuditLog records sensitive cross-tenant or money-affecting operations. Rows
// are append-only by service convention; there is no update API.
type AuditLog struct {
	Base
	ActorUserID uint   `gorm:"index" json:"actor_user_id"`
	ActorRole   string `gorm:"size:50" json:"actor_role"`
	Scope       string `gorm:"size:20;not null;index" json:"scope"` // platform, tenant
	TenantID    uint   `gorm:"index" json:"tenant_id"`
	Action      string `gorm:"size:80;not null;index" json:"action"`
	TargetType  string `gorm:"size:40;not null" json:"target_type"`
	TargetID    uint   `json:"target_id"`
	Reason      string `gorm:"size:255" json:"reason"`
	BeforeJSON  string `gorm:"type:text" json:"before_json,omitempty"`
	AfterJSON   string `gorm:"type:text" json:"after_json,omitempty"`
}
