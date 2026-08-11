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

type HotelService struct{}

type HotelPropertyInput struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	Address      string `json:"address"`
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
	CheckInTime  string `json:"check_in_time"`
	CheckOutTime string `json:"check_out_time"`
	Status       string `json:"status"`
}

type HotelRoomTypeInput struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	MaxGuests   int    `json:"max_guests"`
	BedType     string `json:"bed_type"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type HotelRatePlanInput struct {
	Code                 string `json:"code"`
	Name                 string `json:"name"`
	RetailPriceCents     int64  `json:"retail_price_cents"`
	SettlementPriceCents int64  `json:"settlement_price_cents"`
	BreakfastCount       int    `json:"breakfast_count"`
	CancellationPolicy   string `json:"cancellation_policy"`
	Status               string `json:"status"`
}

type HotelInventoryInput struct {
	StayDate string `json:"stay_date"`
	Capacity int    `json:"capacity"`
	Closed   bool   `json:"closed"`
}

func normalizeHotelStatus(status string) (string, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		status = "active"
	}
	if status != "active" && status != "suspended" {
		return "", errors.New("hotel resource status must be active or suspended")
	}
	return status, nil
}

func normalizeClock(value, fallback string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if _, err := time.Parse("15:04", value); err != nil {
		return "", errors.New("hotel check-in and check-out time must use HH:mm")
	}
	return value, nil
}

func normalizeHotelPropertyInput(input HotelPropertyInput) (HotelPropertyInput, error) {
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	input.Address = strings.TrimSpace(input.Address)
	input.ContactName = strings.TrimSpace(input.ContactName)
	input.ContactPhone = strings.TrimSpace(input.ContactPhone)
	if input.Code == "" || len(input.Code) > 50 || input.Name == "" || len(input.Name) > 120 {
		return input, errors.New("hotel code and name are required")
	}
	var err error
	if input.CheckInTime, err = normalizeClock(input.CheckInTime, "14:00"); err != nil {
		return input, err
	}
	if input.CheckOutTime, err = normalizeClock(input.CheckOutTime, "12:00"); err != nil {
		return input, err
	}
	if input.Status, err = normalizeHotelStatus(input.Status); err != nil {
		return input, err
	}
	return input, nil
}

func normalizeRoomTypeInput(input HotelRoomTypeInput) (HotelRoomTypeInput, error) {
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	input.BedType = strings.TrimSpace(input.BedType)
	input.Description = strings.TrimSpace(input.Description)
	if input.Code == "" || len(input.Code) > 50 || input.Name == "" || len(input.Name) > 100 {
		return input, errors.New("room type code and name are required")
	}
	if input.MaxGuests < 1 || input.MaxGuests > 20 {
		return input, errors.New("room type maximum guests must be between 1 and 20")
	}
	var err error
	input.Status, err = normalizeHotelStatus(input.Status)
	return input, err
}

func normalizeRatePlanInput(input HotelRatePlanInput) (HotelRatePlanInput, error) {
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	input.CancellationPolicy = strings.TrimSpace(input.CancellationPolicy)
	if input.Code == "" || len(input.Code) > 50 || input.Name == "" || len(input.Name) > 100 {
		return input, errors.New("rate plan code and name are required")
	}
	if input.RetailPriceCents <= 0 || input.SettlementPriceCents < 0 || input.SettlementPriceCents > input.RetailPriceCents {
		return input, errors.New("rate plan prices are invalid")
	}
	if input.BreakfastCount < 0 || input.BreakfastCount > 20 {
		return input, errors.New("breakfast count must be between 0 and 20")
	}
	var err error
	input.Status, err = normalizeHotelStatus(input.Status)
	return input, err
}

func (s *HotelService) ListProperties(tenantID uint) ([]model.HotelProperty, error) {
	var rows []model.HotelProperty
	err := model.DB.Where("tenant_id = ?", tenantID).Order("created_at ASC").Find(&rows).Error
	return rows, err
}

func (s *HotelService) CreateProperty(tenantID, operatorID uint, input HotelPropertyInput) (*model.HotelProperty, error) {
	input, err := normalizeHotelPropertyInput(input)
	if err != nil {
		return nil, err
	}
	row := &model.HotelProperty{TenantID: tenantID, Code: input.Code, Name: input.Name, Address: input.Address, ContactName: input.ContactName, ContactPhone: input.ContactPhone, CheckInTime: input.CheckInTime, CheckOutTime: input.CheckOutTime, Status: input.Status}
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.property.create", "hotel_property", row.ID, "create hotel property", "{}", fmt.Sprintf(`{"code":%q,"name":%q}`, row.Code, row.Name))
	})
	return row, err
}

func (s *HotelService) UpdateProperty(tenantID, hotelID, operatorID uint, input HotelPropertyInput) error {
	input, err := normalizeHotelPropertyInput(input)
	if err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		var row model.HotelProperty
		if err := tx.Where("id = ? AND tenant_id = ?", hotelID, tenantID).First(&row).Error; err != nil {
			return err
		}
		before := fmt.Sprintf(`{"name":%q,"status":%q}`, row.Name, row.Status)
		if err := tx.Model(&row).Updates(map[string]interface{}{"name": input.Name, "address": input.Address, "contact_name": input.ContactName, "contact_phone": input.ContactPhone, "check_in_time": input.CheckInTime, "check_out_time": input.CheckOutTime, "status": input.Status}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.property.update", "hotel_property", row.ID, "update hotel property", before, fmt.Sprintf(`{"name":%q,"status":%q}`, input.Name, input.Status))
	})
}

func (s *HotelService) DeleteProperty(tenantID, hotelID, operatorID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		var row model.HotelProperty
		if err := tx.Where("id = ? AND tenant_id = ?", hotelID, tenantID).First(&row).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.HotelRoomType{}).Where("tenant_id = ? AND hotel_id = ?", tenantID, hotelID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("hotel has room types and cannot be deleted; suspend it instead")
		}
		if err := tx.Delete(&row).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.property.delete", "hotel_property", row.ID, "delete hotel property", fmt.Sprintf(`{"code":%q,"name":%q}`, row.Code, row.Name), "{}")
	})
}

func (s *HotelService) ListRoomTypes(tenantID, hotelID uint) ([]model.HotelRoomType, error) {
	var rows []model.HotelRoomType
	err := model.DB.Where("tenant_id = ? AND hotel_id = ?", tenantID, hotelID).Order("created_at ASC").Find(&rows).Error
	return rows, err
}

func (s *HotelService) CreateRoomType(tenantID, hotelID, operatorID uint, input HotelRoomTypeInput) (*model.HotelRoomType, error) {
	input, err := normalizeRoomTypeInput(input)
	if err != nil {
		return nil, err
	}
	row := &model.HotelRoomType{TenantID: tenantID, HotelID: hotelID, Code: input.Code, Name: input.Name, MaxGuests: input.MaxGuests, BedType: input.BedType, Description: input.Description, Status: input.Status}
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		if err := requireHotelPropertyTx(tx, tenantID, hotelID); err != nil {
			return err
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.room_type.create", "hotel_room_type", row.ID, "create hotel room type", "{}", fmt.Sprintf(`{"hotel_id":%d,"code":%q}`, hotelID, row.Code))
	})
	return row, err
}

func (s *HotelService) UpdateRoomType(tenantID, hotelID, roomTypeID, operatorID uint, input HotelRoomTypeInput) error {
	input, err := normalizeRoomTypeInput(input)
	if err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		var row model.HotelRoomType
		if err := tx.Where("id = ? AND tenant_id = ? AND hotel_id = ?", roomTypeID, tenantID, hotelID).First(&row).Error; err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]interface{}{"name": input.Name, "max_guests": input.MaxGuests, "bed_type": input.BedType, "description": input.Description, "status": input.Status}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.room_type.update", "hotel_room_type", row.ID, "update hotel room type", "{}", fmt.Sprintf(`{"name":%q,"status":%q}`, input.Name, input.Status))
	})
}

func (s *HotelService) DeleteRoomType(tenantID, hotelID, roomTypeID, operatorID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		var row model.HotelRoomType
		if err := tx.Where("id = ? AND tenant_id = ? AND hotel_id = ?", roomTypeID, tenantID, hotelID).First(&row).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.HotelRatePlan{}).Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ?", tenantID, hotelID, roomTypeID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("room type has rate plans and cannot be deleted; suspend it instead")
		}
		if err := tx.Model(&model.HotelRoomInventory{}).Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ?", tenantID, hotelID, roomTypeID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("room type has inventory and cannot be deleted; suspend it instead")
		}
		if err := tx.Delete(&row).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.room_type.delete", "hotel_room_type", row.ID, "delete hotel room type", fmt.Sprintf(`{"code":%q}`, row.Code), "{}")
	})
}

func (s *HotelService) ListRatePlans(tenantID, hotelID, roomTypeID uint) ([]model.HotelRatePlan, error) {
	var rows []model.HotelRatePlan
	err := model.DB.Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ?", tenantID, hotelID, roomTypeID).Order("created_at ASC").Find(&rows).Error
	return rows, err
}

func (s *HotelService) CreateRatePlan(tenantID, hotelID, roomTypeID, operatorID uint, input HotelRatePlanInput) (*model.HotelRatePlan, error) {
	input, err := normalizeRatePlanInput(input)
	if err != nil {
		return nil, err
	}
	row := &model.HotelRatePlan{TenantID: tenantID, HotelID: hotelID, RoomTypeID: roomTypeID, Code: input.Code, Name: input.Name, RetailPriceCents: input.RetailPriceCents, SettlementPriceCents: input.SettlementPriceCents, BreakfastCount: input.BreakfastCount, CancellationPolicy: input.CancellationPolicy, Status: input.Status}
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		if err := requireHotelRoomTypeTx(tx, tenantID, hotelID, roomTypeID); err != nil {
			return err
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.rate_plan.create", "hotel_rate_plan", row.ID, "create hotel rate plan", "{}", fmt.Sprintf(`{"room_type_id":%d,"code":%q}`, roomTypeID, row.Code))
	})
	return row, err
}

func (s *HotelService) UpdateRatePlan(tenantID, hotelID, roomTypeID, ratePlanID, operatorID uint, input HotelRatePlanInput) error {
	input, err := normalizeRatePlanInput(input)
	if err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		var row model.HotelRatePlan
		if err := tx.Where("id = ? AND tenant_id = ? AND hotel_id = ? AND room_type_id = ?", ratePlanID, tenantID, hotelID, roomTypeID).First(&row).Error; err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]interface{}{"name": input.Name, "retail_price_cents": input.RetailPriceCents, "settlement_price_cents": input.SettlementPriceCents, "breakfast_count": input.BreakfastCount, "cancellation_policy": input.CancellationPolicy, "status": input.Status}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.rate_plan.update", "hotel_rate_plan", row.ID, "update hotel rate plan", "{}", fmt.Sprintf(`{"name":%q,"status":%q}`, input.Name, input.Status))
	})
}

func (s *HotelService) DeleteRatePlan(tenantID, hotelID, roomTypeID, ratePlanID, operatorID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		var row model.HotelRatePlan
		if err := tx.Where("id = ? AND tenant_id = ? AND hotel_id = ? AND room_type_id = ?", ratePlanID, tenantID, hotelID, roomTypeID).First(&row).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.ScenicHotelPackage{}).Where("tenant_id = ? AND rate_plan_id = ?", tenantID, row.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("rate plan is used by a scenic hotel package; remove the package first")
		}
		if err := tx.Delete(&row).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.rate_plan.delete", "hotel_rate_plan", row.ID, "delete hotel rate plan", fmt.Sprintf(`{"code":%q}`, row.Code), "{}")
	})
}

func (s *HotelService) ListInventory(tenantID, hotelID, roomTypeID uint, startDate, endDate string) ([]model.HotelRoomInventory, error) {
	start, end, err := parseHotelInventoryRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	var rows []model.HotelRoomInventory
	err = model.DB.Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND stay_date BETWEEN ? AND ?", tenantID, hotelID, roomTypeID, start, end).Order("stay_date ASC").Find(&rows).Error
	return rows, err
}

func (s *HotelService) SetInventory(tenantID, hotelID, roomTypeID, operatorID uint, inputs []HotelInventoryInput) error {
	if len(inputs) == 0 || len(inputs) > 93 {
		return errors.New("hotel inventory update must contain between 1 and 93 dates")
	}
	parsed := make(map[string]struct {
		date     time.Time
		capacity int
		closed   bool
	}, len(inputs))
	for _, input := range inputs {
		date, err := time.Parse("2006-01-02", strings.TrimSpace(input.StayDate))
		if err != nil {
			return errors.New("hotel inventory stay date must use YYYY-MM-DD")
		}
		if input.Capacity < 0 || input.Capacity > 100000 {
			return errors.New("hotel inventory capacity is invalid")
		}
		key := date.Format("2006-01-02")
		if _, duplicate := parsed[key]; duplicate {
			return errors.New("hotel inventory contains duplicate dates")
		}
		parsed[key] = struct {
			date     time.Time
			capacity int
			closed   bool
		}{date, input.Capacity, input.Closed}
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		if err := requireHotelRoomTypeTx(tx, tenantID, hotelID, roomTypeID); err != nil {
			return err
		}
		for _, input := range parsed {
			var row model.HotelRoomInventory
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND stay_date = ?", tenantID, hotelID, roomTypeID, input.date).First(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				row = model.HotelRoomInventory{TenantID: tenantID, HotelID: hotelID, RoomTypeID: roomTypeID, StayDate: input.date, Capacity: input.capacity, Closed: input.closed}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			if input.capacity < row.Reserved+row.Sold {
				return fmt.Errorf("hotel inventory for %s cannot be lower than reserved and sold rooms", input.date.Format("2006-01-02"))
			}
			if err := tx.Model(&row).Updates(map[string]interface{}{"capacity": input.capacity, "closed": input.closed}).Error; err != nil {
				return err
			}
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "hotel.inventory.update", "hotel_room_type", roomTypeID, "update hotel room inventory", "{}", fmt.Sprintf(`{"dates":%d}`, len(parsed)))
	})
}

func requireHotelPropertyTx(tx *gorm.DB, tenantID, hotelID uint) error {
	var count int64
	if err := tx.Model(&model.HotelProperty{}).Where("id = ? AND tenant_id = ? AND status = ?", hotelID, tenantID, "active").Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return errors.New("active hotel property not found")
	}
	return nil
}

func requireHotelRoomTypeTx(tx *gorm.DB, tenantID, hotelID, roomTypeID uint) error {
	if err := requireHotelPropertyTx(tx, tenantID, hotelID); err != nil {
		return err
	}
	var count int64
	if err := tx.Model(&model.HotelRoomType{}).Where("id = ? AND tenant_id = ? AND hotel_id = ? AND status = ?", roomTypeID, tenantID, hotelID, "active").Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return errors.New("active hotel room type not found")
	}
	return nil
}

func parseHotelInventoryRange(startDate, endDate string) (time.Time, time.Time, error) {
	start, err := time.Parse("2006-01-02", strings.TrimSpace(startDate))
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("inventory start date must use YYYY-MM-DD")
	}
	end, err := time.Parse("2006-01-02", strings.TrimSpace(endDate))
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("inventory end date must use YYYY-MM-DD")
	}
	if end.Before(start) || end.Sub(start) > 92*24*time.Hour {
		return time.Time{}, time.Time{}, errors.New("inventory date range must be between 1 and 93 days")
	}
	return start, end, nil
}
