package model

// FulfillmentOrder is the supplier-side projection of a sales order. A sales
// order can create multiple fulfillment orders when products come from more
// than one supplier or scenic area.
type FulfillmentOrder struct {
	Base
	FulfillmentNo     string  `gorm:"size:60;uniqueIndex;not null" json:"fulfillment_no"`
	SalesOrderID      uint    `gorm:"index;not null" json:"sales_order_id"`
	SalesOrderNo      string  `gorm:"size:50;index;not null" json:"sales_order_no"`
	SalesTenantID     uint    `gorm:"index;not null" json:"sales_tenant_id"`
	SupplierTenantID  uint    `gorm:"index;not null" json:"supplier_tenant_id"`
	ScenicAreaID      uint    `gorm:"index" json:"scenic_area_id"`
	ProductRevisionID uint    `gorm:"index" json:"product_revision_id"`
	SettlementAmount  float64 `gorm:"type:decimal(10,2);not null" json:"settlement_amount"`
	SettlementStatus  string  `gorm:"size:20;not null;default:'open';index" json:"settlement_status"`
	Status            string  `gorm:"size:20;not null;default:'reserved';index" json:"status"` // reserved, paid, fulfilled, cancelled
}

// TicketEntitlement is the immutable supplier-owned projection of a sold
// ticket. The existing Ticket row remains the operational QR record while this
// row makes supplier ownership explicit for fulfillment and settlement.
type TicketEntitlement struct {
	Base
	FulfillmentOrderID uint   `gorm:"index;not null" json:"fulfillment_order_id"`
	TicketID           uint   `gorm:"uniqueIndex;not null" json:"ticket_id"`
	TicketCode         string `gorm:"size:50;index;not null" json:"ticket_code"`
	SalesTenantID      uint   `gorm:"index;not null" json:"sales_tenant_id"`
	SupplierTenantID   uint   `gorm:"index;not null" json:"supplier_tenant_id"`
	ScenicAreaID       uint   `gorm:"index" json:"scenic_area_id"`
	Status             string `gorm:"size:20;not null;default:'issued';index" json:"status"` // issued, active, used, refunded, void
	RuleSnapshot       string `gorm:"type:text" json:"-"`
}
