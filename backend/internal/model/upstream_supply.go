package model

// UpstreamConnection is a supplier-owned procurement connection, never a sales
// channel. Credentials and live protocol activation are deliberately absent
// until the supplier's current protocol has been verified.
type UpstreamConnection struct {
	Base
	TenantID    uint   `gorm:"not null;index" json:"-"`
	Name        string `gorm:"size:100;not null" json:"name"`
	Provider    string `gorm:"size:30;not null" json:"provider"`
	Environment string `gorm:"size:20;not null;default:'production'" json:"environment"`
	Status      string `gorm:"size:20;not null;default:'draft'" json:"status"`
}

// ProductSupplyConfig is maintained separately from the ordinary product form,
// so old clients cannot silently reset supply settings during a product edit.
type ProductSupplyConfig struct {
	Base
	TenantID        uint  `gorm:"not null;index" json:"-"`
	ProductID       uint  `gorm:"not null;uniqueIndex" json:"product_id"`
	ActiveMappingID *uint `gorm:"index" json:"active_mapping_id"`
}

type UpstreamProductMapping struct {
	Base
	TenantID             uint   `gorm:"not null;index" json:"-"`
	ProductID            uint   `gorm:"not null;index" json:"product_id"`
	UpstreamConnectionID uint   `gorm:"not null;index" json:"upstream_connection_id"`
	ExternalProductCode  string `gorm:"size:200;not null" json:"external_product_code"`
	Status               string `gorm:"size:20;not null;default:'draft'" json:"status"`
}

// OrderItemSupplySnapshot is independent from mutable product configuration.
// Each item retains its original supply identity across date/slot changes.
// Local entries deliberately carry no external identity.
type OrderItemSupplySnapshot struct {
	ID                  uint   `gorm:"primaryKey" json:"id"`
	OrderItemID         uint   `gorm:"not null;uniqueIndex" json:"order_item_id"`
	SalesTenantID       uint   `gorm:"not null" json:"sales_tenant_id"`
	OrderID             uint   `gorm:"not null;index" json:"order_id"`
	Mode                string `gorm:"size:20;not null" json:"mode"`
	FulfillmentTenantID uint   `gorm:"not null" json:"fulfillment_tenant_id"`
	ScenicAreaID        uint   `gorm:"not null" json:"scenic_area_id"`
	ProductID           uint   `gorm:"not null" json:"product_id"`
	ProductRevisionID   uint   `gorm:"not null" json:"product_revision_id"`
	MappingID           uint   `gorm:"not null;default:0" json:"mapping_id"`
	ConnectionID        uint   `gorm:"not null;default:0" json:"connection_id"`
	Provider            string `gorm:"size:30;not null;default:''" json:"provider"`
	Environment         string `gorm:"size:20;not null" json:"environment"`
	ExternalProductCode string `gorm:"size:200;not null;default:''" json:"external_product_code"`
}

// These credentials are not routed to verification yet. The encrypted opaque
// payload may bind many local entitlements without changing TicketCode.
type ExternalAdmissionCredential struct {
	ID                uint   `gorm:"primaryKey"`
	TenantID          uint   `gorm:"not null"`
	ScenicAreaID      uint   `gorm:"not null"`
	ConnectionID      uint   `gorm:"not null"`
	Provider          string `gorm:"size:30;not null"`
	Environment       string `gorm:"size:20;not null"`
	PayloadHash       string `gorm:"size:64;not null"`
	PayloadCiphertext string `gorm:"type:text;not null" json:"-"`
}

type ExternalAdmissionBinding struct {
	ID               uint `gorm:"primaryKey"`
	CredentialID     uint `gorm:"not null;uniqueIndex:idx_external_admission_binding"`
	TicketID         uint `gorm:"not null;uniqueIndex:idx_external_admission_binding"`
	SupplySnapshotID uint `gorm:"not null"`
}
