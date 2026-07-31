//go:build cgo

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

func TestCoreChannelAdapterUsesMappingAndAccountScope(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 2)
	account := model.ChannelAccount{TenantID: tenantID, Code: "core-channel", Type: "ota", Status: "active", PermissionsJSON: `["inventory:reserve","orders:create","orders:query"]`}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&account).Error }); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: productID, ExternalCode: "EXT-CORE-1", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&mapping).Error }); err != nil {
		t.Fatal(err)
	}
	visit := startOfDay(time.Now().AddDate(0, 0, 1))
	adapter := NewCoreChannelAdapter()
	reservation, err := adapter.CreateReservation(context.Background(), ChannelReservationRequest{
		TenantID: tenantID, AccountID: account.ID, Channel: account.Code, ExternalNo: "EXT-ORDER-1",
		ExternalProductCode: "EXT-CORE-1", Quantity: 1, UseDate: &visit, TTL: 10 * time.Minute,
	})
	if err != nil || reservation.ReservationID == 0 {
		t.Fatalf("reservation=%+v err=%v", reservation, err)
	}
	confirmed, err := adapter.ConfirmOrder(context.Background(), ChannelConfirmRequest{
		TenantID: tenantID, AccountID: account.ID, Channel: account.Code, ReservationID: reservation.ReservationID,
		ContactName: "渠道游客", ContactPhone: "13800138000",
	})
	if err != nil || confirmed.Order == nil || confirmed.Status != "paid" || len(confirmed.TicketCodes) != 1 {
		t.Fatalf("confirmed=%+v err=%v", confirmed, err)
	}
	queried, err := adapter.QueryOrder(context.Background(), ChannelQueryRequest{TenantID: tenantID, AccountID: account.ID, Channel: account.Code, ExternalNo: "EXT-ORDER-1"})
	if err != nil || queried.Order == nil || queried.Order.OrderNo != confirmed.Order.OrderNo {
		t.Fatalf("queried=%+v err=%v", queried, err)
	}
	if _, err := adapter.CreateReservation(context.Background(), ChannelReservationRequest{
		TenantID: tenantID, AccountID: account.ID, Channel: account.Code, ExternalNo: "EXT-ORDER-2",
		ExternalProductCode: "UNMAPPED", Quantity: 1, UseDate: &visit,
	}); err == nil {
		t.Fatal("unmapped external product was accepted")
	}
	other := model.ChannelAccount{TenantID: tenantID, Code: "other-channel", Type: "ota", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&other).Error }); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.QueryOrder(context.Background(), ChannelQueryRequest{TenantID: tenantID, AccountID: other.ID, Channel: other.Code, ExternalNo: "EXT-ORDER-1"}); err == nil {
		t.Fatal("channel account could query another account's order")
	}
}

func TestPOSHoldIsDurableOperatorScopedAndRevalidatesOnResume(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	var areaID uint
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Product{}).Where("id = ?", productID).Updates(map[string]interface{}{"type": "offline"}).Error; err != nil {
			return err
		}
		var area model.ScenicArea
		if err := tx.Where("tenant_id = ?", tenantID).First(&area).Error; err != nil {
			return err
		}
		areaID = area.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	operatorID := uint(7001)
	device := model.Device{Name: "POS", SerialNumber: fmt.Sprintf("POS-%d", time.Now().UnixNano()), Type: "pos", Status: "online", TenantID: tenantID, ScenicAreaID: areaID}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&device).Error }); err != nil {
		t.Fatal(err)
	}
	shift := model.POSShift{TenantID: tenantID, ScenicAreaID: areaID, DeviceID: device.ID, OperatorID: operatorID, ShiftNo: fmt.Sprintf("SHIFT-%d", time.Now().UnixNano()), Status: "open", OpenedAt: time.Now()}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&shift).Error }); err != nil {
		t.Fatal(err)
	}
	service := &OperationsService{}
	hold, err := service.CreatePOSHold(tenantID, device.ID, operatorID, shift.ID, []model.POSHoldLine{{ProductID: productID, Quantity: 2}}, "游客", "13800000000", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if hold.Status != "held" || hold.TotalCents != 19900 || len(hold.Items) != 1 {
		t.Fatalf("hold=%+v", hold)
	}
	if _, err := service.ResumePOSHold(tenantID, hold.ID, operatorID+1); err == nil {
		t.Fatal("another operator resumed the hold")
	}
	resumed, err := service.ResumePOSHold(tenantID, hold.ID, operatorID)
	if err != nil || resumed.Status != "resumed" || resumed.Items[0].Quantity != 2 {
		t.Fatalf("resumed=%+v err=%v", resumed, err)
	}
	if _, err := service.ResumePOSHold(tenantID, hold.ID, operatorID); err == nil {
		t.Fatal("hold was resumed twice")
	}
	short, err := service.CreatePOSHold(tenantID, device.ID, operatorID, shift.ID, []model.POSHoldLine{{ProductID: productID, Quantity: 1}}, "", "", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExpirePOSHolds(time.Now().Add(2*time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	var expired model.POSHold
	if err := model.DB.First(&expired, short.ID).Error; err != nil {
		t.Fatal(err)
	}
	if expired.Status != "expired" {
		t.Fatalf("expired hold status=%s", expired.Status)
	}
}

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

func TestAmbiguousProviderFailureIsReconciled(t *testing.T) {
	if !providerRequestMayHaveBeenAccepted("wechat", errors.New("provider request timeout")) {
		t.Fatal("transport timeout must remain reconcilable")
	}
	if providerRequestMayHaveBeenAccepted("wechat", errors.New("WeChat payment is not configured")) {
		t.Fatal("missing provider configuration must fail before reconciliation")
	}
	if providerRequestMayHaveBeenAccepted("cash", errors.New("cashier error")) {
		t.Fatal("cash payments cannot have an ambiguous provider request")
	}
}

func TestDigitalRefundTaskIsBoundedAndCanBeAuditedForRetry(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	refund := model.Refund{TenantID: tenantID, RefundNo: "REF-RETRY", IdempotencyKey: "idem-ref-retry", OrderNo: "ORDER-RETRY", PaymentID: 1, Amount: 1, Method: "wechat", Status: "pending"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&refund).Error }); err != nil {
		t.Fatal(err)
	}
	task := model.DigitalRefundTask{
		TenantID: tenantID, RefundID: refund.ID, Provider: "wechat", PaymentNo: "PAY-REFUND",
		Status: "pending", MaxAttempts: 2, NextAttemptAt: ptrTime(time.Now()),
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&task).Error }); err != nil {
		t.Fatal(err)
	}
	refundService := &RefundService{}
	if err := refundService.deferDigitalRefundTask(task.ID, time.Now(), errors.New("provider timeout")); err != nil {
		t.Fatal(err)
	}
	var firstState model.DigitalRefundTask
	if err := model.DB.First(&firstState, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if firstState.Status != "pending" || firstState.AttemptCount != 1 || firstState.FailureCode != "provider_unavailable" {
		t.Fatalf("first retry state=%+v", firstState)
	}
	if err := refundService.deferDigitalRefundTask(task.ID, time.Now(), errors.New("provider timeout")); err != nil {
		t.Fatal(err)
	}
	var parkedState model.DigitalRefundTask
	if err := model.DB.First(&parkedState, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if parkedState.Status != "manual_review" || parkedState.AttemptCount != 2 || parkedState.ManualReviewAt == nil {
		t.Fatalf("bounded retry state=%+v", parkedState)
	}
	if err := refundService.RetryDigitalRefundTask(tenantID, task.ID, 1, "admin", "provider credentials fixed"); err != nil {
		t.Fatal(err)
	}
	var retriedState model.DigitalRefundTask
	if err := model.DB.First(&retriedState, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if retriedState.Status != "pending" || retriedState.FailureCode != "" || retriedState.ManualReviewAt != nil {
		t.Fatalf("manual retry state=%+v", retriedState)
	}
	var audit model.AuditLog
	if err := model.DB.Where("action = ? AND target_id = ?", "payment.refund.retry", task.ID).First(&audit).Error; err != nil {
		t.Fatal(err)
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

func TestAfterSaleExchangeReplacesWholeItemWithoutChangingMoney(t *testing.T) {
	resetBusinessData(t)
	tenantID, sourceID := seedSellableProduct(t, "daily", 5)
	var targetID uint
	if err := model.Write(func(tx *gorm.DB) error {
		var source model.Product
		if err := tx.Preload("Rule").First(&source, sourceID).Error; err != nil {
			return err
		}
		target := source
		target.Base = model.Base{}
		target.Name = "Adult Ticket Exchange Target"
		target.CurrentRevisionID = 0
		if err := tx.Omit("Rule").Create(&target).Error; err != nil {
			return err
		}
		targetID = target.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	visitDate := startOfDay(time.Now().AddDate(0, 0, 1))
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: sourceID, Quantity: 1, UseDate: &visitDate}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	request := model.AfterSaleRequest{TenantID: tenantID, OrderNo: order.OrderNo, Type: "exchange", IdempotencyKey: "exchange-1", TargetProductID: targetID, OperatorID: 1}
	if err := (&AfterSaleService{}).Create(&request, []string{ticket.TicketCode}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&AfterSaleService{}).Approve(tenantID, request.ID, 2, "same-price exchange"); err != nil {
		t.Fatal(err)
	}
	completed, err := (&AfterSaleService{}).Execute(tenantID, request.ID, 2)
	if err != nil || completed.Status != "completed" {
		t.Fatalf("exchange=%+v err=%v", completed, err)
	}
	var item model.OrderItem
	if err := model.DB.Where("order_id = ?", order.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.ProductID != targetID || item.Price != 99.50 {
		t.Fatalf("exchanged item=%+v", item)
	}
	var storedTicket model.Ticket
	if err := model.DB.First(&storedTicket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedTicket.FulfillmentProductID != targetID || storedTicket.Status != "unused" {
		t.Fatalf("exchanged ticket=%+v", storedTicket)
	}
	var rows []model.ProductInventory
	if err := model.DB.Where("tenant_id = ? AND product_id IN ?", tenantID, []uint{sourceID, targetID}).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ProductID == sourceID && row.Sold != 0 {
			t.Fatalf("source inventory remained reserved: %+v", row)
		}
		if row.ProductID == targetID && row.Sold != 1 {
			t.Fatalf("target inventory not reserved: %+v", row)
		}
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

func TestChannelBillImportMatchesOrdersAndReplaysIdempotently(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{TenantID: tenantID, Code: "bill-channel", Type: "ota", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&account).Error }); err != nil {
		t.Fatal(err)
	}
	externalNo := "BILL-ORDER-1"
	order := model.Order{TenantID: tenantID, Channel: "ota", ChannelAccountID: account.ID, ExternalNo: &externalNo, Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	report, err := (&ChannelService{}).ImportBill(tenantID, account.ID, "bill-batch-1", []ChannelBillInput{{ExternalNo: externalNo, Operation: "sale", AmountCents: moneyCents(order.TotalAmount)}})
	if err != nil || report.Status != "completed" || report.MatchedCount != 1 || report.DifferenceCents != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	retry, err := (&ChannelService{}).ImportBill(tenantID, account.ID, "bill-batch-1", []ChannelBillInput{{ExternalNo: externalNo, Operation: "sale", AmountCents: moneyCents(order.TotalAmount)}})
	if err != nil || retry.ID != report.ID {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
	mismatch, err := (&ChannelService{}).ImportBill(tenantID, account.ID, "bill-batch-2", []ChannelBillInput{{ExternalNo: externalNo, Operation: "payment", AmountCents: moneyCents(order.TotalAmount) + 1}})
	if err != nil || mismatch.Status != "needs_review" || mismatch.DifferenceCents != 1 {
		t.Fatalf("mismatch=%+v err=%v", mismatch, err)
	}
	if _, err := (&ChannelService{}).ImportBill(tenantID, account.ID, "bill-batch-3", []ChannelBillInput{{ExternalNo: externalNo, Operation: "payment", AmountCents: moneyCents(order.TotalAmount) + 2}}); err == nil {
		t.Fatal("duplicate bill fact with conflicting batch was accepted")
	}
	payment := model.Payment{TenantID: tenantID, PaymentNo: "PAY-BILL-1", OrderNo: order.OrderNo, Amount: order.TotalAmount, Method: "wechat", Status: "refunded", TransactionID: "WX-TRADE-1"}
	refund := model.Refund{TenantID: tenantID, RefundNo: "REF-BILL-1", IdempotencyKey: "REF-BILL-IDEM", OrderNo: order.OrderNo, PaymentID: payment.ID, Amount: order.TotalAmount, Method: "wechat", Status: "succeeded", ProviderRefundID: "WX-REFUND-1"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&payment).Error; err != nil {
			return err
		}
		refund.PaymentID = payment.ID
		return tx.Create(&refund).Error
	}); err != nil {
		t.Fatal(err)
	}
	refundReport, err := (&ChannelService{}).ImportBill(tenantID, account.ID, "bill-batch-refund", []ChannelBillInput{{ExternalNo: "WX-REFUND-1", Operation: "refund", AmountCents: moneyCents(order.TotalAmount)}})
	if err != nil || refundReport.Status != "completed" || refundReport.MatchedCount != 1 {
		t.Fatalf("provider refund report=%+v err=%v", refundReport, err)
	}
}

func TestRechargeIsIdempotentAndLeavesCentLedgerEvidence(t *testing.T) {
	resetBusinessData(t)
	var supplier, distributor model.Tenant
	if err := model.Write(func(tx *gorm.DB) error {
		supplier = model.Tenant{Name: "Supplier", SystemCode: "FIN-S", SecretKey: "s"}
		distributor = model.Tenant{Name: "Distributor", SystemCode: "FIN-D", SecretKey: "d"}
		if err := tx.Create(&supplier).Error; err != nil {
			return err
		}
		if err := tx.Create(&distributor).Error; err != nil {
			return err
		}
		return tx.Create(&model.CapitalAccount{OwnerTenantID: distributor.ID, ManagerTenantID: supplier.ID, Status: "active", Balance: 10}).Error
	}); err != nil {
		t.Fatal(err)
	}
	finance := &FinanceService{}
	first, err := finance.RechargeAccount(supplier.ID, distributor.ID, 2500, "topup-1", 7, "bank receipt")
	if err != nil {
		t.Fatal(err)
	}
	second, err := finance.RechargeAccount(supplier.ID, distributor.ID, 2500, "topup-1", 7, "bank receipt")
	if err != nil || first.ID != second.ID {
		t.Fatalf("recharge idempotency first=%+v second=%+v err=%v", first, second, err)
	}
	var account model.CapitalAccount
	if err := model.DB.Where("owner_tenant_id = ? AND manager_tenant_id = ?", distributor.ID, supplier.ID).First(&account).Error; err != nil {
		t.Fatal(err)
	}
	if account.Balance != 35 {
		t.Fatalf("balance=%v, want 35", account.Balance)
	}
	var entries []model.LedgerEntry
	if err := model.DB.Where("account_id = ? AND entry_type = ?", account.ID, "recharge").Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].AmountCents != 2500 {
		t.Fatalf("recharge ledger=%+v", entries)
	}
}

func TestTeamContractPricingAndSettlementAreIdempotent(t *testing.T) {
	resetBusinessData(t)
	var travel, supplier model.Tenant
	if err := model.Write(func(tx *gorm.DB) error {
		travel = model.Tenant{Name: "Travel", SystemCode: "TEAM-FIN-T", SecretKey: "t"}
		supplier = model.Tenant{Name: "Supplier", SystemCode: "TEAM-FIN-S", SecretKey: "s"}
		if err := tx.Create(&travel).Error; err != nil {
			return err
		}
		if err := tx.Create(&supplier).Error; err != nil {
			return err
		}
		return tx.Create(&model.TravelContract{TravelTenantID: travel.ID, SupplierTenantID: supplier.ID, ContractNo: "TEAM-CONTRACT-1", Status: "active", PriceRulesJSON: `[{"product_id":42,"price_cents":9900,"max_quantity":2}]`, CreditLimitCents: 20000}).Error
	}); err != nil {
		t.Fatal(err)
	}
	var contract model.TravelContract
	if err := model.DB.First(&contract).Error; err != nil {
		t.Fatal(err)
	}
	validOrder := model.Order{Items: []model.OrderItem{{ProductID: 42, Price: 99, Quantity: 1}}}
	if err := validateTeamOrderAgainstContract(&contract, &validOrder); err != nil {
		t.Fatal(err)
	}
	invalidOrder := validOrder
	invalidOrder.Items = []model.OrderItem{{ProductID: 42, Price: 100, Quantity: 1}}
	if err := validateTeamOrderAgainstContract(&contract, &invalidOrder); err == nil {
		t.Fatal("contract accepted a non-contract price")
	}
	var group model.TourGroup
	if err := model.Write(func(tx *gorm.DB) error {
		order := model.Order{OrderNo: "TEAM-ORDER-1", TenantID: travel.ID, TotalAmount: 99, Status: "paid", Channel: "online"}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		group = model.TourGroup{TenantID: travel.ID, SupplierTenantID: supplier.ID, ContractID: contract.ID, GroupNo: "TEAM-1", Name: "Team", VisitDate: time.Now(), ExpectedCount: 1, Status: "confirmed", SalesOrderID: order.ID, ContractAmountCents: 9900, SettlementStatus: "open"}
		return tx.Create(&group).Error
	}); err != nil {
		t.Fatal(err)
	}
	team := &TeamService{}
	statement, err := team.GenerateTeamSettlement(travel.ID, group.ID)
	if err != nil || statement.NetCents != 9900 {
		t.Fatalf("statement=%+v err=%v", statement, err)
	}
	retry, err := team.GenerateTeamSettlement(travel.ID, group.ID)
	if err != nil || retry.ID != statement.ID {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
	if err := team.SetTeamSettlementStatus(travel.ID, statement.ID, "supplier_confirmed", ""); err == nil {
		t.Fatal("travel agency confirmed supplier step")
	}
	if err := team.SetTeamSettlementStatus(supplier.ID, statement.ID, "supplier_confirmed", ""); err != nil {
		t.Fatal(err)
	}
	if err := team.SetTeamSettlementStatus(travel.ID, statement.ID, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	if err := team.SetTeamSettlementStatus(travel.ID, statement.ID, "paid", "bank-slip-1"); err != nil {
		t.Fatal(err)
	}
}
