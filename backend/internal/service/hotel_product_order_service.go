package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// hotelProductSaleFacts is the immutable pricing/resource decision made while
// creating an order. It deliberately lives outside OrderItem so the existing
// ticket schema does not become a second hotel catalog.
type hotelProductSaleFacts struct {
	Product     model.HotelProduct
	Revision    model.HotelProductRevision
	Hotel       model.HotelProperty
	RoomType    model.HotelRoomType
	RatePlan    model.HotelRatePlan
	RetailCents int64
	SettleCents int64
	PriceSource string
	CheckInDate *time.Time
}

func loadHotelProductSaleFactsTx(tx *gorm.DB, tenantID, productID uint, useDate *time.Time, now time.Time) (*hotelProductSaleFacts, error) {
	var product model.HotelProduct
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND product_id = ? AND status = ?", tenantID, productID, "online").First(&product).Error; err != nil {
		return nil, errors.New("hotel product is unavailable")
	}
	if err := requireHotelProductResourcesTx(tx, tenantID, product.HotelID, product.RoomTypeID, product.RatePlanID); err != nil {
		return nil, err
	}
	var hotel model.HotelProperty
	if err := tx.Where("id = ? AND tenant_id = ? AND status = ?", product.HotelID, tenantID, "active").First(&hotel).Error; err != nil {
		return nil, errors.New("hotel is unavailable")
	}
	var room model.HotelRoomType
	if err := tx.Where("id = ? AND tenant_id = ? AND hotel_id = ? AND status = ?", product.RoomTypeID, tenantID, product.HotelID, "active").First(&room).Error; err != nil {
		return nil, errors.New("hotel room type is unavailable")
	}
	var rate model.HotelRatePlan
	if err := tx.Where("id = ? AND tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND status = ?", product.RatePlanID, tenantID, product.HotelID, product.RoomTypeID, "active").First(&rate).Error; err != nil {
		return nil, errors.New("hotel rate plan is unavailable")
	}
	var revision model.HotelProductRevision
	if err := tx.Where("id = ? AND hotel_product_id = ? AND tenant_id = ?", product.CurrentRevisionID, product.ID, tenantID).First(&revision).Error; err != nil {
		return nil, errors.New("hotel product revision is unavailable")
	}
	if revision.SaleMode != product.SaleMode || revision.HotelID != product.HotelID || revision.RoomTypeID != product.RoomTypeID || revision.RatePlanID != product.RatePlanID {
		return nil, errors.New("hotel product revision does not match the current sale configuration")
	}
	facts := &hotelProductSaleFacts{Product: product, Revision: revision, Hotel: hotel, RoomType: room, RatePlan: rate, RetailCents: revision.BaseRetailPriceCents, SettleCents: revision.BaseSettlementPriceCents, PriceSource: "base"}
	switch revision.SaleMode {
	case "calendar_room":
		if useDate == nil {
			return nil, errors.New("calendar-room hotel products require a check-in date")
		}
		checkIn := dateOnly(*useDate)
		if checkIn.Before(dateOnly(now)) {
			return nil, errors.New("hotel check-in date cannot be in the past")
		}
		facts.CheckInDate = &checkIn
		var override model.HotelProductCalendarPrice
		if err := tx.Where("tenant_id = ? AND hotel_product_id = ? AND hotel_product_revision_id = ? AND stay_date = ?", tenantID, product.ID, revision.ID, checkIn).First(&override).Error; err == nil {
			facts.RetailCents, facts.SettleCents, facts.PriceSource = override.RetailPriceCents, override.SettlementPriceCents, "calendar"
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	case "presale_room":
		if useDate != nil {
			return nil, errors.New("presale-room hotel products do not accept a check-in date at purchase")
		}
	default:
		return nil, errors.New("hotel product sale mode is invalid")
	}
	return facts, nil
}

func reserveHotelProductInventoryTx(tx *gorm.DB, facts *hotelProductSaleFacts, quantity int, environment string) error {
	if facts == nil || facts.CheckInDate == nil || strings.EqualFold(environment, "sandbox") {
		return nil
	}
	rooms := facts.Product.RoomsPerPackage * quantity
	for offset := 0; offset < facts.Product.Nights; offset++ {
		stayDate := facts.CheckInDate.AddDate(0, 0, offset)
		var inventory model.HotelRoomInventory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND stay_date = ?", facts.Product.TenantID, facts.Product.HotelID, facts.Product.RoomTypeID, stayDate).First(&inventory).Error; err != nil {
			return fmt.Errorf("hotel inventory is not configured for %s", stayDate.Format("2006-01-02"))
		}
		if inventory.Closed || inventory.Capacity-inventory.Reserved-inventory.Sold < rooms {
			return fmt.Errorf("hotel inventory is insufficient for %s", stayDate.Format("2006-01-02"))
		}
		if err := tx.Model(&inventory).Update("reserved", inventory.Reserved+rooms).Error; err != nil {
			return err
		}
	}
	return nil
}

func transitionHotelProductInventoryTx(tx *gorm.DB, reservation *model.HotelProductReservation, target string) error {
	if reservation == nil || reservation.Status == target || reservation.Status == "cancelled" || reservation.Status == "refunded" {
		return nil
	}
	if reservation.CheckOutDate.IsZero() || !reservation.CheckOutDate.After(reservation.CheckInDate) {
		return errors.New("hotel product reservation dates are invalid")
	}
	for stayDate := dateOnly(reservation.CheckInDate); stayDate.Before(dateOnly(reservation.CheckOutDate)); stayDate = stayDate.AddDate(0, 0, 1) {
		var inventory model.HotelRoomInventory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND stay_date = ?", reservation.SupplierTenantID, reservation.HotelID, reservation.RoomTypeID, stayDate).First(&inventory).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{}
		switch {
		case reservation.Status == "reserved" && target == "confirmed":
			if inventory.Reserved < reservation.Rooms {
				return errors.New("hotel product reserved inventory is invalid")
			}
			updates["reserved"], updates["sold"] = inventory.Reserved-reservation.Rooms, inventory.Sold+reservation.Rooms
		case reservation.Status == "reserved" && target == "cancelled":
			if inventory.Reserved < reservation.Rooms {
				return errors.New("hotel product reserved inventory is invalid")
			}
			updates["reserved"] = inventory.Reserved - reservation.Rooms
		case reservation.Status == "confirmed" && target == "cancelled":
			if inventory.Sold < reservation.Rooms {
				return errors.New("hotel product sold inventory is invalid")
			}
			updates["sold"] = inventory.Sold - reservation.Rooms
		default:
			continue
		}
		if err := tx.Model(&inventory).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}

func createHotelProductEntitlementsTx(tx *gorm.DB, order *model.Order, item *model.OrderItem, facts *hotelProductSaleFacts) error {
	if order == nil || item == nil || facts == nil {
		return errors.New("hotel product order facts are required")
	}
	if err := reserveHotelProductInventoryTx(tx, facts, item.Quantity, order.Environment); err != nil {
		return err
	}
	now := time.Now()
	validFrom, validUntil := now, now.AddDate(0, 0, facts.Revision.VoucherValidityDays-1)
	if facts.CheckInDate != nil {
		validFrom = *facts.CheckInDate
		validUntil = validFrom.AddDate(0, 0, facts.Revision.Nights)
	}
	for index := 0; index < item.Quantity; index++ {
		entitlement := model.HotelProductEntitlement{
			EntitlementNo: generateHotelProductEntitlementNo(), SalesTenantID: order.TenantID, SupplierTenantID: facts.Product.TenantID,
			OrderID: order.ID, OrderItemID: item.ID, HotelProductID: facts.Product.ID, HotelProductRevisionID: facts.Revision.ID,
			Rooms: facts.Product.RoomsPerPackage, HotelName: facts.Hotel.Name, RoomTypeName: facts.RoomType.Name, RatePlanName: facts.RatePlan.Name,
			GuestName: order.ContactName, ContactPhone: order.ContactPhone, RetailPriceCents: facts.RetailCents, SettlementPriceCents: facts.SettleCents,
			PriceSource: facts.PriceSource, Status: "pending_booking", ValidFrom: validFrom, ValidUntil: validUntil,
		}
		if err := tx.Create(&entitlement).Error; err != nil {
			return err
		}
		if facts.CheckInDate == nil {
			continue
		}
		checkOut := facts.CheckInDate.AddDate(0, 0, facts.Revision.Nights)
		reservation := model.HotelProductReservation{
			ReservationNo: generateHotelProductReservationNo(), SalesTenantID: order.TenantID, SupplierTenantID: facts.Product.TenantID,
			OrderID: order.ID, OrderItemID: item.ID, EntitlementID: entitlement.ID, HotelProductID: facts.Product.ID, HotelProductRevisionID: facts.Revision.ID,
			HotelID: facts.Product.HotelID, RoomTypeID: facts.Product.RoomTypeID, RatePlanID: facts.Product.RatePlanID,
			HotelName: facts.Hotel.Name, RoomTypeName: facts.RoomType.Name, RatePlanName: facts.RatePlan.Name,
			CheckInDate: *facts.CheckInDate, CheckOutDate: checkOut, Rooms: facts.Product.RoomsPerPackage,
			GuestName: order.ContactName, ContactPhone: order.ContactPhone, RetailPriceCents: facts.RetailCents, SettlementPriceCents: facts.SettleCents,
			PriceSource: facts.PriceSource, Status: "reserved",
		}
		if err := tx.Create(&reservation).Error; err != nil {
			return err
		}
		if err := tx.Model(&entitlement).Updates(map[string]interface{}{"reservation_id": reservation.ID, "check_in_date": facts.CheckInDate, "check_out_date": checkOut, "status": "booked"}).Error; err != nil {
			return err
		}
	}
	return nil
}

// HotelProductFulfillmentLifecycle converts an unpaid date reservation to sold
// inventory after payment and releases it on unpaid cancellation. Presale
// entitlements remain undated until a dedicated booking channel is enabled.
type HotelProductFulfillmentLifecycle struct{}

// hotelProductOrderEnvironmentTx keeps sandbox lifecycle transitions local.
// Sandbox channel orders intentionally do not create production inventory
// rows, so payment confirmation and cancellation must not try to consume or
// release real room inventory later in the workflow.
func hotelProductOrderEnvironmentTx(tx *gorm.DB, orderID uint) (string, error) {
	var order model.Order
	if err := tx.Select("id", "environment").Where("id = ?", orderID).First(&order).Error; err != nil {
		return "", err
	}
	environment := strings.ToLower(strings.TrimSpace(order.Environment))
	if environment == "" {
		// Empty is legacy data, and must retain production semantics rather than
		// becoming an accidental sandbox bypass.
		environment = "production"
	}
	return environment, nil
}

func (HotelProductFulfillmentLifecycle) ConfirmOrderAt(tx *gorm.DB, orderID uint, paidAt time.Time) error {
	environment, err := hotelProductOrderEnvironmentTx(tx, orderID)
	if err != nil {
		return err
	}
	sandbox := environment == "sandbox"
	var reservations []model.HotelProductReservation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ? AND status = ?", orderID, "reserved").Order("id ASC").Find(&reservations).Error; err != nil {
		return err
	}
	for index := range reservations {
		if !sandbox {
			if err := transitionHotelProductInventoryTx(tx, &reservations[index], "confirmed"); err != nil {
				return err
			}
		}
		if err := tx.Model(&reservations[index]).Updates(map[string]interface{}{"status": "confirmed"}).Error; err != nil {
			return err
		}
	}
	var entitlements []model.HotelProductEntitlement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ? AND status = ? AND reservation_id = 0", orderID, "pending_booking").Find(&entitlements).Error; err != nil {
		return err
	}
	for index := range entitlements {
		var revision model.HotelProductRevision
		if err := tx.Where("id = ? AND hotel_product_id = ? AND tenant_id = ?", entitlements[index].HotelProductRevisionID, entitlements[index].HotelProductID, entitlements[index].SupplierTenantID).First(&revision).Error; err != nil {
			return err
		}
		start := paidAt
		if start.IsZero() {
			start = time.Now()
		}
		validUntil := start.AddDate(0, 0, revision.VoucherValidityDays-1)
		if err := tx.Model(&entitlements[index]).Updates(map[string]interface{}{"valid_from": start, "valid_until": validUntil}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (HotelProductFulfillmentLifecycle) CancelOrder(tx *gorm.DB, orderID uint, includeConfirmed bool) error {
	environment, err := hotelProductOrderEnvironmentTx(tx, orderID)
	if err != nil {
		return err
	}
	sandbox := environment == "sandbox"
	var reservations []model.HotelProductReservation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ? AND status IN ?", orderID, []string{"reserved", "confirmed"}).Order("id ASC").Find(&reservations).Error; err != nil {
		return err
	}
	for index := range reservations {
		if reservations[index].Status == "confirmed" && !includeConfirmed {
			return errors.New("confirmed hotel product reservation must use the refund workflow")
		}
		if !sandbox {
			if err := transitionHotelProductInventoryTx(tx, &reservations[index], "cancelled"); err != nil {
				return err
			}
		}
		if err := tx.Model(&reservations[index]).Updates(map[string]interface{}{"status": "cancelled"}).Error; err != nil {
			return err
		}
	}
	return tx.Model(&model.HotelProductEntitlement{}).Where("order_id = ? AND status IN ?", orderID, []string{"pending_booking", "booked"}).Update("status", "cancelled").Error
}

func generateHotelProductEntitlementNo() string {
	raw := make([]byte, 5)
	if _, err := rand.Read(raw); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return fmt.Sprintf("HPE%d%s", time.Now().UnixMilli(), strings.ToUpper(hex.EncodeToString(raw)))
}

func generateHotelProductReservationNo() string {
	raw := make([]byte, 5)
	if _, err := rand.Read(raw); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return fmt.Sprintf("HPR%d%s", time.Now().UnixMilli(), strings.ToUpper(hex.EncodeToString(raw)))
}
