package model

import "time"

// ProductOffer is the supplier-authorized commercial offer consumed by a
// distributor. It is the server-owned source of fulfillment and settlement
// data; a seller listing must never be treated as the source of truth.
type ProductOffer struct {
	Base
	SupplierTenantID        uint       `gorm:"uniqueIndex:idx_offer_pair,priority:1;index;not null" json:"supplier_tenant_id"`
	DistributorTenantID     uint       `gorm:"uniqueIndex:idx_offer_pair,priority:2;index;not null" json:"distributor_tenant_id"`
	SourceProductID         uint       `gorm:"uniqueIndex:idx_offer_pair,priority:3;index;not null" json:"source_product_id"`
	ProductRevisionID       uint       `gorm:"index" json:"product_revision_id"`
	FulfillmentScenicAreaID uint       `gorm:"index" json:"fulfillment_scenic_area_id"`
	SettlementPrice         float64    `gorm:"type:decimal(10,2);not null" json:"settlement_price"`
	MinimumRetailPriceCents int64      `gorm:"not null;default:0" json:"minimum_retail_price_cents"`
	CommissionBPS           int64      `gorm:"not null;default:0" json:"commission_bps"`
	Quota                   int        `gorm:"not null;default:0" json:"quota"` // 0 means unlimited
	ReservedQuantity        int        `gorm:"not null;default:0" json:"reserved_quantity"`
	Status                  string     `gorm:"size:20;not null;default:'active';index" json:"status"` // active, suspended, expired
	SalesStartAt            *time.Time `json:"sales_start_at,omitempty"`
	SalesEndAt              *time.Time `json:"sales_end_at,omitempty"`
	AllowedChannels         string     `gorm:"size:255" json:"allowed_channels,omitempty"`
}

// SellerListing is a sell-side mapping. ProductID points to the legacy
// presentation row while ProductOfferID supplies all fulfillment authority.
type SellerListing struct {
	Base
	SellerTenantID   uint    `gorm:"index;not null" json:"seller_tenant_id"`
	ProductOfferID   uint    `gorm:"index;not null" json:"product_offer_id"`
	ProductID        uint    `gorm:"uniqueIndex;not null" json:"product_id"`
	Name             string  `gorm:"size:100;not null" json:"name"`
	RetailPrice      float64 `gorm:"type:decimal(10,2);not null" json:"retail_price"`
	RetailPriceCents int64   `gorm:"not null;default:0" json:"retail_price_cents"`
	Status           string  `gorm:"size:20;not null;default:'online'" json:"status"`
}
