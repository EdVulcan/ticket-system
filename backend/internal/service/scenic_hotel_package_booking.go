package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (PackageFulfillmentLifecycle) CreateEntitlements(tx *gorm.DB, order *model.Order, item *model.OrderItem, facts *scenicHotelPackageFacts) error {
	if facts == nil || facts.Package.BookingMode != "after_purchase" {
		return nil
	}
	if order == nil || item == nil || len(item.Tickets) != item.Quantity {
		return errors.New("package entitlement quantity does not match package quantity")
	}
	validFrom := time.Now()
	validUntil := time.Date(validFrom.Year(), validFrom.Month(), validFrom.Day(), 23, 59, 59, 0, validFrom.Location()).AddDate(0, 0, facts.Package.VoucherValidityDays-1)
	for index := range item.Tickets {
		row := model.ScenicHotelPackageEntitlement{
			EntitlementNo: generateHotelPackageEntitlementNo(), SalesTenantID: order.TenantID,
			SupplierTenantID: facts.Package.TenantID, OrderID: order.ID, OrderItemID: item.ID,
			TicketID: item.Tickets[index].ID, PackageID: facts.Package.ID, Status: "pending_booking",
			ValidFrom: validFrom, ValidUntil: validUntil, PlatformSyncStatus: "not_required",
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func (lifecycle PackageFulfillmentLifecycle) PrepareBookingTx(tx *gorm.DB, input PackageEntitlementBookingInput) (*model.ScenicHotelPackageEntitlement, error) {
	input.EntitlementNo, input.ClientRequestID = strings.TrimSpace(input.EntitlementNo), strings.TrimSpace(input.ClientRequestID)
	input.GuestName, input.ContactPhone = strings.TrimSpace(input.GuestName), strings.TrimSpace(input.ContactPhone)
	if input.EntitlementNo == "" || input.ClientRequestID == "" || len(input.ClientRequestID) > 100 {
		return nil, errors.New("package entitlement and booking request id are required")
	}
	if input.GuestName == "" || input.ContactPhone == "" || len(input.GuestName) > 50 || len(input.ContactPhone) > 20 {
		return nil, errors.New("package booking requires a valid guest name and contact phone")
	}
	checkIn := dateOnly(input.CheckInDate)
	var entitlement model.ScenicHotelPackageEntitlement
	if err := tx.Where("entitlement_no = ?", input.EntitlementNo).First(&entitlement).Error; err != nil {
		return nil, err
	}
	var order model.Order
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", entitlement.OrderID, entitlement.SalesTenantID).First(&order).Error; err != nil {
		return nil, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND entitlement_no = ?", entitlement.ID, input.EntitlementNo).First(&entitlement).Error; err != nil {
		return nil, err
	}
	if entitlement.ClientRequestID == input.ClientRequestID && (entitlement.Status == "booking_pending" || entitlement.Status == "booked") {
		return &entitlement, nil
	}
	if entitlement.Status != "pending_booking" {
		return nil, errors.New("package entitlement is not awaiting booking")
	}
	now := time.Now()
	if now.Before(entitlement.ValidFrom) || now.After(entitlement.ValidUntil) {
		return nil, errors.New("package entitlement is outside its booking validity")
	}
	if order.Status != "paid" && order.Status != "partial_refunded" {
		return nil, errors.New("package order is not paid")
	}
	var item model.OrderItem
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Tickets").Where("id = ? AND order_id = ?", entitlement.OrderItemID, order.ID).First(&item).Error; err != nil {
		return nil, err
	}
	var ticket model.Ticket
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND order_id = ?", entitlement.TicketID, order.ID).First(&ticket).Error; err != nil {
		return nil, err
	}
	if ticket.PendingRefundID != 0 {
		return nil, errors.New("package entitlement has a pending refund")
	}
	if ticket.Status != "pending_booking" || ticket.CheckInCount != 0 {
		return nil, errors.New("package entitlement ticket is not bookable")
	}
	var packageRow model.ScenicHotelPackage
	if err := tx.Where("id = ? AND tenant_id = ? AND booking_mode = ?", entitlement.PackageID, entitlement.SupplierTenantID, "after_purchase").First(&packageRow).Error; err != nil {
		return nil, errors.New("package booking configuration is unavailable")
	}
	if checkIn.Before(dateOnly(now).AddDate(0, 0, packageRow.MinAdvanceDays)) {
		return nil, fmt.Errorf("package booking requires at least %d days advance", packageRow.MinAdvanceDays)
	}
	checkOut := checkIn.AddDate(0, 0, packageRow.Nights)
	if checkOut.After(dateOnly(entitlement.ValidUntil).AddDate(0, 0, 1)) {
		return nil, errors.New("package stay is outside voucher validity")
	}
	if err := requireConfiguredScenicSupplier(tx, entitlement.SupplierTenantID); err != nil {
		return nil, err
	}
	if err := requireConfiguredHotelSupplier(tx, entitlement.SupplierTenantID); err != nil {
		return nil, err
	}
	facts, err := validateScenicHotelPackageFactsTx(tx, entitlement.SupplierTenantID, &packageRow, false)
	if err != nil {
		return nil, err
	}
	for index := range item.Tickets {
		if item.Tickets[index].ID == ticket.ID {
			item.Tickets[index].Status = "unused"
		}
	}
	selected, err := splitOrderItemForTicketsTx(tx, &item, map[string]struct{}{ticket.TicketCode: {}})
	if err != nil {
		return nil, err
	}
	var product model.Product
	if err := tx.Unscoped().Where("id = ? AND tenant_id = ?", selected.FulfillmentProductID, selected.FulfillmentTenantID).First(&product).Error; err != nil {
		return nil, err
	}
	if !isVisitDateValid(&checkIn, item.ValidityStart, item.ValidityEnd) {
		return nil, errors.New("booking date is outside product validity")
	}
	if ticket.Environment != "sandbox" && selected.ReservedStockType == "voucher_daily" {
		if err := reserveStock(tx, stockProductForReservation(&product, "daily"), &checkIn, selected.StockSlot, 1); err != nil {
			return nil, err
		}
		selected.ReservedStockType = "daily"
	}
	if ticket.Environment != "sandbox" {
		if err := reserveHotelPackageInventoryTx(tx, facts, 1, checkIn); err != nil {
			return nil, err
		}
	}
	selected.UseDate = &checkIn
	selected.ValidityStart = &checkIn
	validEnd := time.Date(checkIn.Year(), checkIn.Month(), checkIn.Day(), 23, 59, 59, 0, checkIn.Location())
	selected.ValidityEnd = &validEnd
	if err := tx.Model(selected).Updates(map[string]interface{}{"use_date": checkIn, "validity_start": checkIn, "validity_end": validEnd, "reserved_stock_type": selected.ReservedStockType}).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&ticket).Updates(map[string]interface{}{
		"status": "pending_booking", "order_item_id": selected.ID,
		"visitor_name": input.GuestName, "visitor_phone": input.ContactPhone,
	}).Error; err != nil {
		return nil, err
	}
	reservation := model.HotelReservation{
		ReservationNo: generateHotelReservationNo(), SalesTenantID: order.TenantID, SupplierTenantID: packageRow.TenantID,
		OrderID: order.ID, OrderItemID: selected.ID, TicketID: ticket.ID, PackageID: packageRow.ID,
		HotelID: packageRow.HotelID, RoomTypeID: packageRow.RoomTypeID, RatePlanID: packageRow.RatePlanID,
		HotelName: facts.Hotel.Name, RoomTypeName: facts.RoomType.Name, RatePlanName: facts.RatePlan.Name,
		CheckInDate: checkIn, CheckOutDate: checkOut, Rooms: packageRow.RoomsPerPackage,
		SettlementPriceCents: packageRow.HotelSettlementPriceCents, Status: "reserved",
	}
	if err := tx.Create(&reservation).Error; err != nil {
		return nil, err
	}
	if entitlement.BookingCancelledAt != nil {
		if entitlement.RescheduleCount >= packageRow.MaxReschedules {
			return nil, errors.New("package entitlement has reached its reschedule limit")
		}
	}
	updates := map[string]interface{}{
		"order_item_id": selected.ID, "reservation_id": reservation.ID, "status": "booking_pending",
		"client_request_id": input.ClientRequestID, "external_book_order_id": input.ExternalBookOrderID,
		"platform_book_id": input.PlatformBookID, "platform_sync_status": "pending", "platform_sync_error": "",
	}
	if err := tx.Model(&entitlement).Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := tx.First(&entitlement, entitlement.ID).Error; err != nil {
		return nil, err
	}
	return &entitlement, nil
}

func (lifecycle PackageFulfillmentLifecycle) FinalizeBookingTx(tx *gorm.DB, entitlementNo, platformBookID string) (*model.ScenicHotelPackageEntitlement, error) {
	var entitlement model.ScenicHotelPackageEntitlement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("entitlement_no = ?", strings.TrimSpace(entitlementNo)).First(&entitlement).Error; err != nil {
		return nil, err
	}
	if entitlement.Status == "booked" {
		if platformBookID != "" && entitlement.PlatformBookID != "" && entitlement.PlatformBookID != platformBookID {
			return nil, errors.New("package booking platform id does not match")
		}
		return &entitlement, nil
	}
	if entitlement.Status != "booking_pending" || entitlement.ReservationID == 0 {
		return nil, errors.New("package entitlement has no prepared booking")
	}
	var ticket model.Ticket
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND order_id = ?", entitlement.TicketID, entitlement.OrderID).First(&ticket).Error; err != nil {
		return nil, err
	}
	if ticket.Status != "pending_booking" || ticket.PendingRefundID != 0 || ticket.CheckInCount != 0 {
		return nil, errors.New("package entitlement ticket cannot be activated")
	}
	var reservation model.HotelReservation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND ticket_id = ?", entitlement.ReservationID, ticket.ID).First(&reservation).Error; err != nil {
		return nil, err
	}
	if ticket.Environment == "sandbox" {
		if err := tx.Model(&reservation).Update("status", "confirmed").Error; err != nil {
			return nil, err
		}
	} else if err := transitionHotelReservationInventoryTx(tx, &reservation, "confirmed"); err != nil {
		return nil, err
	}
	if err := tx.Model(&ticket).Update("status", "unused").Error; err != nil {
		return nil, err
	}
	rescheduleCount := entitlement.RescheduleCount
	if entitlement.BookingCancelledAt != nil {
		rescheduleCount++
	}
	bookedAt := time.Now()
	updates := map[string]interface{}{
		"status": "booked", "booked_at": bookedAt, "booking_cancelled_at": nil,
		"reschedule_count": rescheduleCount, "platform_sync_status": "pending", "platform_sync_error": "",
	}
	if strings.TrimSpace(platformBookID) != "" {
		updates["platform_book_id"] = strings.TrimSpace(platformBookID)
	}
	if err := tx.Model(&entitlement).Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := tx.First(&entitlement, entitlement.ID).Error; err != nil {
		return nil, err
	}
	return &entitlement, nil
}

func (lifecycle PackageFulfillmentLifecycle) BookEntitlementTx(tx *gorm.DB, input PackageEntitlementBookingInput) (*model.ScenicHotelPackageEntitlement, error) {
	prepared, err := lifecycle.PrepareBookingTx(tx, input)
	if err != nil {
		return nil, err
	}
	return lifecycle.FinalizeBookingTx(tx, prepared.EntitlementNo, input.PlatformBookID)
}

func (PackageFulfillmentLifecycle) RollbackPreparedBookingTx(tx *gorm.DB, entitlementNo string) (*model.ScenicHotelPackageEntitlement, error) {
	var entitlement model.ScenicHotelPackageEntitlement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("entitlement_no = ?", strings.TrimSpace(entitlementNo)).First(&entitlement).Error; err != nil {
		return nil, err
	}
	if entitlement.Status == "pending_booking" {
		return &entitlement, nil
	}
	if entitlement.Status != "booking_pending" || entitlement.ReservationID == 0 {
		return nil, errors.New("package entitlement has no prepared booking to roll back")
	}
	var ticket model.Ticket
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", entitlement.TicketID).First(&ticket).Error; err != nil {
		return nil, err
	}
	if ticket.PendingRefundID != 0 || ticket.CheckInCount != 0 || ticket.Status != "pending_booking" {
		return nil, errors.New("prepared package booking ticket cannot be rolled back")
	}
	var reservation model.HotelReservation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND ticket_id = ?", entitlement.ReservationID, ticket.ID).First(&reservation).Error; err != nil {
		return nil, err
	}
	if reservation.Status != "reserved" {
		return nil, errors.New("prepared hotel reservation cannot be rolled back from its current status")
	}
	if ticket.Environment == "sandbox" {
		if err := tx.Model(&reservation).Update("status", "cancelled").Error; err != nil {
			return nil, err
		}
	} else if err := transitionHotelReservationInventoryTx(tx, &reservation, "cancelled"); err != nil {
		return nil, err
	}
	var item model.OrderItem
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", ticket.OrderItemID).First(&item).Error; err != nil {
		return nil, err
	}
	var product model.Product
	if err := tx.Unscoped().Where("id = ? AND tenant_id = ?", item.FulfillmentProductID, item.FulfillmentTenantID).First(&product).Error; err != nil {
		return nil, err
	}
	if ticket.Environment != "sandbox" && item.ReservedStockType == "daily" {
		if err := releaseStock(tx, &product, item.UseDate, item.StockSlot, 1); err != nil {
			return nil, err
		}
	}
	nextReservedStockType := item.ReservedStockType
	if item.ReservedStockType == "daily" {
		nextReservedStockType = "voucher_daily"
	}
	if err := tx.Model(&item).Updates(map[string]interface{}{"use_date": nil, "validity_start": entitlement.ValidFrom, "validity_end": entitlement.ValidUntil, "reserved_stock_type": nextReservedStockType}).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&ticket).Updates(map[string]interface{}{"status": "pending_booking", "visitor_name": "", "visitor_phone": ""}).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&entitlement).Updates(map[string]interface{}{
		"status": "pending_booking", "reservation_id": 0, "platform_sync_status": "not_required",
		"platform_sync_error": "", "client_request_id": "", "external_book_order_id": "", "platform_book_id": "",
	}).Error; err != nil {
		return nil, err
	}
	if err := tx.First(&entitlement, entitlement.ID).Error; err != nil {
		return nil, err
	}
	return &entitlement, nil
}

func (PackageFulfillmentLifecycle) PrepareCancelTx(tx *gorm.DB, entitlementNo string) (*model.ScenicHotelPackageEntitlement, error) {
	var entitlement model.ScenicHotelPackageEntitlement
	if err := tx.Where("entitlement_no = ?", strings.TrimSpace(entitlementNo)).First(&entitlement).Error; err != nil {
		return nil, err
	}
	var order model.Order
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", entitlement.OrderID, entitlement.SalesTenantID).First(&order).Error; err != nil {
		return nil, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND entitlement_no = ?", entitlement.ID, strings.TrimSpace(entitlementNo)).First(&entitlement).Error; err != nil {
		return nil, err
	}
	if entitlement.Status == "pending_booking" || entitlement.Status == "cancel_pending" {
		return &entitlement, nil
	}
	if entitlement.Status != "booked" || entitlement.ReservationID == 0 {
		return nil, errors.New("package entitlement has no cancellable booking")
	}
	var ticket model.Ticket
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", entitlement.TicketID).First(&ticket).Error; err != nil {
		return nil, err
	}
	if ticket.PendingRefundID != 0 || ticket.CheckInCount != 0 || ticket.Status != "unused" {
		return nil, errors.New("package booking cannot be cancelled after refund or admission processing")
	}
	var reservation model.HotelReservation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND ticket_id = ?", entitlement.ReservationID, ticket.ID).First(&reservation).Error; err != nil {
		return nil, err
	}
	if reservation.Status != "reserved" && reservation.Status != "confirmed" {
		return nil, errors.New("hotel reservation has entered fulfillment and cannot be cancelled")
	}
	var packageRow model.ScenicHotelPackage
	if err := tx.Where("id = ? AND tenant_id = ?", entitlement.PackageID, entitlement.SupplierTenantID).First(&packageRow).Error; err != nil {
		return nil, err
	}
	if entitlement.RescheduleCount >= packageRow.MaxReschedules {
		return nil, errors.New("package entitlement has no remaining reschedule opportunity; refund it instead")
	}
	// Freeze admission while the external booking cancellation is in flight.
	// The same transaction owns both locks, so the gate cannot consume the
	// ticket between the local prepare and the remote cancellation request.
	if err := tx.Model(&ticket).Update("status", "pending_booking").Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&entitlement).Updates(map[string]interface{}{
		"status": "cancel_pending", "platform_sync_status": "pending", "platform_sync_error": "",
	}).Error; err != nil {
		return nil, err
	}
	if err := tx.First(&entitlement, entitlement.ID).Error; err != nil {
		return nil, err
	}
	return &entitlement, nil
}

func (PackageFulfillmentLifecycle) FinalizeCancelTx(tx *gorm.DB, entitlementNo string) (*model.ScenicHotelPackageEntitlement, error) {
	var entitlement model.ScenicHotelPackageEntitlement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("entitlement_no = ?", strings.TrimSpace(entitlementNo)).First(&entitlement).Error; err != nil {
		return nil, err
	}
	if entitlement.Status == "pending_booking" {
		return &entitlement, nil
	}
	if entitlement.Status != "cancel_pending" || entitlement.ReservationID == 0 {
		return nil, errors.New("package entitlement has no prepared cancellation")
	}
	var ticket model.Ticket
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", entitlement.TicketID).First(&ticket).Error; err != nil {
		return nil, err
	}
	if ticket.PendingRefundID != 0 || ticket.CheckInCount != 0 || ticket.Status != "pending_booking" {
		return nil, errors.New("package booking cannot be cancelled after refund or admission processing")
	}
	var reservation model.HotelReservation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND ticket_id = ?", entitlement.ReservationID, ticket.ID).First(&reservation).Error; err != nil {
		return nil, err
	}
	if reservation.Status != "reserved" && reservation.Status != "confirmed" {
		return nil, errors.New("hotel reservation has entered fulfillment and cannot be cancelled")
	}
	if ticket.Environment == "sandbox" {
		if err := tx.Model(&reservation).Update("status", "cancelled").Error; err != nil {
			return nil, err
		}
	} else if err := transitionHotelReservationInventoryTx(tx, &reservation, "cancelled"); err != nil {
		return nil, err
	}
	var item model.OrderItem
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", ticket.OrderItemID).First(&item).Error; err != nil {
		return nil, err
	}
	var product model.Product
	if err := tx.Unscoped().Where("id = ? AND tenant_id = ?", item.FulfillmentProductID, item.FulfillmentTenantID).First(&product).Error; err != nil {
		return nil, err
	}
	if ticket.Environment != "sandbox" && item.ReservedStockType == "daily" {
		if err := releaseStock(tx, &product, item.UseDate, item.StockSlot, 1); err != nil {
			return nil, err
		}
	}
	nextReservedStockType := item.ReservedStockType
	if item.ReservedStockType == "daily" {
		nextReservedStockType = "voucher_daily"
	}
	if err := tx.Model(&item).Updates(map[string]interface{}{"use_date": nil, "validity_start": entitlement.ValidFrom, "validity_end": entitlement.ValidUntil, "reserved_stock_type": nextReservedStockType}).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&ticket).Updates(map[string]interface{}{"status": "pending_booking", "visitor_name": "", "visitor_phone": ""}).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	if err := tx.Model(&entitlement).Updates(map[string]interface{}{
		"status": "pending_booking", "reservation_id": 0, "platform_sync_status": "synced", "platform_sync_error": "",
		"booking_cancelled_at": now, "client_request_id": "", "external_book_order_id": "", "platform_book_id": "",
	}).Error; err != nil {
		return nil, err
	}
	if err := tx.First(&entitlement, entitlement.ID).Error; err != nil {
		return nil, err
	}
	return &entitlement, nil
}

func (PackageFulfillmentLifecycle) RollbackCancelTx(tx *gorm.DB, entitlementNo string) (*model.ScenicHotelPackageEntitlement, error) {
	var entitlement model.ScenicHotelPackageEntitlement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("entitlement_no = ?", strings.TrimSpace(entitlementNo)).First(&entitlement).Error; err != nil {
		return nil, err
	}
	if entitlement.Status == "booked" {
		return &entitlement, nil
	}
	if entitlement.Status != "cancel_pending" || entitlement.ReservationID == 0 {
		return nil, errors.New("package entitlement has no prepared cancellation to roll back")
	}
	var ticket model.Ticket
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND order_id = ?", entitlement.TicketID, entitlement.OrderID).First(&ticket).Error; err != nil {
		return nil, err
	}
	if ticket.Status != "pending_booking" || ticket.PendingRefundID != 0 || ticket.CheckInCount != 0 {
		return nil, errors.New("prepared package cancellation ticket cannot be restored")
	}
	if err := tx.Model(&ticket).Update("status", "unused").Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&entitlement).Updates(map[string]interface{}{
		"status": "booked", "platform_sync_status": "synced", "platform_sync_error": "",
	}).Error; err != nil {
		return nil, err
	}
	if err := tx.First(&entitlement, entitlement.ID).Error; err != nil {
		return nil, err
	}
	return &entitlement, nil
}

func (lifecycle PackageFulfillmentLifecycle) CancelEntitlementBookingTx(tx *gorm.DB, entitlementNo string) (*model.ScenicHotelPackageEntitlement, error) {
	prepared, err := lifecycle.PrepareCancelTx(tx, entitlementNo)
	if err != nil {
		return nil, err
	}
	return lifecycle.FinalizeCancelTx(tx, prepared.EntitlementNo)
}
