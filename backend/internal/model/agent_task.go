package model

import "time"

// AgentTask is the durable, tenant-scoped conversation and approval boundary
// for the floating assistant. It stores normalized planning facts, never a
// provider credential, and does not create domain records before confirmation.
type AgentTask struct {
	Base
	TenantID         uint       `gorm:"index;not null;uniqueIndex:idx_agent_task_idempotency,priority:1" json:"tenant_id"`
	ActorUserID      uint       `gorm:"index;not null;uniqueIndex:idx_agent_task_idempotency,priority:2" json:"actor_user_id"`
	ActorRole        string     `gorm:"size:30;not null" json:"actor_role"`
	OperationType    string     `gorm:"size:60;index;not null" json:"operation_type"`
	State            string     `gorm:"size:30;index;not null" json:"state"`
	InputText        string     `gorm:"size:2000;not null" json:"input_text"`
	ContextJSON      string     `gorm:"type:text;not null" json:"context_json"`
	MissingJSON      string     `gorm:"type:text;not null" json:"missing_json"`
	PreviewJSON      string     `gorm:"type:text" json:"preview_json,omitempty"`
	ResultJSON       string     `gorm:"type:text" json:"result_json,omitempty"`
	PlanHash         string     `gorm:"size:64;index" json:"plan_hash,omitempty"`
	LinkedPlanID     uint       `gorm:"index" json:"linked_plan_id,omitempty"`
	IdempotencyKey   string     `gorm:"size:120;not null;uniqueIndex:idx_agent_task_idempotency,priority:3" json:"idempotency_key"`
	LastTurnKey      string     `gorm:"size:120" json:"-"`
	LastResponseJSON string     `gorm:"type:text" json:"-"`
	Version          int        `gorm:"not null;default:1" json:"version"`
	ExpiresAt        time.Time  `gorm:"index;not null" json:"expires_at"`
	ConfirmedAt      *time.Time `json:"confirmed_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	ErrorMessage     string     `gorm:"size:1000" json:"error_message,omitempty"`
	// ProtocolMode is fixed when a task is created. Existing rows remain on
	// legacy_json so a provider/config change cannot reinterpret an in-flight
	// conversation.
	ProtocolMode string `gorm:"size:20;not null;default:'legacy_json'" json:"protocol_mode"`
	ResponseText string `gorm:"type:text" json:"response_text,omitempty"`
}
