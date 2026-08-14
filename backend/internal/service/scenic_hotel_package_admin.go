package service

import (
	"errors"
	"fmt"
	"strings"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func normalizeScenicHotelPackageInput(input ScenicHotelPackageInput) (ScenicHotelPackageInput, error) {
	if input.ProductID == 0 || input.HotelID == 0 || input.RoomTypeID == 0 || input.RatePlanID == 0 {
		return input, errors.New("ticket product, hotel, room type and rate plan are required")
	}
	if input.Nights < 1 || input.Nights > 30 {
		return input, errors.New("package nights must be between 1 and 30")
	}
	if input.RoomsPerPackage < 1 || input.RoomsPerPackage > 10 {
		return input, errors.New("package rooms must be between 1 and 10")
	}
	if input.HotelSettlementPriceCents < 0 {
		return input, errors.New("hotel settlement price cannot be negative")
	}
	input.BookingMode = strings.TrimSpace(input.BookingMode)
	if input.BookingMode == "" {
		input.BookingMode = "at_purchase"
	}
	if input.BookingMode != "at_purchase" && input.BookingMode != "after_purchase" {
		return input, errors.New("package booking mode must be at_purchase or after_purchase")
	}
	if input.BookingMode == "after_purchase" {
		if input.VoucherValidityDays < 1 || input.VoucherValidityDays > 730 {
			return input, errors.New("voucher validity days must be between 1 and 730")
		}
		if input.MinAdvanceDays < 0 || input.MinAdvanceDays > 365 {
			return input, errors.New("minimum advance days must be between 0 and 365")
		}
		if input.MaxReschedules < 0 || input.MaxReschedules > 20 {
			return input, errors.New("maximum reschedules must be between 0 and 20")
		}
	} else {
		input.VoucherValidityDays, input.MinAdvanceDays, input.MaxReschedules = 0, 0, 0
	}
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "offline"
	}
	if input.Status != "online" && input.Status != "offline" {
		return input, errors.New("package status must be online or offline")
	}
	return input, nil
}

func (s *ScenicHotelPackageService) Create(tenantID, operatorID uint, input ScenicHotelPackageInput) (*ScenicHotelPackageView, error) {
	input, err := normalizeScenicHotelPackageInput(input)
	if err != nil {
		return nil, err
	}
	row := model.ScenicHotelPackage{TenantID: tenantID, ProductID: input.ProductID, HotelID: input.HotelID, RoomTypeID: input.RoomTypeID, RatePlanID: input.RatePlanID, Nights: input.Nights, RoomsPerPackage: input.RoomsPerPackage, HotelSettlementPriceCents: input.HotelSettlementPriceCents, BookingMode: input.BookingMode, VoucherValidityDays: input.VoucherValidityDays, MinAdvanceDays: input.MinAdvanceDays, MaxReschedules: input.MaxReschedules, Status: input.Status}
	err = model.Write(func(tx *gorm.DB) error {
		if err := requireActiveScenicSupplier(tx, tenantID); err != nil {
			return err
		}
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		if _, err := validateScenicHotelPackageFactsTx(tx, tenantID, &row, input.Status == "online"); err != nil {
			return err
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "scenic_hotel_package.create", "scenic_hotel_package", row.ID, "create fixed scenic hotel package", "{}", fmt.Sprintf(`{"product_id":%d,"rate_plan_id":%d}`, row.ProductID, row.RatePlanID))
	})
	if err != nil {
		return nil, err
	}
	return s.Get(tenantID, row.ID)
}

func (s *ScenicHotelPackageService) Update(tenantID, packageID, operatorID uint, input ScenicHotelPackageInput) error {
	input, err := normalizeScenicHotelPackageInput(input)
	if err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveScenicSupplier(tx, tenantID); err != nil {
			return err
		}
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		var row model.ScenicHotelPackage
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", packageID, tenantID).First(&row).Error; err != nil {
			return err
		}
		candidate := row
		candidate.ProductID, candidate.HotelID, candidate.RoomTypeID, candidate.RatePlanID = input.ProductID, input.HotelID, input.RoomTypeID, input.RatePlanID
		candidate.Nights, candidate.RoomsPerPackage, candidate.HotelSettlementPriceCents, candidate.Status = input.Nights, input.RoomsPerPackage, input.HotelSettlementPriceCents, input.Status
		candidate.BookingMode, candidate.VoucherValidityDays, candidate.MinAdvanceDays, candidate.MaxReschedules = input.BookingMode, input.VoucherValidityDays, input.MinAdvanceDays, input.MaxReschedules
		var reservationCount int64
		if err := tx.Model(&model.HotelReservation{}).Where("package_id = ?", row.ID).Count(&reservationCount).Error; err != nil {
			return err
		}
		var entitlementCount int64
		if err := tx.Model(&model.ScenicHotelPackageEntitlement{}).Where("package_id = ?", row.ID).Count(&entitlementCount).Error; err != nil {
			return err
		}
		if reservationCount+entitlementCount > 0 && (row.ProductID != candidate.ProductID || row.HotelID != candidate.HotelID || row.RoomTypeID != candidate.RoomTypeID || row.RatePlanID != candidate.RatePlanID || row.Nights != candidate.Nights || row.RoomsPerPackage != candidate.RoomsPerPackage || row.HotelSettlementPriceCents != candidate.HotelSettlementPriceCents || row.BookingMode != candidate.BookingMode || row.VoucherValidityDays != candidate.VoucherValidityDays || row.MinAdvanceDays != candidate.MinAdvanceDays || row.MaxReschedules != candidate.MaxReschedules) {
			return errors.New("package has orders; only its sale status can be changed")
		}
		if _, err := validateScenicHotelPackageFactsTx(tx, tenantID, &candidate, input.Status == "online"); err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]interface{}{"product_id": input.ProductID, "hotel_id": input.HotelID, "room_type_id": input.RoomTypeID, "rate_plan_id": input.RatePlanID, "nights": input.Nights, "rooms_per_package": input.RoomsPerPackage, "hotel_settlement_price_cents": input.HotelSettlementPriceCents, "booking_mode": input.BookingMode, "voucher_validity_days": input.VoucherValidityDays, "min_advance_days": input.MinAdvanceDays, "max_reschedules": input.MaxReschedules, "status": input.Status}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "scenic_hotel_package.update", "scenic_hotel_package", row.ID, "update fixed scenic hotel package", "{}", fmt.Sprintf(`{"status":%q}`, input.Status))
	})
}

func (s *ScenicHotelPackageService) Delete(tenantID, packageID, operatorID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveScenicSupplier(tx, tenantID); err != nil {
			return err
		}
		if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		var row model.ScenicHotelPackage
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", packageID, tenantID).First(&row).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.HotelReservation{}).Where("package_id = ?", row.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("package has orders and cannot be deleted; take it offline instead")
		}
		if err := tx.Model(&model.ScenicHotelPackageEntitlement{}).Where("package_id = ?", row.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("package has sold entitlements and cannot be deleted; take it offline instead")
		}
		if err := tx.Delete(&row).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, tenantID, "admin", "tenant", "scenic_hotel_package.delete", "scenic_hotel_package", row.ID, "delete fixed scenic hotel package", fmt.Sprintf(`{"product_id":%d}`, row.ProductID), "{}")
	})
}

func (s *ScenicHotelPackageService) List(tenantID uint) ([]ScenicHotelPackageView, error) {
	var rows []model.ScenicHotelPackage
	if err := model.DB.Where("tenant_id = ?", tenantID).Order("updated_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	views := make([]ScenicHotelPackageView, 0, len(rows))
	for i := range rows {
		view, err := packageViewTx(model.DB, tenantID, &rows[i])
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, nil
}

func (s *ScenicHotelPackageService) Get(tenantID, packageID uint) (*ScenicHotelPackageView, error) {
	var row model.ScenicHotelPackage
	if err := model.DB.Where("id = ? AND tenant_id = ?", packageID, tenantID).First(&row).Error; err != nil {
		return nil, err
	}
	return packageViewTx(model.DB, tenantID, &row)
}
