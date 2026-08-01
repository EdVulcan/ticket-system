package model

// BundleProduct is a distributor-owned customer-facing product assembled from
// imported supplier listings. Fulfillment authority remains on each component.
type BundleProduct struct {
	Base
	SellerTenantID   uint          `gorm:"index;not null" json:"seller_tenant_id"`
	Name             string        `gorm:"size:100;not null" json:"name"`
	Type             string        `gorm:"size:20;not null;index" json:"type"` // online, offline
	RetailPriceCents int64         `gorm:"not null" json:"retail_price_cents"`
	Status           string        `gorm:"size:20;not null;default:'offline';index" json:"status"` // online, offline
	CurrentVersionID uint          `gorm:"index;default:null" json:"current_version_id"`
	CurrentVersion   BundleVersion `gorm:"foreignKey:CurrentVersionID" json:"current_version,omitempty"`
}

// BundleVersion freezes the commercial composition used by an order. Editing
// a bundle creates a new version, so sold orders never change underneath users.
type BundleVersion struct {
	Base
	BundleProductID  uint              `gorm:"uniqueIndex:idx_bundle_version,priority:1;index;not null" json:"bundle_product_id"`
	SellerTenantID   uint              `gorm:"index;not null" json:"seller_tenant_id"`
	Version          int               `gorm:"uniqueIndex:idx_bundle_version,priority:2;not null" json:"version"`
	RetailPriceCents int64             `gorm:"not null" json:"retail_price_cents"`
	Status           string            `gorm:"size:20;not null;default:'active';index" json:"status"` // active, retired
	Components       []BundleComponent `gorm:"foreignKey:BundleVersionID" json:"components,omitempty"`
}

// BundleComponent stores server-controlled supplier facts for one bundle
// version. SellerProductID points to the distributor's imported listing.
type BundleComponent struct {
	Base
	BundleVersionID          uint  `gorm:"index;not null" json:"bundle_version_id"`
	SellerTenantID           uint  `gorm:"index;not null" json:"seller_tenant_id"`
	SellerProductID          uint  `gorm:"index;not null" json:"seller_product_id"`
	ProductOfferID           uint  `gorm:"index;not null" json:"product_offer_id"`
	SupplierTenantID         uint  `gorm:"index;not null" json:"supplier_tenant_id"`
	SourceProductID          uint  `gorm:"index;not null" json:"source_product_id"`
	ProductRevisionID        uint  `gorm:"index;not null" json:"product_revision_id"`
	FulfillmentScenicAreaID  uint  `gorm:"index;not null" json:"fulfillment_scenic_area_id"`
	Quantity                 int   `gorm:"not null" json:"quantity"`
	RetailAllocationCents    int64 `gorm:"not null" json:"retail_allocation_cents"`
	SettlementUnitPriceCents int64 `gorm:"not null" json:"settlement_unit_price_cents"`
	CommissionBPS            int64 `gorm:"not null;default:0" json:"commission_bps"`
}
