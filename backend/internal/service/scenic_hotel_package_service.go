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

type ScenicHotelPackageService struct{}

// PackageFulfillmentLifecycle owns the state transitions that must keep a
// package ticket, its hotel reservation and room inventory in agreement.
type PackageFulfillmentLifecycle struct{}

func (PackageFulfillmentLifecycle) AssertProductCodeModeSupported(tx *gorm.DB, tenantID, productID uint, codeMode string) error {
	if codeMode == "ticket" {
		return nil
	}
	var count int64
	if err := tx.Model(&model.ScenicHotelPackage{}).Where("tenant_id = ? AND product_id = ?", tenantID, productID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("hotel package ticket product must use one ticket code per package unit")
	}
	return nil
}

func (PackageFulfillmentLifecycle) Reserve(tx *gorm.DB, facts *scenicHotelPackageFacts, product *model.Product, quantity int, checkIn time.Time) error {
	if facts == nil {
		return nil
	}
	if product == nil || (product.CodeMode == "order" && quantity > 1) {
		return errors.New("hotel package order-code products can only be sold one unit per order item")
	}
	return reserveHotelPackageInventoryTx(tx, facts, quantity, checkIn)
}

func (PackageFulfillmentLifecycle) CreateReservations(tx *gorm.DB, order *model.Order, item *model.OrderItem, facts *scenicHotelPackageFacts) error {
	return createHotelPackageReservationsTx(tx, order, item, facts)
}

func (PackageFulfillmentLifecycle) AssertExchangeSupported(tx *gorm.DB, tenantID, targetProductID uint, ticketIDs []uint) error {
	var sourcePackages int64
	if len(ticketIDs) > 0 {
		if err := tx.Model(&model.HotelReservation{}).Where("ticket_id IN ?", ticketIDs).Count(&sourcePackages).Error; err != nil {
			return err
		}
	}
	var targetPackages int64
	if err := tx.Model(&model.ScenicHotelPackage{}).Where("tenant_id = ? AND product_id = ?", tenantID, targetProductID).Count(&targetPackages).Error; err != nil {
		return err
	}
	if sourcePackages > 0 || targetPackages > 0 {
		return errors.New("hotel package exchange is not supported; refund and create a new order instead")
	}
	return nil
}

type ScenicHotelPackageInput struct {
	ProductID                 uint   `json:"product_id"`
	HotelID                   uint   `json:"hotel_id"`
	RoomTypeID                uint   `json:"room_type_id"`
	RatePlanID                uint   `json:"rate_plan_id"`
	Nights                    int    `json:"nights"`
	RoomsPerPackage           int    `json:"rooms_per_package"`
	HotelSettlementPriceCents int64  `json:"hotel_settlement_price_cents"`
	BookingMode               string `json:"booking_mode"`
	VoucherValidityDays       int    `json:"voucher_validity_days"`
	MinAdvanceDays            int    `json:"min_advance_days"`
	MaxReschedules            int    `json:"max_reschedules"`
	Status                    string `json:"status"`
}

type ScenicHotelPackageView struct {
	model.ScenicHotelPackage
	ProductName                string `json:"product_name"`
	RetailPriceCents           int64  `json:"retail_price_cents"`
	TicketSettlementPriceCents int64  `json:"ticket_settlement_price_cents"`
	HotelName                  string `json:"hotel_name"`
	RoomTypeName               string `json:"room_type_name"`
	RatePlanName               string `json:"rate_plan_name"`
	ReservationCount           int64  `json:"reservation_count"`
	EntitlementCount           int64  `json:"entitlement_count"`
}

type HotelReservationPage struct {
	Data     []HotelReservationView `json:"data"`
	Total    int64                  `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
}

type HotelReservationView struct {
	model.HotelReservation
	OrderNo      string `json:"order_no"`
	GuestName    string `json:"guest_name"`
	ContactPhone string `json:"contact_phone"`
	ProductName  string `json:"product_name"`
	TicketCode   string `json:"ticket_code"`
}

type PackageEntitlementPage struct {
	Data     []PackageEntitlementView `json:"data"`
	Total    int64                    `json:"total"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
}

type PackageEntitlementView struct {
	model.ScenicHotelPackageEntitlement
	OrderNo      string `json:"order_no"`
	ProductName  string `json:"product_name"`
	HotelName    string `json:"hotel_name"`
	RoomTypeName string `json:"room_type_name"`
}

type PackageEntitlementBookingInput struct {
	EntitlementNo       string
	CheckInDate         time.Time
	GuestName           string
	ContactPhone        string
	ClientRequestID     string
	ExternalBookOrderID string
	PlatformBookID      string
}

type HotelPackageBusinessSummary struct {
	// PackageUnits remains the backwards-compatible alias for SalesUnits.
	PackageUnits            int64 `json:"package_units"`
	SalesUnits              int64 `json:"sales_units"`
	BookingUnits            int64 `json:"booking_units"`
	StayUnits               int64 `json:"stay_units"`
	AwaitingBookingUnits    int64 `json:"awaiting_booking_units"`
	PendingUnits            int64 `json:"pending_units"`
	ConfirmedUnits          int64 `json:"confirmed_units"`
	CheckedInUnits          int64 `json:"checked_in_units"`
	CheckedOutUnits         int64 `json:"checked_out_units"`
	NoShowUnits             int64 `json:"no_show_units"`
	RefundedUnits           int64 `json:"refunded_units"`
	GrossSalesCents         int64 `json:"gross_sales_cents"`
	RefundedSalesCents      int64 `json:"refunded_sales_cents"`
	NetSalesCents           int64 `json:"net_sales_cents"`
	TicketComponentNetCents int64 `json:"ticket_component_net_cents"`
	HotelComponentNetCents  int64 `json:"hotel_component_net_cents"`
	UnallocatedMarginCents  int64 `json:"unallocated_margin_cents"`
}

type scenicHotelPackageFacts struct {
	Package  model.ScenicHotelPackage
	Hotel    model.HotelProperty
	RoomType model.HotelRoomType
	RatePlan model.HotelRatePlan
}

func (s *ScenicHotelPackageService) SetReservationStatus(tenantID, reservationID, operatorID uint, target, reason string) error {
	target, reason = strings.TrimSpace(target), strings.TrimSpace(reason)
	return model.Write(func(tx *gorm.DB) error {
		if err := requireConfiguredHotelSupplier(tx, tenantID); err != nil {
			return err
		}
		var reservation model.HotelReservation
		if err := tx.Where("id = ? AND supplier_tenant_id = ?", reservationID, tenantID).First(&reservation).Error; err != nil {
			return err
		}
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", reservation.OrderID, reservation.SalesTenantID).First(&order).Error; err != nil {
			return err
		}
		var entitlement model.ScenicHotelPackageEntitlement
		entitlementErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("ticket_id = ?", reservation.TicketID).First(&entitlement).Error
		if entitlementErr != nil && !errors.Is(entitlementErr, gorm.ErrRecordNotFound) {
			return entitlementErr
		}
		if entitlementErr == nil && (entitlement.Status == "booking_pending" || entitlement.Status == "cancel_pending") {
			return errors.New("hotel reservation cannot change while its package booking operation is in progress")
		}
		var ticket model.Ticket
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", reservation.TicketID).First(&ticket).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND supplier_tenant_id = ?", reservationID, tenantID).First(&reservation).Error; err != nil {
			return err
		}
		if ticket.PendingRefundID > 0 {
			return errors.New("hotel reservation cannot change while its ticket has a pending refund")
		}
		allowed := map[string]map[string]bool{
			"confirmed":   {"checked_in": true, "no_show": true},
			"checked_in":  {"checked_out": true, "confirmed": true},
			"checked_out": {"checked_in": true},
			"no_show":     {"confirmed": true},
		}
		if !allowed[reservation.Status][target] {
			return fmt.Errorf("hotel reservation cannot change from %s to %s", reservation.Status, target)
		}
		correction := (reservation.Status == "checked_in" && target == "confirmed") ||
			(reservation.Status == "checked_out" && target == "checked_in") ||
			(reservation.Status == "no_show" && target == "confirmed")
		if (target == "no_show" || correction) && reason == "" {
			return errors.New("a reason is required for no-show or fulfillment correction")
		}
		now := time.Now()
		updates := map[string]interface{}{"status": target}
		switch target {
		case "checked_in":
			if reservation.CheckedInAt == nil {
				updates["checked_in_at"] = now
			}
			updates["checked_out_at"] = nil
		case "checked_out":
			updates["checked_out_at"] = now
		case "no_show":
			updates["no_show_at"] = now
		case "confirmed":
			if reservation.Status == "checked_in" {
				updates["checked_in_at"] = nil
			}
			if reservation.Status == "no_show" {
				updates["no_show_at"] = nil
			}
		}
		if err := tx.Model(&reservation).Updates(updates).Error; err != nil {
			return err
		}
		auditReason := reason
		if auditReason == "" {
			auditReason = map[string]string{"checked_in": "登记酒店已入住", "checked_out": "登记酒店已离店"}[target]
		}
		return recordAuditTx(tx, operatorID, tenantID, auditRoleTx(tx, operatorID), "tenant", "hotel.reservation.status", "hotel_reservation", reservation.ID, auditReason, fmt.Sprintf(`{"status":%q}`, reservation.Status), fmt.Sprintf(`{"status":%q}`, target))
	})
}

func packageViewTx(tx *gorm.DB, tenantID uint, row *model.ScenicHotelPackage) (*ScenicHotelPackageView, error) {
	facts, err := validateScenicHotelPackageFactsTx(tx, tenantID, row, false)
	if err != nil {
		return nil, err
	}
	var product model.Product
	if err := tx.Unscoped().Where("id = ? AND tenant_id = ?", row.ProductID, tenantID).First(&product).Error; err != nil {
		return nil, err
	}
	var reservationCount int64
	if err := tx.Model(&model.HotelReservation{}).Where("package_id = ?", row.ID).Count(&reservationCount).Error; err != nil {
		return nil, err
	}
	var entitlementCount int64
	if err := tx.Model(&model.ScenicHotelPackageEntitlement{}).Where("package_id = ?", row.ID).Count(&entitlementCount).Error; err != nil {
		return nil, err
	}
	return &ScenicHotelPackageView{ScenicHotelPackage: *row, ProductName: product.Name, RetailPriceCents: moneyCents(product.Price), TicketSettlementPriceCents: moneyCents(product.SettlementPrice), HotelName: facts.Hotel.Name, RoomTypeName: facts.RoomType.Name, RatePlanName: facts.RatePlan.Name, ReservationCount: reservationCount, EntitlementCount: entitlementCount}, nil
}

func validateScenicHotelPackageFactsTx(tx *gorm.DB, tenantID uint, row *model.ScenicHotelPackage, requireSellable bool) (*scenicHotelPackageFacts, error) {
	var product model.Product
	query := tx.Where("id = ? AND tenant_id = ? AND type = ?", row.ProductID, tenantID, "online")
	if requireSellable {
		query = query.Where("status = ?", "online")
	}
	if err := query.First(&product).Error; err != nil {
		return nil, errors.New("package ticket product is unavailable")
	}
	if product.SourceProductID != 0 || product.ProductOfferID != 0 {
		return nil, errors.New("fixed scenic hotel package currently requires a supplier-owned ticket product")
	}
	if product.CodeMode == "order" {
		return nil, errors.New("hotel package ticket product must use one ticket code per package unit")
	}
	var hotel model.HotelProperty
	query = tx.Where("id = ? AND tenant_id = ?", row.HotelID, tenantID)
	if requireSellable {
		query = query.Where("status = ?", "active")
	}
	if err := query.First(&hotel).Error; err != nil {
		return nil, errors.New("package hotel is unavailable")
	}
	var room model.HotelRoomType
	query = tx.Where("id = ? AND tenant_id = ? AND hotel_id = ?", row.RoomTypeID, tenantID, row.HotelID)
	if requireSellable {
		query = query.Where("status = ?", "active")
	}
	if err := query.First(&room).Error; err != nil {
		return nil, errors.New("package room type is unavailable")
	}
	var rate model.HotelRatePlan
	query = tx.Where("id = ? AND tenant_id = ? AND hotel_id = ? AND room_type_id = ?", row.RatePlanID, tenantID, row.HotelID, row.RoomTypeID)
	if requireSellable {
		query = query.Where("status = ?", "active")
	}
	if err := query.First(&rate).Error; err != nil {
		return nil, errors.New("package rate plan is unavailable")
	}
	if row.HotelSettlementPriceCents+moneyCents(product.SettlementPrice) > moneyCents(product.Price) {
		return nil, errors.New("package settlement allocation exceeds its retail price")
	}
	return &scenicHotelPackageFacts{Package: *row, Hotel: hotel, RoomType: room, RatePlan: rate}, nil
}

func loadSellableScenicHotelPackageTx(tx *gorm.DB, tenantID, productID uint, useDate *time.Time) (*scenicHotelPackageFacts, error) {
	var row model.ScenicHotelPackage
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND product_id = ?", tenantID, productID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.Status != "online" {
		return nil, errors.New("scenic hotel package is offline")
	}
	if useDate == nil && row.BookingMode != "after_purchase" {
		return nil, errors.New("scenic hotel package requires a check-in date")
	}
	if err := requireActiveScenicSupplier(tx, tenantID); err != nil {
		return nil, err
	}
	if err := requireActiveHotelSupplier(tx, tenantID); err != nil {
		return nil, err
	}
	return validateScenicHotelPackageFactsTx(tx, tenantID, &row, true)
}

func reserveHotelPackageInventoryTx(tx *gorm.DB, facts *scenicHotelPackageFacts, packageQuantity int, checkIn time.Time) error {
	rooms := facts.Package.RoomsPerPackage * packageQuantity
	for offset := 0; offset < facts.Package.Nights; offset++ {
		stayDate := dateOnly(checkIn.AddDate(0, 0, offset))
		var inventory model.HotelRoomInventory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND stay_date = ?", facts.Package.TenantID, facts.Package.HotelID, facts.Package.RoomTypeID, stayDate).First(&inventory).Error; err != nil {
			return fmt.Errorf("hotel inventory is not configured for %s", stayDate.Format("2006-01-02"))
		}
		if inventory.Closed || inventory.Capacity-inventory.Reserved-inventory.Sold < rooms {
			return fmt.Errorf("insufficient hotel rooms for %s", stayDate.Format("2006-01-02"))
		}
		if err := tx.Model(&inventory).Update("reserved", inventory.Reserved+rooms).Error; err != nil {
			return err
		}
	}
	return nil
}

func createHotelPackageReservationsTx(tx *gorm.DB, order *model.Order, item *model.OrderItem, facts *scenicHotelPackageFacts) error {
	if len(item.Tickets) != item.Quantity {
		return errors.New("package ticket quantity does not match package quantity")
	}
	checkIn := dateOnly(*item.UseDate)
	checkOut := checkIn.AddDate(0, 0, facts.Package.Nights)
	for index := range item.Tickets {
		row := model.HotelReservation{ReservationNo: generateHotelReservationNo(), SalesTenantID: order.TenantID, SupplierTenantID: facts.Package.TenantID, OrderID: order.ID, OrderItemID: item.ID, TicketID: item.Tickets[index].ID, PackageID: facts.Package.ID, HotelID: facts.Package.HotelID, RoomTypeID: facts.Package.RoomTypeID, RatePlanID: facts.Package.RatePlanID, HotelName: facts.Hotel.Name, RoomTypeName: facts.RoomType.Name, RatePlanName: facts.RatePlan.Name, CheckInDate: checkIn, CheckOutDate: checkOut, Rooms: facts.Package.RoomsPerPackage, SettlementPriceCents: facts.Package.HotelSettlementPriceCents, Status: "reserved"}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func generateHotelPackageEntitlementNo() string {
	buffer := make([]byte, 5)
	if _, err := rand.Read(buffer); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return fmt.Sprintf("PKG%d%s", time.Now().UnixMilli(), strings.ToUpper(hex.EncodeToString(buffer)))
}

func transitionHotelReservationInventoryTx(tx *gorm.DB, reservation *model.HotelReservation, target string) error {
	if reservation.Status == target {
		return nil
	}
	if target == "confirmed" && reservation.Status != "reserved" {
		return errors.New("hotel reservation cannot be confirmed from its current status")
	}
	if target == "cancelled" && reservation.Status != "reserved" && reservation.Status != "confirmed" {
		return errors.New("hotel reservation cannot be cancelled from its current status")
	}
	if target == "refunded" && reservation.Status != "confirmed" && reservation.Status != "checked_in" && reservation.Status != "checked_out" && reservation.Status != "no_show" {
		return errors.New("hotel reservation cannot be refunded from its current status")
	}
	for stay := dateOnly(reservation.CheckInDate); stay.Before(dateOnly(reservation.CheckOutDate)); stay = stay.AddDate(0, 0, 1) {
		var inventory model.HotelRoomInventory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND stay_date = ?", reservation.SupplierTenantID, reservation.HotelID, reservation.RoomTypeID, stay).First(&inventory).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{}
		switch target {
		case "confirmed":
			if inventory.Reserved < reservation.Rooms {
				return errors.New("hotel reserved inventory projection is invalid")
			}
			updates["reserved"], updates["sold"] = inventory.Reserved-reservation.Rooms, inventory.Sold+reservation.Rooms
		case "cancelled":
			if reservation.Status == "confirmed" {
				if inventory.Sold < reservation.Rooms {
					return errors.New("hotel sold inventory projection is invalid")
				}
				updates["sold"] = inventory.Sold - reservation.Rooms
			} else {
				if inventory.Reserved < reservation.Rooms {
					return errors.New("hotel reserved inventory projection is invalid")
				}
				updates["reserved"] = inventory.Reserved - reservation.Rooms
			}
		case "refunded":
			if inventory.Sold < reservation.Rooms {
				return errors.New("hotel sold inventory projection is invalid")
			}
			updates["sold"] = inventory.Sold - reservation.Rooms
		}
		if err := tx.Model(&inventory).Updates(updates).Error; err != nil {
			return err
		}
	}
	if err := tx.Model(reservation).Update("status", target).Error; err != nil {
		return err
	}
	reservation.Status = target
	return nil
}

func transitionOrderHotelReservationsTx(tx *gorm.DB, orderID uint, from, target string) error {
	var rows []model.HotelReservation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ? AND status = ?", orderID, from).Order("id ASC").Find(&rows).Error; err != nil {
		return err
	}
	for i := range rows {
		if err := transitionHotelReservationInventoryTx(tx, &rows[i], target); err != nil {
			return err
		}
	}
	return nil
}

func (PackageFulfillmentLifecycle) CancelOrder(tx *gorm.DB, orderID uint, includeConfirmed bool) error {
	var entitlements []model.ScenicHotelPackageEntitlement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ?", orderID).Order("id ASC").Find(&entitlements).Error; err != nil {
		return err
	}
	for i := range entitlements {
		if entitlements[i].Status == "booking_pending" || entitlements[i].Status == "cancel_pending" {
			return errors.New("package booking operation is in progress; order cannot be cancelled")
		}
	}
	var rows []model.HotelReservation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ?", orderID).Order("id ASC").Find(&rows).Error; err != nil {
		return err
	}
	for i := range rows {
		switch rows[i].Status {
		case "cancelled":
			continue
		case "reserved":
		case "confirmed":
			if !includeConfirmed {
				return errors.New("confirmed hotel reservation must use the refund workflow")
			}
		default:
			return errors.New("hotel reservation has entered fulfillment and cannot be cancelled")
		}
	}
	for i := range rows {
		if rows[i].Status == "cancelled" {
			continue
		}
		if err := transitionHotelReservationInventoryTx(tx, &rows[i], "cancelled"); err != nil {
			return err
		}
	}
	return tx.Model(&model.ScenicHotelPackageEntitlement{}).Where("order_id = ? AND status IN ?", orderID, []string{"pending_booking", "booked"}).Update("status", "cancelled").Error
}

func (PackageFulfillmentLifecycle) ConfirmOrder(tx *gorm.DB, orderID uint) error {
	return (PackageFulfillmentLifecycle{}).ConfirmOrderAt(tx, orderID, time.Now())
}

func (PackageFulfillmentLifecycle) ConfirmOrderAt(tx *gorm.DB, orderID uint, paidAt time.Time) error {
	if err := transitionOrderHotelReservationsTx(tx, orderID, "reserved", "confirmed"); err != nil {
		return err
	}
	var entitlements []model.ScenicHotelPackageEntitlement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ? AND status = ?", orderID, "pending_booking").Order("id ASC").Find(&entitlements).Error; err != nil {
		return err
	}
	validFrom := paidAt
	if validFrom.IsZero() {
		validFrom = time.Now()
	}
	for i := range entitlements {
		var packageRow model.ScenicHotelPackage
		if err := tx.Where("id = ? AND tenant_id = ? AND booking_mode = ?", entitlements[i].PackageID, entitlements[i].SupplierTenantID, "after_purchase").First(&packageRow).Error; err != nil {
			return errors.New("package booking configuration is unavailable")
		}
		validUntil := time.Date(validFrom.Year(), validFrom.Month(), validFrom.Day(), 23, 59, 59, 0, validFrom.Location()).AddDate(0, 0, packageRow.VoucherValidityDays-1)
		if err := tx.Model(&entitlements[i]).Updates(map[string]interface{}{"valid_from": validFrom, "valid_until": validUntil}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (PackageFulfillmentLifecycle) RescheduleSelected(tx *gorm.DB, tickets []model.Ticket, targetDate time.Time) error {
	if len(tickets) == 0 {
		return nil
	}
	targetDate = dateOnly(targetDate)
	legacyTicketIDs := make([]uint, 0, len(tickets))
	for i := range tickets {
		var entitlement model.ScenicHotelPackageEntitlement
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("ticket_id = ?", tickets[i].ID).First(&entitlement).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			legacyTicketIDs = append(legacyTicketIDs, tickets[i].ID)
			continue
		}
		if err != nil {
			return err
		}
		if entitlement.Status != "booked" || entitlement.ReservationID == 0 {
			return errors.New("package entitlement has no active booking to reschedule")
		}
		if strings.TrimSpace(entitlement.PlatformBookID) != "" {
			return errors.New("platform package booking must be rescheduled through its booking channel")
		}
		now := time.Now()
		if now.Before(entitlement.ValidFrom) || now.After(entitlement.ValidUntil) {
			return errors.New("package entitlement is outside its booking validity")
		}
		var packageRow model.ScenicHotelPackage
		if err := tx.Where("id = ? AND tenant_id = ? AND booking_mode = ?", entitlement.PackageID, entitlement.SupplierTenantID, "after_purchase").First(&packageRow).Error; err != nil {
			return errors.New("package booking configuration is unavailable")
		}
		if entitlement.RescheduleCount >= packageRow.MaxReschedules {
			return errors.New("package entitlement has reached its reschedule limit")
		}
		if targetDate.Before(dateOnly(now).AddDate(0, 0, packageRow.MinAdvanceDays)) {
			return fmt.Errorf("package booking requires at least %d days advance", packageRow.MinAdvanceDays)
		}
		if targetDate.AddDate(0, 0, packageRow.Nights).After(dateOnly(entitlement.ValidUntil).AddDate(0, 0, 1)) {
			return errors.New("package stay is outside voucher validity")
		}
		var reservation model.HotelReservation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND ticket_id = ?", entitlement.ReservationID, tickets[i].ID).First(&reservation).Error; err != nil {
			return err
		}
		if reservation.Status != "reserved" && reservation.Status != "confirmed" {
			return errors.New("hotel reservation has entered fulfillment and cannot be rescheduled")
		}
		projection := "reserved"
		if reservation.Status == "confirmed" {
			projection = "sold"
		}
		if err := moveHotelReservationInventoryTx(tx, &reservation, targetDate, packageRow.Nights, projection); err != nil {
			return err
		}
		if err := tx.Model(&entitlement).Update("reschedule_count", entitlement.RescheduleCount+1).Error; err != nil {
			return err
		}
	}
	if len(legacyTicketIDs) == 0 {
		return nil
	}
	var rows []model.HotelReservation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("ticket_id IN ?", legacyTicketIDs).Order("id ASC").Find(&rows).Error; err != nil {
		return err
	}
	for i := range rows {
		reservation := &rows[i]
		if reservation.Status != "reserved" && reservation.Status != "confirmed" {
			return errors.New("hotel reservation has entered fulfillment and cannot be rescheduled")
		}
		nights := int(dateOnly(reservation.CheckOutDate).Sub(dateOnly(reservation.CheckInDate)).Hours() / 24)
		if nights < 1 {
			return errors.New("hotel reservation stay duration is invalid")
		}
		projection := "reserved"
		if reservation.Status == "confirmed" {
			projection = "sold"
		}
		if err := moveHotelReservationInventoryTx(tx, reservation, dateOnly(targetDate), nights, projection); err != nil {
			return err
		}
	}
	return nil
}

func (PackageFulfillmentLifecycle) AssertRefundSupported(tx *gorm.DB, selected map[string]*model.Ticket, allowFulfilledException bool) error {
	ids := selectedHotelTicketIDs(selected)
	if len(ids) == 0 {
		return nil
	}
	var entitlements []model.ScenicHotelPackageEntitlement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("ticket_id IN ?", ids).Order("id ASC").Find(&entitlements).Error; err != nil {
		return err
	}
	for i := range entitlements {
		if entitlements[i].Status == "booking_pending" || entitlements[i].Status == "cancel_pending" {
			return errors.New("package booking operation is in progress; retry the refund after it finishes")
		}
	}
	var lockedTickets []model.Ticket
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", ids).Order("id ASC").Find(&lockedTickets).Error; err != nil {
		return err
	}
	var reservations []model.HotelReservation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("ticket_id IN ?", ids).Order("id ASC").Find(&reservations).Error; err != nil {
		return err
	}
	for i := range reservations {
		if (reservations[i].Status == "checked_in" || reservations[i].Status == "checked_out" || reservations[i].Status == "no_show") && !allowFulfilledException {
			return errors.New("hotel reservation has entered fulfillment; refund requires the supplier initial administrator exception with a reason")
		}
	}
	return nil
}

func (lifecycle PackageFulfillmentLifecycle) RefundSelected(tx *gorm.DB, selected map[string]*model.Ticket, allowFulfilledException bool) error {
	if err := lifecycle.AssertRefundSupported(tx, selected, allowFulfilledException); err != nil {
		return err
	}
	if err := refundSelectedHotelReservationsTx(tx, selected, allowFulfilledException); err != nil {
		return err
	}
	ids := selectedHotelTicketIDs(selected)
	if len(ids) == 0 {
		return nil
	}
	return tx.Model(&model.ScenicHotelPackageEntitlement{}).Where("ticket_id IN ? AND status IN ?", ids, []string{"pending_booking", "booked", "cancelled"}).Updates(map[string]interface{}{
		"status":               "refunded",
		"platform_sync_status": gorm.Expr("CASE WHEN platform_book_id <> '' THEN 'pending' ELSE platform_sync_status END"),
	}).Error
}

func (PackageFulfillmentLifecycle) ExpirePendingEntitlements(now time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var ids []uint
	if err := model.DB.Model(&model.ScenicHotelPackageEntitlement{}).
		Joins("JOIN orders ON orders.id = scenic_hotel_package_entitlements.order_id AND orders.tenant_id = scenic_hotel_package_entitlements.sales_tenant_id").
		Where("scenic_hotel_package_entitlements.status = ? AND scenic_hotel_package_entitlements.valid_until < ?", "pending_booking", now).
		Where("orders.status IN ?", []string{"paid", "completed", "partial_refunded"}).
		Order("scenic_hotel_package_entitlements.valid_until ASC, scenic_hotel_package_entitlements.id ASC").Limit(limit).
		Pluck("scenic_hotel_package_entitlements.id", &ids).Error; err != nil {
		return 0, err
	}
	expired := 0
	for _, id := range ids {
		err := model.Write(func(tx *gorm.DB) error {
			var entitlement model.ScenicHotelPackageEntitlement
			if err := tx.Where("id = ?", id).First(&entitlement).Error; err != nil {
				return err
			}
			var order model.Order
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND status IN ?", entitlement.OrderID, entitlement.SalesTenantID, []string{"paid", "completed", "partial_refunded"}).First(&order).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ? AND valid_until < ?", id, "pending_booking", now).First(&entitlement).Error; err != nil {
				return err
			}
			var ticket model.Ticket
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ? AND pending_refund_id = 0", entitlement.TicketID, "pending_booking").First(&ticket).Error; err != nil {
				return err
			}
			var item model.OrderItem
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", ticket.OrderItemID).First(&item).Error; err != nil {
				return err
			}
			var product model.Product
			if err := tx.Unscoped().Where("id = ? AND tenant_id = ?", item.FulfillmentProductID, item.FulfillmentTenantID).First(&product).Error; err != nil {
				return err
			}
			if ticket.Environment != "sandbox" {
				if err := releaseStock(tx, stockProductForRelease(&product, item.ReservedStockType), item.UseDate, item.StockSlot, 1); err != nil {
					return err
				}
			}
			if err := tx.Model(&ticket).Update("status", "expired").Error; err != nil {
				return err
			}
			if err := tx.Model(&model.TicketEntitlement{}).Where("ticket_id = ?", ticket.ID).Update("status", "void").Error; err != nil {
				return err
			}
			return tx.Model(&entitlement).Update("status", "expired").Error
		})
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return expired, err
		}
		expired++
	}
	return expired, nil
}

func selectedHotelTicketIDs(selected map[string]*model.Ticket) []uint {
	ids := make([]uint, 0, len(selected))
	for _, ticket := range selected {
		ids = append(ids, ticket.ID)
	}
	return ids
}

func refundSelectedHotelReservationsTx(tx *gorm.DB, selected map[string]*model.Ticket, allowFulfilledException bool) error {
	if len(selected) == 0 {
		return nil
	}
	ids := selectedHotelTicketIDs(selected)
	statuses := []string{"confirmed"}
	if allowFulfilledException {
		statuses = append(statuses, "checked_in", "checked_out", "no_show")
	}
	var rows []model.HotelReservation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("ticket_id IN ? AND status IN ?", ids, statuses).Order("id ASC").Find(&rows).Error; err != nil {
		return err
	}
	for i := range rows {
		if err := transitionHotelReservationInventoryTx(tx, &rows[i], "refunded"); err != nil {
			return err
		}
	}
	return nil
}

func moveHotelReservationInventoryTx(tx *gorm.DB, reservation *model.HotelReservation, targetCheckIn time.Time, nights int, projection string) error {
	for stay := dateOnly(reservation.CheckInDate); stay.Before(dateOnly(reservation.CheckOutDate)); stay = stay.AddDate(0, 0, 1) {
		var inventory model.HotelRoomInventory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND stay_date = ?", reservation.SupplierTenantID, reservation.HotelID, reservation.RoomTypeID, stay).First(&inventory).Error; err != nil {
			return err
		}
		current := inventory.Reserved
		if projection == "sold" {
			current = inventory.Sold
		}
		if current < reservation.Rooms {
			return errors.New("hotel inventory projection is invalid during reschedule")
		}
		if err := tx.Model(&inventory).Update(projection, current-reservation.Rooms).Error; err != nil {
			return err
		}
	}
	for offset := 0; offset < nights; offset++ {
		stay := targetCheckIn.AddDate(0, 0, offset)
		var inventory model.HotelRoomInventory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND stay_date = ?", reservation.SupplierTenantID, reservation.HotelID, reservation.RoomTypeID, stay).First(&inventory).Error; err != nil {
			return fmt.Errorf("hotel inventory is not configured for %s", stay.Format("2006-01-02"))
		}
		if inventory.Closed || inventory.Capacity-inventory.Reserved-inventory.Sold < reservation.Rooms {
			return fmt.Errorf("insufficient hotel rooms for %s", stay.Format("2006-01-02"))
		}
		current := inventory.Reserved
		if projection == "sold" {
			current = inventory.Sold
		}
		if err := tx.Model(&inventory).Update(projection, current+reservation.Rooms).Error; err != nil {
			return err
		}
	}
	checkOut := targetCheckIn.AddDate(0, 0, nights)
	if err := tx.Model(reservation).Updates(map[string]interface{}{"check_in_date": targetCheckIn, "check_out_date": checkOut}).Error; err != nil {
		return err
	}
	reservation.CheckInDate, reservation.CheckOutDate = targetCheckIn, checkOut
	return nil
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, value.Location())
}

func generateHotelReservationNo() string {
	random := make([]byte, 5)
	if _, err := rand.Read(random); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return fmt.Sprintf("HTL%d%s", time.Now().UnixMilli(), strings.ToUpper(hex.EncodeToString(random)))
}
