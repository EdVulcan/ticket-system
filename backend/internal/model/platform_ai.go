package model

import "time"

// PlatformAIConfig is the platform-owned default model connection. The raw
// provider key is encrypted and is never serialized to a tenant response.
type PlatformAIConfig struct {
	Base
	ConfigKey                  string     `gorm:"size:30;uniqueIndex;not null" json:"config_key"`
	Provider                   string     `gorm:"size:30;not null" json:"provider"`
	BaseURL                    string     `gorm:"size:255;not null" json:"base_url"`
	Model                      string     `gorm:"size:100;not null" json:"model"`
	APIKeyCiphertext           string     `gorm:"type:text" json:"-"`
	Enabled                    bool       `gorm:"not null;default:false" json:"enabled"`
	DefaultMonthlyRequestLimit int        `gorm:"not null;default:100" json:"default_monthly_request_limit"`
	DefaultMonthlyTokenLimit   int64      `gorm:"not null;default:200000" json:"default_monthly_token_limit"`
	RequestTimeoutSeconds      int        `gorm:"not null;default:30" json:"request_timeout_seconds"`
	MaxOutputTokens            int        `gorm:"not null;default:0" json:"max_output_tokens"`
	Temperature                float64    `gorm:"not null;default:0.1" json:"temperature"`
	ConfigVersion              int        `gorm:"not null;default:1" json:"config_version"`
	LastTestedAt               *time.Time `json:"last_tested_at,omitempty"`
	LastTestStatus             string     `gorm:"size:20" json:"last_test_status,omitempty"`
	LastTestError              string     `gorm:"size:255" json:"last_test_error,omitempty"`
	UpdatedBy                  uint       `gorm:"index" json:"updated_by"`
}

// AIUsageMonth is the tenant-scoped cost ledger for the platform provider.
// Requests are counted even when the provider fails after reservation so a
// retry storm cannot bypass the platform's monthly budget.
type AIUsageMonth struct {
	Base
	TenantID     uint   `gorm:"uniqueIndex:idx_ai_usage_month,priority:1;index;not null" json:"tenant_id"`
	Period       string `gorm:"size:7;uniqueIndex:idx_ai_usage_month,priority:2;not null" json:"period"`
	RequestCount int    `gorm:"not null;default:0" json:"request_count"`
	TokenCount   int64  `gorm:"not null;default:0" json:"token_count"`
}
