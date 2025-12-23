package model

type Policy struct {
	Base
	TenantID uint   `json:"tenant_id" gorm:"index;not null"`
	Title    string `json:"title" gorm:"size:255;not null"`
	Category string `json:"category" gorm:"size:50;index"` // e.g., "Admission", "Refund", "Pet", "Discount"
	Content  string `json:"content" gorm:"type:text"`
	IsActive bool   `json:"is_active" gorm:"default:true"`
}
