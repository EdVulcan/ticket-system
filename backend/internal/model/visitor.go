package model

// VisitorInput is the per-ticket visitor payload accepted when an order is
// created. It is intentionally not persisted through GORM's order association;
// OrderVisitor is the immutable record written after tickets receive IDs.
type VisitorInput struct {
	Name       string `json:"name"`
	Phone      string `json:"phone"`
	IdentityNo string `json:"identity_no"`
	Region     string `json:"region"`
}

// OrderVisitor stores the visitor snapshot used for one issued ticket. Keeping
// it separate from the mutable contact fields makes per-ticket identity data
// queryable and auditable without changing the sales order owner.
type OrderVisitor struct {
	Base
	TenantID    uint   `gorm:"index;not null" json:"tenant_id"`
	OrderID     uint   `gorm:"index;not null" json:"order_id"`
	OrderItemID uint   `gorm:"index;not null" json:"order_item_id"`
	TicketID    uint   `gorm:"uniqueIndex;not null" json:"ticket_id"`
	TicketCode  string `gorm:"size:50;index;not null" json:"ticket_code"`
	Sequence    int    `gorm:"not null" json:"sequence"`
	Name        string `gorm:"size:50;not null" json:"name"`
	Phone       string `gorm:"size:20" json:"phone"`
	IdentityNo  string `gorm:"size:100" json:"identity_no"`
	Region      string `gorm:"size:50" json:"region"`
}
