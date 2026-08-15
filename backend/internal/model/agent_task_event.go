package model

// AgentTaskEvent is the append-only audit stream for provider and tool
// interactions. Arguments and results are scrubbed by the service before
// persistence; credentials, SQL, and unbounded provider payloads never belong
// in this table.
type AgentTaskEvent struct {
	Base
	TenantID          uint   `gorm:"index;not null" json:"tenant_id"`
	TaskID            uint   `gorm:"index;not null;uniqueIndex:idx_agent_task_event_sequence,priority:1" json:"task_id"`
	ActorUserID       uint   `gorm:"index;not null" json:"actor_user_id"`
	ActorRole         string `gorm:"size:30;not null" json:"actor_role"`
	Sequence          int    `gorm:"not null;uniqueIndex:idx_agent_task_event_sequence,priority:2" json:"sequence"`
	EventType         string `gorm:"size:40;not null;index" json:"event_type"`
	ToolName          string `gorm:"size:100;index" json:"tool_name,omitempty"`
	ToolVersion       string `gorm:"size:20" json:"tool_version,omitempty"`
	ToolCallID        string `gorm:"size:120;index" json:"tool_call_id,omitempty"`
	IdempotencyKey    string `gorm:"size:120;index" json:"idempotency_key,omitempty"`
	Status            string `gorm:"size:30;not null" json:"status"`
	ErrorCode         string `gorm:"size:80" json:"error_code,omitempty"`
	ArgumentsJSON     string `gorm:"type:text" json:"arguments_json,omitempty"`
	ResultJSON        string `gorm:"type:text" json:"result_json,omitempty"`
	Provider          string `gorm:"size:30" json:"provider,omitempty"`
	Model             string `gorm:"size:100" json:"model,omitempty"`
	ConfigVersion     int    `gorm:"not null;default:0" json:"config_version"`
	ProviderRequestID string `gorm:"size:120;index" json:"provider_request_id,omitempty"`
	TokenCount        int64  `gorm:"not null;default:0" json:"token_count"`
	DurationMS        int64  `gorm:"not null;default:0" json:"duration_ms"`
}
