package model

import "time"

// CatalogBatchChangePlan is the durable, tenant-owned approval boundary for
// catalog rule changes. It intentionally stores the normalized operation and
// rendered preview so an approval never has to re-interpret free-form input.
type CatalogBatchChangePlan struct {
	Base
	TenantID       uint                     `gorm:"index;not null;uniqueIndex:idx_catalog_batch_plan_idempotency,priority:1" json:"tenant_id"`
	ActorUserID    uint                     `gorm:"index;not null" json:"actor_user_id"`
	ActorRole      string                   `gorm:"size:30;not null" json:"actor_role"`
	InputText      string                   `gorm:"size:2000;not null" json:"input_text"`
	OperationJSON  string                   `gorm:"type:text;not null" json:"operation_json"`
	PlanHash       string                   `gorm:"size:64;index;not null" json:"plan_hash"`
	IdempotencyKey string                   `gorm:"size:120;not null;uniqueIndex:idx_catalog_batch_plan_idempotency,priority:2" json:"idempotency_key"`
	Status         string                   `gorm:"size:20;index;not null" json:"status"`
	PreviewJSON    string                   `gorm:"type:text;not null" json:"preview_json"`
	ExpiresAt      time.Time                `gorm:"index;not null" json:"expires_at"`
	ConfirmedAt    *time.Time               `json:"confirmed_at,omitempty"`
	CompletedAt    *time.Time               `json:"completed_at,omitempty"`
	ErrorMessage   string                   `gorm:"size:1000" json:"error_message,omitempty"`
	Lines          []CatalogBatchChangeLine `gorm:"foreignKey:PlanID" json:"lines,omitempty"`
}

// CatalogBatchChangeLine is one product-scoped before/after fact inside a
// plan. It is tenant-owned and never rewrites historical tickets.
type CatalogBatchChangeLine struct {
	Base
	PlanID              uint                   `gorm:"index;not null" json:"plan_id"`
	TenantID            uint                   `gorm:"index;not null" json:"tenant_id"`
	ProductID           uint                   `gorm:"index;not null" json:"product_id"`
	ProductName         string                 `gorm:"size:100;not null" json:"product_name"`
	ScenicAreaID        uint                   `gorm:"index;not null" json:"scenic_area_id"`
	BeforeRevisionID    uint                   `gorm:"index;not null" json:"before_revision_id"`
	AfterRevisionID     uint                   `gorm:"index" json:"after_revision_id,omitempty"`
	BeforeJSON          string                 `gorm:"type:text;not null" json:"before_json"`
	AfterJSON           string                 `gorm:"type:text;not null" json:"after_json"`
	Status              string                 `gorm:"size:20;index;not null" json:"status"`
	AffectedOfferCount  int                    `gorm:"not null;default:0" json:"affected_offer_count"`
	AffectedBundleCount int                    `gorm:"not null;default:0" json:"affected_bundle_count"`
	ErrorMessage        string                 `gorm:"size:1000" json:"error_message,omitempty"`
	Plan                CatalogBatchChangePlan `gorm:"foreignKey:PlanID" json:"-"`
}
