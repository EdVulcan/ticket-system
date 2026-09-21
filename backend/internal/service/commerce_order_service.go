package service

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CommerceOrderService owns commercial orders only. It intentionally has no
// dependency on ticket orders, ticket entitlements, or verification services.
type CommerceOrderService struct {
	DB    *gorm.DB
	Clock func() time.Time
}

func (s *CommerceOrderService) db() *gorm.DB {
	if s != nil && s.DB != nil {
		return s.DB
	}
	return model.DB
}

// write keeps isolated test/worker databases on their own transaction
// boundary. The process-wide writer is the default path so normal requests
// retain the platform timeout and serialization policy.
func (s *CommerceOrderService) write(apply func(*gorm.DB) error) error {
	if s != nil && s.DB != nil {
		return s.DB.Transaction(apply)
	}
	return model.Write(apply)
}

func (s *CommerceOrderService) now() time.Time {
	if s != nil && s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

var (
	ErrCommerceOrderInvalid        = errors.New("commerce order is invalid")
	ErrCommerceOrderState          = errors.New("commerce order state is invalid")
	ErrCommerceInventoryShortage   = errors.New("commerce inventory is insufficient")
	ErrCommerceRefundInvalid       = errors.New("commerce refund is invalid")
	ErrCommerceIdempotencyConflict = errors.New("commerce idempotency key is already used for another order")
	ErrCommercePaymentInvalid      = errors.New("commerce payment outcome is invalid")
	ErrCommercePaymentManualReview = errors.New("commerce payment requires manual review")
	ErrCommercePaymentUnavailable  = errors.New("commerce payment provider is not configured")
)

type CommerceOrderItemInput struct {
	ProductID           uint            `json:"product_id"`
	SKUID               uint            `json:"sku_id"`
	Quantity            int             `json:"quantity"`
	Options             json.RawMessage `json:"options,omitempty"`
	OptionsSnapshotJSON string          `json:"options_snapshot,omitempty"`
}

type CreateCommerceOrderInput struct {
	IdempotencyKey   string `json:"idempotency_key"`
	PaymentReference string `json:"-"`
	BusinessType     string `json:"business_type"`
	// Channel is an integration/source fact. It is intentionally excluded from
	// client JSON so a tenant request cannot impersonate another sales channel;
	// trusted adapters may still set it when calling the service directly.
	Channel             string                   `json:"-"`
	CustomerID          string                   `json:"customer_id"`
	LocationID          uint                     `json:"location_id"`
	ContactName         string                   `json:"contact_name"`
	ContactPhone        string                   `json:"contact_phone"`
	ShippingAddressJSON string                   `json:"shipping_address,omitempty"`
	FulfillmentMethod   string                   `json:"fulfillment_method,omitempty"`
	ExpiresAt           *time.Time               `json:"expires_at,omitempty"`
	Items               []CommerceOrderItemInput `json:"items"`
}

type CommerceOrderListFilter struct {
	BusinessType      string
	Search            string
	PaymentStatus     string
	FulfillmentStatus string
	RefundStatus      string
	Page              int
	PageSize          int
}

type commerceMediaSnapshot struct {
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	SortOrder int    `json:"sort_order"`
}

// snapshotCommerceProductMedia freezes only presentation facts that were
// visible at checkout. The catalog may later replace or remove its media;
// historical commercial orders must continue to render the original assets.
func snapshotCommerceProductMedia(media []model.CommerceProductMedia) string {
	if len(media) == 0 {
		return ""
	}
	snapshot := make([]commerceMediaSnapshot, 0, len(media))
	for _, item := range media {
		if strings.TrimSpace(item.URL) == "" || (item.Kind != CommerceProductMediaCover && item.Kind != CommerceProductMediaDetail) {
			continue
		}
		snapshot = append(snapshot, commerceMediaSnapshot{Kind: item.Kind, URL: item.URL, SortOrder: item.SortOrder})
	}
	if len(snapshot) == 0 {
		return ""
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return ""
	}
	return string(encoded)
}

type CommerceRefundResult struct {
	Request *model.CommerceAfterSaleRequest
	Order   *model.CommerceOrder
}

// CommercePaymentOutcome is accepted only by an authenticated payment
// adapter. ProviderPaidAt is the provider event time, not the HTTP callback
// arrival time, so a late callback can still be accepted when payment happened
// inside the order's payment window.
type CommercePaymentOutcome struct {
	Status              string
	ProviderReference   string
	ProviderPaidAt      *time.Time
	ProviderState       string
	ProviderAmountCents int64
}

type RestaurantFulfillmentTransitionInput struct {
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	ActorUserID uint   `json:"-"`
	ActorRole   string `json:"-"`
}

type RetailFulfillmentTransitionInput struct {
	Status      string `json:"status"`
	Carrier     string `json:"carrier,omitempty"`
	TrackingNo  string `json:"tracking_no,omitempty"`
	Reason      string `json:"reason,omitempty"`
	ActorUserID uint   `json:"-"`
	ActorRole   string `json:"-"`
}

const commercePaymentClockSkew = 5 * time.Minute

var commerceOrderSequence atomic.Uint64

func newCommerceOrderNo(now time.Time) string {
	var random [4]byte
	_, _ = rand.Read(random[:])
	return fmt.Sprintf("CO%s%s%04d", now.UTC().Format("20060102150405"), hex.EncodeToString(random[:]), commerceOrderSequence.Add(1)%10000)
}

func normalizeCommerceOrderInput(input CreateCommerceOrderInput) (CreateCommerceOrderInput, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.BusinessType = strings.TrimSpace(input.BusinessType)
	input.Channel = strings.TrimSpace(input.Channel)
	input.CustomerID = strings.TrimSpace(input.CustomerID)
	input.ContactName = strings.TrimSpace(input.ContactName)
	input.ContactPhone = strings.TrimSpace(input.ContactPhone)
	input.PaymentReference = strings.TrimSpace(input.PaymentReference)
	input.FulfillmentMethod = strings.TrimSpace(input.FulfillmentMethod)
	if !validTenantBusinessType(input.BusinessType) || input.LocationID == 0 || len(input.Items) == 0 || len(input.Items) > 100 || input.IdempotencyKey == "" {
		return input, fmt.Errorf("%w: business type, location and items are required", ErrCommerceOrderInvalid)
	}
	if len([]rune(input.IdempotencyKey)) > 100 {
		return input, fmt.Errorf("%w: idempotency key is too long", ErrCommerceOrderInvalid)
	}
	if input.Channel == "" {
		input.Channel = "direct"
	}
	if input.CustomerID == "" {
		return input, fmt.Errorf("%w: customer id is required", ErrCommerceOrderInvalid)
	}
	if len([]rune(input.CustomerID)) > 100 {
		return input, fmt.Errorf("%w: customer id is too long", ErrCommerceOrderInvalid)
	}
	if len([]rune(input.Channel)) > 50 {
		return input, fmt.Errorf("%w: channel is too long", ErrCommerceOrderInvalid)
	}
	if len([]rune(input.ContactName)) > 80 || len([]rune(input.ContactPhone)) > 30 {
		return input, fmt.Errorf("%w: contact is too long", ErrCommerceOrderInvalid)
	}
	if len([]rune(input.PaymentReference)) > 120 {
		return input, fmt.Errorf("%w: payment reference is too long", ErrCommerceOrderInvalid)
	}
	if input.BusinessType == "restaurant" {
		if input.FulfillmentMethod == "" {
			input.FulfillmentMethod = "pickup"
		}
		if input.FulfillmentMethod != "pickup" && input.FulfillmentMethod != "delivery" {
			return input, fmt.Errorf("%w: restaurant method must be pickup or delivery", ErrCommerceOrderInvalid)
		}
		if input.FulfillmentMethod == "delivery" && strings.TrimSpace(input.ShippingAddressJSON) == "" {
			return input, fmt.Errorf("%w: delivery address is required", ErrCommerceOrderInvalid)
		}
	} else if input.FulfillmentMethod != "" {
		return input, fmt.Errorf("%w: retail does not accept restaurant method", ErrCommerceOrderInvalid)
	} else if strings.TrimSpace(input.ShippingAddressJSON) == "" {
		return input, fmt.Errorf("%w: retail shipping address is required", ErrCommerceOrderInvalid)
	}
	for _, item := range input.Items {
		if item.ProductID == 0 || item.SKUID == 0 || item.Quantity <= 0 || item.Quantity > 1000 {
			return input, fmt.Errorf("%w: product, sku and positive quantity are required", ErrCommerceOrderInvalid)
		}
	}
	return input, nil
}

func orderOptionIDs(item CommerceOrderItemInput) ([]uint, error) {
	raw := item.Options
	if len(raw) == 0 && strings.TrimSpace(item.OptionsSnapshotJSON) != "" {
		// Cart snapshots are accepted only as a transport format. Names and
		// prices are ignored; the current product configuration is authoritative.
		var snapshots []optionSnapshot
		if err := json.Unmarshal([]byte(item.OptionsSnapshotJSON), &snapshots); err != nil {
			return nil, fmt.Errorf("%w: options snapshot is invalid", ErrCommerceOrderInvalid)
		}
		ids := make([]uint, 0, len(snapshots))
		for _, snapshot := range snapshots {
			ids = append(ids, snapshot.OptionID)
		}
		raw, _ = json.Marshal(ids)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var ids []uint
	if err := json.Unmarshal(raw, &ids); err != nil {
		var envelope struct {
			OptionIDs []uint `json:"option_ids"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil || envelope.OptionIDs == nil {
			return nil, fmt.Errorf("%w: options must contain option ids", ErrCommerceOrderInvalid)
		}
		ids = envelope.OptionIDs
	}
	seen := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			return nil, fmt.Errorf("%w: option id is invalid", ErrCommerceOrderInvalid)
		}
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("%w: duplicate option id", ErrCommerceOrderInvalid)
		}
		seen[id] = struct{}{}
	}
	return ids, nil
}

func snapshotOrderOptionsTx(tx *gorm.DB, tenantID, productID uint, item CommerceOrderItemInput) (string, int64, error) {
	ids, err := orderOptionIDs(item)
	if err != nil {
		return "", 0, err
	}
	var groups []model.CommerceOptionGroup
	if err := tx.Where("tenant_id = ? AND product_id = ?", tenantID, productID).Preload("Options", "status = ?", "active").Find(&groups).Error; err != nil {
		return "", 0, err
	}
	selected := make(map[uint]optionSnapshot, len(ids))
	for _, group := range groups {
		count := 0
		for _, option := range group.Options {
			if containsUint(ids, option.ID) {
				selected[option.ID] = optionSnapshot{OptionID: option.ID, GroupID: group.ID, GroupName: group.Name, Name: option.Name, PriceDeltaCents: option.PriceDeltaCents}
				count++
			}
		}
		if count < group.MinSelections || count > group.MaxSelections {
			return "", 0, fmt.Errorf("%w: option group %q selection count is invalid", ErrCommerceOrderInvalid, group.Name)
		}
	}
	if len(selected) != len(ids) {
		return "", 0, fmt.Errorf("%w: option is not active or does not belong to product", ErrCommerceOrderInvalid)
	}
	result := make([]optionSnapshot, 0, len(ids))
	var delta int64
	for _, id := range ids {
		snapshot := selected[id]
		result = append(result, snapshot)
		var ok bool
		delta, ok = commerceSafeAddInt64(delta, snapshot.PriceDeltaCents)
		if !ok {
			return "", 0, fmt.Errorf("%w: option price overflow", ErrCommerceOrderInvalid)
		}
	}
	b, err := json.Marshal(result)
	if err != nil {
		return "", 0, fmt.Errorf("%w: encode options", ErrCommerceOrderInvalid)
	}
	return string(b), delta, nil
}

func existingOrderMatchesInput(order *model.CommerceOrder, items []model.CommerceOrderItem, input CreateCommerceOrderInput) bool {
	if order.BusinessType != input.BusinessType || order.Channel != input.Channel || order.CustomerID != input.CustomerID || order.LocationID != input.LocationID || order.ContactName != input.ContactName || order.ContactPhone != input.ContactPhone || order.ShippingAddressJSON != input.ShippingAddressJSON || order.PaymentReference != input.PaymentReference {
		return false
	}
	if (input.ExpiresAt == nil) != (order.ExpiresAt == nil) || input.ExpiresAt != nil && !input.ExpiresAt.Equal(*order.ExpiresAt) {
		// A caller that omitted expiry accepts the server default; compare only
		// explicit expiry values because the default is generated on first write.
		if input.ExpiresAt != nil {
			return false
		}
	}
	if len(items) != len(input.Items) {
		return false
	}
	for i, item := range items {
		requestedIDs, err := orderOptionIDs(input.Items[i])
		if err != nil || item.ProductID != input.Items[i].ProductID || item.SkuID != input.Items[i].SKUID || item.Quantity != input.Items[i].Quantity {
			return false
		}
		var snapshots []optionSnapshot
		if strings.TrimSpace(item.OptionsSnapshotJSON) != "" && json.Unmarshal([]byte(item.OptionsSnapshotJSON), &snapshots) != nil {
			return false
		}
		if len(snapshots) != len(requestedIDs) {
			return false
		}
		for j, id := range requestedIDs {
			if snapshots[j].OptionID != id {
				return false
			}
		}
	}
	return true
}

func (s *CommerceOrderService) CreateOrder(tenantID uint, input CreateCommerceOrderInput) (*model.CommerceOrder, error) {
	input, err := normalizeCommerceOrderInput(input)
	if err != nil {
		return nil, err
	}
	if tenantID == 0 {
		return nil, ErrTenantUnavailable
	}
	var result *model.CommerceOrder
	err = s.write(func(tx *gorm.DB) error {
		// Serialize order creation for a tenant so the idempotency lookup and
		// insert cannot race when the schema has no composite unique index.
		var tenant model.Tenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").Where("id = ?", tenantID).First(&tenant).Error; err != nil {
			return err
		}
		if err := RequireActiveTenantBusinessCapability(tx, tenantID, input.BusinessType); err != nil {
			return err
		}
		now := s.now()
		var existing model.CommerceOrder
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", tenantID, input.IdempotencyKey).First(&existing).Error; err == nil {
			var existingItems []model.CommerceOrderItem
			if err := tx.Where("tenant_id = ? AND order_id = ?", tenantID, existing.ID).Order("id ASC").Find(&existingItems).Error; err != nil {
				return err
			}
			if !existingOrderMatchesInput(&existing, existingItems, input) {
				return ErrCommerceIdempotencyConflict
			}
			// Fulfillment method is stored in the restaurant fulfillment row,
			// rather than on the shared order. Include it in the idempotency
			// contract so a retry cannot silently reuse a pickup order for a
			// delivery request (or vice versa).
			if input.BusinessType == "restaurant" {
				var fulfillment model.RestaurantFulfillment
				if err := tx.Where("tenant_id = ? AND order_id = ?", tenantID, existing.ID).First(&fulfillment).Error; err != nil {
					return err
				}
				if fulfillment.Method != input.FulfillmentMethod {
					return ErrCommerceIdempotencyConflict
				}
			}
			existing.Items = existingItems
			result = &existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var location model.CommerceFulfillmentLocation
		if err := tx.Where("id = ? AND tenant_id = ? AND business_type = ? AND status = ?", input.LocationID, tenantID, input.BusinessType, "active").First(&location).Error; err != nil {
			return err
		}
		order := model.CommerceOrder{
			TenantID: tenantID, OrderNo: newCommerceOrderNo(now), IdempotencyKey: input.IdempotencyKey, BusinessType: input.BusinessType,
			Channel: input.Channel, CustomerID: input.CustomerID, LocationID: input.LocationID,
			PaymentStatus: "unpaid", FulfillmentStatus: func() string {
				if input.BusinessType == "restaurant" {
					return "pending_acceptance"
				}
				return "pending_shipment"
			}(), RefundStatus: "none",
			ContactName: input.ContactName, ContactPhone: input.ContactPhone, ShippingAddressJSON: input.ShippingAddressJSON, PaymentReference: input.PaymentReference,
		}
		if input.ExpiresAt != nil {
			if !input.ExpiresAt.After(now) {
				return fmt.Errorf("%w: expiry must be in the future", ErrCommerceOrderInvalid)
			}
			order.ExpiresAt = input.ExpiresAt
		} else {
			expires := now.Add(15 * time.Minute)
			order.ExpiresAt = &expires
		}
		var items []model.CommerceOrderItem
		for _, itemInput := range input.Items {
			var sku model.CommerceSKU
			if err := tx.Where("id = ? AND tenant_id = ?", itemInput.SKUID, tenantID).First(&sku).Error; err != nil {
				return err
			}
			if sku.ProductID != itemInput.ProductID || sku.Status != "active" {
				return fmt.Errorf("%w: sku does not belong to the requested active product", ErrCommerceOrderInvalid)
			}
			var product model.CommerceProduct
			if err := tx.Where("id = ? AND tenant_id = ?", itemInput.ProductID, tenantID).
				Preload("Media", "tenant_id = ?", tenantID, func(db *gorm.DB) *gorm.DB { return db.Order("kind ASC, sort_order ASC, id ASC") }).
				First(&product).Error; err != nil {
				return err
			}
			if product.BusinessType != input.BusinessType || product.Status != "online" {
				return fmt.Errorf("%w: product is not online in this business domain", ErrCommerceOrderInvalid)
			}
			if product.SaleStartsAt != nil && now.Before(*product.SaleStartsAt) {
				return fmt.Errorf("%w: product sale has not started", ErrCommerceOrderInvalid)
			}
			if product.SaleEndsAt != nil && !now.Before(*product.SaleEndsAt) {
				return fmt.Errorf("%w: product sale has ended", ErrCommerceOrderInvalid)
			}
			options, optionDelta, err := snapshotOrderOptionsTx(tx, tenantID, product.ID, itemInput)
			if err != nil {
				return err
			}
			unitPrice, ok := commerceSafeAddInt64(sku.PriceCents, optionDelta)
			if !ok {
				return fmt.Errorf("%w: sale price overflow", ErrCommerceOrderInvalid)
			}
			originalUnitPrice, ok := commerceSafeAddInt64(sku.OriginalPriceCents, optionDelta)
			if !ok {
				return fmt.Errorf("%w: original price overflow", ErrCommerceOrderInvalid)
			}
			if unitPrice < 0 || originalUnitPrice < 0 || unitPrice > originalUnitPrice {
				return fmt.Errorf("%w: option price produces invalid amount", ErrCommerceOrderInvalid)
			}
			var stock model.CommerceInventory
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND sku_id = ? AND location_id = ?", tenantID, sku.ID, input.LocationID).First(&stock).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrCommerceInventoryShortage
				}
				return err
			}
			if stock.AvailableQty < itemInput.Quantity || stock.AvailableQty < 0 || stock.ReservedQty < 0 || stock.SoldQty < 0 || stock.ReleasedQty < 0 {
				return ErrCommerceInventoryShortage
			}
			newAvailable, availableOK := commerceSafeAddInt(stock.AvailableQty, -itemInput.Quantity)
			newReserved, reservedOK := commerceSafeAddInt(stock.ReservedQty, itemInput.Quantity)
			newVersion, versionOK := commerceSafeIncrementInt64(stock.Version)
			if !availableOK || !reservedOK || !versionOK || newAvailable < 0 {
				return fmt.Errorf("%w: inventory counter overflow", ErrCommerceInventoryShortage)
			}
			if err := tx.Model(&stock).Updates(map[string]interface{}{"available_qty": newAvailable, "reserved_qty": newReserved, "version": newVersion}).Error; err != nil {
				return err
			}
			lineAmount, amountOK := commerceSafeMulInt64(unitPrice, int64(itemInput.Quantity))
			originalLineAmount, originalAmountOK := commerceSafeMulInt64(originalUnitPrice, int64(itemInput.Quantity))
			newOriginalTotal, originalTotalOK := commerceSafeAddInt64(order.OriginalAmountCents, originalLineAmount)
			newTotal, totalOK := commerceSafeAddInt64(order.TotalAmountCents, lineAmount)
			if !amountOK || !originalAmountOK || !originalTotalOK || !totalOK {
				return fmt.Errorf("%w: order amount overflow", ErrCommerceOrderInvalid)
			}
			order.OriginalAmountCents = newOriginalTotal
			order.TotalAmountCents = newTotal
			items = append(items, model.CommerceOrderItem{TenantID: tenantID, ProductID: product.ID, SkuID: sku.ID, ProductNameSnapshot: product.Name, SkuNameSnapshot: sku.Name, DescriptionSnapshot: product.Description, MediaSnapshotJSON: snapshotCommerceProductMedia(product.Media), OptionsSnapshotJSON: options, Quantity: itemInput.Quantity, OriginalUnitPriceCents: originalUnitPrice, UnitPriceCents: unitPrice, DiscountCents: originalLineAmount - lineAmount, LineAmountCents: lineAmount, ReservationStatus: "reserved"})
		}
		if order.TotalAmountCents > order.OriginalAmountCents {
			return fmt.Errorf("%w: sale price exceeds original price", ErrCommerceOrderInvalid)
		}
		order.DiscountCents = order.OriginalAmountCents - order.TotalAmountCents
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		for i := range items {
			items[i].OrderID = order.ID
			if err := tx.Create(&items[i]).Error; err != nil {
				return err
			}
		}
		if input.BusinessType == "restaurant" {
			fulfillment := model.RestaurantFulfillment{TenantID: tenantID, OrderID: order.ID, LocationID: input.LocationID, Method: input.FulfillmentMethod, Status: "pending_acceptance", AddressSnapshot: input.ShippingAddressJSON}
			if err := tx.Create(&fulfillment).Error; err != nil {
				return err
			}
		} else {
			fulfillment := model.RetailFulfillment{TenantID: tenantID, OrderID: order.ID, LocationID: input.LocationID, Status: "pending_shipment"}
			if err := tx.Create(&fulfillment).Error; err != nil {
				return err
			}
		}
		order.Items = items
		result = &order
		return nil
	})
	return result, err
}

// Create is a concise alias used by callers that do not need the longer name.
func (s *CommerceOrderService) Create(tenantID uint, input CreateCommerceOrderInput) (*model.CommerceOrder, error) {
	return s.CreateOrder(tenantID, input)
}

func (s *CommerceOrderService) GetOrder(tenantID, orderID uint) (*model.CommerceOrder, error) {
	if tenantID == 0 || orderID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	db := s.db()
	var order model.CommerceOrder
	if err := db.Where("id = ? AND tenant_id = ?", orderID, tenantID).
		Preload("Items").
		Preload("Adjustments", "tenant_id = ?", tenantID, func(db *gorm.DB) *gorm.DB { return db.Order("id ASC") }).
		Preload("AfterSales", "tenant_id = ?", tenantID, func(db *gorm.DB) *gorm.DB { return db.Order("created_at DESC") }).
		Preload("RestaurantFulfillment", "tenant_id = ?", tenantID).
		Preload("RetailFulfillment", "tenant_id = ?", tenantID).
		First(&order).Error; err != nil {
		return nil, err
	}
	if err := RequireConfiguredTenantBusinessCapability(db, tenantID, order.BusinessType); err != nil {
		return nil, err
	}
	return &order, nil
}

var commercePaymentStatuses = map[string]struct{}{"unpaid": {}, "pending": {}, "paid": {}, "failed": {}, "refunded": {}}
var commerceRefundStatuses = map[string]struct{}{"none": {}, "requested": {}, "processing": {}, "partial": {}, "refunded": {}, "rejected": {}}
var commerceRestaurantFulfillmentStatuses = map[string]struct{}{"pending_acceptance": {}, "accepted": {}, "preparing": {}, "ready": {}, "delivering": {}, "completed": {}, "cancelled": {}}
var commerceRetailFulfillmentStatuses = map[string]struct{}{"pending_shipment": {}, "shipped": {}, "in_transit": {}, "delivered": {}, "completed": {}, "cancelled": {}}

func validCommerceFilterStatus(value string, allowed map[string]struct{}) bool {
	if strings.TrimSpace(value) == "" {
		return true
	}
	_, ok := allowed[strings.TrimSpace(value)]
	return ok
}

func (s *CommerceOrderService) ListOrders(tenantID uint, filter CommerceOrderListFilter) ([]model.CommerceOrder, error) {
	rows, _, err := s.ListOrdersPage(tenantID, filter)
	return rows, err
}

// ListOrdersPage bounds admin reads and returns the total matching count. The
// legacy ListOrders helper above remains for internal callers that only need
// the rows.
func (s *CommerceOrderService) ListOrdersPage(tenantID uint, filter CommerceOrderListFilter) ([]model.CommerceOrder, int64, error) {
	if err := requireActiveTenant(s.db(), tenantID); err != nil {
		return nil, 0, err
	}
	filter.BusinessType = strings.TrimSpace(filter.BusinessType)
	filter.Search = strings.TrimSpace(filter.Search)
	filter.PaymentStatus = strings.TrimSpace(filter.PaymentStatus)
	filter.FulfillmentStatus = strings.TrimSpace(filter.FulfillmentStatus)
	filter.RefundStatus = strings.TrimSpace(filter.RefundStatus)
	if !validCommerceFilterStatus(filter.PaymentStatus, commercePaymentStatuses) || !validCommerceFilterStatus(filter.RefundStatus, commerceRefundStatuses) {
		return nil, 0, ErrCommerceOrderState
	}
	if len([]rune(filter.Search)) > 100 {
		return nil, 0, fmt.Errorf("%w: search is too long", ErrCommerceOrderInvalid)
	}
	if (filter.BusinessType == "restaurant" && !validCommerceFilterStatus(filter.FulfillmentStatus, commerceRestaurantFulfillmentStatuses)) || (filter.BusinessType == "retail" && !validCommerceFilterStatus(filter.FulfillmentStatus, commerceRetailFulfillmentStatuses)) {
		return nil, 0, ErrCommerceOrderState
	}
	if filter.BusinessType == "" && strings.TrimSpace(filter.FulfillmentStatus) != "" && !validCommerceFilterStatus(filter.FulfillmentStatus, map[string]struct{}{"pending_acceptance": {}, "accepted": {}, "preparing": {}, "ready": {}, "delivering": {}, "pending_shipment": {}, "shipped": {}, "in_transit": {}, "delivered": {}, "completed": {}, "cancelled": {}}) {
		return nil, 0, ErrCommerceOrderState
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = 50
	}
	if filter.PageSize > 200 {
		filter.PageSize = 200
	}
	query := s.db().Where("tenant_id = ?", tenantID)
	if strings.TrimSpace(filter.BusinessType) != "" {
		if !validTenantBusinessType(strings.TrimSpace(filter.BusinessType)) {
			return nil, 0, ErrBusinessCapabilityInactive
		}
		if err := RequireConfiguredTenantBusinessCapability(s.db(), tenantID, strings.TrimSpace(filter.BusinessType)); err != nil {
			return nil, 0, err
		}
		query = query.Where("business_type = ?", strings.TrimSpace(filter.BusinessType))
	} else {
		query = query.Where("business_type IN (SELECT business_type FROM tenant_business_capabilities WHERE tenant_id = ? AND status IN ?)", tenantID, []string{"active", "suspended"})
	}
	if filter.PaymentStatus != "" {
		query = query.Where("payment_status = ?", strings.TrimSpace(filter.PaymentStatus))
	}
	if filter.Search != "" {
		pattern := "%" + strings.ReplaceAll(filter.Search, "%", "\\%") + "%"
		pattern = strings.ReplaceAll(pattern, "_", "\\_")
		query = query.Where("(order_no ILIKE ? ESCAPE '\\' OR contact_name ILIKE ? ESCAPE '\\' OR contact_phone ILIKE ? ESCAPE '\\')", pattern, pattern, pattern)
	}
	if filter.FulfillmentStatus != "" {
		query = query.Where("fulfillment_status = ?", strings.TrimSpace(filter.FulfillmentStatus))
	}
	if filter.RefundStatus != "" {
		query = query.Where("refund_status = ?", strings.TrimSpace(filter.RefundStatus))
	}
	var total int64
	if err := query.Model(&model.CommerceOrder{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.CommerceOrder
	if err := query.
		Preload("Items").
		Preload("Adjustments", "tenant_id = ?", tenantID, func(db *gorm.DB) *gorm.DB { return db.Order("id ASC") }).
		Preload("AfterSales", "tenant_id = ?", tenantID, func(db *gorm.DB) *gorm.DB { return db.Order("created_at DESC") }).
		Preload("RestaurantFulfillment", "tenant_id = ?", tenantID).
		Preload("RetailFulfillment", "tenant_id = ?", tenantID).
		Order("created_at DESC").Offset((filter.Page - 1) * filter.PageSize).Limit(filter.PageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	if rows == nil {
		rows = []model.CommerceOrder{}
	}
	return rows, total, nil
}

// ensureCommercePaymentTaskTx creates the durable reconciliation row for a
// pending commercial payment. The caller must already hold the order lock.
// Keeping this write in the same transaction as the pending transition closes
// the crash window where an external payment exists but no worker task does.
func ensureCommercePaymentTaskTx(tx *gorm.DB, order *model.CommerceOrder, now time.Time) error {
	if tx == nil || order == nil || order.ID == 0 || order.TenantID == 0 || order.PaymentStatus != "pending" {
		return fmt.Errorf("%w: pending order is required for reconciliation", ErrCommerceOrderState)
	}
	var task model.CommercePaymentReconciliationTask
	err := tx.Where("tenant_id = ? AND order_id = ?", order.TenantID, order.ID).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		next := now
		task = model.CommercePaymentReconciliationTask{
			TenantID: order.TenantID, OrderID: order.ID, PaymentReference: order.PaymentReference,
			Status: "pending", NextAttemptAt: &next,
		}
		return tx.Create(&task).Error
	}
	if err != nil {
		return err
	}
	updates := map[string]interface{}{}
	if task.PaymentReference == "" && order.PaymentReference != "" {
		updates["payment_reference"] = order.PaymentReference
		task.PaymentReference = order.PaymentReference
	}
	if task.Status == "pending" && task.NextAttemptAt == nil {
		next := now
		updates["next_attempt_at"] = next
		task.NextAttemptAt = &next
	}
	if len(updates) > 0 {
		if err := tx.Model(&task).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}

func ensureCommercePaymentReferenceAvailableTx(tx *gorm.DB, tenantID, orderID uint, reference string) error {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return nil
	}
	var count int64
	if err := tx.Model(&model.CommerceOrder{}).
		Where("tenant_id = ? AND payment_reference = ? AND id <> ? AND deleted_at IS NULL", tenantID, reference, orderID).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: payment reference is already attached to another order", ErrCommercePaymentInvalid)
	}
	return nil
}

func markCommercePaymentTaskTx(tx *gorm.DB, order *model.CommerceOrder, status, providerState, lastError string, outcome *CommercePaymentOutcome, now time.Time) error {
	if order == nil || order.ID == 0 || order.TenantID == 0 {
		return ErrCommerceOrderInvalid
	}
	var task model.CommercePaymentReconciliationTask
	err := tx.Where("tenant_id = ? AND order_id = ?", order.TenantID, order.ID).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		task = model.CommercePaymentReconciliationTask{TenantID: order.TenantID, OrderID: order.ID, Status: status}
		if status == "pending" || status == "manual_review" {
			next := now
			task.NextAttemptAt = &next
		}
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	updates := map[string]interface{}{
		"status": status, "last_provider_state": strings.TrimSpace(providerState),
		"last_error": strings.TrimSpace(lastError), "last_attempt_at": now,
	}
	if status == "pending" {
		updates["next_attempt_at"] = now
		updates["completed_at"] = nil
	} else if status == "manual_review" {
		updates["next_attempt_at"] = nil
		updates["completed_at"] = nil
	} else if status == "completed" || status == "failed" {
		updates["next_attempt_at"] = nil
		updates["completed_at"] = now
	}
	if outcome != nil {
		if reference := strings.TrimSpace(outcome.ProviderReference); reference != "" {
			// PaymentReference is the merchant out_trade_no. The provider's
			// transaction id is a separate fact and must not overwrite it.
			if strings.TrimSpace(task.PaymentReference) == "" {
				// Preserve compatibility for pre-attempt rows created by the
				// original reconciliation model; new attempts always have an
				// out_trade_no in PaymentReference.
				updates["payment_reference"] = reference
			} else {
				updates["provider_reference"] = reference
			}
		}
		if outcome.ProviderPaidAt != nil {
			updates["provider_paid_at"] = outcome.ProviderPaidAt
		}
		if outcome.ProviderAmountCents != 0 {
			updates["provider_amount_cents"] = outcome.ProviderAmountCents
		}
	}
	return tx.Model(&task).Updates(updates).Error
}

// releaseCommerceReservationsTx is the single failure/expiry path for
// reserved commercial inventory. It is idempotent at item level: only rows
// still marked reserved are returned to available stock. The caller must hold
// the order lock before invoking it.
func releaseCommerceReservationsTx(tx *gorm.DB, order *model.CommerceOrder, at time.Time) error {
	if tx == nil || order == nil || order.ID == 0 {
		return ErrCommerceOrderInvalid
	}
	var items []model.CommerceOrderItem
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND order_id = ?", order.TenantID, order.ID).Order("id ASC").Find(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
		if item.ReservationStatus != "reserved" {
			continue
		}
		var stock model.CommerceInventory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND sku_id = ? AND location_id = ?", order.TenantID, item.SkuID, order.LocationID).First(&stock).Error; err != nil {
			return err
		}
		if stock.AvailableQty < 0 || stock.ReservedQty < item.Quantity || stock.ReservedQty < 0 || stock.SoldQty < 0 || stock.ReleasedQty < 0 {
			return fmt.Errorf("%w: reserved stock is inconsistent", ErrCommerceInventoryShortage)
		}
		newReserved, reservedOK := commerceSafeAddInt(stock.ReservedQty, -item.Quantity)
		newAvailable, availableOK := commerceSafeAddInt(stock.AvailableQty, item.Quantity)
		newReleased, releasedOK := commerceSafeAddInt(stock.ReleasedQty, item.Quantity)
		newVersion, versionOK := commerceSafeIncrementInt64(stock.Version)
		if !reservedOK || !availableOK || !releasedOK || !versionOK || newReserved < 0 || newAvailable < 0 || newReleased < 0 {
			return fmt.Errorf("%w: inventory counter overflow", ErrCommerceInventoryShortage)
		}
		if err := tx.Model(&stock).Updates(map[string]interface{}{"reserved_qty": newReserved, "available_qty": newAvailable, "released_qty": newReleased, "version": newVersion}).Error; err != nil {
			return err
		}
		changed := tx.Model(&model.CommerceOrderItem{}).Where("id = ? AND tenant_id = ? AND order_id = ? AND reservation_status = ?", item.ID, order.TenantID, order.ID, "reserved").Updates(map[string]interface{}{"reservation_status": "released", "released_at": at})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return fmt.Errorf("%w: reservation changed concurrently", ErrCommerceInventoryShortage)
		}
	}
	promotions := CommercePromotionService{DB: tx, Clock: func() time.Time { return at }}
	if err := promotions.ReleaseCouponTx(tx, order.TenantID, order.ID); err != nil {
		return err
	}
	if err := tx.Model(&model.CommerceCheckoutQuote{}).
		Where("tenant_id = ? AND consumed_order_id = ? AND status = ?", order.TenantID, order.ID, "consumed").
		Update("status", "cancelled").Error; err != nil {
		return err
	}
	if err := tx.Model(order).Updates(map[string]interface{}{"payment_status": "failed", "fulfillment_status": "cancelled"}).Error; err != nil {
		return err
	}
	if order.BusinessType == "restaurant" {
		if err := tx.Model(&model.RestaurantFulfillment{}).Where("tenant_id = ? AND order_id = ?", order.TenantID, order.ID).Update("status", "cancelled").Error; err != nil {
			return err
		}
	} else {
		if err := tx.Model(&model.RetailFulfillment{}).Where("tenant_id = ? AND order_id = ?", order.TenantID, order.ID).Update("status", "cancelled").Error; err != nil {
			return err
		}
	}
	order.PaymentStatus = "failed"
	order.FulfillmentStatus = "cancelled"
	return nil
}

func confirmCommercePaymentTx(tx *gorm.DB, order *model.CommerceOrder, paidAt time.Time, allowExpired bool) error {
	if order.PaymentStatus == "paid" {
		return nil
	}
	if order.PaymentStatus != "unpaid" && order.PaymentStatus != "pending" {
		return fmt.Errorf("%w: payment status %s cannot be confirmed", ErrCommerceOrderState, order.PaymentStatus)
	}
	if !allowExpired && order.ExpiresAt != nil && !order.ExpiresAt.After(paidAt) {
		return fmt.Errorf("%w: payment window has expired", ErrCommerceOrderState)
	}
	var items []model.CommerceOrderItem
	if err := tx.Where("tenant_id = ? AND order_id = ?", order.TenantID, order.ID).Order("id ASC").Find(&items).Error; err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("%w: order has no items", ErrCommerceInventoryShortage)
	}
	for _, item := range items {
		if item.ReservationStatus != "reserved" {
			return fmt.Errorf("%w: reservation status is inconsistent", ErrCommerceInventoryShortage)
		}
		var stock model.CommerceInventory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND sku_id = ? AND location_id = ?", order.TenantID, item.SkuID, order.LocationID).First(&stock).Error; err != nil {
			return err
		}
		if stock.ReservedQty < item.Quantity || stock.ReservedQty < 0 || stock.SoldQty < 0 {
			return fmt.Errorf("%w: reserved stock is inconsistent", ErrCommerceInventoryShortage)
		}
		newReserved, reservedOK := commerceSafeAddInt(stock.ReservedQty, -item.Quantity)
		newSold, soldOK := commerceSafeAddInt(stock.SoldQty, item.Quantity)
		newVersion, versionOK := commerceSafeIncrementInt64(stock.Version)
		if !reservedOK || !soldOK || !versionOK || newReserved < 0 {
			return fmt.Errorf("%w: inventory counter overflow", ErrCommerceInventoryShortage)
		}
		if err := tx.Model(&stock).Updates(map[string]interface{}{"reserved_qty": newReserved, "sold_qty": newSold, "version": newVersion}).Error; err != nil {
			return err
		}
		changed := tx.Model(&model.CommerceOrderItem{}).Where("id = ? AND tenant_id = ? AND order_id = ? AND reservation_status = ?", item.ID, order.TenantID, order.ID, "reserved").Update("reservation_status", "sold")
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return fmt.Errorf("%w: reservation status changed concurrently", ErrCommerceInventoryShortage)
		}
	}
	promotions := CommercePromotionService{DB: tx, Clock: func() time.Time { return paidAt }}
	if err := promotions.ConsumeCouponTx(tx, order.TenantID, order.ID); err != nil {
		return err
	}
	if err := tx.Model(order).Updates(map[string]interface{}{"payment_status": "paid", "paid_at": paidAt}).Error; err != nil {
		return err
	}
	order.PaymentStatus = "paid"
	order.PaidAt = &paidAt
	payload, err := json.Marshal(map[string]interface{}{
		"tenant_id": order.TenantID, "order_id": order.ID, "order_no": order.OrderNo,
		"business_type": order.BusinessType, "location_id": order.LocationID,
		"total_amount_cents": order.TotalAmountCents, "paid_at": paidAt,
	})
	if err != nil {
		return err
	}
	eventKey := fmt.Sprintf("order.paid:%d", order.ID)
	outbox := &model.CommerceOrderPaidOutbox{
		TenantID: order.TenantID, OrderID: order.ID, EventType: "order.paid",
		EventKey: eventKey, PayloadJSON: string(payload), Status: "pending",
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(outbox).Error; err != nil {
		return err
	}
	return nil
}

// ConfirmPayment is retained for the local service tests and future adapters
// that already verified the provider response. Ordinary tenant routes do not
// expose this method; production adapters should use ApplyPaymentOutcome so
// provider amount, reference and event time are checked as well.
func (s *CommerceOrderService) ConfirmPayment(tenantID, orderID uint) (*model.CommerceOrder, error) {
	var result *model.CommerceOrder
	err := s.write(func(tx *gorm.DB) error {
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", orderID, tenantID).First(&order).Error; err != nil {
			return err
		}
		if err := RequireConfiguredTenantBusinessCapability(tx, tenantID, order.BusinessType); err != nil {
			return err
		}
		if err := confirmCommercePaymentTx(tx, &order, s.now(), false); err != nil {
			return err
		}
		result = &order
		return nil
	})
	return result, err
}

func normalizeCommercePaymentOutcome(outcome CommercePaymentOutcome) (CommercePaymentOutcome, string, error) {
	outcome.Status = strings.ToLower(strings.TrimSpace(outcome.Status))
	outcome.ProviderReference = strings.TrimSpace(outcome.ProviderReference)
	outcome.ProviderState = strings.TrimSpace(outcome.ProviderState)
	if len([]rune(outcome.ProviderReference)) > 120 || len([]rune(outcome.ProviderState)) > 40 {
		return outcome, "", fmt.Errorf("%w: provider identity is too long", ErrCommercePaymentInvalid)
	}
	var normalized string
	switch outcome.Status {
	case "paid", "success", "succeeded", "completed":
		normalized = "paid"
	case "failed", "unpaid", "cancelled", "canceled", "closed":
		normalized = "failed"
	case "pending", "processing", "unknown", "indeterminate":
		normalized = "unknown"
	default:
		return outcome, "", fmt.Errorf("%w: unsupported provider status %q", ErrCommercePaymentInvalid, outcome.Status)
	}
	if outcome.ProviderState == "" {
		outcome.ProviderState = outcome.Status
	}
	if outcome.ProviderAmountCents < 0 {
		return outcome, "", fmt.Errorf("%w: provider amount cannot be negative", ErrCommercePaymentInvalid)
	}
	if normalized == "paid" && outcome.ProviderAmountCents <= 0 {
		return outcome, "", fmt.Errorf("%w: provider amount is required for a paid outcome", ErrCommercePaymentInvalid)
	}
	if normalized == "paid" && outcome.ProviderReference == "" {
		return outcome, "", fmt.Errorf("%w: provider reference is required for a paid outcome", ErrCommercePaymentInvalid)
	}
	return outcome, normalized, nil
}

func validateCommercePaymentIdentity(tx *gorm.DB, order *model.CommerceOrder, outcome CommercePaymentOutcome) error {
	if order == nil {
		return ErrCommerceOrderInvalid
	}
	if tx == nil {
		tx = model.DB
	}
	if outcome.ProviderReference != "" {
		var attempt model.CommercePaymentAttempt
		lookup := tx.Where("tenant_id = ? AND order_id = ?", order.TenantID, order.ID).Order("id DESC").First(&attempt).Error
		if lookup == nil {
			if attempt.ProviderReference != "" && attempt.ProviderReference != outcome.ProviderReference {
				return fmt.Errorf("%w: provider reference does not match the payment attempt", ErrCommercePaymentInvalid)
			}
		} else if !errors.Is(lookup, gorm.ErrRecordNotFound) {
			return lookup
		} else if order.PaymentReference != "" && order.PaymentReference != outcome.ProviderReference {
			// Compatibility path for orders created before the commercial
			// payment-attempt table existed.
			return fmt.Errorf("%w: provider reference does not match the payment attempt", ErrCommercePaymentInvalid)
		}
	}
	if outcome.ProviderAmountCents != 0 && outcome.ProviderAmountCents != order.TotalAmountCents {
		return fmt.Errorf("%w: provider amount does not match the order amount", ErrCommercePaymentInvalid)
	}
	return nil
}

// ApplyPaymentOutcome is the only commerce payment transition intended for a
// real payment adapter. The adapter must authenticate the provider response
// before calling this method. It validates the immutable order amount and
// reference, accepts a late callback only when the provider event happened
// inside the payment window, and persists unknown outcomes for review without
// releasing inventory.
func (s *CommerceOrderService) ApplyPaymentOutcome(tenantID, orderID uint, raw CommercePaymentOutcome) (*model.CommerceOrder, error) {
	outcome, normalized, err := normalizeCommercePaymentOutcome(raw)
	if err != nil {
		return nil, err
	}
	now := s.now()
	paidAt := now
	if outcome.ProviderPaidAt != nil {
		paidAt = outcome.ProviderPaidAt.UTC()
		outcome.ProviderPaidAt = &paidAt
	}
	var result *model.CommerceOrder
	var transitionErr error
	err = s.write(func(tx *gorm.DB) error {
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", orderID, tenantID).First(&order).Error; err != nil {
			return err
		}
		if err := RequireConfiguredTenantBusinessCapability(tx, tenantID, order.BusinessType); err != nil {
			return err
		}
		if err := validateCommercePaymentIdentity(tx, &order, outcome); err != nil {
			return err
		}
		if order.PaymentStatus == "paid" || order.PaymentStatus == "refunded" {
			if normalized != "paid" {
				return fmt.Errorf("%w: final payment cannot be regressed from %s", ErrCommerceOrderState, order.PaymentStatus)
			}
			if err := markCommercePaymentTaskTx(tx, &order, "completed", outcome.ProviderState, "", &outcome, now); err != nil {
				return err
			}
			result = &order
			return nil
		}
		if order.PaymentStatus != "unpaid" && order.PaymentStatus != "pending" && order.PaymentStatus != "failed" {
			return fmt.Errorf("%w: payment status %s cannot accept a provider outcome", ErrCommerceOrderState, order.PaymentStatus)
		}
		if outcome.ProviderReference != "" && order.PaymentReference == "" {
			if err := ensureCommercePaymentReferenceAvailableTx(tx, tenantID, order.ID, outcome.ProviderReference); err != nil {
				return err
			}
			if err := tx.Model(&order).Update("payment_reference", outcome.ProviderReference).Error; err != nil {
				return err
			}
			order.PaymentReference = outcome.ProviderReference
		}
		if order.PaymentStatus == "failed" {
			// A repeated failed outcome is already converged. Do not try to
			// release inventory a second time.
			if normalized == "failed" {
				if err := markCommercePaymentTaskTx(tx, &order, "completed", outcome.ProviderState, "", &outcome, now); err != nil {
					return err
				}
				result = &order
				return nil
			}
			if err := markCommercePaymentTaskTx(tx, &order, "manual_review", outcome.ProviderState, "支付结果在订单释放后到达，禁止自动恢复库存，需人工复核", &outcome, now); err != nil {
				return err
			}
			transitionErr = ErrCommercePaymentManualReview
			result = &order
			return nil
		}
		switch normalized {
		case "paid":
			if order.ExpiresAt != nil && paidAt.After(*order.ExpiresAt) {
				if order.PaymentStatus == "unpaid" {
					if err := tx.Model(&order).Update("payment_status", "pending").Error; err != nil {
						return err
					}
					order.PaymentStatus = "pending"
				}
				if err := markCommercePaymentTaskTx(tx, &order, "manual_review", outcome.ProviderState, "provider payment arrived after the payment window", &outcome, now); err != nil {
					return err
				}
				transitionErr = ErrCommercePaymentManualReview
				result = &order
				return nil
			}
			if err := confirmCommercePaymentTx(tx, &order, paidAt, true); err != nil {
				return err
			}
			if err := markCommercePaymentTaskTx(tx, &order, "completed", outcome.ProviderState, "", &outcome, now); err != nil {
				return err
			}
		case "failed":
			if err := releaseCommerceReservationsTx(tx, &order, now); err != nil {
				return err
			}
			if err := markCommercePaymentTaskTx(tx, &order, "completed", outcome.ProviderState, "", &outcome, now); err != nil {
				return err
			}
		case "unknown":
			if order.PaymentStatus == "unpaid" {
				if err := tx.Model(&order).Update("payment_status", "pending").Error; err != nil {
					return err
				}
				order.PaymentStatus = "pending"
			}
			if err := ensureCommercePaymentTaskTx(tx, &order, now); err != nil {
				return err
			}
			if err := markCommercePaymentTaskTx(tx, &order, "manual_review", outcome.ProviderState, "provider payment status is unknown", &outcome, now); err != nil {
				return err
			}
			transitionErr = ErrCommercePaymentManualReview
		}
		result = &order
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, transitionErr
}

// RetryPaymentReconciliation requeues a manual-review task after an operator
// or an authenticated adapter has decided that another provider query is
// appropriate. It does not change the order payment state or inventory.
func (s *CommerceOrderService) RetryPaymentReconciliation(tenantID, orderID uint) (*model.CommercePaymentReconciliationTask, error) {
	if tenantID == 0 || orderID == 0 {
		return nil, ErrCommerceOrderInvalid
	}
	var result *model.CommercePaymentReconciliationTask
	err := s.write(func(tx *gorm.DB) error {
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", orderID, tenantID).First(&order).Error; err != nil {
			return err
		}
		if order.PaymentStatus != "pending" {
			return fmt.Errorf("%w: only pending payments can be retried", ErrCommerceOrderState)
		}
		if err := RequireConfiguredTenantBusinessCapability(tx, tenantID, order.BusinessType); err != nil {
			return err
		}
		if err := ensureCommercePaymentTaskTx(tx, &order, s.now()); err != nil {
			return err
		}
		var task model.CommercePaymentReconciliationTask
		if err := tx.Where("tenant_id = ? AND order_id = ?", tenantID, orderID).First(&task).Error; err != nil {
			return err
		}
		next := s.now()
		if err := tx.Model(&task).Updates(map[string]interface{}{"status": "pending", "next_attempt_at": next, "last_error": "", "completed_at": nil}).Error; err != nil {
			return err
		}
		task.Status = "pending"
		task.NextAttemptAt = &next
		task.LastError = ""
		task.CompletedAt = nil
		result = &task
		return nil
	})
	return result, err
}

// MarkPaymentPending records that an external payment attempt has started.
// It deliberately does not accept an amount or call a payment provider; the
// adapter that owns that provider must verify its response before calling
// ConfirmPayment. Replaying the transition is safe for an already-pending
// order, while paid/refunded orders are left immutable.
func (s *CommerceOrderService) MarkPaymentPending(tenantID, orderID uint) (*model.CommerceOrder, error) {
	return s.MarkPaymentPendingWithReference(tenantID, orderID, "")
}

// MarkPaymentPendingWithReference records the provider reference once the
// adapter has created a payment attempt. The reference is persisted before
// reconciliation so a restarted worker can resume without relying on memory.
func (s *CommerceOrderService) MarkPaymentPendingWithReference(tenantID, orderID uint, paymentReference string) (*model.CommerceOrder, error) {
	paymentReference = strings.TrimSpace(paymentReference)
	if len([]rune(paymentReference)) > 120 {
		return nil, fmt.Errorf("%w: payment reference is too long", ErrCommerceOrderInvalid)
	}
	var result *model.CommerceOrder
	err := s.write(func(tx *gorm.DB) error {
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", orderID, tenantID).First(&order).Error; err != nil {
			return err
		}
		if err := RequireConfiguredTenantBusinessCapability(tx, tenantID, order.BusinessType); err != nil {
			return err
		}
		switch order.PaymentStatus {
		case "pending":
			if paymentReference != "" {
				if order.PaymentReference != "" && order.PaymentReference != paymentReference {
					return fmt.Errorf("%w: payment reference conflicts with the pending attempt", ErrCommerceOrderState)
				}
				if err := ensureCommercePaymentReferenceAvailableTx(tx, tenantID, order.ID, paymentReference); err != nil {
					return err
				}
				if err := tx.Model(&order).Update("payment_reference", paymentReference).Error; err != nil {
					return err
				}
				order.PaymentReference = paymentReference
			}
			if err := ensureCommercePaymentTaskTx(tx, &order, s.now()); err != nil {
				return err
			}
			result = &order
			return nil
		case "unpaid":
			if order.ExpiresAt != nil && !order.ExpiresAt.After(s.now()) {
				// Keep the order unpaid so the expiry worker can release its
				// reservation exactly once. Never move an expired reservation to
				// pending, where the worker intentionally will not touch it.
				return fmt.Errorf("%w: payment window has expired", ErrCommerceOrderState)
			}
			updates := map[string]interface{}{"payment_status": "pending"}
			if paymentReference != "" {
				if err := ensureCommercePaymentReferenceAvailableTx(tx, tenantID, order.ID, paymentReference); err != nil {
					return err
				}
				updates["payment_reference"] = paymentReference
			}
			if err := tx.Model(&order).Updates(updates).Error; err != nil {
				return err
			}
			order.PaymentStatus = "pending"
			order.PaymentReference = paymentReference
			if err := ensureCommercePaymentTaskTx(tx, &order, s.now()); err != nil {
				return err
			}
			result = &order
			return nil
		default:
			return fmt.Errorf("%w: payment status %s cannot be marked pending", ErrCommerceOrderState, order.PaymentStatus)
		}
	})
	return result, err
}

func (s *CommerceOrderService) Pay(tenantID, orderID uint) (*model.CommerceOrder, error) {
	return s.ConfirmPayment(tenantID, orderID)
}

func (s *CommerceOrderService) ExpireUnpaidOrders(tenantID uint, at time.Time) (int, error) {
	if tenantID == 0 {
		return 0, ErrTenantUnavailable
	}
	count := 0
	err := s.write(func(tx *gorm.DB) error {
		var orders []model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND payment_status = ? AND expires_at IS NOT NULL AND expires_at <= ?", tenantID, "unpaid", at).Find(&orders).Error; err != nil {
			return err
		}
		for i := range orders {
			order := &orders[i]
			if err := RequireConfiguredTenantBusinessCapability(tx, tenantID, order.BusinessType); err != nil {
				return err
			}
			if err := releaseCommerceReservationsTx(tx, order, at); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func (s *CommerceOrderService) ReleaseExpired(tenantID uint, at time.Time) (int, error) {
	return s.ExpireUnpaidOrders(tenantID, at)
}

// ReleaseExpiredReservations is the explicit API name used by background
// workers. It only releases unpaid reservations that have reached their
// persisted expiry and is idempotent through the payment-status transition.
func (s *CommerceOrderService) ReleaseExpiredReservations(tenantID uint, at time.Time) (int, error) {
	return s.ExpireUnpaidOrders(tenantID, at)
}

// ExpireUnpaidOrdersForAllTenants is used by the process worker. It discovers
// only tenants that currently have expired commercial reservations, then
// reuses the tenant-scoped transaction above so one broken tenant cannot widen
// another tenant's query scope.
func (s *CommerceOrderService) ExpireUnpaidOrdersForAllTenants(at time.Time) (int, error) {
	var tenantIDs []uint
	if err := s.db().Model(&model.CommerceOrder{}).
		Where("payment_status = ? AND expires_at IS NOT NULL AND expires_at <= ?", "unpaid", at).
		Distinct("tenant_id").Pluck("tenant_id", &tenantIDs).Error; err != nil {
		return 0, err
	}
	count := 0
	for _, tenantID := range tenantIDs {
		released, err := s.ExpireUnpaidOrders(tenantID, at)
		if err != nil {
			return count, err
		}
		count += released
	}
	return count, nil
}

func (s *CommerceOrderService) CreateRefundRequest(tenantID, orderID uint, idempotencyKey, reason string) (*CommerceRefundResult, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if tenantID == 0 || orderID == 0 || idempotencyKey == "" || len([]rune(idempotencyKey)) > 100 {
		return nil, fmt.Errorf("%w: tenant, order and idempotency key are required", ErrCommerceRefundInvalid)
	}
	var result *CommerceRefundResult
	err := s.write(func(tx *gorm.DB) error {
		// The idempotency key is tenant-scoped, not order-scoped. Lock the
		// tenant before looking up the request so the same key used against two
		// orders converges to a stable conflict instead of leaking a raw unique
		// constraint error from PostgreSQL.
		var tenant model.Tenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", tenantID).First(&tenant).Error; err != nil {
			return err
		}
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", orderID, tenantID).First(&order).Error; err != nil {
			return err
		}
		if err := RequireConfiguredTenantBusinessCapability(tx, tenantID, order.BusinessType); err != nil {
			return err
		}
		// Serialize the idempotency lookup with the order lock. Looking up first
		// lets concurrent requests both observe no row and makes the loser
		// surface a raw PostgreSQL unique-constraint error instead of the same
		// stable idempotent result.
		var existing model.CommerceAfterSaleRequest
		lookupErr := tx.Where("tenant_id = ? AND idempotency_key = ?", tenantID, idempotencyKey).First(&existing).Error
		if lookupErr == nil {
			if existing.OrderID != orderID {
				return ErrCommerceIdempotencyConflict
			}
			result = &CommerceRefundResult{Request: &existing, Order: &order}
			return nil
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return lookupErr
		}
		if order.PaymentStatus != "paid" || order.RefundStatus != "none" {
			return fmt.Errorf("%w: order is not refundable in its current state", ErrCommerceRefundInvalid)
		}
		if err := requireCommerceRefundableFulfillmentTx(tx, &order); err != nil {
			return err
		}
		request := model.CommerceAfterSaleRequest{TenantID: tenantID, OrderID: order.ID, RequestNo: newCommerceOrderNo(s.now()), IdempotencyKey: idempotencyKey, Type: "refund", Status: "requested", AmountCents: order.TotalAmountCents, Reason: strings.TrimSpace(reason)}
		if request.AmountCents <= 0 {
			return fmt.Errorf("%w: refund amount must be positive", ErrCommerceRefundInvalid)
		}
		if err := tx.Create(&request).Error; err != nil {
			return err
		}
		if err := tx.Model(&order).Update("refund_status", "requested").Error; err != nil {
			return err
		}
		order.RefundStatus = "requested"
		result = &CommerceRefundResult{Request: &request, Order: &order}
		return nil
	})
	return result, err
}

func (s *CommerceOrderService) RequestRefund(tenantID, orderID uint, idempotencyKey, reason string) (*CommerceRefundResult, error) {
	return s.CreateRefundRequest(tenantID, orderID, idempotencyKey, reason)
}

// CancelUnpaidOrder cancels a customer order before payment is confirmed. It
// deliberately refuses pending provider-payment states: releasing inventory
// while a provider result is still unknown could turn a late payment into a
// double sale. Reservation release is delegated to the same idempotent path
// used by expiry and payment failure.
func (s *CommerceOrderService) CancelUnpaidOrder(tenantID, orderID uint) (*model.CommerceOrder, error) {
	if tenantID == 0 || orderID == 0 {
		return nil, fmt.Errorf("%w: tenant and order are required", ErrCommerceOrderInvalid)
	}
	var result *model.CommerceOrder
	err := s.write(func(tx *gorm.DB) error {
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", orderID, tenantID).First(&order).Error; err != nil {
			return err
		}
		if order.RefundStatus != "none" {
			return fmt.Errorf("%w: refund is already in progress or completed", ErrCommerceOrderState)
		}
		if order.PaymentStatus == "pending" {
			return fmt.Errorf("%w: payment result is still being confirmed", ErrCommerceOrderState)
		}
		var reconciliation model.CommercePaymentReconciliationTask
		lookupErr := tx.Where("tenant_id = ? AND order_id = ? AND status IN ?", tenantID, orderID, []string{"pending", "processing", "manual_review"}).First(&reconciliation).Error
		if lookupErr == nil {
			return fmt.Errorf("%w: payment result is still being confirmed", ErrCommerceOrderState)
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return lookupErr
		}
		if order.PaymentStatus != "unpaid" && order.PaymentStatus != "failed" {
			return fmt.Errorf("%w: only unpaid orders can be cancelled", ErrCommerceOrderState)
		}
		if order.PaymentStatus == "failed" && order.FulfillmentStatus == "cancelled" {
			result = &order
			return nil
		}
		if err := releaseCommerceReservationsTx(tx, &order, s.now()); err != nil {
			return err
		}
		result = &order
		return nil
	})
	return result, err
}

// ConfirmReceipt is the customer-side terminal acknowledgement for delivered
// commercial orders. Pickup restaurant orders remain merchant-completed; the
// endpoint intentionally does not expose a customer confirmation path for
// them.
func (s *CommerceOrderService) ConfirmReceipt(tenantID, orderID uint) (*model.CommerceOrder, error) {
	if tenantID == 0 || orderID == 0 {
		return nil, fmt.Errorf("%w: tenant and order are required", ErrCommerceOrderInvalid)
	}
	var result *model.CommerceOrder
	err := s.write(func(tx *gorm.DB) error {
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", orderID, tenantID).First(&order).Error; err != nil {
			return err
		}
		if order.PaymentStatus != "paid" || !commerceRefundAllowsFulfillment(order.RefundStatus) || order.FulfillmentStatus == "cancelled" {
			return fmt.Errorf("%w: order cannot confirm receipt", ErrCommerceOrderState)
		}
		now := s.now()
		switch order.BusinessType {
		case "restaurant":
			var fulfillment model.RestaurantFulfillment
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND order_id = ?", tenantID, orderID).First(&fulfillment).Error; err != nil {
				return err
			}
			if fulfillment.Method != "delivery" {
				return fmt.Errorf("%w: restaurant pickup orders are completed by the merchant", ErrCommerceOrderState)
			}
			if fulfillment.Status == "completed" {
				result = &order
				return nil
			}
			if fulfillment.Status != "delivering" {
				return fmt.Errorf("%w: restaurant delivery is not ready for confirmation", ErrCommerceOrderState)
			}
			if err := tx.Model(&fulfillment).Updates(map[string]interface{}{"status": "completed", "completed_at": now}).Error; err != nil {
				return err
			}
			if err := tx.Model(&order).Update("fulfillment_status", "completed").Error; err != nil {
				return err
			}
			order.FulfillmentStatus = "completed"
		case "retail":
			var fulfillment model.RetailFulfillment
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND order_id = ?", tenantID, orderID).First(&fulfillment).Error; err != nil {
				return err
			}
			if fulfillment.Status == "completed" {
				result = &order
				return nil
			}
			if fulfillment.Status != "delivered" {
				return fmt.Errorf("%w: retail order is not delivered", ErrCommerceOrderState)
			}
			if err := tx.Model(&fulfillment).Updates(map[string]interface{}{"status": "completed"}).Error; err != nil {
				return err
			}
			if err := tx.Model(&order).Update("fulfillment_status", "completed").Error; err != nil {
				return err
			}
			order.FulfillmentStatus = "completed"
		default:
			return fmt.Errorf("%w: unsupported business type", ErrCommerceOrderState)
		}
		result = &order
		return nil
	})
	return result, err
}

// CompleteRefund is intentionally unavailable at the ordinary tenant API
// boundary. A refund request can only be completed after an authenticated
// payment adapter has confirmed the provider refund; otherwise a tenant admin
// could forge a refund, restore inventory, and mark money as returned.
func (s *CommerceOrderService) CompleteRefund(tenantID, requestID uint) (*CommerceRefundResult, error) {
	return nil, ErrCommercePaymentUnavailable
}

// CompleteRefundAfterProviderConfirmation is the trusted adapter boundary.
// The caller must authenticate the provider response before invoking it. The
// provider reference and amount are checked against the immutable order fact,
// then the existing transaction performs the refund exactly once. The
// provider refund identity is a separate fact from the payment reference.
func (s *CommerceOrderService) CompleteRefundAfterProviderConfirmation(tenantID, requestID uint, providerReference string, providerAmountCents int64) (*CommerceRefundResult, error) {
	providerReference = strings.TrimSpace(providerReference)
	if providerReference == "" || len([]rune(providerReference)) > 120 || providerAmountCents <= 0 {
		return nil, fmt.Errorf("%w: provider refund identity and amount are required", ErrCommercePaymentInvalid)
	}
	var result *CommerceRefundResult
	err := s.write(func(tx *gorm.DB) error {
		var request model.CommerceAfterSaleRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", requestID, tenantID).First(&request).Error; err != nil {
			return err
		}
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", request.OrderID, tenantID).First(&order).Error; err != nil {
			return err
		}
		if request.Status == "completed" {
			if request.ProviderRefundReference == "" || request.ProviderRefundAmountCents <= 0 {
				return fmt.Errorf("%w: completed refund is missing provider facts", ErrCommercePaymentInvalid)
			}
			if request.ProviderRefundReference != providerReference || request.ProviderRefundAmountCents != providerAmountCents {
				return fmt.Errorf("%w: provider refund confirmation conflicts with the completed refund", ErrCommercePaymentInvalid)
			}
			result = &CommerceRefundResult{Request: &request, Order: &order}
			return nil
		}
		if request.Status != "requested" && request.Status != "approved" && request.Status != "processing" {
			return fmt.Errorf("%w: refund request is %s", ErrCommerceRefundInvalid, request.Status)
		}
		if order.PaymentStatus != "paid" || (order.RefundStatus != "requested" && order.RefundStatus != "processing") {
			return fmt.Errorf("%w: order is not refundable in its current state", ErrCommerceRefundInvalid)
		}
		if providerAmountCents != order.TotalAmountCents {
			return fmt.Errorf("%w: provider refund amount does not match the order amount", ErrCommercePaymentInvalid)
		}
		if request.ProviderRefundReference != "" && request.ProviderRefundReference != providerReference {
			return fmt.Errorf("%w: provider refund reference conflicts with the refund request", ErrCommercePaymentInvalid)
		}
		if request.ProviderRefundAmountCents != 0 && request.ProviderRefundAmountCents != providerAmountCents {
			return fmt.Errorf("%w: provider refund amount conflicts with the refund request", ErrCommercePaymentInvalid)
		}
		var providerReferenceCount int64
		if err := tx.Model(&model.CommerceAfterSaleRequest{}).
			Where("tenant_id = ? AND provider_refund_reference = ? AND id <> ? AND deleted_at IS NULL", tenantID, providerReference, request.ID).
			Count(&providerReferenceCount).Error; err != nil {
			return err
		}
		if providerReferenceCount > 0 {
			return fmt.Errorf("%w: provider refund reference is already attached to another refund", ErrCommercePaymentInvalid)
		}
		if err := RequireConfiguredTenantBusinessCapability(tx, tenantID, order.BusinessType); err != nil {
			return err
		}
		if err := requireCommerceRefundableFulfillmentTx(tx, &order); err != nil {
			return err
		}
		if request.AmountCents != order.TotalAmountCents {
			return fmt.Errorf("%w: only full commercial refunds are supported", ErrCommerceRefundInvalid)
		}
		if request.ProviderRefundReference == "" {
			if err := tx.Model(&request).Updates(map[string]interface{}{
				"provider_refund_reference":    providerReference,
				"provider_refund_amount_cents": providerAmountCents,
			}).Error; err != nil {
				return err
			}
			request.ProviderRefundReference = providerReference
			request.ProviderRefundAmountCents = providerAmountCents
		}
		var items []model.CommerceOrderItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND order_id = ?", tenantID, order.ID).Order("id ASC").Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			if item.ReservationStatus != "sold" {
				return fmt.Errorf("%w: reservation status is inconsistent", ErrCommerceRefundInvalid)
			}
			var stock model.CommerceInventory
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND sku_id = ? AND location_id = ?", tenantID, item.SkuID, order.LocationID).First(&stock).Error; err != nil {
				return err
			}
			if stock.SoldQty < item.Quantity {
				return fmt.Errorf("%w: sold stock is inconsistent", ErrCommerceRefundInvalid)
			}
			newSold, soldOK := commerceSafeAddInt(stock.SoldQty, -item.Quantity)
			newAvailable, availableOK := commerceSafeAddInt(stock.AvailableQty, item.Quantity)
			newVersion, versionOK := commerceSafeIncrementInt64(stock.Version)
			if !soldOK || !availableOK || !versionOK || newSold < 0 || newAvailable < 0 {
				return fmt.Errorf("%w: inventory counter overflow", ErrCommerceRefundInvalid)
			}
			if err := tx.Model(&stock).Updates(map[string]interface{}{"sold_qty": newSold, "available_qty": newAvailable, "version": newVersion}).Error; err != nil {
				return err
			}
			changed := tx.Model(&model.CommerceOrderItem{}).Where("id = ? AND tenant_id = ? AND order_id = ? AND reservation_status = ?", item.ID, tenantID, order.ID, "sold").Updates(map[string]interface{}{"reservation_status": "refunded", "released_at": s.now()})
			if changed.Error != nil {
				return changed.Error
			}
			if changed.RowsAffected != 1 {
				return fmt.Errorf("%w: reservation changed concurrently", ErrCommerceRefundInvalid)
			}
		}
		if order.BusinessType == "restaurant" {
			if err := tx.Model(&model.RestaurantFulfillment{}).Where("tenant_id = ? AND order_id = ? AND status = ?", tenantID, order.ID, "pending_acceptance").Update("status", "cancelled").Error; err != nil {
				return err
			}
		} else {
			if err := tx.Model(&model.RetailFulfillment{}).Where("tenant_id = ? AND order_id = ? AND status = ?", tenantID, order.ID, "pending_shipment").Update("status", "cancelled").Error; err != nil {
				return err
			}
		}
		promotions := CommercePromotionService{DB: tx, Clock: s.Clock}
		if err := promotions.ReturnCouponAfterFullUnfulfilledRefundTx(tx, tenantID, order.ID); err != nil {
			return err
		}
		if err := tx.Model(&model.CommerceCheckoutQuote{}).
			Where("tenant_id = ? AND consumed_order_id = ? AND status = ?", tenantID, order.ID, "consumed").
			Update("status", "cancelled").Error; err != nil {
			return err
		}
		if err := tx.Create(&model.CommerceAfterSaleEvent{TenantID: tenantID, RequestID: request.ID, EventType: "refund_completed", PayloadJSON: fmt.Sprintf(`{"amount_cents":%d,"provider_reference":%q}`, request.AmountCents, providerReference)}).Error; err != nil {
			return err
		}
		now := s.now()
		if err := tx.Model(&request).Updates(map[string]interface{}{"status": "completed", "processed_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&order).Updates(map[string]interface{}{"payment_status": "refunded", "refund_status": "refunded", "fulfillment_status": "cancelled"}).Error; err != nil {
			return err
		}
		request.Status = "completed"
		request.ProcessedAt = &now
		order.PaymentStatus = "refunded"
		order.RefundStatus = "refunded"
		result = &CommerceRefundResult{Request: &request, Order: &order}
		return nil
	})
	return result, err
}

func requireCommerceRefundableFulfillmentTx(tx *gorm.DB, order *model.CommerceOrder) error {
	if tx == nil || order == nil {
		return ErrCommerceRefundInvalid
	}
	if order.BusinessType == "restaurant" {
		var fulfillment model.RestaurantFulfillment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND order_id = ?", order.TenantID, order.ID).First(&fulfillment).Error; err != nil {
			return err
		}
		if fulfillment.Status != "pending_acceptance" {
			return fmt.Errorf("%w: restaurant order is already being prepared or fulfilled", ErrCommerceRefundInvalid)
		}
		return nil
	}
	if order.BusinessType == "retail" {
		var fulfillment model.RetailFulfillment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND order_id = ?", order.TenantID, order.ID).First(&fulfillment).Error; err != nil {
			return err
		}
		if fulfillment.Status != "pending_shipment" {
			return fmt.Errorf("%w: retail order has already been shipped", ErrCommerceRefundInvalid)
		}
		return nil
	}
	return fmt.Errorf("%w: unsupported business type", ErrCommerceRefundInvalid)
}

func restaurantTransitionAllowed(current, next, method string) bool {
	switch current {
	case "pending_acceptance":
		return next == "accepted"
	case "accepted":
		return next == "preparing"
	case "preparing":
		return next == "ready"
	case "ready":
		return next == "delivering" && method == "delivery" || next == "completed" && method == "pickup"
	case "delivering":
		return next == "completed"
	default:
		return false
	}
}

func retailTransitionAllowed(current, next string) bool {
	switch current {
	case "pending_shipment":
		return next == "shipped"
	case "shipped":
		return next == "in_transit"
	case "in_transit":
		return next == "delivered"
	case "delivered":
		return next == "completed"
	default:
		return false
	}
}

func (s *CommerceOrderService) TransitionRestaurantFulfillment(tenantID, orderID uint, input RestaurantFulfillmentTransitionInput) (*model.RestaurantFulfillment, error) {
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		return nil, ErrCommerceOrderState
	}
	var result *model.RestaurantFulfillment
	err := s.write(func(tx *gorm.DB) error {
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND business_type = ?", orderID, tenantID, "restaurant").First(&order).Error; err != nil {
			return err
		}
		if err := RequireConfiguredTenantBusinessCapability(tx, tenantID, "restaurant"); err != nil {
			return err
		}
		if order.PaymentStatus != "paid" || (order.RefundStatus != "none" && order.RefundStatus != "rejected") {
			return fmt.Errorf("%w: order is not fulfillable", ErrCommerceOrderState)
		}
		var fulfillment model.RestaurantFulfillment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ? AND tenant_id = ?", orderID, tenantID).First(&fulfillment).Error; err != nil {
			return err
		}
		if !restaurantTransitionAllowed(fulfillment.Status, input.Status, fulfillment.Method) {
			return fmt.Errorf("%w: restaurant %s -> %s is not allowed", ErrCommerceOrderState, fulfillment.Status, input.Status)
		}
		beforeJSON, _ := json.Marshal(fulfillment)
		now := s.now()
		updates := map[string]interface{}{"status": input.Status}
		if input.Status == "accepted" {
			updates["accepted_at"] = now
		}
		if input.Status == "ready" {
			updates["ready_at"] = now
		}
		if input.Status == "completed" {
			updates["completed_at"] = now
		}
		if err := tx.Model(&fulfillment).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Model(&order).Update("fulfillment_status", input.Status).Error; err != nil {
			return err
		}
		fulfillment.Status = input.Status
		if err := recordCommerceAuditTx(tx, input.ActorUserID, tenantID, input.ActorRole, "commerce.restaurant_fulfillment.transition", "restaurant_fulfillment", fulfillment.ID, commerceTransitionReason(input.Reason), string(beforeJSON), fmt.Sprintf(`{"status":%q}`, input.Status)); err != nil {
			return err
		}
		result = &fulfillment
		return nil
	})
	return result, err
}

func (s *CommerceOrderService) TransitionRetailFulfillment(tenantID, orderID uint, input RetailFulfillmentTransitionInput) (*model.RetailFulfillment, error) {
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		return nil, ErrCommerceOrderState
	}
	var result *model.RetailFulfillment
	err := s.write(func(tx *gorm.DB) error {
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND business_type = ?", orderID, tenantID, "retail").First(&order).Error; err != nil {
			return err
		}
		if err := RequireConfiguredTenantBusinessCapability(tx, tenantID, "retail"); err != nil {
			return err
		}
		if order.PaymentStatus != "paid" || (order.RefundStatus != "none" && order.RefundStatus != "rejected") {
			return fmt.Errorf("%w: order is not fulfillable", ErrCommerceOrderState)
		}
		var fulfillment model.RetailFulfillment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ? AND tenant_id = ?", orderID, tenantID).First(&fulfillment).Error; err != nil {
			return err
		}
		if !retailTransitionAllowed(fulfillment.Status, input.Status) {
			return fmt.Errorf("%w: retail %s -> %s is not allowed", ErrCommerceOrderState, fulfillment.Status, input.Status)
		}
		if input.Status == "shipped" && (strings.TrimSpace(input.Carrier) == "" || strings.TrimSpace(input.TrackingNo) == "") {
			return fmt.Errorf("%w: carrier and tracking number are required", ErrCommerceOrderInvalid)
		}
		beforeJSON, _ := json.Marshal(fulfillment)
		now := s.now()
		updates := map[string]interface{}{"status": input.Status}
		if input.Status == "shipped" {
			updates["carrier"] = strings.TrimSpace(input.Carrier)
			updates["tracking_no"] = strings.TrimSpace(input.TrackingNo)
			updates["shipped_at"] = now
		}
		if input.Status == "delivered" {
			updates["delivered_at"] = now
		}
		if err := tx.Model(&fulfillment).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Model(&order).Update("fulfillment_status", input.Status).Error; err != nil {
			return err
		}
		fulfillment.Status = input.Status
		if input.Status == "shipped" {
			fulfillment.Carrier = strings.TrimSpace(input.Carrier)
			fulfillment.TrackingNo = strings.TrimSpace(input.TrackingNo)
			fulfillment.ShippedAt = &now
		}
		if input.Status == "delivered" {
			fulfillment.DeliveredAt = &now
		}
		if err := recordCommerceAuditTx(tx, input.ActorUserID, tenantID, input.ActorRole, "commerce.retail_fulfillment.transition", "retail_fulfillment", fulfillment.ID, commerceTransitionReason(input.Reason), string(beforeJSON), fmt.Sprintf(`{"status":%q,"carrier":%q,"tracking_no":%q}`, input.Status, fulfillment.Carrier, fulfillment.TrackingNo)); err != nil {
			return err
		}
		result = &fulfillment
		return nil
	})
	return result, err
}

func commerceTransitionReason(reason string) string {
	if reason = strings.TrimSpace(reason); reason != "" {
		return reason
	}
	return "商业履约状态更新"
}

func (s *CommerceOrderService) UpdateRestaurantFulfillment(tenantID, orderID uint, input RestaurantFulfillmentTransitionInput) (*model.RestaurantFulfillment, error) {
	return s.TransitionRestaurantFulfillment(tenantID, orderID, input)
}

func (s *CommerceOrderService) UpdateRetailFulfillment(tenantID, orderID uint, input RetailFulfillmentTransitionInput) (*model.RetailFulfillment, error) {
	return s.TransitionRetailFulfillment(tenantID, orderID, input)
}
