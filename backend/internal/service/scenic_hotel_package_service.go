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

type ScenicHotelPackageInput struct {
	ProductID                 uint   `json:"product_id"`
	HotelID                   uint   `json:"hotel_id"`
	RoomTypeID                uint   `json:"room_type_id"`
	RatePlanID                uint   `json:"rate_plan_id"`
	Nights                    int    `json:"nights"`
	RoomsPerPackage           int    `json:"rooms_per_package"`
	HotelSettlementPriceCents int64  `json:"hotel_settlement_price_cents"`
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
}

type scenicHotelPackageFacts struct {
	Package  model.ScenicHotelPackage
	Hotel    model.HotelProperty
	RoomType model.HotelRoomType
	RatePlan model.HotelRatePlan
}

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
	row := model.ScenicHotelPackage{TenantID: tenantID, ProductID: input.ProductID, HotelID: input.HotelID, RoomTypeID: input.RoomTypeID, RatePlanID: input.RatePlanID, Nights: input.Nights, RoomsPerPackage: input.RoomsPerPackage, HotelSettlementPriceCents: input.HotelSettlementPriceCents, Status: input.Status}
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
		if err := tx.Where("id = ? AND tenant_id = ?", packageID, tenantID).First(&row).Error; err != nil {
			return err
		}
		candidate := row
		candidate.ProductID, candidate.HotelID, candidate.RoomTypeID, candidate.RatePlanID = input.ProductID, input.HotelID, input.RoomTypeID, input.RatePlanID
		candidate.Nights, candidate.RoomsPerPackage, candidate.HotelSettlementPriceCents, candidate.Status = input.Nights, input.RoomsPerPackage, input.HotelSettlementPriceCents, input.Status
		var reservationCount int64
		if err := tx.Model(&model.HotelReservation{}).Where("package_id = ?", row.ID).Count(&reservationCount).Error; err != nil {
			return err
		}
		if reservationCount > 0 && (row.ProductID != candidate.ProductID || row.HotelID != candidate.HotelID || row.RoomTypeID != candidate.RoomTypeID || row.RatePlanID != candidate.RatePlanID || row.Nights != candidate.Nights || row.RoomsPerPackage != candidate.RoomsPerPackage || row.HotelSettlementPriceCents != candidate.HotelSettlementPriceCents) {
			return errors.New("package has orders; only its sale status can be changed")
		}
		if _, err := validateScenicHotelPackageFactsTx(tx, tenantID, &candidate, input.Status == "online"); err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]interface{}{"product_id": input.ProductID, "hotel_id": input.HotelID, "room_type_id": input.RoomTypeID, "rate_plan_id": input.RatePlanID, "nights": input.Nights, "rooms_per_package": input.RoomsPerPackage, "hotel_settlement_price_cents": input.HotelSettlementPriceCents, "status": input.Status}).Error; err != nil {
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
		if err := tx.Where("id = ? AND tenant_id = ?", packageID, tenantID).First(&row).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.HotelReservation{}).Where("package_id = ?", row.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("package has orders and cannot be deleted; take it offline instead")
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

func (s *ScenicHotelPackageService) ListReservations(tenantID uint, status, orderNo string, page, pageSize int) (*HotelReservationPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize != 10 && pageSize != 20 && pageSize != 40 {
		pageSize = 20
	}
	query := model.DB.Table("hotel_reservations AS reservation").
		Joins("JOIN orders ON orders.id = reservation.order_id AND orders.tenant_id = reservation.sales_tenant_id").
		Where("reservation.supplier_tenant_id = ? AND reservation.deleted_at IS NULL AND orders.deleted_at IS NULL", tenantID)
	if status = strings.TrimSpace(status); status != "" {
		query = query.Where("reservation.status = ?", status)
	}
	if orderNo = strings.TrimSpace(orderNo); orderNo != "" {
		query = query.Where("orders.order_no ILIKE ?", "%"+orderNo+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []HotelReservationView
	if err := query.Select("reservation.*, orders.order_no, orders.contact_name AS guest_name, orders.contact_phone").
		Order("reservation.created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return &HotelReservationPage{Data: rows, Total: total, Page: page, PageSize: pageSize}, nil
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
	return &ScenicHotelPackageView{ScenicHotelPackage: *row, ProductName: product.Name, RetailPriceCents: moneyCents(product.Price), TicketSettlementPriceCents: moneyCents(product.SettlementPrice), HotelName: facts.Hotel.Name, RoomTypeName: facts.RoomType.Name, RatePlanName: facts.RatePlan.Name, ReservationCount: reservationCount}, nil
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
	if useDate == nil {
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

func transitionHotelReservationInventoryTx(tx *gorm.DB, reservation *model.HotelReservation, target string) error {
	if reservation.Status == target {
		return nil
	}
	if target == "confirmed" && reservation.Status != "reserved" {
		return errors.New("hotel reservation cannot be confirmed from its current status")
	}
	if target == "cancelled" && reservation.Status != "reserved" {
		return errors.New("hotel reservation cannot be cancelled from its current status")
	}
	if target == "refunded" && reservation.Status != "confirmed" {
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
			if inventory.Reserved < reservation.Rooms {
				return errors.New("hotel reserved inventory projection is invalid")
			}
			updates["reserved"] = inventory.Reserved - reservation.Rooms
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

func refundSelectedHotelReservationsTx(tx *gorm.DB, selected map[string]*model.Ticket) error {
	if len(selected) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(selected))
	for _, ticket := range selected {
		ids = append(ids, ticket.ID)
	}
	var rows []model.HotelReservation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("ticket_id IN ? AND status = ?", ids, "confirmed").Order("id ASC").Find(&rows).Error; err != nil {
		return err
	}
	for i := range rows {
		if err := transitionHotelReservationInventoryTx(tx, &rows[i], "refunded"); err != nil {
			return err
		}
	}
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
