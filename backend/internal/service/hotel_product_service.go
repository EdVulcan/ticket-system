package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// HotelProductService owns the sellable accommodation product profile.  The
// backing Product is a stable sales/channel identity only; ticket fulfillment
// remains outside this service.
type HotelProductService struct{}

type HotelProductInput struct {
	Name                     string `json:"name"`
	HotelID                  uint   `json:"hotel_id"`
	RoomTypeID               uint   `json:"room_type_id"`
	RatePlanID               uint   `json:"rate_plan_id"`
	SaleMode                 string `json:"sale_mode"`
	BaseRetailPriceCents     int64  `json:"base_retail_price_cents"`
	BaseSettlementPriceCents int64  `json:"base_settlement_price_cents"`
	Nights                   int    `json:"nights"`
	RoomsPerPackage          int    `json:"rooms_per_package"`
	VoucherValidityDays      int    `json:"voucher_validity_days"`
	MinAdvanceDays           int    `json:"min_advance_days"`
	MaxReschedules           int    `json:"max_reschedules"`
	Status                   string `json:"status"`
}

type HotelProductCalendarPriceInput struct {
	StayDate             string `json:"stay_date"`
	RetailPriceCents     int64  `json:"retail_price_cents"`
	SettlementPriceCents int64  `json:"settlement_price_cents"`
	ClearOverride        bool   `json:"clear_override"`
}

type HotelProductCalendarRow struct {
	StayDate                 string `json:"stay_date"`
	RetailPriceCents         int64  `json:"retail_price_cents"`
	SettlementPriceCents     int64  `json:"settlement_price_cents"`
	BaseRetailPriceCents     int64  `json:"base_retail_price_cents"`
	BaseSettlementPriceCents int64  `json:"base_settlement_price_cents"`
	HasOverride              bool   `json:"has_override"`
	Source                   string `json:"source"`
}

type HotelProductView struct {
	model.HotelProduct
	Product model.Product `json:"product"`
}

func normalizeHotelProductInput(input HotelProductInput) (HotelProductInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.SaleMode = strings.TrimSpace(input.SaleMode)
	input.Status = strings.TrimSpace(input.Status)
	if input.Name == "" || len(input.Name) > 100 {
		return input, errors.New("hotel product name is required")
	}
	if input.HotelID == 0 || input.RoomTypeID == 0 || input.RatePlanID == 0 {
		return input, errors.New("hotel, room type and rate plan are required")
	}
	if input.SaleMode != "calendar_room" && input.SaleMode != "presale_room" {
		return input, errors.New("hotel product sale mode must be calendar_room or presale_room")
	}
	if input.BaseRetailPriceCents <= 0 || input.BaseSettlementPriceCents < 0 || input.BaseSettlementPriceCents > input.BaseRetailPriceCents {
		return input, errors.New("hotel product prices are invalid")
	}
	if input.Nights == 0 {
		input.Nights = 1
	}
	if input.RoomsPerPackage == 0 {
		input.RoomsPerPackage = 1
	}
	// The first accommodation-sales slice deliberately keeps the reservation
	// contract to one room for one night.  The fields are retained in the
	// revision now so a later, explicit multi-night design remains compatible.
	if input.Nights != 1 || input.RoomsPerPackage != 1 {
		return input, errors.New("hotel products currently support one room for one night")
	}
	if input.Status == "" {
		input.Status = "offline"
	}
	if input.Status != "online" && input.Status != "offline" {
		return input, errors.New("hotel product status must be online or offline")
	}
	if input.SaleMode == "presale_room" {
		// Presale rooms require a later booking, cancellation and refund
		// coordinator. That lifecycle is not available for independent hotel
		// products yet, so publishing one would create paid rights with no safe
		// exit path.
		if input.Status == "online" {
			return input, errors.New("presale hotel products cannot be online until the booking and refund lifecycle is enabled")
		}
		if input.VoucherValidityDays < 1 || input.VoucherValidityDays > 730 {
			return input, errors.New("presale hotel product voucher validity must be between 1 and 730 days")
		}
		if input.MinAdvanceDays < 0 || input.MinAdvanceDays > 365 {
			return input, errors.New("presale hotel product minimum advance must be between 0 and 365 days")
		}
		if input.MaxReschedules < 0 || input.MaxReschedules > 20 {
			return input, errors.New("presale hotel product maximum reschedules must be between 0 and 20")
		}
	} else {
		input.VoucherValidityDays = 0
		input.MinAdvanceDays = 0
		input.MaxReschedules = 0
	}
	return input, nil
}

func hotelProductPrice(cents int64) float64 {
	return float64(cents) / 100
}

func requireHotelProductResourcesTx(tx *gorm.DB, tenantID, hotelID, roomTypeID, ratePlanID uint) error {
	if err := requireHotelRoomTypeTx(tx, tenantID, hotelID, roomTypeID); err != nil {
		return err
	}
	var count int64
	if err := tx.Model(&model.HotelRatePlan{}).
		Where("id = ? AND tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND status = ?", ratePlanID, tenantID, hotelID, roomTypeID, "active").
		Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return errors.New("active hotel rate plan not found")
	}
	return nil
}

func hotelProductViewTx(tx *gorm.DB, tenantID, hotelProductID uint) (*HotelProductView, error) {
	var hotelProduct model.HotelProduct
	if err := tx.Where("id = ? AND tenant_id = ?", hotelProductID, tenantID).First(&hotelProduct).Error; err != nil {
		return nil, err
	}
	var product model.Product
	if err := tx.Where("id = ? AND tenant_id = ? AND product_kind = ?", hotelProduct.ProductID, tenantID, "hotel").First(&product).Error; err != nil {
		return nil, err
	}
	return &HotelProductView{HotelProduct: hotelProduct, Product: product}, nil
}

func (s *HotelProductService) List(tenantID uint) ([]HotelProductView, error) {
	var hotelProducts []model.HotelProduct
	if err := model.DB.Where("tenant_id = ?", tenantID).Order("created_at ASC").Find(&hotelProducts).Error; err != nil {
		return nil, err
	}
	if len(hotelProducts) == 0 {
		return []HotelProductView{}, nil
	}
	productIDs := make([]uint, 0, len(hotelProducts))
	for _, hotelProduct := range hotelProducts {
		productIDs = append(productIDs, hotelProduct.ProductID)
	}
	var products []model.Product
	if err := model.DB.Where("tenant_id = ? AND product_kind = ? AND id IN ?", tenantID, "hotel", productIDs).Find(&products).Error; err != nil {
		return nil, err
	}
	productsByID := make(map[uint]model.Product, len(products))
	for _, product := range products {
		productsByID[product.ID] = product
	}
	rows := make([]HotelProductView, 0, len(hotelProducts))
	for _, hotelProduct := range hotelProducts {
		product, ok := productsByID[hotelProduct.ProductID]
		if !ok {
			return nil, errors.New("hotel product sales identity is unavailable")
		}
		rows = append(rows, HotelProductView{HotelProduct: hotelProduct, Product: product})
	}
	return rows, nil
}

func (s *HotelProductService) Create(tenantID, operatorID uint, input HotelProductInput) (*HotelProductView, error) {
	input, err := normalizeHotelProductInput(input)
	if err != nil {
		return nil, err
	}
	var result *HotelProductView
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		if err := requireHotelProductResourcesTx(tx, tenantID, input.HotelID, input.RoomTypeID, input.RatePlanID); err != nil {
			return err
		}
		product := model.Product{
			// An online hotel product requires a current revision at the database
			// boundary. Create the identity and draft profile offline, then publish
			// both only after the first immutable revision exists.
			Name: input.Name, TenantID: tenantID, ProductKind: "hotel", Type: "online", Status: "offline",
			Price: hotelProductPrice(input.BaseRetailPriceCents), SettlementPrice: hotelProductPrice(input.BaseSettlementPriceCents),
		}
		if err := tx.Create(&product).Error; err != nil {
			return err
		}
		hotelProduct := model.HotelProduct{
			ProductID: product.ID, TenantID: tenantID, HotelID: input.HotelID, RoomTypeID: input.RoomTypeID, RatePlanID: input.RatePlanID,
			SaleMode: input.SaleMode, BaseRetailPriceCents: input.BaseRetailPriceCents, BaseSettlementPriceCents: input.BaseSettlementPriceCents,
			Nights: input.Nights, RoomsPerPackage: input.RoomsPerPackage, VoucherValidityDays: input.VoucherValidityDays, MinAdvanceDays: input.MinAdvanceDays, MaxReschedules: input.MaxReschedules, Status: "offline",
		}
		if err := tx.Create(&hotelProduct).Error; err != nil {
			return err
		}
		revision := model.HotelProductRevision{
			HotelProductID: hotelProduct.ID, TenantID: tenantID, ProductID: product.ID, Version: 1,
			HotelID: input.HotelID, RoomTypeID: input.RoomTypeID, RatePlanID: input.RatePlanID, SaleMode: input.SaleMode,
			BaseRetailPriceCents: input.BaseRetailPriceCents, BaseSettlementPriceCents: input.BaseSettlementPriceCents,
			Nights: input.Nights, RoomsPerPackage: input.RoomsPerPackage, VoucherValidityDays: input.VoucherValidityDays, MinAdvanceDays: input.MinAdvanceDays, MaxReschedules: input.MaxReschedules,
		}
		if err := tx.Create(&revision).Error; err != nil {
			return err
		}
		if err := tx.Model(&hotelProduct).Updates(map[string]interface{}{"current_revision_id": revision.ID, "status": input.Status}).Error; err != nil {
			return err
		}
		hotelProduct.CurrentRevisionID = revision.ID
		hotelProduct.Status = input.Status
		if err := tx.Model(&product).Update("status", input.Status).Error; err != nil {
			return err
		}
		product.Status = input.Status
		if err := recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.product.create", "hotel_product", hotelProduct.ID, "create hotel product", "{}", fmt.Sprintf(`{"product_id":%d,"sale_mode":%q}`, product.ID, hotelProduct.SaleMode)); err != nil {
			return err
		}
		result = &HotelProductView{HotelProduct: hotelProduct, Product: product}
		return nil
	})
	return result, err
}

func (s *HotelProductService) Update(tenantID, hotelProductID, operatorID uint, input HotelProductInput) error {
	input, err := normalizeHotelProductInput(input)
	if err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		var hotelProduct model.HotelProduct
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", hotelProductID, tenantID).First(&hotelProduct).Error; err != nil {
			return err
		}
		if err := requireHotelProductResourcesTx(tx, tenantID, input.HotelID, input.RoomTypeID, input.RatePlanID); err != nil {
			return err
		}
		var product model.Product
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND product_kind = ?", hotelProduct.ProductID, tenantID, "hotel").First(&product).Error; err != nil {
			return err
		}
		keyChanged := hotelProduct.HotelID != input.HotelID || hotelProduct.RoomTypeID != input.RoomTypeID || hotelProduct.RatePlanID != input.RatePlanID ||
			hotelProduct.SaleMode != input.SaleMode || hotelProduct.BaseRetailPriceCents != input.BaseRetailPriceCents || hotelProduct.BaseSettlementPriceCents != input.BaseSettlementPriceCents ||
			hotelProduct.Nights != input.Nights || hotelProduct.RoomsPerPackage != input.RoomsPerPackage || hotelProduct.VoucherValidityDays != input.VoucherValidityDays ||
			hotelProduct.MinAdvanceDays != input.MinAdvanceDays || hotelProduct.MaxReschedules != input.MaxReschedules
		before := fmt.Sprintf(`{"name":%q,"status":%q,"revision_id":%d}`, product.Name, hotelProduct.Status, hotelProduct.CurrentRevisionID)
		if keyChanged {
			if hotelProduct.CurrentRevisionID == 0 {
				return errors.New("hotel product has no current revision")
			}
			var current model.HotelProductRevision
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND hotel_product_id = ? AND tenant_id = ?", hotelProduct.CurrentRevisionID, hotelProduct.ID, tenantID).First(&current).Error; err != nil {
				return err
			}
			next := model.HotelProductRevision{
				HotelProductID: hotelProduct.ID, TenantID: tenantID, ProductID: hotelProduct.ProductID, Version: current.Version + 1,
				HotelID: input.HotelID, RoomTypeID: input.RoomTypeID, RatePlanID: input.RatePlanID, SaleMode: input.SaleMode,
				BaseRetailPriceCents: input.BaseRetailPriceCents, BaseSettlementPriceCents: input.BaseSettlementPriceCents,
				Nights: input.Nights, RoomsPerPackage: input.RoomsPerPackage, VoucherValidityDays: input.VoucherValidityDays, MinAdvanceDays: input.MinAdvanceDays, MaxReschedules: input.MaxReschedules,
			}
			if err := tx.Create(&next).Error; err != nil {
				return err
			}
			// Calendar overrides describe the product's sale price, not its hotel
			// resource price. Preserve them across a product revision unless the
			// product is switched to presale mode, where they are forbidden.
			if input.SaleMode == "calendar_room" && hotelProduct.SaleMode == "calendar_room" {
				var overrides []model.HotelProductCalendarPrice
				if err := tx.Where("tenant_id = ? AND hotel_product_id = ? AND hotel_product_revision_id = ?", tenantID, hotelProduct.ID, current.ID).Find(&overrides).Error; err != nil {
					return err
				}
				for _, override := range overrides {
					clone := model.HotelProductCalendarPrice{TenantID: tenantID, HotelProductID: hotelProduct.ID, HotelProductRevisionID: next.ID, StayDate: override.StayDate, RetailPriceCents: override.RetailPriceCents, SettlementPriceCents: override.SettlementPriceCents}
					if err := tx.Create(&clone).Error; err != nil {
						return err
					}
				}
			}
			hotelProduct.CurrentRevisionID = next.ID
		}
		if err := tx.Model(&hotelProduct).Updates(map[string]interface{}{
			"hotel_id": input.HotelID, "room_type_id": input.RoomTypeID, "rate_plan_id": input.RatePlanID, "sale_mode": input.SaleMode,
			"base_retail_price_cents": input.BaseRetailPriceCents, "base_settlement_price_cents": input.BaseSettlementPriceCents,
			"nights": input.Nights, "rooms_per_package": input.RoomsPerPackage, "voucher_validity_days": input.VoucherValidityDays,
			"min_advance_days": input.MinAdvanceDays, "max_reschedules": input.MaxReschedules, "status": input.Status, "current_revision_id": hotelProduct.CurrentRevisionID,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&product).Updates(map[string]interface{}{"name": input.Name, "status": input.Status, "price": hotelProductPrice(input.BaseRetailPriceCents), "settlement_price": hotelProductPrice(input.BaseSettlementPriceCents)}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.product.update", "hotel_product", hotelProduct.ID, "update hotel product", before, fmt.Sprintf(`{"name":%q,"status":%q,"revision_id":%d}`, input.Name, input.Status, hotelProduct.CurrentRevisionID))
	})
}

func (s *HotelProductService) Delete(tenantID, hotelProductID, operatorID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		var hotelProduct model.HotelProduct
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", hotelProductID, tenantID).First(&hotelProduct).Error; err != nil {
			return err
		}
		for _, dependency := range []struct {
			model interface{}
			where string
			args  []interface{}
			name  string
		}{
			{&model.ChannelProductMapping{}, "product_id = ?", []interface{}{hotelProduct.ProductID}, "channel mappings"},
			{&model.OrderItem{}, "product_id = ?", []interface{}{hotelProduct.ProductID}, "sales records"},
		} {
			var count int64
			if err := tx.Model(dependency.model).Where(dependency.where, dependency.args...).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return fmt.Errorf("hotel product has %s and cannot be deleted; take it offline instead", dependency.name)
			}
		}
		if err := tx.Unscoped().Where("tenant_id = ? AND hotel_product_id = ?", tenantID, hotelProduct.ID).Delete(&model.HotelProductCalendarPrice{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("tenant_id = ? AND hotel_product_id = ?", tenantID, hotelProduct.ID).Delete(&model.HotelProductRevision{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&hotelProduct).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.Product{}, hotelProduct.ProductID).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.product.delete", "hotel_product", hotelProduct.ID, "delete hotel product", fmt.Sprintf(`{"product_id":%d}`, hotelProduct.ProductID), "{}")
	})
}

func (s *HotelProductService) ListCalendar(tenantID, hotelProductID uint, startDate, endDate string) ([]HotelProductCalendarRow, error) {
	start, end, err := parseHotelDateRange(startDate, endDate, "hotel product calendar")
	if err != nil {
		return nil, err
	}
	var hotelProduct model.HotelProduct
	if err := model.DB.Where("id = ? AND tenant_id = ?", hotelProductID, tenantID).First(&hotelProduct).Error; err != nil {
		return nil, err
	}
	if hotelProduct.SaleMode != "calendar_room" {
		return nil, errors.New("presale room products do not support a price calendar")
	}
	var revision model.HotelProductRevision
	if err := model.DB.Where("id = ? AND hotel_product_id = ? AND tenant_id = ?", hotelProduct.CurrentRevisionID, hotelProduct.ID, tenantID).First(&revision).Error; err != nil {
		return nil, err
	}
	var overrides []model.HotelProductCalendarPrice
	if err := model.DB.Where("tenant_id = ? AND hotel_product_id = ? AND hotel_product_revision_id = ? AND stay_date BETWEEN ? AND ?", tenantID, hotelProduct.ID, revision.ID, start, end).Find(&overrides).Error; err != nil {
		return nil, err
	}
	byDate := make(map[string]model.HotelProductCalendarPrice, len(overrides))
	for _, override := range overrides {
		byDate[override.StayDate.Format("2006-01-02")] = override
	}
	rows := make([]HotelProductCalendarRow, 0, int(end.Sub(start)/(24*time.Hour))+1)
	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		row := HotelProductCalendarRow{StayDate: date.Format("2006-01-02"), RetailPriceCents: revision.BaseRetailPriceCents, SettlementPriceCents: revision.BaseSettlementPriceCents, BaseRetailPriceCents: revision.BaseRetailPriceCents, BaseSettlementPriceCents: revision.BaseSettlementPriceCents, Source: "base"}
		if override, ok := byDate[row.StayDate]; ok {
			row.RetailPriceCents = override.RetailPriceCents
			row.SettlementPriceCents = override.SettlementPriceCents
			row.HasOverride = true
			row.Source = "override"
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *HotelProductService) SetCalendar(tenantID, hotelProductID, operatorID uint, inputs []HotelProductCalendarPriceInput) error {
	return model.Write(func(tx *gorm.DB) error {
		return s.setCalendarTx(tx, tenantID, hotelProductID, operatorID, inputs)
	})
}

// setCalendarTx is the transaction-owned mutation seam shared by the admin
// API and Agent confirmation. It keeps current-revision locking and the
// presale-room rejection in one authoritative implementation.
func (s *HotelProductService) setCalendarTx(tx *gorm.DB, tenantID, hotelProductID, operatorID uint, inputs []HotelProductCalendarPriceInput) error {
	if len(inputs) == 0 || len(inputs) > 93 {
		return errors.New("hotel product calendar update must contain between 1 and 93 dates")
	}
	type parsedInput struct {
		date                 time.Time
		retailPriceCents     int64
		settlementPriceCents int64
		clearOverride        bool
	}
	parsed := make(map[string]parsedInput, len(inputs))
	var minDate, maxDate time.Time
	for _, input := range inputs {
		date, err := time.Parse("2006-01-02", strings.TrimSpace(input.StayDate))
		if err != nil {
			return errors.New("hotel product calendar stay date must use YYYY-MM-DD")
		}
		key := date.Format("2006-01-02")
		if _, exists := parsed[key]; exists {
			return errors.New("hotel product calendar contains duplicate dates")
		}
		if !input.ClearOverride && (input.RetailPriceCents <= 0 || input.SettlementPriceCents < 0 || input.SettlementPriceCents > input.RetailPriceCents) {
			return errors.New("hotel product calendar prices are invalid")
		}
		if minDate.IsZero() || date.Before(minDate) {
			minDate = date
		}
		if maxDate.IsZero() || date.After(maxDate) {
			maxDate = date
		}
		parsed[key] = parsedInput{date: date, retailPriceCents: input.RetailPriceCents, settlementPriceCents: input.SettlementPriceCents, clearOverride: input.ClearOverride}
	}
	if maxDate.Sub(minDate) > 92*24*time.Hour {
		return errors.New("hotel product calendar date range must be between 1 and 93 days")
	}
	if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
		return err
	}
	var hotelProduct model.HotelProduct
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", hotelProductID, tenantID).First(&hotelProduct).Error; err != nil {
		return err
	}
	if hotelProduct.SaleMode != "calendar_room" {
		return errors.New("presale room products do not support a price calendar")
	}
	if err := requireHotelProductResourcesTx(tx, tenantID, hotelProduct.HotelID, hotelProduct.RoomTypeID, hotelProduct.RatePlanID); err != nil {
		return err
	}
	if hotelProduct.CurrentRevisionID == 0 {
		return errors.New("hotel product has no current revision")
	}
	for _, input := range parsed {
		if input.clearOverride {
			if err := tx.Unscoped().Where("tenant_id = ? AND hotel_product_id = ? AND hotel_product_revision_id = ? AND stay_date = ?", tenantID, hotelProduct.ID, hotelProduct.CurrentRevisionID, input.date).Delete(&model.HotelProductCalendarPrice{}).Error; err != nil {
				return err
			}
			continue
		}
		var row model.HotelProductCalendarPrice
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND hotel_product_id = ? AND hotel_product_revision_id = ? AND stay_date = ?", tenantID, hotelProduct.ID, hotelProduct.CurrentRevisionID, input.date).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row = model.HotelProductCalendarPrice{TenantID: tenantID, HotelProductID: hotelProduct.ID, HotelProductRevisionID: hotelProduct.CurrentRevisionID, StayDate: input.date, RetailPriceCents: input.retailPriceCents, SettlementPriceCents: input.settlementPriceCents}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]interface{}{"retail_price_cents": input.retailPriceCents, "settlement_price_cents": input.settlementPriceCents}).Error; err != nil {
			return err
		}
	}
	return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.product_calendar.update", "hotel_product", hotelProduct.ID, "update hotel product stay-date prices", "{}", fmt.Sprintf(`{"dates":%d}`, len(parsed)))
}
