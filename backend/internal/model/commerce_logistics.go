package model

import "time"

// CommerceShipment is the retail fulfillment projection for one package.
// Shipment identity is intentionally not unique per order: the first release
// creates one shipment, while the model remains compatible with multi-package
// fulfillment later.
type CommerceShipment struct {
	Base
	TenantID        uint                    `gorm:"not null;index:idx_commerce_shipments_scope" json:"tenant_id"`
	OrderID         uint                    `gorm:"not null;index:idx_commerce_shipments_scope" json:"order_id"`
	LocationID      uint                    `gorm:"not null;index:idx_commerce_shipments_scope" json:"location_id"`
	ShipmentNo      string                  `gorm:"size:80;not null;uniqueIndex:idx_commerce_shipments_tenant_no,priority:1" json:"shipment_no"`
	CarrierCode     string                  `gorm:"size:80;not null;default:''" json:"carrier_code,omitempty"`
	CarrierName     string                  `gorm:"size:120;not null;default:''" json:"carrier_name,omitempty"`
	TrackingNo      string                  `gorm:"size:120;not null;default:'';index:idx_commerce_shipments_tracking" json:"tracking_no,omitempty"`
	Status          string                  `gorm:"size:30;not null;default:'pending_shipment';index;check:chk_commerce_shipments_status,status IN ('pending_shipment','shipped','in_transit','out_for_delivery','delivered','exception','cancelled')" json:"status"`
	Source          string                  `gorm:"size:20;not null;default:'manual';check:chk_commerce_shipments_source,source IN ('manual','provider')" json:"source"`
	ShippedAt       *time.Time              `json:"shipped_at,omitempty"`
	DeliveredAt     *time.Time              `json:"delivered_at,omitempty"`
	AddressSnapshot string                  `gorm:"type:text;not null;default:''" json:"address_snapshot,omitempty"`
	Events          []CommerceShipmentEvent `gorm:"foreignKey:ShipmentID" json:"events,omitempty"`
}

// CommerceShipmentEvent is append-only. Nullable identities are used so
// PostgreSQL permits multiple manual events without forcing a fake provider
// identity. Provider identities remain unique within a tenant/shipment/source.
type CommerceShipmentEvent struct {
	Base
	TenantID        uint      `gorm:"not null;index:idx_commerce_shipment_events_scope;uniqueIndex:idx_commerce_shipment_provider_event,priority:1;uniqueIndex:idx_commerce_shipment_event_idempotency,priority:1" json:"tenant_id"`
	ShipmentID      uint      `gorm:"not null;index:idx_commerce_shipment_events_scope;uniqueIndex:idx_commerce_shipment_provider_event,priority:2;uniqueIndex:idx_commerce_shipment_event_idempotency,priority:2" json:"shipment_id"`
	ProviderEventID *string   `gorm:"size:160;uniqueIndex:idx_commerce_shipment_provider_event,priority:4" json:"provider_event_id,omitempty"`
	Source          string    `gorm:"size:20;not null;default:'manual';index:idx_commerce_shipment_provider_event,priority:3;uniqueIndex:idx_commerce_shipment_event_idempotency,priority:3;check:chk_commerce_shipment_events_source,source IN ('manual','provider')" json:"source"`
	Status          string    `gorm:"size:30;not null;check:chk_commerce_shipment_events_status,status IN ('pending_shipment','shipped','in_transit','out_for_delivery','delivered','exception','cancelled')" json:"status"`
	Description     string    `gorm:"size:500;not null;default:''" json:"description,omitempty"`
	OccurredAt      time.Time `gorm:"not null;index" json:"occurred_at"`
	Location        string    `gorm:"size:160;not null;default:''" json:"location,omitempty"`
	PayloadHash     string    `gorm:"size:128;not null;default:''" json:"payload_hash,omitempty"`
	PayloadJSON     string    `gorm:"type:text;not null;default:''" json:"payload,omitempty"`
	IdempotencyKey  *string   `gorm:"size:160;uniqueIndex:idx_commerce_shipment_event_idempotency,priority:4" json:"-"`
}
