package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The operations service is the second, still isolated, commercial-domain
// slice. It owns option configuration, fulfillment locations, stock and carts;
// ticket products and ticket inventory never pass through these methods.
type CommerceOperationsService struct {
	DB    *gorm.DB
	Clock func() time.Time
}

func (s *CommerceOperationsService) db() *gorm.DB {
	if s != nil && s.DB != nil {
		return s.DB
	}
	return model.DB
}

// write keeps injected databases (tests, migrations and isolated workers)
// consistent with reads. The application-wide coordinator remains the
// default path so normal writes retain the platform timeout/serialization
// policy.
func (s *CommerceOperationsService) write(apply func(*gorm.DB) error) error {
	if s != nil && s.DB != nil {
		return s.DB.Transaction(apply)
	}
	return model.Write(apply)
}

func (s *CommerceOperationsService) now() time.Time {
	if s != nil && s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

type CreateCommerceOptionGroupInput struct {
	Name          string `json:"name"`
	Required      bool   `json:"required"`
	MinSelections int    `json:"min_selections"`
	MaxSelections int    `json:"max_selections"`
}

type CreateCommerceOptionInput struct {
	Name            string `json:"name"`
	PriceDeltaCents int64  `json:"price_delta_cents"`
	Status          string `json:"status"`
}

type CreateCommerceLocationInput struct {
	BusinessType string `json:"business_type"`
	Name         string `json:"name"`
	LocationType string `json:"location_type"`
	Status       string `json:"status"`
}

type CommerceInventoryInput struct {
	SkuID        uint   `json:"sku_id"`
	LocationID   uint   `json:"location_id"`
	AvailableQty int    `json:"available_qty"`
	Reason       string `json:"reason,omitempty"`
	ActorUserID  uint   `json:"-"`
	ActorRole    string `json:"-"`
}

type CommerceInventoryAdjustmentInput struct {
	SkuID       uint   `json:"sku_id"`
	LocationID  uint   `json:"location_id"`
	Delta       int    `json:"delta"`
	Reason      string `json:"reason,omitempty"`
	ActorUserID uint   `json:"-"`
	ActorRole   string `json:"-"`
}

type CommerceCartInput struct {
	BusinessType     string `json:"business_type"`
	CustomerID       string `json:"customer_id"`
	ChannelAccountID uint   `json:"channel_account_id"`
	LocationID       uint   `json:"location_id"`
}

type CommerceCartItemInput struct {
	ProductID uint   `json:"product_id"`
	SkuID     uint   `json:"sku_id"`
	Quantity  int    `json:"quantity"`
	OptionIDs []uint `json:"option_ids"`
}

type CommerceCheckoutCartInput struct {
	IdempotencyKey    string     `json:"idempotency_key"`
	ContactName       string     `json:"contact_name"`
	ContactPhone      string     `json:"contact_phone"`
	ShippingAddress   string     `json:"shipping_address,omitempty"`
	FulfillmentMethod string     `json:"fulfillment_method,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
}

var (
	ErrCommerceOptionInvalid    = errors.New("commerce option is invalid")
	ErrCommerceLocationInvalid  = errors.New("commerce fulfillment location is invalid")
	ErrCommerceInventoryInvalid = errors.New("commerce inventory is invalid")
	ErrCommerceCartInvalid      = errors.New("commerce cart is invalid")
)

func normalizeOptionGroupInput(input CreateCommerceOptionGroupInput) (CreateCommerceOptionGroupInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 80 {
		return input, fmt.Errorf("%w: option group name is required and bounded", ErrCommerceOptionInvalid)
	}
	if input.MinSelections < 0 || input.MaxSelections < 0 || input.MaxSelections < input.MinSelections {
		return input, fmt.Errorf("%w: invalid selection bounds", ErrCommerceOptionInvalid)
	}
	if input.Required && input.MinSelections == 0 {
		input.MinSelections = 1
	}
	if input.MaxSelections == 0 {
		input.MaxSelections = 1
	}
	if input.MaxSelections < input.MinSelections {
		return input, fmt.Errorf("%w: invalid selection bounds", ErrCommerceOptionInvalid)
	}
	return input, nil
}

func normalizeOptionInput(input CreateCommerceOptionInput) (CreateCommerceOptionInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Status = strings.TrimSpace(input.Status)
	if input.Name == "" || len([]rune(input.Name)) > 80 {
		return input, fmt.Errorf("%w: option name is required and bounded", ErrCommerceOptionInvalid)
	}
	if input.Status == "" {
		input.Status = "active"
	}
	if input.Status != "active" && input.Status != "inactive" {
		return input, fmt.Errorf("%w: option status is invalid", ErrCommerceOptionInvalid)
	}
	return input, nil
}

func normalizeLocationInput(input CreateCommerceLocationInput) (CreateCommerceLocationInput, error) {
	input.BusinessType = strings.TrimSpace(input.BusinessType)
	input.Name = strings.TrimSpace(input.Name)
	input.LocationType = strings.TrimSpace(input.LocationType)
	input.Status = strings.TrimSpace(input.Status)
	if !validTenantBusinessType(input.BusinessType) {
		return input, fmt.Errorf("%w: unsupported business type", ErrCommerceLocationInvalid)
	}
	if input.Name == "" || len([]rune(input.Name)) > 120 {
		return input, fmt.Errorf("%w: location name is required and bounded", ErrCommerceLocationInvalid)
	}
	if input.LocationType == "" {
		input.LocationType = "store"
	}
	if input.LocationType != "store" && input.LocationType != "warehouse" && input.LocationType != "pickup" {
		return input, fmt.Errorf("%w: location type is invalid", ErrCommerceLocationInvalid)
	}
	if input.Status == "" {
		input.Status = "active"
	}
	if input.Status != "active" && input.Status != "inactive" {
		return input, fmt.Errorf("%w: location status is invalid", ErrCommerceLocationInvalid)
	}
	return input, nil
}

func (s *CommerceOperationsService) CreateOptionGroup(tenantID, productID uint, input CreateCommerceOptionGroupInput) (*model.CommerceOptionGroup, error) {
	input, err := normalizeOptionGroupInput(input)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceOptionGroup
	err = s.write(func(tx *gorm.DB) error {
		var product model.CommerceProduct
		if err := tx.Where("id = ? AND tenant_id = ?", productID, tenantID).First(&product).Error; err != nil {
			return err
		}
		if err := requireActiveCommerceCapability(tx, tenantID, product.BusinessType); err != nil {
			return err
		}
		group := model.CommerceOptionGroup{TenantID: tenantID, ProductID: productID, Name: input.Name, Required: input.Required, MinSelections: input.MinSelections, MaxSelections: input.MaxSelections}
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		result = &group
		return nil
	})
	return result, err
}

func (s *CommerceOperationsService) ListOptionGroups(tenantID, productID uint) ([]model.CommerceOptionGroup, error) {
	db := s.db()
	var product model.CommerceProduct
	if err := db.Where("id = ? AND tenant_id = ?", productID, tenantID).First(&product).Error; err != nil {
		return nil, err
	}
	if err := requireActiveCommerceCapability(db, tenantID, product.BusinessType); err != nil {
		return nil, err
	}
	var groups []model.CommerceOptionGroup
	if err := db.Where("tenant_id = ? AND product_id = ?", tenantID, productID).Preload("Options").Order("created_at ASC").Find(&groups).Error; err != nil {
		return nil, err
	}
	if groups == nil {
		groups = []model.CommerceOptionGroup{}
	}
	return groups, nil
}

func (s *CommerceOperationsService) CreateOption(tenantID, groupID uint, input CreateCommerceOptionInput) (*model.CommerceOption, error) {
	input, err := normalizeOptionInput(input)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceOption
	err = s.write(func(tx *gorm.DB) error {
		var group model.CommerceOptionGroup
		if err := tx.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return err
		}
		var product model.CommerceProduct
		if err := tx.Where("id = ? AND tenant_id = ?", group.ProductID, tenantID).First(&product).Error; err != nil {
			return err
		}
		if err := requireActiveCommerceCapability(tx, tenantID, product.BusinessType); err != nil {
			return err
		}
		option := model.CommerceOption{TenantID: tenantID, OptionGroupID: groupID, Name: input.Name, PriceDeltaCents: input.PriceDeltaCents, Status: input.Status}
		if err := tx.Create(&option).Error; err != nil {
			return err
		}
		result = &option
		return nil
	})
	return result, err
}

func (s *CommerceOperationsService) CreateLocation(tenantID uint, input CreateCommerceLocationInput) (*model.CommerceFulfillmentLocation, error) {
	input, err := normalizeLocationInput(input)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceFulfillmentLocation
	err = s.write(func(tx *gorm.DB) error {
		if err := requireActiveCommerceCapability(tx, tenantID, input.BusinessType); err != nil {
			return err
		}
		location := model.CommerceFulfillmentLocation{TenantID: tenantID, BusinessType: input.BusinessType, Name: input.Name, LocationType: input.LocationType, Status: input.Status}
		if err := tx.Create(&location).Error; err != nil {
			return err
		}
		result = &location
		return nil
	})
	return result, err
}

func (s *CommerceOperationsService) ListLocations(tenantID uint, businessType, status string) ([]model.CommerceFulfillmentLocation, error) {
	db := s.db()
	businessType = strings.TrimSpace(businessType)
	status = strings.TrimSpace(status)
	var enabledDomains []string
	if businessType != "" {
		if !validTenantBusinessType(businessType) {
			return nil, fmt.Errorf("%w: unsupported business type", ErrCommerceLocationInvalid)
		}
		if err := requireActiveCommerceCapability(db, tenantID, businessType); err != nil {
			return nil, err
		}
	} else {
		if err := requireActiveTenant(db, tenantID); err != nil {
			return nil, err
		}
		var err error
		enabledDomains, err = activeCommerceBusinessTypes(db, tenantID)
		if err != nil {
			return nil, err
		}
	}
	if status != "" && status != "active" && status != "inactive" {
		return nil, fmt.Errorf("%w: location status is invalid", ErrCommerceLocationInvalid)
	}
	query := db.Where("tenant_id = ?", tenantID)
	if businessType != "" {
		query = query.Where("business_type = ?", businessType)
	} else {
		query = query.Where("business_type IN ?", enabledDomains)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var rows []model.CommerceFulfillmentLocation
	if err := query.Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []model.CommerceFulfillmentLocation{}
	}
	return rows, nil
}

func (s *CommerceOperationsService) SetLocationStatus(tenantID, locationID uint, status string) (*model.CommerceFulfillmentLocation, error) {
	status = strings.TrimSpace(status)
	if status != "active" && status != "inactive" {
		return nil, fmt.Errorf("%w: location status is invalid", ErrCommerceLocationInvalid)
	}
	var result *model.CommerceFulfillmentLocation
	err := s.write(func(tx *gorm.DB) error {
		var location model.CommerceFulfillmentLocation
		if err := tx.Where("id = ? AND tenant_id = ?", locationID, tenantID).First(&location).Error; err != nil {
			return err
		}
		if err := requireActiveCommerceCapability(tx, tenantID, location.BusinessType); err != nil {
			return err
		}
		if err := tx.Model(&location).Update("status", status).Error; err != nil {
			return err
		}
		location.Status = status
		result = &location
		return nil
	})
	return result, err
}

func (s *CommerceOperationsService) SetInventory(tenantID uint, input CommerceInventoryInput) (*model.CommerceInventory, error) {
	if input.SkuID == 0 || input.LocationID == 0 || input.AvailableQty < 0 {
		return nil, fmt.Errorf("%w: sku, location and nonnegative stock are required", ErrCommerceInventoryInvalid)
	}
	var result *model.CommerceInventory
	err := s.write(func(tx *gorm.DB) error {
		product, location, err := commerceSKUAndLocationTx(tx, tenantID, input.SkuID, input.LocationID, true)
		if err != nil {
			return err
		}
		if product.BusinessType != location.BusinessType {
			return fmt.Errorf("%w: sku and location business domains differ", ErrCommerceInventoryInvalid)
		}
		var stock model.CommerceInventory
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND sku_id = ? AND location_id = ?", tenantID, input.SkuID, input.LocationID).First(&stock).Error
		beforeJSON := "{}"
		if errors.Is(err, gorm.ErrRecordNotFound) {
			stock = model.CommerceInventory{TenantID: tenantID, SkuID: input.SkuID, LocationID: input.LocationID, AvailableQty: input.AvailableQty, Version: 1}
			if err := tx.Create(&stock).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			beforeJSON = fmt.Sprintf(`{"available_qty":%d,"reserved_qty":%d,"sold_qty":%d,"released_qty":%d,"version":%d}`, stock.AvailableQty, stock.ReservedQty, stock.SoldQty, stock.ReleasedQty, stock.Version)
			if stock.ReservedQty < 0 || stock.SoldQty < 0 || stock.ReleasedQty < 0 {
				return fmt.Errorf("%w: existing stock facts are invalid", ErrCommerceInventoryInvalid)
			}
			newVersion, versionOK := commerceSafeIncrementInt64(stock.Version)
			if stock.Version < 0 || !versionOK {
				return fmt.Errorf("%w: inventory version overflow", ErrCommerceInventoryInvalid)
			}
			if err := tx.Model(&stock).Updates(map[string]interface{}{"available_qty": input.AvailableQty, "version": newVersion}).Error; err != nil {
				return err
			}
			stock.AvailableQty = input.AvailableQty
			stock.Version = newVersion
		}
		if err := recordCommerceAuditTx(tx, input.ActorUserID, tenantID, input.ActorRole, "commerce.inventory.set", "commerce_inventory", stock.ID, commerceInventoryReason(input.Reason), beforeJSON, fmt.Sprintf(`{"available_qty":%d,"sku_id":%d,"location_id":%d}`, input.AvailableQty, input.SkuID, input.LocationID)); err != nil {
			return err
		}
		result = &stock
		return nil
	})
	return result, err
}

func (s *CommerceOperationsService) AdjustInventory(tenantID uint, input CommerceInventoryAdjustmentInput) (*model.CommerceInventory, error) {
	if input.SkuID == 0 || input.LocationID == 0 || input.Delta == 0 {
		return nil, fmt.Errorf("%w: sku, location and nonzero delta are required", ErrCommerceInventoryInvalid)
	}
	var result *model.CommerceInventory
	err := s.write(func(tx *gorm.DB) error {
		product, location, err := commerceSKUAndLocationTx(tx, tenantID, input.SkuID, input.LocationID, true)
		if err != nil {
			return err
		}
		if product.BusinessType != location.BusinessType {
			return fmt.Errorf("%w: sku and location business domains differ", ErrCommerceInventoryInvalid)
		}
		var stock model.CommerceInventory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND sku_id = ? AND location_id = ?", tenantID, input.SkuID, input.LocationID).First(&stock).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: stock row does not exist", ErrCommerceInventoryInvalid)
			}
			return err
		}
		if stock.AvailableQty < 0 || stock.ReservedQty < 0 || stock.SoldQty < 0 || stock.ReleasedQty < 0 || stock.Version < 0 {
			return fmt.Errorf("%w: existing stock facts are invalid", ErrCommerceInventoryInvalid)
		}
		newAvailable, availableOK := commerceSafeAddInt(stock.AvailableQty, input.Delta)
		newVersion, versionOK := commerceSafeIncrementInt64(stock.Version)
		if !availableOK || !versionOK || newAvailable < 0 {
			return fmt.Errorf("%w: stock cannot become negative", ErrCommerceInventoryInvalid)
		}
		if err := tx.Model(&stock).Updates(map[string]interface{}{"available_qty": newAvailable, "version": newVersion}).Error; err != nil {
			return err
		}
		beforeJSON := fmt.Sprintf(`{"available_qty":%d,"reserved_qty":%d,"sold_qty":%d,"released_qty":%d,"version":%d}`, stock.AvailableQty, stock.ReservedQty, stock.SoldQty, stock.ReleasedQty, stock.Version)
		stock.AvailableQty = newAvailable
		stock.Version = newVersion
		if err := recordCommerceAuditTx(tx, input.ActorUserID, tenantID, input.ActorRole, "commerce.inventory.adjust", "commerce_inventory", stock.ID, commerceInventoryReason(input.Reason), beforeJSON, fmt.Sprintf(`{"available_qty":%d,"delta":%d,"sku_id":%d,"location_id":%d,"version":%d}`, newAvailable, input.Delta, input.SkuID, input.LocationID, newVersion)); err != nil {
			return err
		}
		result = &stock
		return nil
	})
	return result, err
}

func (s *CommerceOperationsService) ListInventory(tenantID uint, businessType string) ([]model.CommerceInventory, error) {
	db := s.db()
	businessType = strings.TrimSpace(businessType)
	var enabledDomains []string
	if businessType != "" {
		if !validTenantBusinessType(businessType) {
			return nil, fmt.Errorf("%w: unsupported business type", ErrCommerceInventoryInvalid)
		}
		if err := requireActiveCommerceCapability(db, tenantID, businessType); err != nil {
			return nil, err
		}
	} else {
		if err := requireActiveTenant(db, tenantID); err != nil {
			return nil, err
		}
		var err error
		enabledDomains, err = activeCommerceBusinessTypes(db, tenantID)
		if err != nil {
			return nil, err
		}
	}
	query := db.Model(&model.CommerceInventory{}).
		Joins("JOIN commerce_skus sku ON sku.id = commerce_inventories.sku_id AND sku.tenant_id = commerce_inventories.tenant_id").
		Joins("JOIN commerce_products product ON product.id = sku.product_id AND product.tenant_id = sku.tenant_id").
		Where("commerce_inventories.tenant_id = ?", tenantID)
	if businessType != "" {
		query = query.Where("product.business_type = ?", businessType)
	} else {
		query = query.Where("product.business_type IN ?", enabledDomains)
	}
	var rows []model.CommerceInventory
	// The inventory model intentionally exposes only immutable IDs. Returning
	// joined SKU/location objects here would make it too easy for a caller to
	// mistake a denormalized view for an ownership authority; callers can fetch
	// the scoped catalog rows separately.
	if err := query.Order("commerce_inventories.created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []model.CommerceInventory{}
	}
	return rows, nil
}

func activeCommerceBusinessTypes(db *gorm.DB, tenantID uint) ([]string, error) {
	var enabled []string
	if err := db.Model(&model.TenantBusinessCapability{}).
		Where("tenant_id = ? AND status = ?", tenantID, "active").
		Pluck("business_type", &enabled).Error; err != nil {
		return nil, err
	}
	if len(enabled) == 0 {
		return nil, ErrBusinessCapabilityInactive
	}
	return enabled, nil
}

func commerceSKUAndLocationTx(tx *gorm.DB, tenantID, skuID, locationID uint, requireActiveLocation bool) (*model.CommerceProduct, *model.CommerceFulfillmentLocation, error) {
	var sku model.CommerceSKU
	if err := tx.Where("id = ? AND tenant_id = ?", skuID, tenantID).First(&sku).Error; err != nil {
		return nil, nil, err
	}
	var product model.CommerceProduct
	if err := tx.Where("id = ? AND tenant_id = ?", sku.ProductID, tenantID).First(&product).Error; err != nil {
		return nil, nil, err
	}
	if err := requireActiveCommerceCapability(tx, tenantID, product.BusinessType); err != nil {
		return nil, nil, err
	}
	var location model.CommerceFulfillmentLocation
	query := tx.Where("id = ? AND tenant_id = ? AND business_type = ?", locationID, tenantID, product.BusinessType)
	if requireActiveLocation {
		query = query.Where("status = ?", "active")
	}
	if err := query.First(&location).Error; err != nil {
		return nil, nil, err
	}
	return &product, &location, nil
}

func normalizeCartInput(input CommerceCartInput) (CommerceCartInput, error) {
	input.BusinessType = strings.TrimSpace(input.BusinessType)
	input.CustomerID = strings.TrimSpace(input.CustomerID)
	if !validTenantBusinessType(input.BusinessType) || input.CustomerID == "" || len([]rune(input.CustomerID)) > 100 || input.LocationID == 0 {
		return input, fmt.Errorf("%w: business type, customer and location are required", ErrCommerceCartInvalid)
	}
	return input, nil
}

func (s *CommerceOperationsService) GetOrCreateCart(tenantID uint, input CommerceCartInput) (*model.CommerceCart, error) {
	input, err := normalizeCartInput(input)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceCart
	err = s.write(func(tx *gorm.DB) error {
		if err := requireActiveCommerceCapability(tx, tenantID, input.BusinessType); err != nil {
			return err
		}
		var location model.CommerceFulfillmentLocation
		if err := tx.Where("id = ? AND tenant_id = ? AND business_type = ? AND status = ?", input.LocationID, tenantID, input.BusinessType, "active").First(&location).Error; err != nil {
			return err
		}
		var cart model.CommerceCart
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND business_type = ? AND customer_id = ? AND channel_account_id = ? AND location_id = ? AND status = ?", tenantID, input.BusinessType, input.CustomerID, input.ChannelAccountID, input.LocationID, "active")
		err := query.Preload("Items").First(&cart).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			cart = model.CommerceCart{TenantID: tenantID, BusinessType: input.BusinessType, CustomerID: input.CustomerID, ChannelAccountID: input.ChannelAccountID, LocationID: input.LocationID, Status: "active"}
			// Use ON CONFLICT instead of catching a duplicate-key error: a
			// PostgreSQL constraint error aborts the transaction and cannot be
			// followed by a read on the same connection. The no-op insert lets a
			// concurrent request win and the scoped query below returns that row.
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&cart).Error; err != nil {
				return err
			}
			if cart.ID == 0 {
				if err := query.Preload("Items").First(&cart).Error; err != nil {
					return err
				}
			}
		} else if err != nil {
			return err
		}
		result = &cart
		return nil
	})
	return result, err
}

func (s *CommerceOperationsService) GetCart(tenantID, cartID uint) (*model.CommerceCart, error) {
	if cartID == 0 {
		return nil, fmt.Errorf("%w: cart id is required", ErrCommerceCartInvalid)
	}
	db := s.db()
	var cart model.CommerceCart
	if err := db.Where("id = ? AND tenant_id = ?", cartID, tenantID).Preload("Items").First(&cart).Error; err != nil {
		return nil, err
	}
	if err := requireActiveCommerceCapability(db, tenantID, cart.BusinessType); err != nil {
		return nil, err
	}
	return &cart, nil
}

// CheckoutCart bridges the durable cart aggregate into the order aggregate.
// The deterministic default idempotency key means concurrent retries create at
// most one order, while CheckedOutOrderID lets later reads return that order
// without reopening or re-pricing the cart.
func (s *CommerceOperationsService) CheckoutCart(tenantID, cartID uint, input CommerceCheckoutCartInput) (*model.CommerceOrder, error) {
	if tenantID == 0 || cartID == 0 {
		return nil, fmt.Errorf("%w: tenant and cart are required", ErrCommerceCartInvalid)
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = fmt.Sprintf("commerce-cart-checkout-%d", cartID)
	}
	var result *model.CommerceOrder
	err := s.write(func(tx *gorm.DB) error {
		// Hold the cart row for the whole order creation. Without this lock,
		// concurrent requests using different idempotency keys can both create
		// orders before one of them marks the cart checked out, leaving an order
		// with reserved inventory that is no longer reachable from the cart.
		var cart model.CommerceCart
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", cartID, tenantID).Preload("Items").First(&cart).Error; err != nil {
			return err
		}
		orderService := &CommerceOrderService{DB: tx, Clock: s.Clock}
		if cart.Status == "checked_out" {
			if cart.CheckedOutOrderID == 0 {
				return fmt.Errorf("%w: checked-out cart is missing its order", ErrCommerceCartInvalid)
			}
			order, err := orderService.GetOrder(tenantID, cart.CheckedOutOrderID)
			if err != nil {
				return err
			}
			result = order
			return nil
		}
		if cart.Status != "active" || len(cart.Items) == 0 {
			return fmt.Errorf("%w: cart is empty or no longer active", ErrCommerceCartInvalid)
		}
		items := make([]CommerceOrderItemInput, 0, len(cart.Items))
		for _, item := range cart.Items {
			items = append(items, CommerceOrderItemInput{ProductID: item.ProductID, SKUID: item.SkuID, Quantity: item.Quantity, OptionsSnapshotJSON: item.OptionsSnapshotJSON})
		}
		order, err := orderService.CreateOrder(tenantID, CreateCommerceOrderInput{
			IdempotencyKey: input.IdempotencyKey, BusinessType: cart.BusinessType, CustomerID: cart.CustomerID,
			LocationID: cart.LocationID, ContactName: input.ContactName, ContactPhone: input.ContactPhone,
			ShippingAddressJSON: input.ShippingAddress, FulfillmentMethod: input.FulfillmentMethod, ExpiresAt: input.ExpiresAt,
			Items: items,
		})
		if err != nil {
			return err
		}
		if err := tx.Model(&cart).Updates(map[string]interface{}{"status": "checked_out", "checked_out_order_id": order.ID}).Error; err != nil {
			return err
		}
		result = order
		return nil
	})
	return result, err
}

func normalizeCartItemInput(input CommerceCartItemInput) (CommerceCartItemInput, error) {
	if input.ProductID == 0 || input.SkuID == 0 || input.Quantity < 1 || input.Quantity > 99 {
		return input, fmt.Errorf("%w: product, sku and quantity are invalid", ErrCommerceCartInvalid)
	}
	seen := make(map[uint]struct{}, len(input.OptionIDs))
	for _, id := range input.OptionIDs {
		if id == 0 {
			return input, fmt.Errorf("%w: option id is invalid", ErrCommerceCartInvalid)
		}
		if _, ok := seen[id]; ok {
			return input, fmt.Errorf("%w: duplicate option id", ErrCommerceCartInvalid)
		}
		seen[id] = struct{}{}
	}
	sort.Slice(input.OptionIDs, func(i, j int) bool { return input.OptionIDs[i] < input.OptionIDs[j] })
	return input, nil
}

type optionSnapshot struct {
	OptionID        uint   `json:"option_id"`
	GroupID         uint   `json:"group_id"`
	GroupName       string `json:"group_name"`
	Name            string `json:"name"`
	PriceDeltaCents int64  `json:"price_delta_cents"`
}

func snapshotCartOptionsTx(tx *gorm.DB, tenantID, productID uint, optionIDs []uint) (string, error) {
	var groups []model.CommerceOptionGroup
	if err := tx.Where("tenant_id = ? AND product_id = ?", tenantID, productID).Preload("Options", "status = ?", "active").Find(&groups).Error; err != nil {
		return "", err
	}
	selected := make(map[uint]optionSnapshot, len(optionIDs))
	for _, group := range groups {
		count := 0
		for _, option := range group.Options {
			if containsUint(optionIDs, option.ID) {
				selected[option.ID] = optionSnapshot{OptionID: option.ID, GroupID: group.ID, GroupName: group.Name, Name: option.Name, PriceDeltaCents: option.PriceDeltaCents}
				count++
			}
		}
		if count < group.MinSelections || count > group.MaxSelections {
			return "", fmt.Errorf("%w: option group %q selection count is invalid", ErrCommerceCartInvalid, group.Name)
		}
	}
	if len(selected) != len(optionIDs) {
		return "", fmt.Errorf("%w: option is not active or does not belong to product", ErrCommerceCartInvalid)
	}
	result := make([]optionSnapshot, 0, len(optionIDs))
	for _, id := range optionIDs {
		result = append(result, selected[id])
	}
	b, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("%w: encode options", ErrCommerceCartInvalid)
	}
	return string(b), nil
}

func containsUint(values []uint, target uint) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *CommerceOperationsService) AddCartItem(tenantID, cartID uint, input CommerceCartItemInput) (*model.CommerceCart, error) {
	input, err := normalizeCartItemInput(input)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceCart
	err = s.write(func(tx *gorm.DB) error {
		var cart model.CommerceCart
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", cartID, tenantID).First(&cart).Error; err != nil {
			return err
		}
		if cart.Status != "active" {
			return fmt.Errorf("%w: cart is no longer active", ErrCommerceCartInvalid)
		}
		if err := requireActiveCommerceCapability(tx, tenantID, cart.BusinessType); err != nil {
			return err
		}
		var sku model.CommerceSKU
		if err := tx.Where("id = ? AND tenant_id = ? AND product_id = ? AND status = ?", input.SkuID, tenantID, input.ProductID, "active").First(&sku).Error; err != nil {
			return err
		}
		var product model.CommerceProduct
		if err := tx.Where("id = ? AND tenant_id = ? AND business_type = ? AND status = ?", input.ProductID, tenantID, cart.BusinessType, "online").First(&product).Error; err != nil {
			return err
		}
		now := s.now()
		if product.SaleStartsAt != nil && product.SaleStartsAt.After(now) || product.SaleEndsAt != nil && !now.Before(*product.SaleEndsAt) {
			return fmt.Errorf("%w: product is outside its sale window", ErrCommerceCartInvalid)
		}
		var location model.CommerceFulfillmentLocation
		if err := tx.Where("id = ? AND tenant_id = ? AND business_type = ? AND status = ?", cart.LocationID, tenantID, cart.BusinessType, "active").First(&location).Error; err != nil {
			return err
		}
		optionsJSON, err := snapshotCartOptionsTx(tx, tenantID, input.ProductID, input.OptionIDs)
		if err != nil {
			return err
		}
		var item model.CommerceCartItem
		findErr := tx.Where("tenant_id = ? AND cart_id = ? AND product_id = ? AND sku_id = ? AND options_snapshot_json = ?", tenantID, cartID, input.ProductID, input.SkuID, optionsJSON).First(&item).Error
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			item = model.CommerceCartItem{TenantID: tenantID, CartID: cartID, ProductID: input.ProductID, SkuID: input.SkuID, Quantity: input.Quantity, OptionsSnapshotJSON: optionsJSON}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		} else if findErr != nil {
			return findErr
		} else {
			newQuantity := item.Quantity + input.Quantity
			if newQuantity > 99 {
				return fmt.Errorf("%w: cart item quantity cannot exceed 99", ErrCommerceCartInvalid)
			}
			if err := tx.Model(&item).Update("quantity", newQuantity).Error; err != nil {
				return err
			}
		}
		if err := tx.Preload("Items").First(&cart, cart.ID).Error; err != nil {
			return err
		}
		result = &cart
		return nil
	})
	return result, err
}

func (s *CommerceOperationsService) UpdateCartItem(tenantID, cartID, itemID uint, quantity int) (*model.CommerceCart, error) {
	if quantity < 1 || quantity > 99 {
		return nil, fmt.Errorf("%w: quantity must be between 1 and 99", ErrCommerceCartInvalid)
	}
	var result *model.CommerceCart
	err := s.write(func(tx *gorm.DB) error {
		var cart model.CommerceCart
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND status = ?", cartID, tenantID, "active").First(&cart).Error; err != nil {
			return err
		}
		if err := requireActiveCommerceCapability(tx, tenantID, cart.BusinessType); err != nil {
			return err
		}
		var item model.CommerceCartItem
		if err := tx.Where("id = ? AND tenant_id = ? AND cart_id = ?", itemID, tenantID, cartID).First(&item).Error; err != nil {
			return err
		}
		if err := tx.Model(&item).Update("quantity", quantity).Error; err != nil {
			return err
		}
		if err := tx.Preload("Items").First(&cart, cart.ID).Error; err != nil {
			return err
		}
		result = &cart
		return nil
	})
	return result, err
}

func (s *CommerceOperationsService) RemoveCartItem(tenantID, cartID, itemID uint) error {
	return s.write(func(tx *gorm.DB) error {
		var cart model.CommerceCart
		if err := tx.Where("id = ? AND tenant_id = ? AND status = ?", cartID, tenantID, "active").First(&cart).Error; err != nil {
			return err
		}
		if err := requireActiveCommerceCapability(tx, tenantID, cart.BusinessType); err != nil {
			return err
		}
		result := tx.Where("id = ? AND tenant_id = ? AND cart_id = ?", itemID, tenantID, cartID).Delete(&model.CommerceCartItem{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *CommerceOperationsService) AbandonCart(tenantID, cartID uint) error {
	return s.write(func(tx *gorm.DB) error {
		var cart model.CommerceCart
		if err := tx.Where("id = ? AND tenant_id = ?", cartID, tenantID).First(&cart).Error; err != nil {
			return err
		}
		if err := requireActiveCommerceCapability(tx, tenantID, cart.BusinessType); err != nil {
			return err
		}
		if cart.Status != "active" {
			return nil
		}
		return tx.Model(&cart).Update("status", "abandoned").Error
	})
}

func commerceInventoryReason(reason string) string {
	if reason = strings.TrimSpace(reason); reason != "" {
		return reason
	}
	return "商业库存盘点调整"
}
