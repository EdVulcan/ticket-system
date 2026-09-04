package service

import (
	"errors"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

func seedHotelProductResources(t *testing.T, tenantID uint, suffix string) (model.HotelProperty, model.HotelRoomType, model.HotelRatePlan) {
	t.Helper()
	hotelService := &HotelService{}
	hotel, err := hotelService.CreateProperty(tenantID, 11, HotelPropertyInput{Code: "HP-" + suffix, Name: "Hotel " + suffix})
	if err != nil {
		t.Fatal(err)
	}
	room, err := hotelService.CreateRoomType(tenantID, hotel.ID, 11, HotelRoomTypeInput{Code: "ROOM-" + suffix, Name: "Room " + suffix, MaxGuests: 2})
	if err != nil {
		t.Fatal(err)
	}
	rate, err := hotelService.CreateRatePlan(tenantID, hotel.ID, room.ID, 11, HotelRatePlanInput{Code: "RATE-" + suffix, Name: "Rate " + suffix, RetailPriceCents: 50000, SettlementPriceCents: 40000})
	if err != nil {
		t.Fatal(err)
	}
	return *hotel, *room, *rate
}

func TestHotelProductRejectsCrossTenantResources(t *testing.T) {
	resetBusinessData(t)
	owner := seedHotelSupplier(t, "HOTEL-PRODUCT-OWNER")
	other := seedHotelSupplier(t, "HOTEL-PRODUCT-OTHER")
	hotel, room, rate := seedHotelProductResources(t, owner.ID, "OWNER")

	_, err := (&HotelProductService{}).Create(other.ID, 22, HotelProductInput{
		Name: "Foreign Hotel Product", HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: "calendar_room", BaseRetailPriceCents: 50000, BaseSettlementPriceCents: 40000,
	})
	if err == nil {
		t.Fatal("cross-tenant hotel resources were accepted")
	}
}

func TestHotelProductCalendarUsesBasePriceAndRejectsPresale(t *testing.T) {
	resetBusinessData(t)
	tenant := seedHotelSupplier(t, "HOTEL-PRODUCT-CALENDAR")
	hotel, room, rate := seedHotelProductResources(t, tenant.ID, "CAL")
	service := &HotelProductService{}
	calendar, err := service.Create(tenant.ID, 11, HotelProductInput{
		Name: "Calendar Room", HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: "calendar_room", BaseRetailPriceCents: 51000, BaseSettlementPriceCents: 41000, Status: "online",
	})
	if err != nil {
		t.Fatal(err)
	}
	if calendar.Status != "online" || calendar.Product.Status != "online" || calendar.CurrentRevisionID == 0 {
		t.Fatalf("hotel product was not published after initial revision: %+v", calendar)
	}
	date := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	rows, err := service.ListCalendar(tenant.ID, calendar.ID, date, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].HasOverride || rows[0].Source != "base" || rows[0].RetailPriceCents != 51000 || rows[0].SettlementPriceCents != 41000 {
		t.Fatalf("calendar base fallback=%+v", rows)
	}
	presale, err := service.Create(tenant.ID, 11, HotelProductInput{
		Name: "Presale Room", HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: "presale_room", BaseRetailPriceCents: 52000, BaseSettlementPriceCents: 42000, VoucherValidityDays: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetCalendar(tenant.ID, presale.ID, 11, []HotelProductCalendarPriceInput{{StayDate: date, RetailPriceCents: 53000, SettlementPriceCents: 43000}}); err == nil {
		t.Fatal("presale product accepted a price calendar")
	}
}

func TestHotelProductUpdateCreatesRevisionAndKeepsCalendarSnapshot(t *testing.T) {
	resetBusinessData(t)
	tenant := seedHotelSupplier(t, "HOTEL-PRODUCT-REVISION")
	hotel, room, rate := seedHotelProductResources(t, tenant.ID, "REV")
	service := &HotelProductService{}
	created, err := service.Create(tenant.ID, 11, HotelProductInput{
		Name: "Revision Room", HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: "calendar_room", BaseRetailPriceCents: 50000, BaseSettlementPriceCents: 40000,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstRevisionID := created.CurrentRevisionID
	date := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	if err := service.SetCalendar(tenant.ID, created.ID, 11, []HotelProductCalendarPriceInput{{StayDate: date, RetailPriceCents: 56000, SettlementPriceCents: 45000}}); err != nil {
		t.Fatal(err)
	}
	if err := service.Update(tenant.ID, created.ID, 11, HotelProductInput{
		Name: "Revision Room Updated", HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: "calendar_room", BaseRetailPriceCents: 52000, BaseSettlementPriceCents: 42000,
	}); err != nil {
		t.Fatal(err)
	}
	var current model.HotelProduct
	if err := model.DB.Where("id = ? AND tenant_id = ?", created.ID, tenant.ID).First(&current).Error; err != nil {
		t.Fatal(err)
	}
	if current.CurrentRevisionID == firstRevisionID {
		t.Fatal("key product update did not create a new revision")
	}
	var first model.HotelProductRevision
	if err := model.DB.Where("id = ? AND hotel_product_id = ?", firstRevisionID, created.ID).First(&first).Error; err != nil {
		t.Fatal(err)
	}
	if first.BaseRetailPriceCents != 50000 || first.Version != 1 {
		t.Fatalf("old revision was rewritten: %+v", first)
	}
	rows, err := service.ListCalendar(tenant.ID, created.ID, date, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].HasOverride || rows[0].RetailPriceCents != 56000 || rows[0].BaseRetailPriceCents != 52000 {
		t.Fatalf("new revision did not retain product calendar override: %+v", rows)
	}
}

func TestSandboxHotelProductLifecycleDoesNotReadProductionInventory(t *testing.T) {
	resetBusinessData(t)
	tenant := seedHotelSupplier(t, "HOTEL-PRODUCT-SANDBOX")
	hotel, room, rate := seedHotelProductResources(t, tenant.ID, "SANDBOX")
	view, err := (&HotelProductService{}).Create(tenant.ID, 11, HotelProductInput{
		Name: "Sandbox Calendar Room", HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: "calendar_room", BaseRetailPriceCents: 50000, BaseSettlementPriceCents: 40000,
		Status: "online",
	})
	if err != nil {
		t.Fatal(err)
	}
	checkIn := time.Now().AddDate(0, 0, 3).Truncate(24 * time.Hour)
	checkOut := checkIn.AddDate(0, 0, 1)
	var order model.Order
	var reservationID uint
	err = model.Write(func(tx *gorm.DB) error {
		order = model.Order{OrderNo: "SANDBOX-HOTEL-ORDER", TenantID: tenant.ID, Status: "unpaid", Channel: "online", Environment: "sandbox", TotalAmount: 500}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		item := model.OrderItem{OrderID: order.ID, ProductID: view.Product.ID, ProductName: view.Product.Name, Price: 500, SettlementPrice: 400, Quantity: 1, FulfillmentProductID: view.Product.ID, FulfillmentTenantID: tenant.ID, FulfillmentScenicAreaID: 0, ReservedStockType: "hotel", ValidityType: "date"}
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
		entitlement := model.HotelProductEntitlement{
			EntitlementNo: "HPE-SANDBOX-1", SalesTenantID: tenant.ID, SupplierTenantID: tenant.ID,
			OrderID: order.ID, OrderItemID: item.ID, HotelProductID: view.ID, HotelProductRevisionID: view.CurrentRevisionID,
			CheckInDate: &checkIn, CheckOutDate: &checkOut, Rooms: 1, HotelName: hotel.Name, RoomTypeName: room.Name, RatePlanName: rate.Name,
			RetailPriceCents: 50000, SettlementPriceCents: 40000, PriceSource: "base", Status: "pending_booking",
			ValidFrom: checkIn, ValidUntil: checkOut,
		}
		if err := tx.Create(&entitlement).Error; err != nil {
			return err
		}
		reservation := model.HotelProductReservation{
			ReservationNo: "HPR-SANDBOX-1", SalesTenantID: tenant.ID, SupplierTenantID: tenant.ID,
			OrderID: order.ID, OrderItemID: item.ID, EntitlementID: entitlement.ID, HotelProductID: view.ID, HotelProductRevisionID: view.CurrentRevisionID,
			HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID, HotelName: hotel.Name, RoomTypeName: room.Name, RatePlanName: rate.Name,
			CheckInDate: checkIn, CheckOutDate: checkOut, Rooms: 1, RetailPriceCents: 50000, SettlementPriceCents: 40000, PriceSource: "base", Status: "reserved",
		}
		if err := tx.Create(&reservation).Error; err != nil {
			return err
		}
		reservationID = reservation.ID
		return tx.Model(&entitlement).Updates(map[string]interface{}{"reservation_id": reservation.ID, "status": "booked"}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return (HotelProductFulfillmentLifecycle{}).ConfirmOrderAt(tx, order.ID, time.Now())
	}); err != nil {
		t.Fatalf("sandbox confirmation read production inventory: %v", err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return (HotelProductFulfillmentLifecycle{}).CancelOrder(tx, order.ID, true)
	}); err != nil {
		t.Fatalf("sandbox cancellation read production inventory: %v", err)
	}
	var reservation model.HotelProductReservation
	if err := model.DB.First(&reservation, reservationID).Error; err != nil || reservation.Status != "cancelled" {
		t.Fatalf("sandbox reservation status=%q err=%v", reservation.Status, err)
	}
	var inventoryCount int64
	if err := model.DB.Model(&model.HotelRoomInventory{}).Where("hotel_id = ? AND room_type_id = ?", hotel.ID, room.ID).Count(&inventoryCount).Error; err != nil {
		t.Fatal(err)
	}
	if inventoryCount != 0 {
		t.Fatalf("sandbox lifecycle created production inventory rows=%d", inventoryCount)
	}
}

func TestHotelProductRejectsExternalChannelOrdersUntilProtocolIsEnabled(t *testing.T) {
	resetBusinessData(t)
	tenant := seedHotelSupplier(t, "HOTEL-PRODUCT-CHANNEL")
	hotel, room, rate := seedHotelProductResources(t, tenant.ID, "CHANNEL")
	view, err := (&HotelProductService{}).Create(tenant.ID, 11, HotelProductInput{
		Name: "Channel Calendar Room", HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: "calendar_room", BaseRetailPriceCents: 50000, BaseSettlementPriceCents: 40000, Status: "online",
	})
	if err != nil {
		t.Fatal(err)
	}
	account := model.ChannelAccount{TenantID: tenant.ID, Code: "hotel-channel-sandbox", Type: "xiaohongshu", Status: "sandbox", Environment: "sandbox"}
	if err := model.DB.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	externalNo := "XHS-HOTEL-1"
	err = (&OrderService{}).Create(&model.Order{
		TenantID: tenant.ID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo,
		Items: []model.OrderItem{{ProductID: view.Product.ID, Quantity: 1}},
	})
	if err == nil || !strings.Contains(err.Error(), "not available through external channels") {
		t.Fatalf("external hotel order error=%v", err)
	}
}

func TestHotelProductRejectsOnlinePresaleAndDirectSalesUntilLifecycleExists(t *testing.T) {
	resetBusinessData(t)
	tenant := seedHotelSupplier(t, "HOTEL-PRODUCT-P0-SALES")
	hotel, room, rate := seedHotelProductResources(t, tenant.ID, "P0-SALES")
	service := &HotelProductService{}

	if _, err := service.Create(tenant.ID, 11, HotelProductInput{
		Name: "Online Presale", HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: "presale_room", BaseRetailPriceCents: 50000, BaseSettlementPriceCents: 40000,
		VoucherValidityDays: 30, Status: "online",
	}); err == nil || !strings.Contains(err.Error(), "cannot be online") {
		t.Fatalf("online presale create error=%v", err)
	}

	presale, err := service.Create(tenant.ID, 11, HotelProductInput{
		Name: "Offline Presale", HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: "presale_room", BaseRetailPriceCents: 50000, BaseSettlementPriceCents: 40000,
		VoucherValidityDays: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Update(tenant.ID, presale.ID, 11, HotelProductInput{
		Name: "Offline Presale", HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: "presale_room", BaseRetailPriceCents: 50000, BaseSettlementPriceCents: 40000,
		VoucherValidityDays: 30, Status: "online",
	}); err == nil || !strings.Contains(err.Error(), "cannot be online") {
		t.Fatalf("online presale update error=%v", err)
	}

	calendar, err := service.Create(tenant.ID, 11, HotelProductInput{
		Name: "Online Calendar", HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: "calendar_room", BaseRetailPriceCents: 50000, BaseSettlementPriceCents: 40000, Status: "online",
	})
	if err != nil {
		t.Fatal(err)
	}
	checkIn := time.Now().AddDate(0, 0, 7)
	err = (&OrderService{}).Create(&model.Order{
		TenantID: tenant.ID, Channel: "online", Items: []model.OrderItem{{ProductID: calendar.Product.ID, Quantity: 1, UseDate: &checkIn}},
	})
	if !errors.Is(err, errStandaloneHotelProductSaleUnavailable) {
		t.Fatalf("direct hotel sale error=%v", err)
	}
	for _, assertion := range []struct {
		table interface{}
		where string
	}{
		{&model.Order{}, "tenant_id = ?"},
		{&model.HotelProductEntitlement{}, "sales_tenant_id = ?"},
		{&model.HotelProductReservation{}, "sales_tenant_id = ?"},
	} {
		var count int64
		if err := model.DB.Model(assertion.table).Where(assertion.where, tenant.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("blocked direct hotel sale left %T rows=%d", assertion.table, count)
		}
	}
}
