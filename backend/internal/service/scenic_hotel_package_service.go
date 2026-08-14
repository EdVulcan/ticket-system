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

func (s *ScenicHotelPackageService) ListReservations(tenantID, hotelID uint, status, orderNo string, page, pageSize int) (*HotelReservationPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize != 10 && pageSize != 20 && pageSize != 40 {
		pageSize = 20
	}
	query := hotelReservationViewQuery(tenantID).
		Where("reservation.deleted_at IS NULL AND orders.deleted_at IS NULL")
	if hotelID != 0 {
		query = query.Where("reservation.hotel_id = ?", hotelID)
	}
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
	if err := scanHotelReservationViews(query, (page-1)*pageSize, pageSize, &rows); err != nil {
		return nil, err
	}
	return &HotelReservationPage{Data: rows, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *ScenicHotelPackageService) ListEntitlements(tenantID, hotelID uint, status, orderNo string, page, pageSize int) (*PackageEntitlementPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize != 10 && pageSize != 20 && pageSize != 40 {
		pageSize = 20
	}
	query := model.DB.Table("scenic_hotel_package_entitlements AS entitlement").
		Joins("JOIN scenic_hotel_packages AS package ON package.id = entitlement.package_id AND package.tenant_id = entitlement.supplier_tenant_id").
		Joins("JOIN orders ON orders.id = entitlement.order_id AND orders.tenant_id = entitlement.sales_tenant_id").
		Joins("JOIN order_items AS item ON item.id = entitlement.order_item_id AND item.order_id = entitlement.order_id").
		Joins("JOIN hotel_properties AS hotel ON hotel.id = package.hotel_id AND hotel.tenant_id = package.tenant_id").
		Joins("JOIN hotel_room_types AS room ON room.id = package.room_type_id AND room.hotel_id = package.hotel_id").
		Where("entitlement.supplier_tenant_id = ? AND entitlement.deleted_at IS NULL AND orders.status IN ?", tenantID, []string{"paid", "completed", "partial_refunded"})
	if hotelID != 0 {
		query = query.Where("package.hotel_id = ?", hotelID)
	}
	if status = strings.TrimSpace(status); status != "" {
		query = query.Where("entitlement.status = ?", status)
	}
	if orderNo = strings.TrimSpace(orderNo); orderNo != "" {
		query = query.Where("orders.order_no ILIKE ?", "%"+orderNo+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []PackageEntitlementView
	if err := query.Select("entitlement.*, orders.order_no, item.product_name, hotel.name AS hotel_name, room.name AS room_type_name").
		Order("entitlement.valid_until ASC, entitlement.created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return &PackageEntitlementPage{Data: rows, Total: total, Page: page, PageSize: pageSize}, nil
}

func hotelReservationViewQuery(tenantID uint) *gorm.DB {
	return model.DB.Table("hotel_reservations AS reservation").
		Joins("JOIN orders ON orders.id = reservation.order_id AND orders.tenant_id = reservation.sales_tenant_id").
		Joins("JOIN order_items ON order_items.id = reservation.order_item_id AND order_items.order_id = reservation.order_id").
		Joins("JOIN tickets ON tickets.id = reservation.ticket_id AND tickets.order_item_id = reservation.order_item_id").
		Where("reservation.supplier_tenant_id = ?", tenantID)
}

func scanHotelReservationViews(query *gorm.DB, offset, limit int, rows *[]HotelReservationView) error {
	return query.Select("reservation.*, orders.order_no, COALESCE(NULLIF(tickets.visitor_name, ''), orders.contact_name) AS guest_name, COALESCE(NULLIF(tickets.visitor_phone, ''), orders.contact_phone) AS contact_phone, order_items.product_name, tickets.ticket_code").
		Order("reservation.check_in_date ASC, reservation.created_at DESC").Offset(offset).Limit(limit).Scan(rows).Error
}

func (s *ScenicHotelPackageService) ExportReservations(tenantID, hotelID uint, status, orderNo string) ([]HotelReservationView, error) {
	query := hotelReservationViewQuery(tenantID).Where("reservation.deleted_at IS NULL AND orders.deleted_at IS NULL")
	if hotelID != 0 {
		query = query.Where("reservation.hotel_id = ?", hotelID)
	}
	if status = strings.TrimSpace(status); status != "" {
		query = query.Where("reservation.status = ?", status)
	}
	if orderNo = strings.TrimSpace(orderNo); orderNo != "" {
		query = query.Where("orders.order_no ILIKE ?", "%"+orderNo+"%")
	}
	var rows []HotelReservationView
	if err := scanHotelReservationViews(query, 0, 10000, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *ScenicHotelPackageService) BusinessSummary(tenantID, hotelID uint, startDate, endDate string) (*HotelPackageBusinessSummary, error) {
	start, end, err := hotelReportWindow(startDate, endDate)
	if err != nil {
		return nil, err
	}
	var result HotelPackageBusinessSummary
	salesQuery := `
		WITH paid_orders AS (
			SELECT orders.id,
			       COALESCE(MAX(COALESCE(payments.paid_at, payments.created_at)), orders.created_at) AS sold_at
			FROM orders
			LEFT JOIN payments ON payments.tenant_id = orders.tenant_id
			 AND payments.order_no = orders.order_no
			 AND payments.status IN ('paid','partial_refunded','refunded')
			 AND payments.purpose IN ('','order')
			WHERE orders.deleted_at IS NULL AND orders.environment = 'production'
			  AND orders.status IN ('paid','completed','partial_refunded','refunded')
			GROUP BY orders.id, orders.created_at
		), package_facts AS (
			SELECT entitlement.order_item_id, entitlement.package_id
			FROM scenic_hotel_package_entitlements AS entitlement
			WHERE entitlement.supplier_tenant_id = ? AND entitlement.deleted_at IS NULL
			UNION
			SELECT reservation.order_item_id, reservation.package_id
			FROM hotel_reservations AS reservation
			WHERE reservation.supplier_tenant_id = ? AND reservation.deleted_at IS NULL
		), package_sales AS (
			SELECT item.id AS order_item_id, item.quantity, item.price, item.settlement_price,
			       item.product_revision_id, item.fulfillment_product_id, item.fulfillment_tenant_id,
			       package.hotel_settlement_price_cents,
			       COALESCE(ticket.refunded_units, 0) AS refunded_units
			FROM paid_orders AS paid
			JOIN order_items AS item ON item.order_id = paid.id AND item.deleted_at IS NULL
			JOIN package_facts AS fact ON fact.order_item_id = item.id
			JOIN scenic_hotel_packages AS package
			  ON package.id = fact.package_id
			 AND package.tenant_id = item.fulfillment_tenant_id
			LEFT JOIN LATERAL (
				SELECT COUNT(*) FILTER (WHERE tickets.status = 'refunded') AS refunded_units
				FROM tickets WHERE tickets.order_item_id = item.id AND tickets.deleted_at IS NULL
			) AS ticket ON TRUE
			WHERE item.fulfillment_tenant_id = ? AND paid.sold_at >= ? AND paid.sold_at < ?
			  AND (? = 0 OR package.hotel_id = ?)
		)
		SELECT
			COALESCE(SUM(sale.quantity), 0) AS sales_units,
			COALESCE(SUM(sale.quantity), 0) AS package_units,
			COALESCE(SUM(sale.refunded_units), 0) AS refunded_units,
			COALESCE(SUM(CAST(ROUND(sale.price * 100.0) AS BIGINT) * sale.quantity), 0) AS gross_sales_cents,
			COALESCE(SUM(CAST(ROUND(sale.price * 100.0) AS BIGINT) * sale.refunded_units), 0) AS refunded_sales_cents,
			COALESCE(SUM(CAST(ROUND(sale.price * 100.0) AS BIGINT) * (sale.quantity - sale.refunded_units)), 0) AS net_sales_cents,
			COALESCE(SUM(COALESCE(revision.settlement_cents, CAST(ROUND(sale.settlement_price * 100.0) AS BIGINT)) * (sale.quantity - sale.refunded_units)), 0) AS ticket_component_net_cents,
			COALESCE(SUM(sale.hotel_settlement_price_cents * (sale.quantity - sale.refunded_units)), 0) AS hotel_component_net_cents
		FROM package_sales AS sale
		LEFT JOIN product_revisions AS revision ON revision.id = sale.product_revision_id
		 AND revision.product_id = sale.fulfillment_product_id AND revision.tenant_id = sale.fulfillment_tenant_id`
	if err := model.DB.Raw(salesQuery, tenantID, tenantID, tenantID, start, end, hotelID, hotelID).Scan(&result).Error; err != nil {
		return nil, err
	}
	reservationQuery := `
		SELECT
			COUNT(*) AS booking_units,
			COUNT(*) FILTER (WHERE reservation.status = 'reserved') AS pending_units,
			COUNT(*) FILTER (WHERE reservation.status = 'confirmed') AS confirmed_units,
			COUNT(*) FILTER (WHERE reservation.status = 'checked_in') AS checked_in_units,
			COUNT(*) FILTER (WHERE reservation.status = 'checked_out') AS checked_out_units,
			COUNT(*) FILTER (WHERE reservation.status = 'no_show') AS no_show_units
		FROM hotel_reservations AS reservation
		WHERE reservation.supplier_tenant_id = ? AND reservation.deleted_at IS NULL
		  AND reservation.created_at >= ? AND reservation.created_at < ?
		  AND (? = 0 OR reservation.hotel_id = ?)`
	var bookingFacts struct {
		BookingUnits    int64
		PendingUnits    int64
		ConfirmedUnits  int64
		CheckedInUnits  int64
		CheckedOutUnits int64
		NoShowUnits     int64
	}
	if err := model.DB.Raw(reservationQuery, tenantID, start, end, hotelID, hotelID).Scan(&bookingFacts).Error; err != nil {
		return nil, err
	}
	result.BookingUnits, result.PendingUnits, result.ConfirmedUnits = bookingFacts.BookingUnits, bookingFacts.PendingUnits, bookingFacts.ConfirmedUnits
	result.CheckedInUnits, result.CheckedOutUnits, result.NoShowUnits = bookingFacts.CheckedInUnits, bookingFacts.CheckedOutUnits, bookingFacts.NoShowUnits
	stayQuery := `
		SELECT COUNT(*) AS stay_units
		FROM hotel_reservations AS reservation
		WHERE reservation.supplier_tenant_id = ? AND reservation.deleted_at IS NULL
		  AND reservation.status NOT IN ('cancelled','refunded')
		  AND reservation.check_in_date >= ? AND reservation.check_in_date < ?
		  AND (? = 0 OR reservation.hotel_id = ?)`
	var stayFacts struct{ StayUnits int64 }
	if err := model.DB.Raw(stayQuery, tenantID, start, end, hotelID, hotelID).Scan(&stayFacts).Error; err != nil {
		return nil, err
	}
	result.StayUnits = stayFacts.StayUnits
	if err := model.DB.Table("scenic_hotel_package_entitlements AS entitlement").
		Joins("JOIN scenic_hotel_packages AS package ON package.id = entitlement.package_id").
		Joins("JOIN orders ON orders.id = entitlement.order_id AND orders.tenant_id = entitlement.sales_tenant_id").
		Where("entitlement.supplier_tenant_id = ? AND entitlement.status = ? AND orders.status IN ?", tenantID, "pending_booking", []string{"paid", "completed", "partial_refunded"}).
		Where(`COALESCE((
			SELECT MAX(COALESCE(payment.paid_at, payment.created_at))
			FROM payments AS payment
			WHERE payment.tenant_id = orders.tenant_id AND payment.order_no = orders.order_no
			  AND payment.status IN ('paid','partial_refunded','refunded')
			  AND payment.purpose IN ('','order')
		), orders.created_at) >= ?`, start).
		Where(`COALESCE((
			SELECT MAX(COALESCE(payment.paid_at, payment.created_at))
			FROM payments AS payment
			WHERE payment.tenant_id = orders.tenant_id AND payment.order_no = orders.order_no
			  AND payment.status IN ('paid','partial_refunded','refunded')
			  AND payment.purpose IN ('','order')
		), orders.created_at) < ?`, end).
		Where("? = 0 OR package.hotel_id = ?", hotelID, hotelID).Distinct("entitlement.id").Count(&result.AwaitingBookingUnits).Error; err != nil {
		return nil, err
	}
	result.UnallocatedMarginCents = result.NetSalesCents - result.TicketComponentNetCents - result.HotelComponentNetCents
	return &result, nil
}

func hotelReportWindow(startDate, endDate string) (time.Time, time.Time, error) {
	start := dateOnly(time.Now().AddDate(0, 0, -29))
	end := dateOnly(time.Now()).AddDate(0, 0, 1)
	var err error
	if strings.TrimSpace(startDate) != "" {
		start, err = time.ParseInLocation("2006-01-02", strings.TrimSpace(startDate), time.Local)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("start date is invalid")
		}
	}
	if strings.TrimSpace(endDate) != "" {
		parsed, parseErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(endDate), time.Local)
		if parseErr != nil {
			return time.Time{}, time.Time{}, errors.New("end date is invalid")
		}
		end = parsed.AddDate(0, 0, 1)
	}
	if !start.Before(end) || end.Sub(start) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, errors.New("report date range must be between 1 and 366 days")
	}
	return start, end, nil
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
