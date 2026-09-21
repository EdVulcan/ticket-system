package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrCommerceDeliveryInvalid = errors.New("commerce delivery configuration is invalid")
	ErrCommerceQuoteInvalid    = errors.New("commerce checkout quote is invalid")
	ErrCommerceQuoteExpired    = errors.New("commerce checkout quote has expired")
	ErrCommerceQuoteConflict   = errors.New("commerce checkout quote conflicts with the order")
)

type CommerceDeliveryService struct {
	DB    *gorm.DB
	Clock func() time.Time
}

func (s *CommerceDeliveryService) db() *gorm.DB {
	if s != nil && s.DB != nil {
		return s.DB
	}
	return model.DB
}

func (s *CommerceDeliveryService) now() time.Time {
	if s != nil && s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func (s *CommerceDeliveryService) write(fn func(*gorm.DB) error) error {
	if s != nil && s.DB != nil {
		return s.DB.Transaction(fn)
	}
	return model.Write(fn)
}

type CommerceLocationServiceConfigInput struct {
	BusinessType               string `json:"business_type"`
	PickupEnabled              bool   `json:"pickup_enabled"`
	DeliveryEnabled            bool   `json:"delivery_enabled"`
	ShippingEnabled            bool   `json:"shipping_enabled"`
	MinGoodsCents              int64  `json:"min_goods_cents"`
	PackagingFeeCents          int64  `json:"packaging_fee_cents"`
	ShippingFeeCents           int64  `json:"shipping_fee_cents"`
	FreeShippingThresholdCents int64  `json:"free_shipping_threshold_cents"`
	EstimatedMinutes           int    `json:"estimated_minutes"`
	Status                     string `json:"status"`
	ContactName                string `json:"contact_name"`
	ContactPhone               string `json:"contact_phone"`
	Address                    string `json:"address"`
}

type CommerceDeliveryZoneInput struct {
	Name                  string `json:"name"`
	Province              string `json:"province"`
	City                  string `json:"city"`
	District              string `json:"district"`
	FeeCents              int64  `json:"fee_cents"`
	MinGoodsCentsOverride *int64 `json:"min_goods_cents_override,omitempty"`
	EstimatedMinutes      int    `json:"estimated_minutes"`
	Status                string `json:"status"`
	SortOrder             int    `json:"sort_order"`
}

type CommerceDeliverySlotInput struct {
	ZoneID             *uint  `json:"zone_id,omitempty"`
	DayOfWeek          int    `json:"day_of_week"`
	StartMinute        int    `json:"start_minute"`
	EndMinute          int    `json:"end_minute"`
	OrderCutoffMinutes int    `json:"order_cutoff_minutes"`
	Capacity           int    `json:"capacity"`
	Status             string `json:"status"`
}

type CommerceDeliveryAddress struct {
	Province string `json:"province"`
	City     string `json:"city"`
	District string `json:"district"`
	Detail   string `json:"detail,omitempty"`
}

type CommerceCheckoutQuoteInput struct {
	TenantID           uint
	ChannelAccountID   uint
	BusinessType       string
	CustomerID         string
	LocationID         uint
	FulfillmentMethod  string
	Address            CommerceDeliveryAddress
	ZoneID             uint
	SlotID             uint
	SlotDate           time.Time
	GoodsSubtotalCents int64 `json:"-"` // populated by a trusted cart/order calculator
	ExpiresIn          time.Duration
}

type CommerceCheckoutQuoteResult struct {
	Quote            model.CommerceCheckoutQuote `json:"quote"`
	RawToken         string                      `json:"token"`
	EstimatedMinutes int                         `json:"estimated_minutes"`
}

type CommerceConsumeQuoteInput struct {
	RawToken            string
	TenantID            uint
	ChannelAccountID    uint
	BusinessType        string
	CustomerID          string
	LocationID          uint
	FulfillmentMethod   string
	GoodsSubtotalCents  int64
	AddressSnapshotJSON string
	OrderID             uint
}

func normalizeDeliveryInput(input CommerceLocationServiceConfigInput) (CommerceLocationServiceConfigInput, error) {
	input.BusinessType = strings.TrimSpace(input.BusinessType)
	input.Status = strings.TrimSpace(input.Status)
	input.ContactName, input.ContactPhone, input.Address = strings.TrimSpace(input.ContactName), strings.TrimSpace(input.ContactPhone), strings.TrimSpace(input.Address)
	if !validTenantBusinessType(input.BusinessType) {
		return input, fmt.Errorf("%w: unsupported business type", ErrCommerceDeliveryInvalid)
	}
	if input.Status == "" {
		input.Status = "active"
	}
	if input.Status != "active" && input.Status != "inactive" {
		return input, fmt.Errorf("%w: invalid config status", ErrCommerceDeliveryInvalid)
	}
	if input.MinGoodsCents < 0 || input.PackagingFeeCents < 0 || input.ShippingFeeCents < 0 || input.FreeShippingThresholdCents < 0 || input.EstimatedMinutes < 0 {
		return input, fmt.Errorf("%w: fees and estimate must be non-negative", ErrCommerceDeliveryInvalid)
	}
	if input.BusinessType == "restaurant" && (input.ShippingEnabled || input.ShippingFeeCents != 0 || input.FreeShippingThresholdCents != 0) {
		return input, fmt.Errorf("%w: restaurant configuration cannot include retail shipping settings", ErrCommerceDeliveryInvalid)
	}
	if input.BusinessType == "retail" && (input.PickupEnabled || input.DeliveryEnabled || input.PackagingFeeCents != 0) {
		return input, fmt.Errorf("%w: retail configuration cannot include restaurant fulfillment settings", ErrCommerceDeliveryInvalid)
	}
	return input, nil
}

func normalizeZoneInput(input CommerceDeliveryZoneInput) (CommerceDeliveryZoneInput, error) {
	input.Name, input.Province, input.City, input.District = strings.TrimSpace(input.Name), strings.TrimSpace(input.Province), strings.TrimSpace(input.City), strings.TrimSpace(input.District)
	input.Status = strings.TrimSpace(input.Status)
	if input.Name == "" || input.Province == "" || input.City == "" || input.District == "" {
		return input, fmt.Errorf("%w: zone identity and region are required", ErrCommerceDeliveryInvalid)
	}
	if input.Status == "" {
		input.Status = "active"
	}
	if input.Status != "active" && input.Status != "inactive" || input.FeeCents < 0 || input.EstimatedMinutes < 0 || input.SortOrder < 0 {
		return input, fmt.Errorf("%w: invalid zone values", ErrCommerceDeliveryInvalid)
	}
	if input.MinGoodsCentsOverride != nil && *input.MinGoodsCentsOverride < 0 {
		return input, fmt.Errorf("%w: zone minimum must be non-negative", ErrCommerceDeliveryInvalid)
	}
	return input, nil
}

func normalizeSlotInput(input CommerceDeliverySlotInput) (CommerceDeliverySlotInput, error) {
	if input.DayOfWeek < 0 || input.DayOfWeek > 6 || input.StartMinute < 0 || input.StartMinute > 1439 || input.EndMinute < 1 || input.EndMinute > 1440 || input.EndMinute <= input.StartMinute || input.OrderCutoffMinutes < 0 || input.OrderCutoffMinutes > 1440 || input.Capacity < 0 {
		return input, fmt.Errorf("%w: invalid delivery slot window", ErrCommerceDeliveryInvalid)
	}
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "active"
	}
	if input.Status != "active" && input.Status != "inactive" {
		return input, fmt.Errorf("%w: invalid slot status", ErrCommerceDeliveryInvalid)
	}
	return input, nil
}

func (s *CommerceDeliveryService) locationScope(tx *gorm.DB, tenantID, locationID uint, businessType string) (*model.CommerceFulfillmentLocation, error) {
	if tx == nil || tenantID == 0 || locationID == 0 || !validTenantBusinessType(strings.TrimSpace(businessType)) {
		return nil, fmt.Errorf("%w: tenant, location and business are required", ErrCommerceDeliveryInvalid)
	}
	if err := RequireActiveTenantBusinessCapability(tx, tenantID, businessType); err != nil {
		return nil, err
	}
	var location model.CommerceFulfillmentLocation
	if err := tx.Where("id = ? AND tenant_id = ? AND business_type = ? AND status = ?", locationID, tenantID, businessType, "active").First(&location).Error; err != nil {
		return nil, err
	}
	return &location, nil
}

func (s *CommerceDeliveryService) channelScope(tx *gorm.DB, tenantID, channelID uint) error {
	if channelID == 0 {
		return fmt.Errorf("%w: channel account is required", ErrCommerceQuoteInvalid)
	}
	var account model.ChannelAccount
	if err := tx.Where("id = ? AND tenant_id = ? AND status IN ?", channelID, tenantID, []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return err
	}
	return nil
}

func (s *CommerceDeliveryService) UpsertLocationConfig(tenantID, locationID uint, input CommerceLocationServiceConfigInput) (*model.CommerceLocationServiceConfig, error) {
	var result *model.CommerceLocationServiceConfig
	input, err := normalizeDeliveryInput(input)
	if err != nil {
		return nil, err
	}
	err = s.write(func(tx *gorm.DB) error {
		if _, err := s.locationScope(tx, tenantID, locationID, input.BusinessType); err != nil {
			return err
		}
		var row model.CommerceLocationServiceConfig
		err := tx.Where("tenant_id = ? AND location_id = ? AND business_type = ?", tenantID, locationID, input.BusinessType).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row = model.CommerceLocationServiceConfig{TenantID: tenantID, LocationID: locationID, BusinessType: input.BusinessType, ConfigVersion: 1}
		} else if err != nil {
			return err
		} else {
			row.ConfigVersion++
		}
		row.PickupEnabled, row.DeliveryEnabled, row.ShippingEnabled, row.MinGoodsCents, row.PackagingFeeCents, row.ShippingFeeCents, row.FreeShippingThresholdCents, row.EstimatedMinutes = input.PickupEnabled, input.DeliveryEnabled, input.ShippingEnabled, input.MinGoodsCents, input.PackagingFeeCents, input.ShippingFeeCents, input.FreeShippingThresholdCents, input.EstimatedMinutes
		row.Status, row.ContactName, row.ContactPhone, row.Address = input.Status, input.ContactName, input.ContactPhone, input.Address
		if row.ID == 0 {
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else if err := tx.Save(&row).Error; err != nil {
			return err
		}
		result = &row
		return nil
	})
	return result, err
}

func (s *CommerceDeliveryService) GetLocationConfig(tenantID, locationID uint, businessType string) (*model.CommerceLocationServiceConfig, error) {
	if _, err := s.locationScope(s.db(), tenantID, locationID, businessType); err != nil {
		return nil, err
	}
	var row model.CommerceLocationServiceConfig
	if err := s.db().Where("tenant_id = ? AND location_id = ? AND business_type = ?", tenantID, locationID, businessType).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *CommerceDeliveryService) CreateZone(tenantID, locationID uint, businessType string, input CommerceDeliveryZoneInput) (*model.CommerceDeliveryZone, error) {
	if strings.TrimSpace(businessType) != "restaurant" {
		return nil, fmt.Errorf("%w: delivery zones belong to restaurant delivery", ErrCommerceDeliveryInvalid)
	}
	input, err := normalizeZoneInput(input)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceDeliveryZone
	err = s.write(func(tx *gorm.DB) error {
		if _, err := s.locationScope(tx, tenantID, locationID, businessType); err != nil {
			return err
		}
		row := model.CommerceDeliveryZone{TenantID: tenantID, LocationID: locationID, Name: input.Name, Province: input.Province, City: input.City, District: input.District, FeeCents: input.FeeCents, MinGoodsCentsOverride: input.MinGoodsCentsOverride, EstimatedMinutes: input.EstimatedMinutes, Status: input.Status, SortOrder: input.SortOrder}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		result = &row
		return nil
	})
	return result, err
}

func (s *CommerceDeliveryService) UpdateZone(tenantID, locationID, zoneID uint, businessType string, input CommerceDeliveryZoneInput) (*model.CommerceDeliveryZone, error) {
	if strings.TrimSpace(businessType) != "restaurant" {
		return nil, fmt.Errorf("%w: delivery zones belong to restaurant delivery", ErrCommerceDeliveryInvalid)
	}
	input, err := normalizeZoneInput(input)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceDeliveryZone
	err = s.write(func(tx *gorm.DB) error {
		if _, err := s.locationScope(tx, tenantID, locationID, businessType); err != nil {
			return err
		}
		var row model.CommerceDeliveryZone
		if err := tx.Where("id = ? AND tenant_id = ? AND location_id = ?", zoneID, tenantID, locationID).First(&row).Error; err != nil {
			return err
		}
		row.Name, row.Province, row.City, row.District, row.FeeCents, row.MinGoodsCentsOverride, row.EstimatedMinutes, row.Status, row.SortOrder = input.Name, input.Province, input.City, input.District, input.FeeCents, input.MinGoodsCentsOverride, input.EstimatedMinutes, input.Status, input.SortOrder
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		result = &row
		return nil
	})
	return result, err
}

func (s *CommerceDeliveryService) ListZones(tenantID, locationID uint, businessType string) ([]model.CommerceDeliveryZone, error) {
	if strings.TrimSpace(businessType) != "restaurant" {
		return nil, fmt.Errorf("%w: delivery zones belong to restaurant delivery", ErrCommerceDeliveryInvalid)
	}
	if _, err := s.locationScope(s.db(), tenantID, locationID, businessType); err != nil {
		return nil, err
	}
	var rows []model.CommerceDeliveryZone
	err := s.db().Where("tenant_id = ? AND location_id = ?", tenantID, locationID).Order("sort_order ASC, id ASC").Find(&rows).Error
	return rows, err
}

func (s *CommerceDeliveryService) CreateSlot(tenantID, locationID uint, businessType string, input CommerceDeliverySlotInput) (*model.CommerceDeliverySlot, error) {
	if strings.TrimSpace(businessType) != "restaurant" {
		return nil, fmt.Errorf("%w: delivery slots belong to restaurant delivery", ErrCommerceDeliveryInvalid)
	}
	input, err := normalizeSlotInput(input)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceDeliverySlot
	err = s.write(func(tx *gorm.DB) error {
		if _, err := s.locationScope(tx, tenantID, locationID, businessType); err != nil {
			return err
		}
		if input.ZoneID != nil {
			var z model.CommerceDeliveryZone
			if err := tx.Where("id = ? AND tenant_id = ? AND location_id = ?", *input.ZoneID, tenantID, locationID).First(&z).Error; err != nil {
				return err
			}
		}
		row := model.CommerceDeliverySlot{TenantID: tenantID, LocationID: locationID, ZoneID: input.ZoneID, DayOfWeek: input.DayOfWeek, StartMinute: input.StartMinute, EndMinute: input.EndMinute, OrderCutoffMinutes: input.OrderCutoffMinutes, Capacity: input.Capacity, Status: input.Status}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		result = &row
		return nil
	})
	return result, err
}

func (s *CommerceDeliveryService) UpdateSlot(tenantID, locationID, slotID uint, businessType string, input CommerceDeliverySlotInput) (*model.CommerceDeliverySlot, error) {
	if strings.TrimSpace(businessType) != "restaurant" {
		return nil, fmt.Errorf("%w: delivery slots belong to restaurant delivery", ErrCommerceDeliveryInvalid)
	}
	input, err := normalizeSlotInput(input)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceDeliverySlot
	err = s.write(func(tx *gorm.DB) error {
		if _, err := s.locationScope(tx, tenantID, locationID, businessType); err != nil {
			return err
		}
		if input.ZoneID != nil {
			var z model.CommerceDeliveryZone
			if err := tx.Where("id = ? AND tenant_id = ? AND location_id = ?", *input.ZoneID, tenantID, locationID).First(&z).Error; err != nil {
				return err
			}
		}
		var row model.CommerceDeliverySlot
		if err := tx.Where("id = ? AND tenant_id = ? AND location_id = ?", slotID, tenantID, locationID).First(&row).Error; err != nil {
			return err
		}
		row.ZoneID, row.DayOfWeek, row.StartMinute, row.EndMinute, row.OrderCutoffMinutes, row.Capacity, row.Status = input.ZoneID, input.DayOfWeek, input.StartMinute, input.EndMinute, input.OrderCutoffMinutes, input.Capacity, input.Status
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		result = &row
		return nil
	})
	return result, err
}

func (s *CommerceDeliveryService) ListSlots(tenantID, locationID uint, businessType string, zoneID *uint) ([]model.CommerceDeliverySlot, error) {
	if strings.TrimSpace(businessType) != "restaurant" {
		return nil, fmt.Errorf("%w: delivery slots belong to restaurant delivery", ErrCommerceDeliveryInvalid)
	}
	if _, err := s.locationScope(s.db(), tenantID, locationID, businessType); err != nil {
		return nil, err
	}
	query := s.db().Where("tenant_id = ? AND location_id = ?", tenantID, locationID)
	if zoneID != nil {
		query = query.Where("zone_id = ?", *zoneID)
	}
	var rows []model.CommerceDeliverySlot
	err := query.Order("day_of_week ASC, start_minute ASC, id ASC").Find(&rows).Error
	return rows, err
}

func customerHash(customerID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(customerID)))
	return hex.EncodeToString(sum[:])
}

func randomQuoteToken() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(sum[:])
	return raw, hash, nil
}

func normalizeAddress(address CommerceDeliveryAddress) CommerceDeliveryAddress {
	address.Province, address.City, address.District, address.Detail = strings.TrimSpace(address.Province), strings.TrimSpace(address.City), strings.TrimSpace(address.District), strings.TrimSpace(address.Detail)
	return address
}

// normalizeAddressSnapshot canonicalizes the address representation used by
// a quote and by the order snapshot. Malformed or incomplete delivery
// addresses fail closed; pickup must not carry an address at all.
func normalizeAddressSnapshot(method, raw string) (string, error) {
	method, raw = strings.TrimSpace(method), strings.TrimSpace(raw)
	if method == "pickup" {
		if raw != "" {
			return "", fmt.Errorf("%w: pickup quote cannot carry a shipping address", ErrCommerceQuoteConflict)
		}
		return "", nil
	}
	if (method != "delivery" && method != "shipping") || raw == "" {
		return "", fmt.Errorf("%w: fulfillment method and address are inconsistent", ErrCommerceQuoteConflict)
	}
	var snapshot struct {
		Province   string `json:"province"`
		City       string `json:"city"`
		District   string `json:"district"`
		Detail     string `json:"detail"`
		CampusName string `json:"campus_name"`
		ZoneName   string `json:"zone_name"`
		Building   string `json:"building"`
		Room       string `json:"room"`
	}
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return "", fmt.Errorf("%w: shipping address snapshot is invalid", ErrCommerceQuoteConflict)
	}
	address := CommerceDeliveryAddress{Province: snapshot.Province, City: snapshot.City, District: snapshot.District, Detail: snapshot.Detail}
	// Historical CAMPUS addresses remain valid during the generic-delivery
	// migration. New addresses use DELIVERY, but old immutable order snapshots
	// are canonically mapped instead of being rewritten or rejected.
	if strings.TrimSpace(snapshot.CampusName) != "" {
		if strings.TrimSpace(address.Province) == "" {
			address.Province = snapshot.CampusName
		}
		if strings.TrimSpace(address.City) == "" {
			address.City = snapshot.CampusName
		}
		if strings.TrimSpace(address.District) == "" {
			address.District = snapshot.ZoneName
		}
		address.Detail = strings.TrimSpace(strings.Join([]string{snapshot.Building, snapshot.Room, snapshot.Detail}, " "))
	}
	address = normalizeAddress(address)
	if address.Province == "" || address.City == "" || address.District == "" {
		return "", fmt.Errorf("%w: delivery address is incomplete", ErrCommerceQuoteConflict)
	}
	bytes, err := json.Marshal(address)
	if err != nil {
		return "", fmt.Errorf("%w: shipping address snapshot cannot be encoded", ErrCommerceQuoteConflict)
	}
	return string(bytes), nil
}

func (s *CommerceDeliveryService) CreateQuote(input CommerceCheckoutQuoteInput) (*CommerceCheckoutQuoteResult, error) {
	input.BusinessType, input.FulfillmentMethod, input.CustomerID = strings.TrimSpace(input.BusinessType), strings.TrimSpace(input.FulfillmentMethod), strings.TrimSpace(input.CustomerID)
	if input.CustomerID == "" || input.TenantID == 0 || input.LocationID == 0 || input.ExpiresIn <= 0 {
		return nil, fmt.Errorf("%w: authenticated scope and expiry are required", ErrCommerceQuoteInvalid)
	}
	if input.ExpiresIn > 24*time.Hour {
		return nil, fmt.Errorf("%w: quote expiry is too long", ErrCommerceQuoteInvalid)
	}
	if input.GoodsSubtotalCents < 0 {
		return nil, fmt.Errorf("%w: goods subtotal must be non-negative", ErrCommerceQuoteInvalid)
	}
	if (input.BusinessType == "restaurant" && input.FulfillmentMethod != "pickup" && input.FulfillmentMethod != "delivery") ||
		(input.BusinessType == "retail" && input.FulfillmentMethod != "shipping") {
		return nil, fmt.Errorf("%w: invalid fulfillment method for business", ErrCommerceQuoteInvalid)
	}
	input.Address = normalizeAddress(input.Address)
	if !input.SlotDate.IsZero() {
		// A delivery slot is a calendar-day fact. Persist one canonical value so
		// different times or timezone offsets on the same selected date cannot
		// split capacity accounting into separate buckets.
		input.SlotDate = time.Date(input.SlotDate.Year(), input.SlotDate.Month(), input.SlotDate.Day(), 0, 0, 0, 0, time.UTC)
	}
	rawToken, tokenHash, err := randomQuoteToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	var result *CommerceCheckoutQuoteResult
	err = s.write(func(tx *gorm.DB) error {
		if err := s.channelScope(tx, input.TenantID, input.ChannelAccountID); err != nil {
			return err
		}
		location, err := s.locationScope(tx, input.TenantID, input.LocationID, input.BusinessType)
		if err != nil {
			return err
		}
		var config model.CommerceLocationServiceConfig
		if err := tx.Where("tenant_id = ? AND location_id = ? AND business_type = ? AND status = ?", input.TenantID, input.LocationID, input.BusinessType, "active").First(&config).Error; err != nil {
			return err
		}
		if input.BusinessType == "restaurant" && input.FulfillmentMethod == "pickup" && !config.PickupEnabled {
			return fmt.Errorf("%w: pickup is disabled", ErrCommerceQuoteInvalid)
		}
		if input.BusinessType == "restaurant" && input.FulfillmentMethod == "delivery" && !config.DeliveryEnabled {
			return fmt.Errorf("%w: delivery is disabled", ErrCommerceQuoteInvalid)
		}
		if input.BusinessType == "retail" && !config.ShippingEnabled {
			return fmt.Errorf("%w: shipping is disabled", ErrCommerceQuoteInvalid)
		}
		if input.BusinessType == "retail" && (input.ZoneID != 0 || input.SlotID != 0 || !input.SlotDate.IsZero()) {
			return fmt.Errorf("%w: retail shipping does not accept restaurant zone or slot", ErrCommerceQuoteInvalid)
		}
		if input.FulfillmentMethod == "shipping" && (input.Address.Province == "" || input.Address.City == "" || input.Address.District == "" || input.Address.Detail == "") {
			return fmt.Errorf("%w: shipping address is required", ErrCommerceQuoteInvalid)
		}
		if input.FulfillmentMethod == "pickup" && (input.Address.Province != "" || input.Address.City != "" || input.Address.District != "" || input.Address.Detail != "") {
			return fmt.Errorf("%w: pickup does not accept a shipping address", ErrCommerceQuoteInvalid)
		}
		var zone *model.CommerceDeliveryZone
		var slot *model.CommerceDeliverySlot
		if input.BusinessType == "restaurant" && input.FulfillmentMethod == "delivery" {
			if input.ZoneID == 0 || input.SlotID == 0 || input.SlotDate.IsZero() || input.Address.Province == "" || input.Address.City == "" || input.Address.District == "" {
				return fmt.Errorf("%w: delivery address, zone, slot and date are required", ErrCommerceQuoteInvalid)
			}
			var z model.CommerceDeliveryZone
			if err := tx.Where("id = ? AND tenant_id = ? AND location_id = ? AND status = ?", input.ZoneID, input.TenantID, input.LocationID, "active").First(&z).Error; err != nil {
				return err
			}
			if !strings.EqualFold(z.Province, input.Address.Province) || !strings.EqualFold(z.City, input.Address.City) || !strings.EqualFold(z.District, input.Address.District) {
				return fmt.Errorf("%w: address does not match delivery zone", ErrCommerceQuoteInvalid)
			}
			zone = &z
			var sl model.CommerceDeliverySlot
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND location_id = ? AND status = ?", input.SlotID, input.TenantID, input.LocationID, "active").First(&sl).Error; err != nil {
				return err
			}
			if sl.ZoneID != nil && *sl.ZoneID != input.ZoneID {
				return fmt.Errorf("%w: slot does not match delivery zone", ErrCommerceQuoteInvalid)
			}
			localDate := input.SlotDate
			if int(localDate.Weekday()) != sl.DayOfWeek {
				return fmt.Errorf("%w: selected date does not match slot weekday", ErrCommerceQuoteInvalid)
			}
			start := time.Date(localDate.Year(), localDate.Month(), localDate.Day(), 0, 0, 0, 0, now.Location()).Add(time.Duration(sl.StartMinute) * time.Minute)
			if now.After(start.Add(-time.Duration(sl.OrderCutoffMinutes) * time.Minute)) {
				return fmt.Errorf("%w: slot cutoff has passed", ErrCommerceQuoteInvalid)
			}
			// A customer refreshing checkout replaces its prior active hold,
			// regardless of whether the selected slot has a finite capacity.
			// Keep this update in the transaction so a later validation error
			// rolls it back instead of destroying a still-valid quote.
			if err := tx.Model(&model.CommerceCheckoutQuote{}).
				Where("tenant_id = ? AND channel_account_id = ? AND business_type = ? AND customer_hash = ? AND location_id = ? AND status = ?", input.TenantID, input.ChannelAccountID, input.BusinessType, customerHash(input.CustomerID), input.LocationID, "active").
				Update("status", "cancelled").Error; err != nil {
				return err
			}
			if sl.Capacity > 0 {
				// Release only quotes whose hold has actually elapsed. A valid active
				// quote must continue consuming slot capacity until it is consumed or
				// cancelled.
				if err := tx.Model(&model.CommerceCheckoutQuote{}).Where("tenant_id = ? AND location_id = ? AND slot_id = ? AND status = ? AND expires_at <= ?", input.TenantID, input.LocationID, input.SlotID, "active", now).Update("status", "expired").Error; err != nil {
					return err
				}
				var count int64
				if err := tx.Model(&model.CommerceCheckoutQuote{}).
					Where("tenant_id = ? AND location_id = ? AND slot_id = ? AND slot_date = ?", input.TenantID, input.LocationID, input.SlotID, input.SlotDate).
					Where("(status = ? AND expires_at > ?) OR status = ?", "active", now, "consumed").
					Count(&count).Error; err != nil {
					return err
				}
				if count >= int64(sl.Capacity) {
					return fmt.Errorf("%w: delivery slot is full", ErrCommerceQuoteInvalid)
				}
			}
			slot = &sl
		}
		minimum := config.MinGoodsCents
		fee := int64(0)
		shippingFee := int64(0)
		estimated := config.EstimatedMinutes
		if zone != nil {
			fee = zone.FeeCents
			if zone.MinGoodsCentsOverride != nil {
				minimum = *zone.MinGoodsCentsOverride
			}
			if zone.EstimatedMinutes > 0 {
				estimated = zone.EstimatedMinutes
			}
		}
		if input.BusinessType == "retail" {
			if config.FreeShippingThresholdCents == 0 || input.GoodsSubtotalCents < config.FreeShippingThresholdCents {
				shippingFee = config.ShippingFeeCents
			}
		}
		if input.GoodsSubtotalCents < minimum {
			return fmt.Errorf("%w: goods subtotal is below minimum", ErrCommerceQuoteInvalid)
		}
		pack := config.PackagingFeeCents
		total, ok := commerceSafeAddInt64(input.GoodsSubtotalCents, pack)
		if !ok {
			return fmt.Errorf("%w: quote amount overflow", ErrCommerceQuoteInvalid)
		}
		total, ok = commerceSafeAddInt64(total, fee)
		if !ok {
			return fmt.Errorf("%w: quote amount overflow", ErrCommerceQuoteInvalid)
		}
		total, ok = commerceSafeAddInt64(total, shippingFee)
		if !ok {
			return fmt.Errorf("%w: quote amount overflow", ErrCommerceQuoteInvalid)
		}
		addressJSON := ""
		if input.FulfillmentMethod == "delivery" || input.FulfillmentMethod == "shipping" {
			bytes, _ := json.Marshal(input.Address)
			addressJSON = string(bytes)
		}
		expiresAt := now.Add(input.ExpiresIn)
		row := model.CommerceCheckoutQuote{TenantID: input.TenantID, ChannelAccountID: input.ChannelAccountID, BusinessType: input.BusinessType, CustomerHash: customerHash(input.CustomerID), LocationID: location.ID, AddressSnapshotJSON: addressJSON, FulfillmentMethod: input.FulfillmentMethod, GoodsSubtotalCents: input.GoodsSubtotalCents, PackagingFeeCents: pack, DeliveryFeeCents: fee, ShippingFeeCents: shippingFee, DiscountCents: 0, TotalCents: total, Status: "active", TokenHash: tokenHash, ExpiresAt: expiresAt, ConfigVersion: config.ConfigVersion}
		if zone != nil {
			id := zone.ID
			row.ZoneID = &id
		}
		if slot != nil {
			id := slot.ID
			row.SlotID = &id
			date := input.SlotDate
			row.SlotDate = &date
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		result = &CommerceCheckoutQuoteResult{Quote: row, RawToken: rawToken, EstimatedMinutes: estimated}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CommerceDeliveryService) ConsumeQuoteTx(tx *gorm.DB, input CommerceConsumeQuoteInput) (*model.CommerceCheckoutQuote, error) {
	input.BusinessType = strings.TrimSpace(input.BusinessType)
	input.FulfillmentMethod = strings.TrimSpace(input.FulfillmentMethod)
	if tx == nil || input.RawToken == "" || input.OrderID == 0 || input.TenantID == 0 || input.LocationID == 0 || input.ChannelAccountID == 0 || input.CustomerID == "" || !validTenantBusinessType(input.BusinessType) || (input.FulfillmentMethod != "pickup" && input.FulfillmentMethod != "delivery" && input.FulfillmentMethod != "shipping") || input.GoodsSubtotalCents < 0 {
		return nil, fmt.Errorf("%w: incomplete quote consumption scope", ErrCommerceQuoteInvalid)
	}
	addressSnapshot, err := normalizeAddressSnapshot(input.FulfillmentMethod, input.AddressSnapshotJSON)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(input.RawToken))
	tokenHash := hex.EncodeToString(sum[:])
	var quote model.CommerceCheckoutQuote
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("token_hash = ?", tokenHash).First(&quote).Error; err != nil {
		return nil, err
	}
	if quote.TenantID != input.TenantID || quote.ChannelAccountID != input.ChannelAccountID || quote.BusinessType != input.BusinessType || quote.LocationID != input.LocationID || quote.CustomerHash != customerHash(input.CustomerID) {
		return nil, fmt.Errorf("%w: quote ownership mismatch", ErrCommerceQuoteConflict)
	}
	if quote.FulfillmentMethod != input.FulfillmentMethod || quote.GoodsSubtotalCents != input.GoodsSubtotalCents || quote.AddressSnapshotJSON != addressSnapshot {
		return nil, fmt.Errorf("%w: quote checkout facts do not match the order", ErrCommerceQuoteConflict)
	}
	now := s.now()
	if quote.Status == "consumed" {
		if quote.ConsumedOrderID != nil && *quote.ConsumedOrderID == input.OrderID {
			return &quote, nil
		}
		return nil, fmt.Errorf("%w: quote already consumed", ErrCommerceQuoteConflict)
	}
	if quote.Status != "active" {
		return nil, fmt.Errorf("%w: quote is not active", ErrCommerceQuoteConflict)
	}
	if !quote.ExpiresAt.After(now) {
		if err := tx.Model(&quote).Update("status", "expired").Error; err != nil {
			return nil, err
		}
		return nil, ErrCommerceQuoteExpired
	}
	if err := tx.Model(&quote).Updates(map[string]interface{}{"status": "consumed", "consumed_order_id": input.OrderID}).Error; err != nil {
		return nil, err
	}
	quote.Status, quote.ConsumedOrderID = "consumed", &input.OrderID
	return &quote, nil
}

func (s *CommerceDeliveryService) CancelQuote(tenantID, quoteID uint) error {
	if tenantID == 0 || quoteID == 0 {
		return fmt.Errorf("%w: quote identity is required", ErrCommerceQuoteInvalid)
	}
	return s.write(func(tx *gorm.DB) error {
		var quote model.CommerceCheckoutQuote
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", quoteID, tenantID).First(&quote).Error; err != nil {
			return err
		}
		if quote.Status == "active" {
			return tx.Model(&quote).Update("status", "cancelled").Error
		}
		return nil
	})
}

func (s *CommerceDeliveryService) ExpireQuote(tenantID, quoteID uint) error {
	if tenantID == 0 || quoteID == 0 {
		return fmt.Errorf("%w: quote identity is required", ErrCommerceQuoteInvalid)
	}
	return s.db().Model(&model.CommerceCheckoutQuote{}).Where("id = ? AND tenant_id = ? AND status = ? AND expires_at <= ?", quoteID, tenantID, "active", s.now()).Update("status", "expired").Error
}
