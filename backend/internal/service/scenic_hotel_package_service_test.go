package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func seedDeferredPackageXiaohongshuAccount(t *testing.T, fixture packageFixture, code, status, environment string) model.ChannelAccount {
	t.Helper()
	account := model.ChannelAccount{TenantID: fixture.tenantID, Code: code, Type: "xiaohongshu", Status: status, Environment: environment}
	if err := model.DB.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.ChannelProductMapping{
		ChannelAccountID: account.ID, ProductID: fixture.productID, ExternalCode: strings.ToUpper(code),
		ChannelSaleCents: 49900, ChannelCostCents: 8000, Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	return account
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

	var refundedTicket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).Order("id ASC").First(&refundedTicket).Error; err != nil {
		t.Fatal(err)
	}
	paidAt := time.Now()
	payment := model.Payment{
		TenantID: fixture.tenantID, PaymentNo: "PAY-PKG-REFUND-1", OrderNo: order.OrderNo,
		Purpose: "order", Amount: order.TotalAmount, AmountCents: moneyCents(order.TotalAmount),
		Method: "cash", Status: "paid", PaidAt: &paidAt,
	}
	if err := model.DB.Create(&payment).Error; err != nil {
		t.Fatal(err)
	}
	refund := model.Refund{
		TenantID: fixture.tenantID, RefundNo: "PKG-REFUND-1", IdempotencyKey: "PKG-REFUND-1",
		OrderNo: order.OrderNo, PaymentID: payment.ID, Amount: 499, AmountCents: 49900,
		Method: "cash", Status: "succeeded", TicketCodesJSON: fmt.Sprintf(`["%s"]`, refundedTicket.TicketCode),
	}
	if err := model.DB.Create(&refund).Error; err != nil {
		t.Fatal(err)
	}
	err = model.Write(func(tx *gorm.DB) error {
		var stored model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Product").Preload("Items.Tickets").Where("id = ?", order.ID).First(&stored).Error; err != nil {
			return err
		}
		selected := map[string]*model.Ticket{stored.Items[0].Tickets[0].TicketCode: &stored.Items[0].Tickets[0]}
		return applyRefundBusinessFactsTx(tx, &stored, &refund, selected)
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

func TestDeferredScenicHotelPackageBooksAndReleasesOneEntitlement(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	input := ScenicHotelPackageInput{
		ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID,
		RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1,
		HotelSettlementPriceCents: 30000, BookingMode: "after_purchase",
		VoucherValidityDays: 90, MinAdvanceDays: 1, MaxReschedules: 1, Status: "online",
	}
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, input); err != nil {
		t.Fatal(err)
	}

	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-deferred-lifecycle", "active", "production")
	externalNo := "XHS-DEFERRED-LIFECYCLE"
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 2}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	var entitlements []model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).Order("id ASC").Find(&entitlements).Error; err != nil {
		t.Fatal(err)
	}
	if len(entitlements) != 2 || entitlements[0].Status != "pending_booking" || entitlements[1].Status != "pending_booking" {
		t.Fatalf("deferred entitlements=%+v", entitlements)
	}
	var tickets []model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).Order("id ASC").Find(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 2 || tickets[0].Status != "pending_booking" || tickets[1].Status != "pending_booking" {
		t.Fatalf("deferred tickets=%+v", tickets)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.Reserved != 0 || row.Sold != 0 {
			t.Fatalf("purchase unexpectedly occupied dated hotel inventory: %+v", row)
		}
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}

	err := model.Write(func(tx *gorm.DB) error {
		_, err := (PackageFulfillmentLifecycle{}).BookEntitlementTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: entitlements[0].EntitlementNo, CheckInDate: fixture.checkIn,
			GuestName: "预约游客", ContactPhone: "13800138000", ClientRequestID: "booking-1",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var booked model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("id = ?", entitlements[0].ID).First(&booked).Error; err != nil {
		t.Fatal(err)
	}
	if booked.Status != "booked" || booked.ReservationID == 0 || booked.RescheduleCount != 0 {
		t.Fatalf("booked entitlement=%+v", booked)
	}
	var bookedTicket model.Ticket
	if err := model.DB.First(&bookedTicket, booked.TicketID).Error; err != nil {
		t.Fatal(err)
	}
	if bookedTicket.Status != "unused" || bookedTicket.VisitorName != "预约游客" || bookedTicket.VisitorPhone != "13800138000" {
		t.Fatalf("booked ticket=%+v", bookedTicket)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.Reserved != 0 || row.Sold != 1 {
			t.Fatalf("booked inventory=%+v", row)
		}
	}

	if err := model.Write(func(tx *gorm.DB) error {
		_, err := (PackageFulfillmentLifecycle{}).CancelEntitlementBookingTx(tx, booked.EntitlementNo)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&booked, booked.ID).Error; err != nil {
		t.Fatal(err)
	}
	if booked.Status != "pending_booking" || booked.ReservationID != 0 || booked.BookingCancelledAt == nil {
		t.Fatalf("cancelled booking entitlement=%+v", booked)
	}
	if err := model.DB.First(&bookedTicket, bookedTicket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if bookedTicket.Status != "pending_booking" || bookedTicket.VisitorName != "" || bookedTicket.VisitorPhone != "" {
		t.Fatalf("cancelled booking ticket=%+v", bookedTicket)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.Reserved != 0 || row.Sold != 0 {
			t.Fatalf("cancelled booking inventory=%+v", row)
		}
	}

	err = model.Write(func(tx *gorm.DB) error {
		_, err := (PackageFulfillmentLifecycle{}).BookEntitlementTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: booked.EntitlementNo, CheckInDate: fixture.checkIn,
			GuestName: "重约游客", ContactPhone: "13900139000", ClientRequestID: "booking-2",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&booked, booked.ID).Error; err != nil {
		t.Fatal(err)
	}
	if booked.Status != "booked" || booked.RescheduleCount != 1 {
		t.Fatalf("rebooked entitlement=%+v", booked)
	}
	err = model.Write(func(tx *gorm.DB) error {
		_, err := (PackageFulfillmentLifecycle{}).CancelEntitlementBookingTx(tx, booked.EntitlementNo)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "no remaining reschedule") {
		t.Fatalf("cancel beyond reschedule limit error=%v", err)
	}
}

func TestDeferredPackagePartialAdmissionDoesNotCompleteOrderWithPendingEntitlement(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	input := ScenicHotelPackageInput{
		ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID,
		RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1,
		HotelSettlementPriceCents: 30000, BookingMode: "after_purchase",
		VoucherValidityDays: 90, MinAdvanceDays: 0, MaxReschedules: 1, Status: "online",
	}
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, input); err != nil {
		t.Fatal(err)
	}
	today := dateOnly(time.Now())
	if err := (&HotelService{}).SetInventory(fixture.tenantID, fixture.hotel.ID, fixture.room.ID, 1, []HotelInventoryInput{
		{StayDate: today.Format("2006-01-02"), Capacity: 2},
		{StayDate: today.AddDate(0, 0, 1).Format("2006-01-02"), Capacity: 2},
	}); err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-deferred-admission", "active", "production")
	externalNo := "XHS-DEFERRED-ADMISSION-ORDER"
	order := model.Order{
		TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo,
		Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 2}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	var entitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).Order("id ASC").First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := (PackageFulfillmentLifecycle{}).BookEntitlementTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: entitlement.EntitlementNo, CheckInDate: today,
			GuestName: "部分核销游客", ContactPhone: "13800138000", ClientRequestID: "partial-admission-booking",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.First(&ticket, entitlement.TicketID).Error; err != nil {
		t.Fatal(err)
	}
	var checkpoint model.CheckPoint
	if err := model.DB.Where("tenant_id = ?", fixture.tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&TicketService{}).Verify(ticket.TicketCode, checkpoint.ID, verificationDeviceID(t, fixture.tenantID, checkpoint.ID), fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	var stored model.Order
	if err := model.DB.First(&stored, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "paid" {
		t.Fatalf("partially admitted deferred order status=%q, want paid while another entitlement awaits booking", stored.Status)
	}
}

func TestDeferredPackageSalesRejectUnsupportedChannels(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	input := ScenicHotelPackageInput{
		ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID,
		RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1,
		HotelSettlementPriceCents: 30000, BookingMode: "after_purchase",
		VoucherValidityDays: 90, MinAdvanceDays: 0, MaxReschedules: 1, Status: "online",
	}
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, input); err != nil {
		t.Fatal(err)
	}
	type channelCase struct {
		name        string
		channel     string
		accountType string
	}
	for _, test := range []channelCase{
		{name: "window", channel: "window"},
		{name: "ctrip", channel: "ctrip:official", accountType: "ctrip"},
		{name: "generic ota", channel: "custom-ota", accountType: "custom"},
	} {
		t.Run(test.name, func(t *testing.T) {
			order := model.Order{TenantID: fixture.tenantID, Channel: test.channel, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1}}}
			if test.accountType != "" {
				account := model.ChannelAccount{TenantID: fixture.tenantID, Code: "unsupported-" + test.accountType, Type: test.accountType, Status: "active", Environment: "production"}
				if err := model.DB.Create(&account).Error; err != nil {
					t.Fatal(err)
				}
				order.ChannelAccountID = account.ID
				externalNo := "unsupported-" + test.accountType
				order.ExternalNo = &externalNo
				if test.accountType == "ctrip" {
					if err := model.DB.Create(&model.ChannelProductMapping{
						ChannelAccountID: account.ID, ProductID: fixture.productID, ExternalCode: "UNSUPPORTED-CTRIP",
						ChannelSaleCents: 49900, ChannelCostCents: 8000, Status: "active",
					}).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			err := (&OrderService{}).Create(&order)
			if err == nil || !strings.Contains(err.Error(), "xiaohongshu") {
				t.Fatalf("unsupported deferred package channel error=%v", err)
			}
		})
	}
}

func TestDeferredPackageSandboxCreatesEntitlementsWithoutProductionInventory(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	input := ScenicHotelPackageInput{
		ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID,
		RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1,
		HotelSettlementPriceCents: 30000, BookingMode: "after_purchase",
		VoucherValidityDays: 90, MinAdvanceDays: 0, MaxReschedules: 1, Status: "online",
	}
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, input); err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-deferred-sandbox", "sandbox", "sandbox")
	externalNo := "XHS-DEFERRED-SANDBOX"
	order := model.Order{
		TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo,
		Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 2}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	var entitlements []model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).Order("id ASC").Find(&entitlements).Error; err != nil {
		t.Fatal(err)
	}
	if len(entitlements) != 2 {
		t.Fatalf("sandbox deferred entitlements=%d, want 2", len(entitlements))
	}
	var tickets []model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).Order("id ASC").Find(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 2 || tickets[0].Status != "pending_booking" || tickets[0].Environment != "sandbox" || tickets[1].Status != "pending_booking" || tickets[1].Environment != "sandbox" {
		t.Fatalf("sandbox deferred tickets=%+v", tickets)
	}
	var product model.Product
	if err := model.DB.First(&product, fixture.productID).Error; err != nil {
		t.Fatal(err)
	}
	if product.DailyStock != 20 {
		t.Fatalf("sandbox deferred order changed production total stock=%d", product.DailyStock)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.Reserved != 0 || row.Sold != 0 {
			t.Fatalf("sandbox deferred order changed hotel inventory=%+v", row)
		}
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := (PackageFulfillmentLifecycle{}).BookEntitlementTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: entitlements[0].EntitlementNo, CheckInDate: fixture.checkIn,
			GuestName: "沙箱预约游客", ContactPhone: "13800138000", ClientRequestID: "sandbox-booking-1",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&product, fixture.productID).Error; err != nil {
		t.Fatal(err)
	}
	if product.DailyStock != 20 {
		t.Fatalf("sandbox booking changed production total stock=%d", product.DailyStock)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.Reserved != 0 || row.Sold != 0 {
			t.Fatalf("sandbox booking changed hotel inventory=%+v", row)
		}
	}
}

func TestDeferredPackageAfterSaleRescheduleUsesCurrentReservationAndRules(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	input := ScenicHotelPackageInput{
		ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID,
		RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1,
		HotelSettlementPriceCents: 30000, BookingMode: "after_purchase",
		VoucherValidityDays: 90, MinAdvanceDays: 0, MaxReschedules: 3, Status: "online",
	}
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, input); err != nil {
		t.Fatal(err)
	}
	target := fixture.checkIn.AddDate(0, 0, 5)
	if err := (&HotelService{}).SetInventory(fixture.tenantID, fixture.hotel.ID, fixture.room.ID, 1, []HotelInventoryInput{
		{StayDate: target.Format("2006-01-02"), Capacity: 2},
		{StayDate: target.AddDate(0, 0, 1).Format("2006-01-02"), Capacity: 2},
	}); err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-deferred-reschedule", "active", "production")
	externalNo := "XHS-DEFERRED-RESCHEDULE"
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	var entitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	book := func(requestID, guest string) {
		t.Helper()
		if err := model.Write(func(tx *gorm.DB) error {
			_, err := (PackageFulfillmentLifecycle{}).BookEntitlementTx(tx, PackageEntitlementBookingInput{
				EntitlementNo: entitlement.EntitlementNo, CheckInDate: fixture.checkIn,
				GuestName: guest, ContactPhone: "13800138000", ClientRequestID: requestID,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	book("deferred-reschedule-first", "首次预约")
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := (PackageFulfillmentLifecycle{}).CancelEntitlementBookingTx(tx, entitlement.EntitlementNo)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	book("deferred-reschedule-second", "再次预约")
	if err := model.DB.First(&entitlement, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.First(&ticket, entitlement.TicketID).Error; err != nil {
		t.Fatal(err)
	}
	codes, _ := json.Marshal([]string{ticket.TicketCode})
	if err := model.Write(func(tx *gorm.DB) error {
		return executeRescheduleTx(tx, &model.AfterSaleRequest{
			TenantID: fixture.tenantID, OrderNo: order.OrderNo, TicketCodesJSON: string(codes), TargetDate: &target,
		})
	}); err != nil {
		t.Fatalf("reschedule with cancelled booking history: %v", err)
	}
	if err := model.DB.First(&entitlement, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if entitlement.RescheduleCount != 2 {
		t.Fatalf("reschedule count=%d, want 2 after one rebooking and one direct reschedule", entitlement.RescheduleCount)
	}
	var current model.HotelReservation
	if err := model.DB.First(&current, entitlement.ReservationID).Error; err != nil {
		t.Fatal(err)
	}
	if current.CheckInDate.Format("2006-01-02") != target.Format("2006-01-02") || current.Status != "confirmed" {
		t.Fatalf("current reservation after reschedule=%+v", current)
	}
	var cancelled int64
	if err := model.DB.Model(&model.HotelReservation{}).Where("ticket_id = ? AND status = ?", ticket.ID, "cancelled").Count(&cancelled).Error; err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 {
		t.Fatalf("cancelled reservation history=%d, want 1", cancelled)
	}
}

func TestDeferredPackageBookingAndCancellationPhasesFreezeAdmissionAndInventory(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	input := ScenicHotelPackageInput{
		ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID,
		RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1,
		HotelSettlementPriceCents: 30000, BookingMode: "after_purchase",
		VoucherValidityDays: 90, MinAdvanceDays: 0, MaxReschedules: 2, Status: "online",
	}
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, input); err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-deferred-phases", "active", "production")
	externalNo := "XHS-DEFERRED-PHASES"
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	var entitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	lifecycle := PackageFulfillmentLifecycle{}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.PrepareBookingTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: entitlement.EntitlementNo, CheckInDate: fixture.checkIn,
			GuestName: "阶段预约游客", ContactPhone: "13800138000", ClientRequestID: "phase-book-1",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&entitlement, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.First(&ticket, entitlement.TicketID).Error; err != nil {
		t.Fatal(err)
	}
	if entitlement.Status != "booking_pending" || ticket.Status != "pending_booking" {
		t.Fatalf("prepared booking entitlement=%q ticket=%q", entitlement.Status, ticket.Status)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.PrepareBookingTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: entitlement.EntitlementNo, CheckInDate: fixture.checkIn,
			GuestName: "阶段预约游客", ContactPhone: "13800138000", ClientRequestID: "phase-book-1",
		})
		return err
	}); err != nil {
		t.Fatalf("idempotent prepare booking: %v", err)
	}
	for _, inventory := range loadPackageInventory(t, fixture) {
		if inventory.Reserved != 1 || inventory.Sold != 0 {
			t.Fatalf("prepared booking inventory=%+v", inventory)
		}
	}
	selected := map[string]*model.Ticket{ticket.TicketCode: &ticket}
	if err := model.Write(func(tx *gorm.DB) error { return lifecycle.AssertRefundSupported(tx, selected, false) }); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("refund during prepared booking error=%v", err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.FinalizeBookingTx(tx, entitlement.EntitlementNo, "XHS-BOOK-1")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.FinalizeBookingTx(tx, entitlement.EntitlementNo, "XHS-BOOK-1")
		return err
	}); err != nil {
		t.Fatalf("idempotent finalize booking: %v", err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.PrepareCancelTx(tx, entitlement.EntitlementNo)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.Status != "pending_booking" {
		t.Fatalf("prepared cancellation ticket=%q, want frozen pending_booking", ticket.Status)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.RollbackCancelTx(tx, entitlement.EntitlementNo)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.RollbackCancelTx(tx, entitlement.EntitlementNo)
		return err
	}); err != nil {
		t.Fatalf("idempotent rollback cancellation: %v", err)
	}
	if err := model.DB.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.Status != "unused" {
		t.Fatalf("rolled back cancellation ticket=%q, want unused", ticket.Status)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		if _, err := lifecycle.PrepareCancelTx(tx, entitlement.EntitlementNo); err != nil {
			return err
		}
		if _, err := lifecycle.PrepareCancelTx(tx, entitlement.EntitlementNo); err != nil {
			return err
		}
		_, err := lifecycle.FinalizeCancelTx(tx, entitlement.EntitlementNo)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := lifecycle.FinalizeCancelTx(tx, entitlement.EntitlementNo)
		return err
	}); err != nil {
		t.Fatalf("idempotent finalize cancellation: %v", err)
	}
	if err := model.DB.First(&entitlement, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if entitlement.Status != "pending_booking" || entitlement.ReservationID != 0 || entitlement.PlatformBookID != "" || ticket.Status != "pending_booking" {
		t.Fatalf("finalized cancellation entitlement=%+v ticket=%+v", entitlement, ticket)
	}
	for _, inventory := range loadPackageInventory(t, fixture) {
		if inventory.Reserved != 0 || inventory.Sold != 0 {
			t.Fatalf("finalized cancellation inventory=%+v", inventory)
		}
	}
}

func TestDeferredPackageExpiryVoidsGenericEntitlementProjection(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	input := ScenicHotelPackageInput{
		ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID,
		RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1,
		HotelSettlementPriceCents: 30000, BookingMode: "after_purchase",
		VoucherValidityDays: 1, MinAdvanceDays: 0, MaxReschedules: 1, Status: "online",
	}
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, input); err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-deferred-expiry", "active", "production")
	externalNo := "XHS-DEFERRED-EXPIRY"
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	var entitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().Add(-time.Hour)
	if err := model.DB.Model(&entitlement).Updates(map[string]interface{}{"valid_from": expiredAt.Add(-time.Hour), "valid_until": expiredAt}).Error; err != nil {
		t.Fatal(err)
	}
	count, err := (PackageFulfillmentLifecycle{}).ExpirePendingEntitlements(time.Now(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unpaid voucher expired count=%d, want 0", count)
	}
	if err := model.DB.First(&entitlement, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if entitlement.Status != "pending_booking" {
		t.Fatalf("unpaid voucher status=%q, want pending_booking", entitlement.Status)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	// Payment starts a fresh validity window; move it back again to exercise
	// the paid expiry transition below.
	if err := model.DB.Model(&entitlement).Updates(map[string]interface{}{"valid_from": expiredAt.Add(-time.Hour), "valid_until": expiredAt}).Error; err != nil {
		t.Fatal(err)
	}
	count, err = (PackageFulfillmentLifecycle{}).ExpirePendingEntitlements(time.Now(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expired count=%d, want 1", count)
	}
	var projection model.TicketEntitlement
	if err := model.DB.Where("ticket_id = ?", entitlement.TicketID).First(&projection).Error; err != nil {
		t.Fatal(err)
	}
	if projection.Status != "void" {
		t.Fatalf("expired generic entitlement status=%q, want void", projection.Status)
	}
}

func TestDeferredPackageVoucherValidityStartsWhenPaymentSucceeds(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	input := ScenicHotelPackageInput{
		ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID,
		RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1,
		HotelSettlementPriceCents: 30000, BookingMode: "after_purchase",
		VoucherValidityDays: 30, MinAdvanceDays: 0, MaxReschedules: 1, Status: "online",
	}
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, input); err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-deferred-payment-validity", "active", "production")
	externalNo := "XHS-DEFERRED-PAYMENT-VALIDITY"
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	var entitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	oldStart := time.Now().AddDate(0, 0, -20)
	oldEnd := oldStart.AddDate(0, 0, 29)
	if err := model.DB.Model(&entitlement).Updates(map[string]interface{}{"valid_from": oldStart, "valid_until": oldEnd}).Error; err != nil {
		t.Fatal(err)
	}
	paidAt := time.Now()
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&entitlement, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if entitlement.ValidFrom.Before(paidAt.Add(-time.Second)) || entitlement.ValidFrom.After(time.Now().Add(time.Second)) {
		t.Fatalf("voucher valid_from=%v, want payment confirmation time", entitlement.ValidFrom)
	}
	wantEnd := time.Date(entitlement.ValidFrom.Year(), entitlement.ValidFrom.Month(), entitlement.ValidFrom.Day(), 23, 59, 59, 0, entitlement.ValidFrom.Location()).AddDate(0, 0, 29)
	if !entitlement.ValidUntil.Equal(wantEnd) {
		t.Fatalf("voucher valid_until=%v, want %v", entitlement.ValidUntil, wantEnd)
	}
}

func TestDeferredPackageSoldRightSurvivesSupplierAndResourceSuspension(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	input := ScenicHotelPackageInput{
		ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID,
		RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1,
		HotelSettlementPriceCents: 30000, BookingMode: "after_purchase",
		VoucherValidityDays: 90, MinAdvanceDays: 0, MaxReschedules: 1, Status: "online",
	}
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, input); err != nil {
		t.Fatal(err)
	}
	soldStart := dateOnly(time.Now())
	soldEnd := fixture.checkIn.AddDate(0, 0, 20)
	if err := model.DB.Model(&model.Product{}).Where("id = ?", fixture.productID).Updates(map[string]interface{}{
		"validity_start_date": soldStart, "validity_end_date": soldEnd,
	}).Error; err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-deferred-suspended-obligation", "active", "production")
	externalNo := "XHS-DEFERRED-SUSPENDED-OBLIGATION"
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	var entitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	var soldItem model.OrderItem
	if err := model.DB.First(&soldItem, entitlement.OrderItemID).Error; err != nil {
		t.Fatal(err)
	}
	if !isVisitDateValid(&fixture.checkIn, soldItem.ValidityStart, soldItem.ValidityEnd) {
		t.Fatalf("fixture check-in %v is outside sale-time item validity %+v", fixture.checkIn, soldItem)
	}

	// A later catalog change may stop new sales but must not rewrite a paid
	// voucher's promise. Make the current product validity exclude check-in and
	// suspend both supplier verticals and all hotel catalog resources.
	excludedStart := fixture.checkIn.AddDate(0, 0, 20)
	excludedEnd := fixture.checkIn.AddDate(0, 0, 30)
	if err := model.DB.Model(&model.Product{}).Where("id = ?", fixture.productID).Updates(map[string]interface{}{
		"validity_start_date": excludedStart, "validity_end_date": excludedEnd, "status": "offline",
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, businessType := range []string{"scenic", "hotel"} {
		if err := model.DB.Model(&model.SupplierBusinessType{}).Where("tenant_id = ? AND business_type = ?", fixture.tenantID, businessType).Update("status", "suspended").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := model.DB.Model(&model.HotelProperty{}).Where("id = ?", fixture.hotel.ID).Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.HotelRoomType{}).Where("id = ?", fixture.room.ID).Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.HotelRatePlan{}).Where("id = ?", fixture.rate.ID).Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}

	if err := model.Write(func(tx *gorm.DB) error {
		_, err := (PackageFulfillmentLifecycle{}).BookEntitlementTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: entitlement.EntitlementNo, CheckInDate: fixture.checkIn,
			GuestName: "Suspended obligation guest", ContactPhone: "13800138000", ClientRequestID: "suspended-obligation-book",
		})
		return err
	}); err != nil {
		t.Fatalf("paid voucher could not book after catalog suspension: %v", err)
	}
	if err := model.DB.First(&entitlement, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	var reservation model.HotelReservation
	if err := model.DB.First(&reservation, entitlement.ReservationID).Error; err != nil {
		t.Fatal(err)
	}
	if reservation.Status != "confirmed" {
		t.Fatalf("suspended catalog booking reservation=%+v, want confirmed", reservation)
	}
	if err := (&ScenicHotelPackageService{}).SetReservationStatus(fixture.tenantID, reservation.ID, 1, "checked_in", ""); err != nil {
		t.Fatalf("existing reservation could not check in after suspension: %v", err)
	}
}

func TestDeferredPackageBusinessSummarySeparatesSaleBookingAndStayPeriods(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	input := ScenicHotelPackageInput{
		ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID,
		RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1,
		HotelSettlementPriceCents: 30000, BookingMode: "after_purchase",
		VoucherValidityDays: 90, MinAdvanceDays: 0, MaxReschedules: 1, Status: "online",
	}
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, input); err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-deferred-summary", "active", "production")
	externalNo := "XHS-DEFERRED-SUMMARY"
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	saleAt := dateOnly(time.Now().AddDate(0, 0, -50)).Add(12 * time.Hour)
	payment := model.Payment{
		TenantID: fixture.tenantID, PaymentNo: "PAY-DEFERRED-SUMMARY", OrderNo: order.OrderNo,
		Amount: order.TotalAmount, AmountCents: moneyCents(order.TotalAmount), Method: "xiaohongshu", Status: "paid", PaidAt: &saleAt,
	}
	if err := model.DB.Create(&payment).Error; err != nil {
		t.Fatal(err)
	}

	var entitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := (PackageFulfillmentLifecycle{}).BookEntitlementTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: entitlement.EntitlementNo, CheckInDate: fixture.checkIn,
			GuestName: "报表游客", ContactPhone: "13800138000", ClientRequestID: "deferred-summary-booking",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&entitlement, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	bookingAt := dateOnly(time.Now().AddDate(0, 0, -20)).Add(12 * time.Hour)
	if err := model.DB.Model(&model.HotelReservation{}).Where("id = ?", entitlement.ReservationID).Update("created_at", bookingAt).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.ScenicHotelPackageEntitlement{}).Where("id = ?", entitlement.ID).Update("booked_at", bookingAt).Error; err != nil {
		t.Fatal(err)
	}

	report := func(day time.Time) *HotelPackageBusinessSummary {
		t.Helper()
		value, err := (&ScenicHotelPackageService{}).BusinessSummary(fixture.tenantID, fixture.hotel.ID, day.Format("2006-01-02"), day.Format("2006-01-02"))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	sale := report(saleAt)
	if sale.SalesUnits != 1 || sale.PackageUnits != 1 || sale.GrossSalesCents != 49900 || sale.BookingUnits != 0 || sale.StayUnits != 0 {
		t.Fatalf("sale-period package summary=%+v", sale)
	}
	booking := report(bookingAt)
	if booking.SalesUnits != 0 || booking.GrossSalesCents != 0 || booking.BookingUnits != 1 || booking.StayUnits != 0 {
		t.Fatalf("booking-period package summary=%+v", booking)
	}
	stay := report(fixture.checkIn)
	if stay.SalesUnits != 0 || stay.GrossSalesCents != 0 || stay.BookingUnits != 0 || stay.StayUnits != 1 {
		t.Fatalf("stay-period package summary=%+v", stay)
	}
}

func TestScenicHotelPackageBusinessSummaryUsesActualPackageAndRestatesSalePeriodAfterRefund(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	service := &ScenicHotelPackageService{}
	oldPackageID := fixture.packageView.ID
	if err := service.Delete(fixture.tenantID, oldPackageID, 1); err != nil {
		t.Fatal(err)
	}
	recreated, err := service.Create(fixture.tenantID, 1, ScenicHotelPackageInput{
		ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID,
		RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1,
		HotelSettlementPriceCents: 30000, Status: "online",
	})
	if err != nil {
		t.Fatal(err)
	}
	if recreated.ID == oldPackageID {
		t.Fatalf("recreated package reused deleted id %d", oldPackageID)
	}
	useDate := fixture.checkIn
	order := model.Order{TenantID: fixture.tenantID, Channel: "online", ContactName: "period guest", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1, UseDate: &useDate}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	saleAt := dateOnly(time.Now().AddDate(0, 0, -40)).Add(12 * time.Hour)
	payment := model.Payment{
		TenantID: fixture.tenantID, PaymentNo: "PAY-PACKAGE-PERIOD", OrderNo: order.OrderNo,
		Purpose: "order", Amount: order.TotalAmount, AmountCents: moneyCents(order.TotalAmount),
		Method: "cash", Status: "paid", PaidAt: &saleAt,
	}
	if err := model.DB.Create(&payment).Error; err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	refundAt := dateOnly(time.Now().AddDate(0, 0, -5)).Add(12 * time.Hour)
	refund := model.Refund{
		TenantID: fixture.tenantID, RefundNo: "REFUND-PACKAGE-PERIOD", IdempotencyKey: "REFUND-PACKAGE-PERIOD",
		OrderNo: order.OrderNo, PaymentID: payment.ID, Amount: order.TotalAmount, AmountCents: moneyCents(order.TotalAmount),
		Method: "cash", Status: "succeeded", TicketCodesJSON: fmt.Sprintf(`["%s"]`, ticket.TicketCode),
	}
	if err := model.DB.Create(&refund).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&refund).Updates(map[string]interface{}{"created_at": refundAt, "updated_at": refundAt}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		var stored model.Order
		if err := tx.Preload("Items.Product").Preload("Items.Tickets").First(&stored, order.ID).Error; err != nil {
			return err
		}
		return applyRefundBusinessFactsTx(tx, &stored, &refund, map[string]*model.Ticket{ticket.TicketCode: &ticket})
	}); err != nil {
		t.Fatal(err)
	}

	report := func(day time.Time) *HotelPackageBusinessSummary {
		t.Helper()
		value, err := service.BusinessSummary(fixture.tenantID, fixture.hotel.ID, day.Format("2006-01-02"), day.Format("2006-01-02"))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	sale := report(saleAt)
	if sale.SalesUnits != 1 || sale.GrossSalesCents != 49900 || sale.RefundedUnits != 1 || sale.RefundedSalesCents != 49900 || sale.NetSalesCents != 0 || sale.TicketComponentNetCents != 0 || sale.HotelComponentNetCents != 0 {
		t.Fatalf("sale-period summary=%+v, want the later refund restated against its original sale", sale)
	}
	refundPeriod := report(refundAt)
	if refundPeriod.SalesUnits != 0 || refundPeriod.GrossSalesCents != 0 || refundPeriod.RefundedUnits != 0 || refundPeriod.RefundedSalesCents != 0 || refundPeriod.NetSalesCents != 0 {
		t.Fatalf("refund-period summary=%+v, want no duplicate activity-period reversal", refundPeriod)
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

func TestPendingPackageBookingCancellationBlocksHotelFulfillmentStatusChange(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	input := ScenicHotelPackageInput{
		ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID,
		RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1,
		HotelSettlementPriceCents: 30000, BookingMode: "after_purchase",
		VoucherValidityDays: 90, MinAdvanceDays: 1, MaxReschedules: 1, Status: "online",
	}
	if err := (&ScenicHotelPackageService{}).Update(fixture.tenantID, fixture.packageView.ID, 1, input); err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-cancel-fulfillment-race", "active", "production")
	externalNo := "XHS-CANCEL-FULFILLMENT-RACE"
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	var entitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := (PackageFulfillmentLifecycle{}).BookEntitlementTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: entitlement.EntitlementNo, CheckInDate: fixture.checkIn,
			GuestName: "取消中游客", ContactPhone: "13800138000", ClientRequestID: "cancel-race-book",
			ExternalBookOrderID: "BOOK-CANCEL-RACE", PlatformBookID: "PLATFORM-CANCEL-RACE",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := (PackageFulfillmentLifecycle{}).PrepareCancelTx(tx, entitlement.EntitlementNo)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var reservation model.HotelReservation
	if err := model.DB.Where("order_id = ?", order.ID).First(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	err := (&ScenicHotelPackageService{}).SetReservationStatus(fixture.tenantID, reservation.ID, 1, "checked_in", "")
	if err == nil || !strings.Contains(err.Error(), "booking operation is in progress") {
		t.Fatalf("pending package cancellation fulfillment error=%v", err)
	}
	if err := model.DB.First(&reservation, reservation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reservation.Status != "confirmed" || reservation.CheckedInAt != nil {
		t.Fatalf("pending package cancellation changed reservation=%+v", reservation)
	}
	if err := model.DB.First(&entitlement, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if entitlement.Status != "cancel_pending" {
		t.Fatalf("pending package cancellation entitlement=%+v", entitlement)
	}
	var ticket model.Ticket
	if err := model.DB.First(&ticket, entitlement.TicketID).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.Status != "pending_booking" {
		t.Fatalf("pending package cancellation ticket=%+v", ticket)
	}
}

func TestConfiguredHotelSupplierCanCompleteExistingReservationAfterSuspension(t *testing.T) {
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
	if err != nil {
		t.Fatalf("suspended hotel supplier could not complete existing reservation: %v", err)
	}
	if err := model.DB.First(&reservation, reservation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reservation.Status != "checked_in" || reservation.CheckedInAt == nil {
		t.Fatalf("suspended supplier reservation=%+v, want checked_in", reservation)
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

func TestScenicHotelPackageMutationSerializesWithConcurrentDeferredSale(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ScenicHotelPackageService, packageFixture, ScenicHotelPackageInput) error
	}{
		{
			name: "update",
			mutate: func(service *ScenicHotelPackageService, fixture packageFixture, input ScenicHotelPackageInput) error {
				input.Nights = 3
				return service.Update(fixture.tenantID, fixture.packageView.ID, 1, input)
			},
		},
		{
			name: "delete",
			mutate: func(service *ScenicHotelPackageService, fixture packageFixture, _ ScenicHotelPackageInput) error {
				return service.Delete(fixture.tenantID, fixture.packageView.ID, 1)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetBusinessData(t)
			fixture := seedScenicHotelPackage(t, 2)
			service := &ScenicHotelPackageService{}
			input := ScenicHotelPackageInput{
				ProductID: fixture.productID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID,
				RatePlanID: fixture.rate.ID, Nights: 2, RoomsPerPackage: 1,
				HotelSettlementPriceCents: 30000, BookingMode: "after_purchase",
				VoucherValidityDays: 90, MinAdvanceDays: 1, MaxReschedules: 1, Status: "online",
			}
			if err := service.Update(fixture.tenantID, fixture.packageView.ID, 1, input); err != nil {
				t.Fatal(err)
			}
			account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-package-mutation-"+test.name, "active", "production")

			sqlDB, err := model.DB.DB()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			lockConn, err := sqlDB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lockConn.Close()
			var advisoryKey int64
			if err := lockConn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&advisoryKey); err != nil {
				t.Fatal(err)
			}
			functionName := fmt.Sprintf("test_package_sale_barrier_%d", advisoryKey)
			triggerName := functionName + "_trigger"
			if _, err := lockConn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey); err != nil {
				t.Fatal(err)
			}
			locked := true
			defer func() {
				if locked {
					_, _ = lockConn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryKey)
				}
				_ = model.DB.Exec(fmt.Sprintf("DROP FUNCTION IF EXISTS %s() CASCADE", functionName)).Error
			}()
			if err := model.DB.Exec(fmt.Sprintf(`
				CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN
					PERFORM pg_advisory_xact_lock(%d);
					RETURN NEW;
				END
				$$`, functionName, advisoryKey)).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.DB.Exec(fmt.Sprintf(`
				CREATE TRIGGER %s
				BEFORE INSERT ON orders
				FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)).Error; err != nil {
				t.Fatal(err)
			}

			externalNo := "XHS-PACKAGE-MUTATION-" + strings.ToUpper(test.name)
			order := model.Order{
				TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo,
				Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1}},
			}
			orderCh := make(chan error, 1)
			go func() { orderCh <- (&OrderService{}).Create(&order) }()
			if err := waitForDatabaseLockWaiters(ctx, 1); err != nil {
				t.Fatalf("sale did not reach the transaction barrier: %v", err)
			}

			mutationCh := make(chan error, 1)
			go func() { mutationCh <- test.mutate(service, fixture, input) }()
			if err := waitForDatabaseLockWaiters(ctx, 2); err != nil {
				t.Fatalf("package mutation did not wait for the in-flight sale: %v", err)
			}
			if _, err := lockConn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey); err != nil {
				t.Fatal(err)
			}
			locked = false
			if err := <-orderCh; err != nil {
				t.Fatalf("complete deferred package sale: %v", err)
			}
			mutationErr := <-mutationCh
			if mutationErr == nil || !strings.Contains(mutationErr.Error(), "package has") {
				t.Fatalf("concurrent package %s error=%v, want sold-package rejection", test.name, mutationErr)
			}
			var entitlements int64
			if err := model.DB.Model(&model.ScenicHotelPackageEntitlement{}).Where("package_id = ?", fixture.packageView.ID).Count(&entitlements).Error; err != nil || entitlements != 1 {
				t.Fatalf("entitlements=%d err=%v, want one preserved sale", entitlements, err)
			}
			var stored model.ScenicHotelPackage
			if err := model.DB.Where("id = ?", fixture.packageView.ID).First(&stored).Error; err != nil {
				t.Fatalf("load package after rejected %s: %v", test.name, err)
			}
			if stored.Nights != 2 || stored.Status != "online" {
				t.Fatalf("package changed despite concurrent sale: %+v", stored)
			}
		})
	}
}
