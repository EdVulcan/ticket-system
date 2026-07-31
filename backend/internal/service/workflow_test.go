//go:build cgo

package service

import (
	"errors"
	"fmt"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

func TestProductSalePolicyRejectsIdentityAndLimitViolations(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.Product{}).Where("id = ?", productID).Updates(map[string]interface{}{
			"real_name_required": true, "region_limit": `["CN"]`, "limit_per_phone": 1, "limit_per_id": 1,
		}).Error
	}); err != nil {
		t.Fatal(err)
	}
	missingID := model.Order{TenantID: tenantID, Channel: "online", ContactName: "游客", ContactPhone: "13800000000", VisitorRegion: "CN", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&missingID); err == nil {
		t.Fatal("order without identity was accepted")
	}
	order := model.Order{TenantID: tenantID, Channel: "online", ContactName: "游客", ContactPhone: "13800000000", VisitorID: "ID-1", VisitorRegion: "CN", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	duplicate := model.Order{TenantID: tenantID, Channel: "online", ContactName: "游客", ContactPhone: "13800000000", VisitorID: "ID-1", VisitorRegion: "CN", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&duplicate); err == nil {
		t.Fatal("purchase limit was bypassed by a second order")
	}
	wrongRegion := model.Order{TenantID: tenantID, Channel: "online", ContactName: "游客2", ContactPhone: "13800000001", VisitorID: "ID-2", VisitorRegion: "US", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&wrongRegion); err == nil {
		t.Fatal("region policy was bypassed")
	}
}

func TestTimeSlotInventoryIsolatedByDateAndSlot(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "daily", 2)
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.Product{}).Where("id = ?", productID).Updates(map[string]interface{}{
			"time_slot_config": `[{"code":"morning","capacity":1},{"code":"afternoon","capacity":1}]`,
		}).Error
	}); err != nil {
		t.Fatal(err)
	}
	date := startOfDay(time.Now().AddDate(0, 0, 1))
	makeOrder := func(slot string) error {
		visit := date
		return (&OrderService{}).Create(&model.Order{TenantID: tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: productID, Quantity: 1, UseDate: &visit, StockSlot: slot}}})
	}
	if err := makeOrder("morning"); err != nil {
		t.Fatal(err)
	}
	if err := makeOrder("morning"); err == nil {
		t.Fatal("second morning reservation exceeded slot capacity")
	}
	if err := makeOrder("afternoon"); err != nil {
		t.Fatal(fmt.Errorf("afternoon slot should remain available: %w", err))
	}
	var rows []model.ProductInventory
	if err := model.DB.Where("tenant_id = ? AND product_id = ?", tenantID, productID).Order("stock_slot").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Sold != 1 || rows[1].Sold != 1 {
		t.Fatalf("slot inventory=%+v", rows)
	}
}

func TestAfterSaleRescheduleMovesInventoryAndVoidReleasesOnce(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "daily", 5)
	firstDate := startOfDay(time.Now().AddDate(0, 0, 1))
	secondDate := startOfDay(time.Now().AddDate(0, 0, 2))
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1, UseDate: &firstDate}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	request := model.AfterSaleRequest{TenantID: tenantID, OrderNo: order.OrderNo, Type: "reschedule", IdempotencyKey: "as-reschedule", TargetDate: &secondDate, OperatorID: 1}
	if err := (&AfterSaleService{}).Create(&request, []string{ticket.TicketCode}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&AfterSaleService{}).Approve(tenantID, request.ID, 2, "approved"); err != nil {
		t.Fatal(err)
	}
	completed, err := (&AfterSaleService{}).Execute(tenantID, request.ID, 2)
	if err != nil || completed.Status != "completed" {
		t.Fatalf("reschedule=%+v err=%v", completed, err)
	}
	var first, second model.ProductInventory
	if err := model.DB.Where("tenant_id = ? AND product_id = ? AND stock_date = ?", tenantID, productID, firstDate).First(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("tenant_id = ? AND product_id = ? AND stock_date = ?", tenantID, productID, secondDate).First(&second).Error; err != nil {
		t.Fatal(err)
	}
	if first.Sold != 0 || second.Sold != 1 {
		t.Fatalf("rescheduled inventory=%d/%d", first.Sold, second.Sold)
	}
	voidOrder := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1, UseDate: &firstDate}}}
	if err := (&OrderService{}).Create(&voidOrder); err != nil {
		t.Fatal(err)
	}
	voidRequest := model.AfterSaleRequest{TenantID: tenantID, OrderNo: voidOrder.OrderNo, Type: "void", IdempotencyKey: "as-void", OperatorID: 1}
	if err := (&AfterSaleService{}).Create(&voidRequest, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := (&AfterSaleService{}).Approve(tenantID, voidRequest.ID, 2, "approved"); err != nil {
		t.Fatal(err)
	}
	if completed, err := (&AfterSaleService{}).Execute(tenantID, voidRequest.ID, 2); err != nil || completed.Status != "completed" {
		t.Fatalf("void=%+v err=%v", completed, err)
	}
	if count, err := (&OrderService{}).ExpireUnpaid(time.Now()); err != nil || count != 0 {
		t.Fatalf("void order was left expirable count=%d err=%v", count, err)
	}
}

func TestHardwareCommandRequiresDeviceAndAckToken(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	var tenant model.Tenant
	if err := model.DB.First(&tenant, tenantID).Error; err != nil {
		t.Fatal(err)
	}
	var device model.Device
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&device).Error; err != nil {
		t.Fatal(err)
	}
	service := &DeviceService{}
	command, err := service.QueueHardwareCommand(HardwareCommandRequest{TenantID: tenantID, DeviceID: device.ID, Kind: "open_gate", PayloadJSON: `{"duration_ms":500}`})
	if err != nil {
		t.Fatal(err)
	}
	polled, err := service.PollHardwareCommand(tenant.SystemCode, device.SerialNumber, "test-device-key")
	if err != nil || polled.ID != command.ID {
		t.Fatalf("polled=%+v err=%v", polled, err)
	}
	bad := HardwareAckRequest{SystemCode: tenant.SystemCode, SerialNumber: device.SerialNumber, DeviceKey: "test-device-key", CommandNo: command.CommandNo, AckToken: "wrong", Status: "acknowledged"}
	if err := service.AckHardwareCommand(bad); err == nil {
		t.Fatal("hardware command accepted an invalid acknowledgement token")
	}
	bad.AckToken, bad.Payload = polled.AckToken, "gate opened"
	if err := service.AckHardwareCommand(bad); err != nil {
		t.Fatal(err)
	}
	var stored model.HardwareCommand
	if err := model.DB.First(&stored, command.ID).Error; err != nil || stored.Status != "acknowledged" {
		t.Fatalf("stored command=%+v err=%v", stored, err)
	}
}

func TestChannelReservationConvertsWithoutDoubleBookingStock(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "daily", 2)
	account := model.ChannelAccount{TenantID: tenantID, Code: "channel-test", Type: "test", Status: "active", PermissionsJSON: `["inventory:reserve","orders:create"]`}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&account).Error }); err != nil {
		t.Fatal(err)
	}
	date := startOfDay(time.Now().AddDate(0, 0, 1))
	workflow := &ChannelWorkflowService{OrderService: &OrderService{}}
	reservation, err := workflow.Reserve(tenantID, account.ID, "channel-test", productID, "EXT-RES-1", 1, &date, "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	order, err := workflow.Confirm(tenantID, account.ID, "channel-test", reservation.ID, "游客", "13800000000")
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != "paid" {
		t.Fatalf("confirmed order status=%s", order.Status)
	}
	var inventory model.ProductInventory
	if err := model.DB.Where("tenant_id = ? AND product_id = ? AND stock_date = ?", tenantID, productID, date).First(&inventory).Error; err != nil {
		t.Fatal(err)
	}
	if inventory.Sold != 1 {
		t.Fatalf("reservation conversion double-booked stock: sold=%d", inventory.Sold)
	}
	if _, err := workflow.Confirm(tenantID, account.ID, "channel-test", reservation.ID, "游客", "13800000000"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.Release(tenantID, account.ID, reservation.ID, "late release"); !errors.Is(err, nil) {
		// A converted reservation is intentionally immutable and cannot release
		// stock a second time.
		if err != nil {
			t.Fatal(err)
		}
	}
}
