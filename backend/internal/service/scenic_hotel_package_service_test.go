package service

import (
	"context"
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

func TestScenicHotelPackageBusinessSummaryUsesSaleTimeTicketSettlementForCtrip(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	account := model.ChannelAccount{TenantID: fixture.tenantID, Code: "ctrip-package-summary", Type: "ctrip", Status: "active", Environment: "production"}
	if err := model.DB.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{
		ChannelAccountID: account.ID,
		ProductID:        fixture.productID,
		ExternalCode:     "CTRIP-PACKAGE-SUMMARY",
		Status:           "active",
		ChannelSaleCents: 45000,
		ChannelCostCents: 35000,
	}
	if err := model.DB.Create(&mapping).Error; err != nil {
		t.Fatal(err)
	}

	useDate := fixture.checkIn
	externalNo := "CTRIP-PACKAGE-SUMMARY-ORDER"
	order := model.Order{
		TenantID: fixture.tenantID, Channel: "ctrip", ChannelAccountID: account.ID, ExternalNo: &externalNo,
		ContactName: "Ctrip package guest", ContactPhone: "13800138000",
		Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}},
	}
	orders := &OrderService{}
	if err := orders.Create(&order); err != nil {
		t.Fatal(err)
	}
	if got := moneyCents(order.Items[0].SettlementPrice); got != mapping.ChannelCostCents {
		t.Fatalf("Ctrip order item settlement=%d, want channel package settlement %d", got, mapping.ChannelCostCents)
	}
	if order.Items[0].ProductRevisionID == 0 {
		t.Fatal("Ctrip package order did not retain its sale-time product revision")
	}
	var saleRevision model.ProductRevision
	if err := model.DB.First(&saleRevision, order.Items[0].ProductRevisionID).Error; err != nil {
		t.Fatal(err)
	}
	if saleRevision.SettlementCents != 8000 {
		t.Fatalf("sale revision ticket settlement=%d, want 8000", saleRevision.SettlementCents)
	}
	if err := orders.MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}

	var product model.Product
	if err := model.DB.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Where("id = ? AND tenant_id = ?", fixture.productID, fixture.tenantID).First(&product).Error; err != nil {
		t.Fatal(err)
	}
	product.SettlementPrice = 100
	if err := (&ProductService{}).Update(product.ID, fixture.tenantID, &product, &product.Rule); err != nil {
		t.Fatal(err)
	}
	var revised model.Product
	if err := model.DB.First(&revised, fixture.productID).Error; err != nil {
		t.Fatal(err)
	}
	if revised.CurrentRevisionID == order.Items[0].ProductRevisionID {
		t.Fatal("product price change did not create a new revision")
	}
	var currentRevision model.ProductRevision
	if err := model.DB.First(&currentRevision, revised.CurrentRevisionID).Error; err != nil {
		t.Fatal(err)
	}
	if currentRevision.SettlementCents != 10000 {
		t.Fatalf("current revision ticket settlement=%d, want 10000", currentRevision.SettlementCents)
	}

	summary, err := (&ScenicHotelPackageService{}).BusinessSummary(fixture.tenantID, fixture.hotel.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.PackageUnits != 1 || summary.GrossSalesCents != 45000 || summary.NetSalesCents != 45000 || summary.TicketComponentNetCents != 8000 || summary.HotelComponentNetCents != 30000 || summary.UnallocatedMarginCents != 7000 {
		t.Fatalf("Ctrip package business summary=%+v", summary)
	}

	if err := model.DB.Model(&model.OrderItem{}).Where("id = ?", order.Items[0].ID).Update("product_revision_id", 0).Error; err != nil {
		t.Fatal(err)
	}
	fallback, err := (&ScenicHotelPackageService{}).BusinessSummary(fixture.tenantID, fixture.hotel.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if fallback.TicketComponentNetCents != mapping.ChannelCostCents {
		t.Fatalf("legacy Ctrip package ticket settlement fallback=%d, want item settlement %d", fallback.TicketComponentNetCents, mapping.ChannelCostCents)
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

func TestPaidChannelPackageCancellationReleasesConfirmedHotelInventory(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	account := model.ChannelAccount{TenantID: fixture.tenantID, Code: "package-cancel-channel", Type: "test", Status: "active", Environment: "production"}
	if err := model.DB.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	useDate := fixture.checkIn
	externalNo := "PACKAGE-CHANNEL-CANCEL-1"
	order := model.Order{
		TenantID: fixture.tenantID, Channel: "ota", ChannelAccountID: account.ID, ExternalNo: &externalNo,
		ContactName: "Channel guest", ContactPhone: "13800138000",
		Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}},
	}
	orders := &OrderService{}
	if err := orders.Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := orders.MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	if err := orders.CancelChannelOrder(order.OrderNo, fixture.tenantID, account.ID); err != nil {
		t.Fatal(err)
	}

	var reservation model.HotelReservation
	if err := model.DB.Where("order_id = ?", order.ID).First(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	if reservation.Status != "cancelled" {
		t.Fatalf("hotel reservation status=%q, want cancelled", reservation.Status)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.Reserved != 0 || row.Sold != 0 {
			t.Fatalf("cancelled paid channel hotel inventory=%+v", row)
		}
	}
}

func TestPaidChannelPackageCancellationRejectsFulfilledHotelReservation(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	account := model.ChannelAccount{TenantID: fixture.tenantID, Code: "fulfilled-package-cancel-channel", Type: "test", Status: "active", Environment: "production"}
	if err := model.DB.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	useDate := fixture.checkIn
	externalNo := "FULFILLED-PACKAGE-CHANNEL-CANCEL-1"
	order := model.Order{
		TenantID: fixture.tenantID, Channel: "ota", ChannelAccountID: account.ID, ExternalNo: &externalNo,
		ContactName: "Stayed channel guest", ContactPhone: "13800138000",
		Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}},
	}
	orders := &OrderService{}
	if err := orders.Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := orders.MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	var reservation model.HotelReservation
	if err := model.DB.Where("order_id = ?", order.ID).First(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&ScenicHotelPackageService{}).SetReservationStatus(fixture.tenantID, reservation.ID, 1, "no_show", "hotel confirmed guest did not arrive"); err != nil {
		t.Fatal(err)
	}

	err := orders.CancelChannelOrder(order.OrderNo, fixture.tenantID, account.ID)
	if err == nil || !strings.Contains(err.Error(), "entered fulfillment") {
		t.Fatalf("fulfilled package channel cancellation error=%v", err)
	}
	if err := model.DB.First(&order, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != "paid" {
		t.Fatalf("rejected channel cancellation changed order status=%q", order.Status)
	}
	if err := model.DB.First(&reservation, reservation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reservation.Status != "no_show" {
		t.Fatalf("rejected channel cancellation changed hotel status=%q", reservation.Status)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.Reserved != 0 || row.Sold != 1 {
			t.Fatalf("rejected channel cancellation changed hotel inventory=%+v", row)
		}
	}
}

func TestScenicHotelPackageExchangeFailsClosedBeforeChangingFulfillment(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	targetID := cloneExchangeTarget(t, fixture.productID, 499, 80)
	useDate := fixture.checkIn
	order := model.Order{TenantID: fixture.tenantID, Channel: "online", ContactName: "Package guest", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	codes, _ := json.Marshal([]string{ticket.TicketCode})
	err := model.Write(func(tx *gorm.DB) error {
		return executeExchangeTx(tx, &model.AfterSaleRequest{TenantID: fixture.tenantID, OrderNo: order.OrderNo, TicketCodesJSON: string(codes), TargetProductID: targetID})
	})
	if err == nil || !strings.Contains(err.Error(), "hotel package") {
		t.Fatalf("package exchange error=%v", err)
	}
	var item model.OrderItem
	if err := model.DB.Where("order_id = ?", order.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.ProductID != fixture.productID {
		t.Fatalf("package order item changed to product %d", item.ProductID)
	}
	var reservation model.HotelReservation
	if err := model.DB.Where("ticket_id = ?", ticket.ID).First(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	if reservation.Status != "confirmed" || reservation.PackageID != fixture.packageView.ID {
		t.Fatalf("package reservation changed=%+v", reservation)
	}
}

func TestFulfilledHotelReservationCannotBeRescheduled(t *testing.T) {
	for _, status := range []string{"checked_in", "checked_out", "no_show"} {
		t.Run(status, func(t *testing.T) {
			resetBusinessData(t)
			fixture := seedScenicHotelPackage(t, 1)
			target := fixture.checkIn.AddDate(0, 0, 5)
			if err := (&HotelService{}).SetInventory(fixture.tenantID, fixture.hotel.ID, fixture.room.ID, 1, []HotelInventoryInput{{StayDate: target.Format("2006-01-02"), Capacity: 1}, {StayDate: target.AddDate(0, 0, 1).Format("2006-01-02"), Capacity: 1}}); err != nil {
				t.Fatal(err)
			}
			useDate := fixture.checkIn
			order := model.Order{TenantID: fixture.tenantID, Channel: "online", ContactName: "Fulfilled guest", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}}}
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
			fulfillment := &ScenicHotelPackageService{}
			switch status {
			case "checked_in":
				if err := fulfillment.SetReservationStatus(fixture.tenantID, reservation.ID, 1, "checked_in", ""); err != nil {
					t.Fatal(err)
				}
			case "checked_out":
				if err := fulfillment.SetReservationStatus(fixture.tenantID, reservation.ID, 1, "checked_in", ""); err != nil {
					t.Fatal(err)
				}
				if err := fulfillment.SetReservationStatus(fixture.tenantID, reservation.ID, 1, "checked_out", ""); err != nil {
					t.Fatal(err)
				}
			case "no_show":
				if err := fulfillment.SetReservationStatus(fixture.tenantID, reservation.ID, 1, "no_show", "guest did not arrive"); err != nil {
					t.Fatal(err)
				}
			}
			var ticket model.Ticket
			if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
				t.Fatal(err)
			}
			request := model.AfterSaleRequest{TenantID: fixture.tenantID, OrderNo: order.OrderNo, Type: "reschedule", IdempotencyKey: "fulfilled-reschedule-" + status, TargetDate: &target, OperatorID: 1, Reason: "guest requested date change"}
			service := &AfterSaleService{}
			if err := service.Create(&request, []string{ticket.TicketCode}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Approve(fixture.tenantID, request.ID, 2, "reviewed"); err != nil {
				t.Fatal(err)
			}
			failed, err := service.Execute(fixture.tenantID, request.ID, 2)
			if err != nil || failed.Status != "failed" || !strings.Contains(failed.ErrorMessage, "hotel reservation") {
				t.Fatalf("fulfilled hotel reschedule=%+v err=%v", failed, err)
			}
			var item model.OrderItem
			if err := model.DB.Where("order_id = ?", order.ID).First(&item).Error; err != nil {
				t.Fatal(err)
			}
			if item.UseDate == nil || item.UseDate.Format("2006-01-02") != fixture.checkIn.Format("2006-01-02") {
				t.Fatalf("ticket date changed after rejected reschedule: %v", item.UseDate)
			}
		})
	}
}

func TestFulfilledHotelReservationRefundRequiresInitialAdminException(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	initial := model.User{TenantID: fixture.tenantID, Username: "package-refund-initial", Password: "test", Role: "super_admin", IsInitialAdmin: true}
	ordinary := model.User{TenantID: fixture.tenantID, Username: "package-refund-ordinary", Password: "test", Role: "admin"}
	if err := model.DB.Create(&initial).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&ordinary).Error; err != nil {
		t.Fatal(err)
	}
	useDate := fixture.checkIn
	order := model.Order{TenantID: fixture.tenantID, Channel: "online", ContactName: "Stayed guest", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&PaymentService{}).CreatePayment(fixture.tenantID, &model.Payment{OrderNo: order.OrderNo, Method: "cash", OperatorID: ordinary.ID}); err != nil {
		t.Fatal(err)
	}
	var reservation model.HotelReservation
	if err := model.DB.Where("order_id = ?", order.ID).First(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	fulfillment := &ScenicHotelPackageService{}
	if err := fulfillment.SetReservationStatus(fixture.tenantID, reservation.ID, initial.ID, "checked_in", ""); err != nil {
		t.Fatal(err)
	}
	if err := fulfillment.SetReservationStatus(fixture.tenantID, reservation.ID, initial.ID, "checked_out", ""); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	refunds := &RefundService{}
	if _, err := refunds.CreateCashRefundAs(RefundActor{TenantID: fixture.tenantID, UserID: ordinary.ID}, order.OrderNo, "fulfilled-hotel-ordinary", order.TotalAmount, []string{ticket.TicketCode}, "guest request"); err == nil || !strings.Contains(err.Error(), "hotel reservation") {
		t.Fatalf("ordinary fulfilled hotel refund error=%v", err)
	}
	if _, err := refunds.CreateCashRefundAs(RefundActor{TenantID: fixture.tenantID, UserID: initial.ID, OverrideRefundPolicy: true}, order.OrderNo, "fulfilled-hotel-no-reason", order.TotalAmount, []string{ticket.TicketCode}, ""); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("reasonless fulfilled hotel refund error=%v", err)
	}
	refund, err := refunds.CreateCashRefundAs(RefundActor{TenantID: fixture.tenantID, UserID: initial.ID, OverrideRefundPolicy: true}, order.OrderNo, "fulfilled-hotel-initial", order.TotalAmount, []string{ticket.TicketCode}, "supplier verified exceptional refund")
	if err != nil {
		t.Fatal(err)
	}
	if !refund.AuthorizedPolicyOverride || refund.AuthorizedBy != initial.ID || refund.Status != "succeeded" {
		t.Fatalf("fulfilled hotel refund authorization=%+v", refund)
	}
	if err := model.DB.First(&reservation, reservation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reservation.Status != "refunded" {
		t.Fatalf("hotel reservation status=%q, want refunded", reservation.Status)
	}
}

func TestOrderCodeHotelPackageRejectsMultipleUnitsBeforeInventoryReservation(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	if err := model.DB.Model(&model.Product{}).Where("id = ?", fixture.productID).Update("code_mode", "order").Error; err != nil {
		t.Fatal(err)
	}
	useDate := fixture.checkIn
	order := model.Order{TenantID: fixture.tenantID, Channel: "online", ContactName: "Package guest", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 2, UseDate: &useDate}}}
	err := (&OrderService{}).Create(&order)
	if err == nil || !strings.Contains(err.Error(), "one ticket code per package unit") {
		t.Fatalf("order-code package quantity error=%v", err)
	}
	var orders int64
	if err := model.DB.Model(&model.Order{}).Where("tenant_id = ?", fixture.tenantID).Count(&orders).Error; err != nil {
		t.Fatal(err)
	}
	if orders != 0 {
		t.Fatalf("rejected order-code package persisted %d orders", orders)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.Reserved != 0 || row.Sold != 0 {
			t.Fatalf("rejected order-code package changed inventory=%+v", row)
		}
	}
}

func TestPendingDigitalRefundBlocksHotelFulfillmentStatusChange(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	useDate := fixture.checkIn
	order := model.Order{TenantID: fixture.tenantID, Channel: "online", ContactName: "Pending refund guest", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}}}
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
	if err := model.DB.Model(&model.Ticket{}).Where("id = ?", reservation.TicketID).Update("pending_refund_id", 987).Error; err != nil {
		t.Fatal(err)
	}
	err := (&ScenicHotelPackageService{}).SetReservationStatus(fixture.tenantID, reservation.ID, 1, "checked_in", "")
	if err == nil || !strings.Contains(err.Error(), "pending refund") {
		t.Fatalf("pending refund hotel fulfillment error=%v", err)
	}
	if err := model.DB.First(&reservation, reservation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reservation.Status != "confirmed" || reservation.CheckedInAt != nil {
		t.Fatalf("pending refund changed hotel fulfillment=%+v", reservation)
	}
}

func TestInactiveHotelSupplierCannotChangeReservationStatus(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	useDate := fixture.checkIn
	order := model.Order{TenantID: fixture.tenantID, Channel: "online", ContactName: "Hotel guest", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}}}
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
	if err := model.DB.Model(&model.SupplierBusinessType{}).Where("tenant_id = ? AND business_type = ?", fixture.tenantID, "hotel").Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	err := (&ScenicHotelPackageService{}).SetReservationStatus(fixture.tenantID, reservation.ID, 1, "checked_in", "")
	if !errors.Is(err, ErrSupplierBusinessTypeInactive) {
		t.Fatalf("inactive hotel supplier status change error=%v", err)
	}
}

func TestExistingHotelPackageProductCannotSwitchToOrderCodeMode(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	var product model.Product
	if err := model.DB.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Where("id = ? AND tenant_id = ?", fixture.productID, fixture.tenantID).First(&product).Error; err != nil {
		t.Fatal(err)
	}
	product.CodeMode = "order"
	err := (&ProductService{}).Update(product.ID, fixture.tenantID, &product, &product.Rule)
	if err == nil || !strings.Contains(err.Error(), "one ticket code per package unit") {
		t.Fatalf("existing package order-code update error=%v", err)
	}
	var stored model.Product
	if err := model.DB.First(&stored, product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CodeMode != "ticket" {
		t.Fatalf("rejected package code mode persisted as %q", stored.CodeMode)
	}
}

func TestFailedDigitalHotelRefundCannotRetryAfterFulfillment(t *testing.T) {
	for _, status := range []string{"checked_in", "checked_out", "no_show"} {
		t.Run(status, func(t *testing.T) {
			resetBusinessData(t)
			fixture := seedScenicHotelPackage(t, 1)
			useDate := fixture.checkIn
			order := model.Order{TenantID: fixture.tenantID, Channel: "online", ContactName: "Retry guest", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}}}
			if err := (&OrderService{}).Create(&order); err != nil {
				t.Fatal(err)
			}
			if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
				t.Fatal(err)
			}
			payment := model.Payment{TenantID: fixture.tenantID, OrderNo: order.OrderNo, PaymentNo: "PAY-HOTEL-RETRY-" + status, Amount: order.TotalAmount, AmountCents: moneyCents(order.TotalAmount), Method: "wechat", Status: "paid"}
			if err := model.DB.Create(&payment).Error; err != nil {
				t.Fatal(err)
			}
			var ticket model.Ticket
			if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
				t.Fatal(err)
			}
			refund, err := (&RefundService{}).CreateDigitalRefund(fixture.tenantID, order.OrderNo, "hotel-retry-"+status, order.TotalAmount, []string{ticket.TicketCode}, "guest request")
			if err != nil {
				t.Fatal(err)
			}
			providerCalls := 0
			worker := &RefundService{Provider: refundProviderFunc(func(context.Context, *model.Refund, *model.Payment) (RefundProviderResult, error) {
				providerCalls++
				return RefundProviderResult{Status: "failed", ProviderRefundID: "DECLINED-HOTEL-" + status}, nil
			})}
			if processed, err := worker.ProcessDigitalRefundTasks(context.Background(), time.Now().Add(time.Second), 1); err != nil || processed != 1 {
				t.Fatalf("initial failed refund processed=%d err=%v", processed, err)
			}
			var reservation model.HotelReservation
			if err := model.DB.Where("order_id = ?", order.ID).First(&reservation).Error; err != nil {
				t.Fatal(err)
			}
			fulfillment := &ScenicHotelPackageService{}
			switch status {
			case "checked_out":
				if err := fulfillment.SetReservationStatus(fixture.tenantID, reservation.ID, 1, "checked_in", ""); err != nil {
					t.Fatal(err)
				}
				if err := fulfillment.SetReservationStatus(fixture.tenantID, reservation.ID, 1, "checked_out", ""); err != nil {
					t.Fatal(err)
				}
			case "no_show":
				if err := fulfillment.SetReservationStatus(fixture.tenantID, reservation.ID, 1, "no_show", "guest did not arrive"); err != nil {
					t.Fatal(err)
				}
			default:
				if err := fulfillment.SetReservationStatus(fixture.tenantID, reservation.ID, 1, status, ""); err != nil {
					t.Fatal(err)
				}
			}
			var task model.DigitalRefundTask
			if err := model.DB.Where("refund_id = ?", refund.ID).First(&task).Error; err != nil {
				t.Fatal(err)
			}
			err = worker.RetryDigitalRefundTask(fixture.tenantID, task.ID, 1, "admin", "retry provider rejection")
			if err == nil || !strings.Contains(err.Error(), "hotel reservation") {
				t.Fatalf("fulfilled hotel refund retry error=%v", err)
			}
			if processed, err := worker.ProcessDigitalRefundTasks(context.Background(), time.Now().Add(time.Minute), 1); err != nil || processed != 0 {
				t.Fatalf("rejected retry processed=%d err=%v", processed, err)
			}
			if providerCalls != 1 {
				t.Fatalf("provider calls=%d, want only initial failed call", providerCalls)
			}
			if err := model.DB.First(&refund, refund.ID).Error; err != nil || refund.Status != "failed" {
				t.Fatalf("refund status=%q err=%v, want failed", refund.Status, err)
			}
			if err := model.DB.First(&task, task.ID).Error; err != nil || task.Status != "failed" {
				t.Fatalf("task status=%q err=%v, want failed", task.Status, err)
			}
			if err := model.DB.First(&ticket, ticket.ID).Error; err != nil || ticket.PendingRefundID != 0 {
				t.Fatalf("ticket pending refund=%d err=%v, want 0", ticket.PendingRefundID, err)
			}
		})
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
