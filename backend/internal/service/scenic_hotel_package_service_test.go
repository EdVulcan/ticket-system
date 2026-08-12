package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type packageFixture struct {
	tenantID    uint
	productID   uint
	hotel       model.HotelProperty
	room        model.HotelRoomType
	rate        model.HotelRatePlan
	packageView *ScenicHotelPackageView
	checkIn     time.Time
}

func seedScenicHotelPackage(t *testing.T, capacity int) packageFixture {
	t.Helper()
	tenantID, productID := seedSellableProduct(t, "daily", 20)
	if err := model.DB.Create(&model.SupplierBusinessType{TenantID: tenantID, BusinessType: "hotel", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Product{}).Where("id = ?", productID).Updates(map[string]interface{}{"name": "门票加住宿套餐", "price": 499.0, "settlement_price": 80.0}).Error; err != nil {
		t.Fatal(err)
	}
	hotelService := &HotelService{}
	hotel, err := hotelService.CreateProperty(tenantID, 1, HotelPropertyInput{Code: "PKG-HOTEL", Name: "云门山酒店"})
	if err != nil {
		t.Fatal(err)
	}
	room, err := hotelService.CreateRoomType(tenantID, hotel.ID, 1, HotelRoomTypeInput{Code: "QUEEN", Name: "山景大床房", MaxGuests: 2})
	if err != nil {
		t.Fatal(err)
	}
	rate, err := hotelService.CreateRatePlan(tenantID, hotel.ID, room.ID, 1, HotelRatePlanInput{Code: "BREAKFAST", Name: "含双早", RetailPriceCents: 39900, SettlementPriceCents: 30000, BreakfastCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	checkIn := dateOnly(time.Now().AddDate(0, 0, 10))
	items := []HotelInventoryInput{}
	for offset := 0; offset < 2; offset++ {
		items = append(items, HotelInventoryInput{StayDate: checkIn.AddDate(0, 0, offset).Format("2006-01-02"), Capacity: capacity})
	}
	if err := hotelService.SetInventory(tenantID, hotel.ID, room.ID, 1, items); err != nil {
		t.Fatal(err)
	}
	packageView, err := (&ScenicHotelPackageService{}).Create(tenantID, 1, ScenicHotelPackageInput{ProductID: productID, HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID, Nights: 2, RoomsPerPackage: 1, HotelSettlementPriceCents: 30000, Status: "online"})
	if err != nil {
		t.Fatal(err)
	}
	return packageFixture{tenantID: tenantID, productID: productID, hotel: *hotel, room: *room, rate: *rate, packageView: packageView, checkIn: checkIn}
}

func loadPackageInventory(t *testing.T, fixture packageFixture) []model.HotelRoomInventory {
	t.Helper()
	var rows []model.HotelRoomInventory
	if err := model.DB.Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ?", fixture.tenantID, fixture.hotel.ID, fixture.room.ID).Order("stay_date ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestScenicHotelPackageOrderPaymentAndPartialRefund(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 3)
	useDate := fixture.checkIn
	order := model.Order{TenantID: fixture.tenantID, Channel: "online", ContactName: "测试游客", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 2, UseDate: &useDate}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if order.TotalAmount != 998 {
		t.Fatalf("package total=%v, want 998", order.TotalAmount)
	}
	var reservations []model.HotelReservation
	if err := model.DB.Where("order_id = ?", order.ID).Order("id ASC").Find(&reservations).Error; err != nil {
		t.Fatal(err)
	}
	if len(reservations) != 2 || reservations[0].Status != "reserved" || reservations[0].CheckOutDate.Sub(reservations[0].CheckInDate) != 48*time.Hour {
		t.Fatalf("reservations=%+v", reservations)
	}
	page, err := (&ScenicHotelPackageService{}).ListReservations(fixture.tenantID, fixture.hotel.ID, "reserved", order.OrderNo, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Data) != 2 || page.Data[0].OrderNo != order.OrderNo || page.Data[0].GuestName != "测试游客" || page.Data[0].ContactPhone != "13800138000" {
		t.Fatalf("reservation page=%+v", page)
	}
	packageInput := ScenicHotelPackageInput{ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID, RatePlanID: fixture.rate.ID, Nights: 3, RoomsPerPackage: 1, HotelSettlementPriceCents: 30000, Status: "online"}
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, packageInput); err == nil {
		t.Fatal("package fulfillment facts changed after reservations existed")
	}
	packageInput.Nights, packageInput.Status = 2, "offline"
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, packageInput); err != nil {
		t.Fatalf("take package offline after sale: %v", err)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.Reserved != 2 || row.Sold != 0 {
			t.Fatalf("reserved inventory=%+v", row)
		}
	}

	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.Reserved != 0 || row.Sold != 2 {
			t.Fatalf("confirmed inventory=%+v", row)
		}
	}

	err = model.Write(func(tx *gorm.DB) error {
		var stored model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Product").Preload("Items.Tickets").Where("id = ?", order.ID).First(&stored).Error; err != nil {
			return err
		}
		selected := map[string]*model.Ticket{stored.Items[0].Tickets[0].TicketCode: &stored.Items[0].Tickets[0]}
		return applyRefundBusinessFactsTx(tx, &stored, &model.Refund{RefundNo: "PKG-REFUND-1"}, selected)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.Reserved != 0 || row.Sold != 1 {
			t.Fatalf("refunded inventory=%+v", row)
		}
	}
	if err := model.DB.Where("order_id = ? AND status = ?", order.ID, "refunded").First(&model.HotelReservation{}).Error; err != nil {
		t.Fatal(err)
	}
	summary, err := (&ScenicHotelPackageService{}).BusinessSummary(fixture.tenantID, fixture.hotel.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.PackageUnits != 2 || summary.RefundedUnits != 1 || summary.GrossSalesCents != 99800 || summary.RefundedSalesCents != 49900 || summary.NetSalesCents != 49900 || summary.TicketComponentNetCents != 8000 || summary.HotelComponentNetCents != 30000 || summary.UnallocatedMarginCents != 11900 {
		t.Fatalf("package business summary=%+v", summary)
	}
}

func TestHotelReservationFulfillmentStatusIsAudited(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	useDate := fixture.checkIn
	order := model.Order{TenantID: fixture.tenantID, Channel: "online", ContactName: "入住游客", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	var reservation model.HotelReservation
	if err := model.DB.Where("order_id = ?", order.ID).First(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	service := &ScenicHotelPackageService{}
	if err := service.SetReservationStatus(fixture.tenantID, reservation.ID, 1, "no_show", ""); err == nil {
		t.Fatal("no-show without reason was accepted")
	}
	if err := service.SetReservationStatus(fixture.tenantID, reservation.ID, 1, "no_show", "客人确认不入住"); err != nil {
		t.Fatal(err)
	}
	if err := service.SetReservationStatus(fixture.tenantID, reservation.ID, 1, "confirmed", "客人恢复行程"); err != nil {
		t.Fatal(err)
	}
	if err := service.SetReservationStatus(fixture.tenantID, reservation.ID, 1, "checked_in", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.SetReservationStatus(fixture.tenantID, reservation.ID, 1, "checked_out", ""); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("id = ?", reservation.ID).First(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	if reservation.Status != "checked_out" || reservation.CheckedInAt == nil || reservation.CheckedOutAt == nil || reservation.NoShowAt != nil {
		t.Fatalf("reservation fulfillment=%+v", reservation)
	}
	var audits int64
	if err := model.DB.Model(&model.AuditLog{}).Where("tenant_id = ? AND action = ? AND target_id = ?", fixture.tenantID, "hotel.reservation.status", reservation.ID).Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 4 {
		t.Fatalf("fulfillment audits=%d, want 4", audits)
	}
}

func TestScenicHotelPackageRescheduleMovesTicketAndHotelInventory(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	target := fixture.checkIn.AddDate(0, 0, 5)
	if err := (&HotelService{}).SetInventory(fixture.tenantID, fixture.hotel.ID, fixture.room.ID, 1, []HotelInventoryInput{{StayDate: target.Format("2006-01-02"), Capacity: 1}, {StayDate: target.AddDate(0, 0, 1).Format("2006-01-02"), Capacity: 1}}); err != nil {
		t.Fatal(err)
	}
	useDate := fixture.checkIn
	order := model.Order{TenantID: fixture.tenantID, Channel: "online", ContactName: "改期游客", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Joins("JOIN order_items ON order_items.id = tickets.order_item_id").Where("order_items.order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	codes, _ := json.Marshal([]string{ticket.TicketCode})
	err := model.Write(func(tx *gorm.DB) error {
		return executeRescheduleTx(tx, &model.AfterSaleRequest{TenantID: fixture.tenantID, OrderNo: order.OrderNo, TicketCodesJSON: string(codes), TargetDate: &target})
	})
	if err != nil {
		t.Fatal(err)
	}
	var reservation model.HotelReservation
	if err := model.DB.Where("ticket_id = ?", ticket.ID).First(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	if reservation.CheckInDate.Format("2006-01-02") != target.Format("2006-01-02") || reservation.CheckOutDate.Format("2006-01-02") != target.AddDate(0, 0, 2).Format("2006-01-02") {
		t.Fatalf("rescheduled reservation=%+v", reservation)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.StayDate.Before(target) && row.Sold != 0 {
			t.Fatalf("old hotel inventory not released: %+v", row)
		}
		if !row.StayDate.Before(target) && row.Sold != 1 {
			t.Fatalf("target hotel inventory not sold: %+v", row)
		}
	}
}

func TestScenicHotelPackageCancelAndOversellAreAtomic(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	useDate := fixture.checkIn
	missingContact := model.Order{TenantID: fixture.tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}}}
	if err := (&OrderService{}).Create(&missingContact); err == nil || !strings.Contains(err.Error(), "guest name") {
		t.Fatalf("missing hotel guest contact error=%v", err)
	}
	order := model.Order{TenantID: fixture.tenantID, Channel: "online", ContactName: "测试游客", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 2, UseDate: &useDate}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	over := model.Order{TenantID: fixture.tenantID, Channel: "online", ContactName: "测试游客", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}}}
	if err := (&OrderService{}).Create(&over); err == nil {
		t.Fatal("hotel package oversell was accepted")
	}
	if err := (&OrderService{}).Cancel(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.Reserved != 0 || row.Sold != 0 {
			t.Fatalf("cancelled inventory=%+v", row)
		}
	}
	var cancelled int64
	if err := model.DB.Model(&model.HotelReservation{}).Where("order_id = ? AND status = ?", order.ID, "cancelled").Count(&cancelled).Error; err != nil {
		t.Fatal(err)
	}
	if cancelled != 2 {
		t.Fatalf("cancelled reservations=%d, want 2", cancelled)
	}
}

func TestScenicHotelPackageRequiresBothSupplierVerticals(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	_, err := (&ScenicHotelPackageService{}).Create(tenantID, 1, ScenicHotelPackageInput{ProductID: productID, HotelID: 1, RoomTypeID: 1, RatePlanID: 1, Nights: 1, RoomsPerPackage: 1, Status: "offline"})
	if !errors.Is(err, ErrSupplierBusinessTypeInactive) {
		t.Fatalf("missing hotel vertical error=%v", err)
	}
	if _, err := (&ScenicHotelPackageService{}).Get(tenantID, 999); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing package error=%v", err)
	}
}

func TestScenicHotelPackageCanBeRecreatedAfterSoftDelete(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	service := &ScenicHotelPackageService{}
	if err := (&ProductService{}).Delete(fixture.productID, fixture.tenantID); err == nil {
		t.Fatal("package product was deleted while still referenced")
	}
	if err := (&HotelService{}).DeleteRatePlan(fixture.tenantID, fixture.hotel.ID, fixture.room.ID, fixture.rate.ID, 1); err == nil {
		t.Fatal("package rate plan was deleted while still referenced")
	}
	if err := service.Delete(fixture.tenantID, fixture.packageView.ID, 1); err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(fixture.tenantID, 1, ScenicHotelPackageInput{ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID, RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1, HotelSettlementPriceCents: 30000, Status: "online"})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == fixture.packageView.ID {
		t.Fatalf("recreated package reused deleted id %d", created.ID)
	}
}
