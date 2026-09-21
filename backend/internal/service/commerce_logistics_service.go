package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrCommerceLogisticsInvalid  = errors.New("commerce logistics is invalid")
	ErrCommerceLogisticsConflict = errors.New("commerce logistics has a conflicting identity")
	ErrCommerceLogisticsState    = errors.New("commerce logistics state is invalid")
)

const (
	shipmentSourceManual   = "manual"
	shipmentSourceProvider = "provider"
)

var validShipmentStatuses = map[string]bool{
	"pending_shipment": true,
	"shipped":          true,
	"in_transit":       true,
	"out_for_delivery": true,
	"delivered":        true,
	"exception":        true,
	"cancelled":        true,
}

// CommerceCarrierProvider is deliberately an adapter boundary. The default
// implementation remains manual: no provider response is invented when no
// carrier integration is configured.
type CommerceCarrierProvider interface {
	QueryTracking(context.Context, CommerceCarrierQuery) ([]CommerceCarrierEvent, error)
}

type CommerceCarrierQuery struct {
	TenantID    uint
	ShipmentID  uint
	CarrierCode string
	TrackingNo  string
}

type CommerceCarrierEvent struct {
	ProviderEventID string
	Status          string
	Description     string
	OccurredAt      time.Time
	Location        string
	PayloadJSON     string
}

type CommerceLogisticsService struct {
	DB    *gorm.DB
	Clock func() time.Time
}

func (s *CommerceLogisticsService) db() *gorm.DB {
	if s != nil && s.DB != nil {
		return s.DB
	}
	return model.DB
}

func (s *CommerceLogisticsService) write(apply func(*gorm.DB) error) error {
	if s != nil && s.DB != nil {
		return s.DB.Transaction(apply)
	}
	return model.Write(apply)
}

func (s *CommerceLogisticsService) now() time.Time {
	if s != nil && s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

type CreateCommerceShipmentInput struct {
	OrderID         uint   `json:"order_id"`
	LocationID      uint   `json:"location_id"`
	ShipmentNo      string `json:"shipment_no"`
	CarrierCode     string `json:"carrier_code"`
	CarrierName     string `json:"carrier_name"`
	TrackingNo      string `json:"tracking_no"`
	Source          string `json:"source"`
	AddressSnapshot string `json:"address_snapshot"`
	// MarkShipped is server-controlled. A caller must use the trusted service
	// boundary to request the atomic create-and-mark-shipped flow.
	MarkShipped bool `json:"-"`
}

// UpdateCommerceShipmentInput contains only mutable package metadata. The
// tenant, order, location, source and status remain server-owned facts; status
// changes must be appended as events so the timeline stays auditable.
type UpdateCommerceShipmentInput struct {
	ShipmentNo      string `json:"shipment_no"`
	CarrierCode     string `json:"carrier_code"`
	CarrierName     string `json:"carrier_name"`
	TrackingNo      string `json:"tracking_no"`
	AddressSnapshot string `json:"address_snapshot"`
}

type AppendCommerceShipmentEventInput struct {
	Source          string    `json:"source"`
	ProviderEventID string    `json:"provider_event_id"`
	IdempotencyKey  string    `json:"idempotency_key"`
	Status          string    `json:"status"`
	Description     string    `json:"description"`
	OccurredAt      time.Time `json:"occurred_at"`
	Location        string    `json:"location"`
	PayloadHash     string    `json:"payload_hash"`
	PayloadJSON     string    `json:"payload"`
	// Exception recovery is an explicit operator/provider decision. Without
	// this flag, an exception cannot silently move backwards in the timeline.
	AllowExceptionRecovery bool `json:"allow_exception_recovery"`
}

type CommerceShipmentTimeline struct {
	Shipment     model.CommerceShipment        `json:"shipment"`
	TrackingMode string                        `json:"tracking_mode"`
	Events       []model.CommerceShipmentEvent `json:"events"`
}

func normalizeShipmentSource(source string) (string, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		source = shipmentSourceManual
	}
	if source != shipmentSourceManual && source != shipmentSourceProvider {
		return "", fmt.Errorf("%w: unsupported shipment source", ErrCommerceLogisticsInvalid)
	}
	return source, nil
}

func normalizeShipmentStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func commerceRefundAllowsFulfillment(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "" || status == "none" || status == "rejected"
}

func (s *CommerceLogisticsService) CreateShipment(tenantID uint, input CreateCommerceShipmentInput) (*model.CommerceShipment, error) {
	if tenantID == 0 || input.OrderID == 0 {
		return nil, fmt.Errorf("%w: tenant and order are required", ErrCommerceLogisticsInvalid)
	}
	source, err := normalizeShipmentSource(input.Source)
	if err != nil {
		return nil, err
	}
	input.ShipmentNo = strings.TrimSpace(input.ShipmentNo)
	input.CarrierCode = strings.TrimSpace(input.CarrierCode)
	input.CarrierName = strings.TrimSpace(input.CarrierName)
	input.TrackingNo = strings.TrimSpace(input.TrackingNo)
	input.AddressSnapshot = strings.TrimSpace(input.AddressSnapshot)
	if len(input.ShipmentNo) > 80 || len(input.CarrierCode) > 80 || len(input.CarrierName) > 120 || len(input.TrackingNo) > 120 || len(input.AddressSnapshot) > 20000 {
		return nil, fmt.Errorf("%w: shipment fields exceed limits", ErrCommerceLogisticsInvalid)
	}
	if input.ShipmentNo == "" {
		input.ShipmentNo = fmt.Sprintf("SHP-%d", s.now().UnixNano())
	}
	if input.MarkShipped && (input.TrackingNo == "" || (input.CarrierCode == "" && input.CarrierName == "")) {
		return nil, fmt.Errorf("%w: shipped shipment requires carrier and tracking number", ErrCommerceLogisticsInvalid)
	}
	var result *model.CommerceShipment
	err = s.write(func(tx *gorm.DB) error {
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", input.OrderID, tenantID).First(&order).Error; err != nil {
			return err
		}
		if order.BusinessType != "retail" || order.PaymentStatus != "paid" {
			return fmt.Errorf("%w: shipment requires a paid retail order at the requested location", ErrCommerceLogisticsInvalid)
		}
		if !commerceRefundAllowsFulfillment(order.RefundStatus) {
			return fmt.Errorf("%w: shipment is locked while refund status is %s", ErrCommerceLogisticsState, order.RefundStatus)
		}
		if input.LocationID != 0 && order.LocationID != input.LocationID {
			return fmt.Errorf("%w: shipment location does not match the order", ErrCommerceLogisticsConflict)
		}
		input.LocationID = order.LocationID
		var fulfillment model.RetailFulfillment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ? AND tenant_id = ?", order.ID, tenantID).First(&fulfillment).Error; err != nil {
			return err
		}
		if fulfillment.LocationID != input.LocationID || fulfillment.Status != "pending_shipment" {
			return fmt.Errorf("%w: retail fulfillment is not pending shipment", ErrCommerceLogisticsState)
		}
		var existingShipmentCount int64
		if err := tx.Model(&model.CommerceShipment{}).Where("tenant_id = ? AND order_id = ? AND deleted_at IS NULL", tenantID, order.ID).Count(&existingShipmentCount).Error; err != nil {
			return err
		}
		if existingShipmentCount > 0 {
			return fmt.Errorf("%w: an order can have only one active shipment", ErrCommerceLogisticsConflict)
		}
		if input.AddressSnapshot == "" {
			input.AddressSnapshot = order.ShippingAddressJSON
		}
		var sameNo int64
		if err := tx.Model(&model.CommerceShipment{}).Where("tenant_id = ? AND shipment_no = ?", tenantID, input.ShipmentNo).Count(&sameNo).Error; err != nil {
			return err
		}
		if sameNo > 0 {
			return fmt.Errorf("%w: shipment number already exists", ErrCommerceLogisticsConflict)
		}
		if input.TrackingNo != "" {
			var existing model.CommerceShipment
			if err := tx.Where("tenant_id = ? AND carrier_code = ? AND tracking_no = ?", tenantID, input.CarrierCode, input.TrackingNo).First(&existing).Error; err == nil {
				return fmt.Errorf("%w: tracking number belongs to another shipment", ErrCommerceLogisticsConflict)
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		shipmentStatus := "pending_shipment"
		var shippedAt *time.Time
		if input.MarkShipped {
			shipmentStatus = "shipped"
			markedAt := s.now()
			shippedAt = &markedAt
		}
		shipment := &model.CommerceShipment{
			TenantID: tenantID, OrderID: order.ID, LocationID: input.LocationID, ShipmentNo: input.ShipmentNo,
			CarrierCode: input.CarrierCode, CarrierName: input.CarrierName, TrackingNo: input.TrackingNo,
			Status: shipmentStatus, Source: source, ShippedAt: shippedAt, AddressSnapshot: input.AddressSnapshot,
		}
		if err := tx.Create(shipment).Error; err != nil {
			return err
		}
		carrier := input.CarrierName
		if carrier == "" {
			carrier = input.CarrierCode
		}
		fulfillmentUpdates := map[string]interface{}{
			"carrier":     carrier,
			"tracking_no": input.TrackingNo,
		}
		if input.MarkShipped {
			fulfillmentUpdates["status"] = "shipped"
			fulfillmentUpdates["shipped_at"] = *shippedAt
		}
		if err := tx.Model(&fulfillment).Updates(fulfillmentUpdates).Error; err != nil {
			return err
		}
		if input.MarkShipped {
			if err := tx.Model(&order).Update("fulfillment_status", "shipped").Error; err != nil {
				return err
			}
			idempotencyKey := fmt.Sprintf("shipment-created:%d:%d", tenantID, order.ID)
			event := &model.CommerceShipmentEvent{
				TenantID: tenantID, ShipmentID: shipment.ID, Source: shipmentSourceManual,
				Status: "shipped", Description: "shipment created and marked shipped",
				OccurredAt: *shippedAt, IdempotencyKey: &idempotencyKey,
			}
			if err := tx.Create(event).Error; err != nil {
				return err
			}
		}
		result = shipment
		return nil
	})
	return result, err
}

// UpdateShipment updates the mutable metadata of one tenant-owned shipment.
// It deliberately does not accept order/location/status/source fields: those
// values define ownership and lifecycle and can only be established by create
// or an append-only event.
func (s *CommerceLogisticsService) UpdateShipment(tenantID, shipmentID uint, input UpdateCommerceShipmentInput) (*model.CommerceShipment, error) {
	if tenantID == 0 || shipmentID == 0 {
		return nil, fmt.Errorf("%w: tenant and shipment are required", ErrCommerceLogisticsInvalid)
	}
	input.ShipmentNo = strings.TrimSpace(input.ShipmentNo)
	input.CarrierCode = strings.TrimSpace(input.CarrierCode)
	input.CarrierName = strings.TrimSpace(input.CarrierName)
	input.TrackingNo = strings.TrimSpace(input.TrackingNo)
	input.AddressSnapshot = strings.TrimSpace(input.AddressSnapshot)
	if input.ShipmentNo == "" || len(input.ShipmentNo) > 80 || len(input.CarrierCode) > 80 || len(input.CarrierName) > 120 || len(input.TrackingNo) > 120 || len(input.AddressSnapshot) > 20000 {
		return nil, fmt.Errorf("%w: shipment fields exceed limits or shipment number is missing", ErrCommerceLogisticsInvalid)
	}
	var result *model.CommerceShipment
	err := s.write(func(tx *gorm.DB) error {
		var initial model.CommerceShipment
		if err := tx.Where("id = ? AND tenant_id = ?", shipmentID, tenantID).First(&initial).Error; err != nil {
			return err
		}
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND business_type = ?", initial.OrderID, tenantID, "retail").First(&order).Error; err != nil {
			return err
		}
		var fulfillment model.RetailFulfillment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ? AND tenant_id = ?", order.ID, tenantID).First(&fulfillment).Error; err != nil {
			return err
		}
		if initial.LocationID != order.LocationID || fulfillment.LocationID != initial.LocationID {
			return fmt.Errorf("%w: shipment ownership does not match retail fulfillment", ErrCommerceLogisticsInvalid)
		}
		var shipment model.CommerceShipment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", shipmentID, tenantID).First(&shipment).Error; err != nil {
			return err
		}
		if input.ShipmentNo != shipment.ShipmentNo {
			var count int64
			if err := tx.Model(&model.CommerceShipment{}).Where("tenant_id = ? AND shipment_no = ? AND id <> ?", tenantID, input.ShipmentNo, shipment.ID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return fmt.Errorf("%w: shipment number already exists", ErrCommerceLogisticsConflict)
			}
		}
		if input.TrackingNo != "" {
			var existing model.CommerceShipment
			query := tx.Where("tenant_id = ? AND carrier_code = ? AND tracking_no = ? AND id <> ?", tenantID, input.CarrierCode, input.TrackingNo, shipment.ID)
			if err := query.First(&existing).Error; err == nil {
				return fmt.Errorf("%w: tracking number belongs to another shipment", ErrCommerceLogisticsConflict)
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if err := tx.Model(&shipment).Updates(map[string]interface{}{
			"shipment_no": input.ShipmentNo, "carrier_code": input.CarrierCode,
			"carrier_name": input.CarrierName, "tracking_no": input.TrackingNo,
			"address_snapshot": input.AddressSnapshot,
		}).Error; err != nil {
			return err
		}
		shipment.ShipmentNo = input.ShipmentNo
		shipment.CarrierCode = input.CarrierCode
		shipment.CarrierName = input.CarrierName
		shipment.TrackingNo = input.TrackingNo
		shipment.AddressSnapshot = input.AddressSnapshot
		carrier := input.CarrierName
		if carrier == "" {
			carrier = input.CarrierCode
		}
		if err := tx.Model(&fulfillment).Updates(map[string]interface{}{
			"carrier":     carrier,
			"tracking_no": input.TrackingNo,
		}).Error; err != nil {
			return err
		}
		result = &shipment
		return nil
	})
	return result, err
}

func shipmentStatusRank(status string) int {
	switch status {
	case "pending_shipment":
		return 0
	case "shipped":
		return 1
	case "in_transit":
		return 2
	case "out_for_delivery":
		return 3
	case "delivered":
		return 4
	default:
		return -1
	}
}

func validShipmentTransition(current, next string, allowExceptionRecovery bool) bool {
	if current == next {
		return true
	}
	if current == "cancelled" || next == "pending_shipment" {
		return false
	}
	if current == "delivered" {
		return false
	}
	if current == "exception" {
		return allowExceptionRecovery && next != "cancelled" && next != "exception"
	}
	if next == "exception" {
		return true
	}
	return shipmentStatusRank(next) > shipmentStatusRank(current)
}

// projectRetailFulfillmentStatus maps carrier states into the existing retail
// order state machine. Carrier exceptions are kept on the shipment timeline,
// while the order projection stays at its last actionable state. The second
// return value tells callers whether the projection should be updated.
func projectRetailFulfillmentStatus(shipmentStatus, current string) (string, bool, error) {
	var projected string
	switch shipmentStatus {
	case "pending_shipment":
		projected = "pending_shipment"
	case "shipped":
		projected = "shipped"
	case "in_transit", "out_for_delivery":
		projected = "in_transit"
	case "delivered":
		projected = "delivered"
	case "cancelled":
		projected = "cancelled"
	case "exception":
		return current, false, nil
	default:
		return "", false, fmt.Errorf("%w: unsupported shipment projection status", ErrCommerceLogisticsInvalid)
	}
	if projected == current {
		return current, false, nil
	}
	if !retailTransitionAllowed(current, projected) {
		return "", false, fmt.Errorf("%w: retail fulfillment cannot move %s to %s", ErrCommerceLogisticsState, current, projected)
	}
	return projected, true, nil
}

func sameShipmentEvent(a *model.CommerceShipmentEvent, input AppendCommerceShipmentEventInput, source string, occurredAt time.Time, payloadHash string) bool {
	return a.Source == source && a.Status == normalizeShipmentStatus(input.Status) && a.Description == strings.TrimSpace(input.Description) && a.OccurredAt.Equal(occurredAt) && a.Location == strings.TrimSpace(input.Location) && a.PayloadHash == payloadHash && a.PayloadJSON == input.PayloadJSON
}

func (s *CommerceLogisticsService) AppendShipmentEvent(tenantID, shipmentID uint, input AppendCommerceShipmentEventInput) (*model.CommerceShipmentEvent, error) {
	if tenantID == 0 || shipmentID == 0 {
		return nil, fmt.Errorf("%w: tenant and shipment are required", ErrCommerceLogisticsInvalid)
	}
	source, err := normalizeShipmentSource(input.Source)
	if err != nil {
		return nil, err
	}
	input.Status = normalizeShipmentStatus(input.Status)
	if !validShipmentStatuses[input.Status] {
		return nil, fmt.Errorf("%w: unsupported shipment status", ErrCommerceLogisticsInvalid)
	}
	input.ProviderEventID = strings.TrimSpace(input.ProviderEventID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Description = strings.TrimSpace(input.Description)
	input.Location = strings.TrimSpace(input.Location)
	input.PayloadJSON = strings.TrimSpace(input.PayloadJSON)
	if source == shipmentSourceProvider && input.ProviderEventID == "" {
		return nil, fmt.Errorf("%w: provider event id is required", ErrCommerceLogisticsInvalid)
	}
	if source == shipmentSourceManual && input.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: manual event idempotency key is required", ErrCommerceLogisticsInvalid)
	}
	if len(input.ProviderEventID) > 160 || len(input.IdempotencyKey) > 160 || len(input.Description) > 500 || len(input.Location) > 160 || len(input.PayloadJSON) > 32768 {
		return nil, fmt.Errorf("%w: event fields exceed limits", ErrCommerceLogisticsInvalid)
	}
	occurredAt := input.OccurredAt.UTC()
	if input.OccurredAt.IsZero() {
		occurredAt = s.now()
	}
	payloadHash := strings.TrimSpace(input.PayloadHash)
	if payloadHash == "" && input.PayloadJSON != "" {
		digest := sha256.Sum256([]byte(input.PayloadJSON))
		payloadHash = hex.EncodeToString(digest[:])
	}
	if len(payloadHash) > 128 {
		return nil, fmt.Errorf("%w: payload hash is too long", ErrCommerceLogisticsInvalid)
	}
	var result *model.CommerceShipmentEvent
	err = s.write(func(tx *gorm.DB) error {
		var initial model.CommerceShipment
		if err := tx.Where("id = ? AND tenant_id = ?", shipmentID, tenantID).First(&initial).Error; err != nil {
			return err
		}
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND business_type = ?", initial.OrderID, tenantID, "retail").First(&order).Error; err != nil {
			return err
		}
		var fulfillment model.RetailFulfillment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ? AND tenant_id = ?", order.ID, tenantID).First(&fulfillment).Error; err != nil {
			return err
		}
		if initial.LocationID != order.LocationID || fulfillment.LocationID != initial.LocationID {
			return fmt.Errorf("%w: shipment ownership does not match retail fulfillment", ErrCommerceLogisticsInvalid)
		}
		var shipment model.CommerceShipment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", shipmentID, tenantID).First(&shipment).Error; err != nil {
			return err
		}
		findExisting := func(query *gorm.DB) error {
			var existing model.CommerceShipmentEvent
			if err := query.First(&existing).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			if !sameShipmentEvent(&existing, input, source, occurredAt, payloadHash) {
				return fmt.Errorf("%w: duplicate event identity has different contents", ErrCommerceLogisticsConflict)
			}
			result = &existing
			return nil
		}
		if input.ProviderEventID != "" {
			if err := findExisting(tx.Where("tenant_id = ? AND shipment_id = ? AND source = ? AND provider_event_id = ?", tenantID, shipmentID, source, input.ProviderEventID)); err != nil || result != nil {
				return err
			}
		}
		if input.IdempotencyKey != "" {
			if err := findExisting(tx.Where("tenant_id = ? AND shipment_id = ? AND source = ? AND idempotency_key = ?", tenantID, shipmentID, source, input.IdempotencyKey)); err != nil || result != nil {
				return err
			}
		}
		if order.PaymentStatus != "paid" || !commerceRefundAllowsFulfillment(order.RefundStatus) {
			return fmt.Errorf("%w: shipment updates are locked by payment or refund state", ErrCommerceLogisticsState)
		}
		if !validShipmentTransition(shipment.Status, input.Status, input.AllowExceptionRecovery) {
			return fmt.Errorf("%w: cannot move %s to %s", ErrCommerceLogisticsState, shipment.Status, input.Status)
		}
		projectedStatus, updateFulfillment, err := projectRetailFulfillmentStatus(input.Status, fulfillment.Status)
		if err != nil {
			return err
		}
		var providerID *string
		if input.ProviderEventID != "" {
			providerID = &input.ProviderEventID
		}
		var idempotencyKey *string
		if input.IdempotencyKey != "" {
			idempotencyKey = &input.IdempotencyKey
		}
		event := &model.CommerceShipmentEvent{TenantID: tenantID, ShipmentID: shipmentID, ProviderEventID: providerID, Source: source, Status: input.Status, Description: input.Description, OccurredAt: occurredAt, Location: input.Location, PayloadHash: payloadHash, PayloadJSON: input.PayloadJSON, IdempotencyKey: idempotencyKey}
		if err := tx.Create(event).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{}
		if input.Status != shipment.Status {
			updates["status"] = input.Status
		}
		if input.Status == "shipped" && (shipment.ShippedAt == nil || occurredAt.Before(*shipment.ShippedAt)) {
			updates["shipped_at"] = occurredAt
		}
		if input.Status == "delivered" && (shipment.DeliveredAt == nil || occurredAt.Before(*shipment.DeliveredAt)) {
			updates["delivered_at"] = occurredAt
		}
		if len(updates) > 0 {
			if err := tx.Model(&shipment).Updates(updates).Error; err != nil {
				return err
			}
		}
		if updateFulfillment {
			fulfillmentUpdates := map[string]interface{}{"status": projectedStatus}
			if input.Status == "shipped" && (fulfillment.ShippedAt == nil || occurredAt.Before(*fulfillment.ShippedAt)) {
				fulfillmentUpdates["shipped_at"] = occurredAt
			}
			if input.Status == "delivered" && (fulfillment.DeliveredAt == nil || occurredAt.Before(*fulfillment.DeliveredAt)) {
				fulfillmentUpdates["delivered_at"] = occurredAt
			}
			if err := tx.Model(&fulfillment).Updates(fulfillmentUpdates).Error; err != nil {
				return err
			}
			if err := tx.Model(&order).Update("fulfillment_status", projectedStatus).Error; err != nil {
				return err
			}
		}
		result = event
		return nil
	})
	return result, err
}

func (s *CommerceLogisticsService) ListShipmentTimeline(tenantID, shipmentID uint) (*CommerceShipmentTimeline, error) {
	if tenantID == 0 || shipmentID == 0 {
		return nil, fmt.Errorf("%w: tenant and shipment are required", ErrCommerceLogisticsInvalid)
	}
	var shipment model.CommerceShipment
	err := s.db().Preload("Events", func(db *gorm.DB) *gorm.DB { return db.Order("occurred_at ASC, id ASC") }).Where("id = ? AND tenant_id = ?", shipmentID, tenantID).First(&shipment).Error
	if err != nil {
		return nil, err
	}
	return &CommerceShipmentTimeline{Shipment: shipment, TrackingMode: shipment.Source, Events: shipment.Events}, nil
}

// AdminOrderTimeline resolves the one active retail shipment for an order so
// an admin order detail view does not need to know the internal shipment ID.
// The tenant and retail order checks remain server-owned at this boundary.
func (s *CommerceLogisticsService) AdminOrderTimeline(tenantID, orderID uint) (*CommerceShipmentTimeline, error) {
	if tenantID == 0 || orderID == 0 {
		return nil, fmt.Errorf("%w: tenant and order are required", ErrCommerceLogisticsInvalid)
	}
	var order model.CommerceOrder
	if err := s.db().Where("id = ? AND tenant_id = ? AND business_type = ?", orderID, tenantID, "retail").First(&order).Error; err != nil {
		return nil, err
	}
	var shipment model.CommerceShipment
	err := s.db().Preload("Events", func(db *gorm.DB) *gorm.DB { return db.Order("occurred_at ASC, id ASC") }).Where("tenant_id = ? AND order_id = ? AND deleted_at IS NULL", tenantID, order.ID).Order("id ASC").First(&shipment).Error
	if err != nil {
		return nil, err
	}
	return &CommerceShipmentTimeline{Shipment: shipment, TrackingMode: shipment.Source, Events: shipment.Events}, nil
}

func (s *CommerceLogisticsService) ListCustomerShipmentTimeline(tenantID uint, customerID string, orderID uint) (*CommerceShipmentTimeline, error) {
	customerID = strings.TrimSpace(customerID)
	if tenantID == 0 || orderID == 0 || customerID == "" {
		return nil, fmt.Errorf("%w: tenant, customer and order are required", ErrCommerceLogisticsInvalid)
	}
	var order model.CommerceOrder
	if err := s.db().Where("id = ? AND tenant_id = ? AND customer_id = ? AND business_type = ?", orderID, tenantID, customerID, "retail").First(&order).Error; err != nil {
		return nil, err
	}
	var shipment model.CommerceShipment
	err := s.db().Preload("Events", func(db *gorm.DB) *gorm.DB { return db.Order("occurred_at ASC, id ASC") }).Where("tenant_id = ? AND order_id = ?", tenantID, order.ID).Order("id ASC").First(&shipment).Error
	if err != nil {
		return nil, err
	}
	return &CommerceShipmentTimeline{Shipment: shipment, TrackingMode: shipment.Source, Events: shipment.Events}, nil
}
