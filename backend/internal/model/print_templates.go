package model

import "time"

// PrintTemplate is a tenant/scenic-area-owned binding. ProductID and
// ProductRevisionID are both zero for the scenic-area default. A non-zero
// ProductID binds an override to that product; ProductRevisionID can narrow
// it to one immutable product revision.
type PrintTemplate struct {
	Base
	TenantID          uint   `gorm:"index;not null" json:"tenant_id"`
	ScenicAreaID      uint   `gorm:"index;not null" json:"scenic_area_id"`
	ProductID         uint   `gorm:"index;not null;default:0" json:"product_id"`
	ProductRevisionID uint   `gorm:"index;not null;default:0" json:"product_revision_id"`
	Name              string `gorm:"size:100;not null" json:"name"`
	Status            string `gorm:"size:20;not null;default:'active';index" json:"status"` // active, disabled
	CurrentRevisionID uint   `gorm:"index;not null;default:0" json:"current_revision_id"`
	PaperWidthMM      int    `gorm:"not null;default:58" json:"paper_width_mm"`
	PrinterProfile    string `gorm:"size:30;not null;default:'escpos'" json:"printer_profile"`
}

// PrintTemplateRevision is immutable once published. A new edit always
// creates/updates a draft revision and publication atomically switches the
// template's current revision pointer.
type PrintTemplateRevision struct {
	Base
	TenantID       uint       `gorm:"index;not null" json:"tenant_id"`
	ScenicAreaID   uint       `gorm:"index;not null" json:"scenic_area_id"`
	TemplateID     uint       `gorm:"index;not null;uniqueIndex:idx_print_template_revision_version,priority:1" json:"template_id"`
	Version        int        `gorm:"uniqueIndex:idx_print_template_revision_version,priority:2;not null" json:"version"`
	Status         string     `gorm:"size:20;not null;index" json:"status"` // draft, published, retired
	DefinitionJSON string     `gorm:"type:text;not null" json:"definition_json"`
	DefinitionHash string     `gorm:"size:64;not null;index" json:"definition_hash"`
	CreatedBy      uint       `gorm:"not null" json:"created_by"`
	PublishedBy    uint       `json:"published_by,omitempty"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
}

// PrintTemplateBlock is intentionally structured. It is not HTML/JavaScript
// and therefore cannot turn the printer into an arbitrary code execution
// surface.
type PrintTemplateBlock struct {
	Kind      string `json:"kind"`
	Text      string `json:"text,omitempty"`
	Align     string `json:"align,omitempty"` // left, center, right
	FontSize  int    `json:"font_size,omitempty"`
	Bold      bool   `json:"bold,omitempty"`
	Spacing   int    `json:"spacing,omitempty"`
	Separator bool   `json:"separator,omitempty"`
}

type PrintTemplateDefinition struct {
	SchemaVersion int                  `json:"schema_version"`
	PaperWidthMM  int                  `json:"paper_width_mm"`
	Blocks        []PrintTemplateBlock `json:"blocks"`
}

// PrintDocument is the server-rendered, immutable payload consumed by a
// future hardware adapter. It contains business values, never tenant IDs,
// secrets, settlement prices, or provider credentials.
type PrintDocument struct {
	SchemaVersion int                  `json:"schema_version"`
	PaperWidthMM  int                  `json:"paper_width_mm"`
	TemplateName  string               `json:"template_name"`
	ScenicArea    string               `json:"scenic_area"`
	Blocks        []PrintDocumentBlock `json:"blocks"`
}

type PrintDocumentBlock struct {
	Kind      string `json:"kind"`
	Text      string `json:"text,omitempty"`
	Align     string `json:"align"`
	FontSize  int    `json:"font_size"`
	Bold      bool   `json:"bold"`
	Spacing   int    `json:"spacing"`
	Separator bool   `json:"separator"`
}
