package service

import (
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

func TestAfterSaleRescheduleFailureRollsBackOriginalReservation(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "daily", 1)
	originalDate := startOfDay(time.Now().AddDate(0, 0, 1))
	fullDate := startOfDay(time.Now().AddDate(0, 0, 2))
	createPaid := func(date time.Time) model.Order {
		order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1, UseDate: &date}}}
		if err := (&OrderService{}).Create(&order); err != nil {
			t.Fatal(err)
		}
		if err := (&OrderService{}).MarkAsPaid(order.OrderNo, tenantID); err != nil {
			t.Fatal(err)
		}
		return order
	}
	original := createPaid(originalDate)
	_ = createPaid(fullDate)
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", original.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	request := model.AfterSaleRequest{TenantID: tenantID, OrderNo: original.OrderNo, Type: "reschedule", IdempotencyKey: "rollback-reschedule", TargetDate: &fullDate, OperatorID: 7}
	service := &AfterSaleService{}
	if err := service.Create(&request, []string{ticket.TicketCode}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Approve(tenantID, request.ID, 8, "approved"); err != nil {
		t.Fatal(err)
	}
	failed, err := service.Execute(tenantID, request.ID, 7)
	if err != nil || failed.Status != "failed" {
		t.Fatalf("failed request=%+v err=%v", failed, err)
	}
	var item model.OrderItem
	if err := model.DB.Where("order_id = ?", original.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.UseDate == nil || item.UseDate.Format("2006-01-02") != originalDate.Format("2006-01-02") {
		t.Fatalf("original visit date changed after failed reschedule: %v", item.UseDate)
	}
	for _, date := range []time.Time{originalDate, fullDate} {
		var inventory model.ProductInventory
		if err := model.DB.Where("tenant_id = ? AND product_id = ? AND stock_date = ?", tenantID, productID, date).First(&inventory).Error; err != nil {
			t.Fatal(err)
		}
		if inventory.Sold != 1 {
			t.Fatalf("inventory %s sold=%d, want 1", date.Format("2006-01-02"), inventory.Sold)
		}
	}
}

func TestAfterSaleExchangeRejectsCodeModeChange(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	targetID := cloneExchangeTarget(t, productID, 99.50, 60)
	if err := model.DB.Model(&model.Product{}).Where("id = ?", targetID).Update("code_mode", "order").Error; err != nil {
		t.Fatal(err)
	}
	visitDate := startOfDay(time.Now().AddDate(0, 0, 1))
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1, UseDate: &visitDate}}}
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
	request := model.AfterSaleRequest{TenantID: tenantID, OrderNo: order.OrderNo, Type: "exchange", IdempotencyKey: "code-mode-change", TargetProductID: targetID, OperatorID: 7}
	service := &AfterSaleService{}
	if err := service.Create(&request, []string{ticket.TicketCode}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Approve(tenantID, request.ID, 8, "approved"); err != nil {
		t.Fatal(err)
	}
	failed, err := service.Execute(tenantID, request.ID, 7)
	if err != nil || failed.Status != "failed" || !strings.Contains(failed.ErrorMessage, "code mode") {
		t.Fatalf("exchange result=%+v err=%v", failed, err)
	}
	var item model.OrderItem
	if err := model.DB.Where("order_id = ?", order.ID).First(&item).Error; err != nil || item.ProductID != productID {
		t.Fatalf("order item=%+v err=%v", item, err)
	}
}

func TestPartialRefundLeavesRemainingTicketAdmissible(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: productID, Quantity: 2}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&PaymentService{}).CreatePayment(tenantID, &model.Payment{OrderNo: order.OrderNo, Method: "cash"}); err != nil {
		t.Fatal(err)
	}
	var tickets []model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).Order("id").Find(&tickets).Error; err != nil || len(tickets) != 2 {
		t.Fatalf("tickets=%+v err=%v", tickets, err)
	}
	if _, err := (&RefundService{}).CreateCashRefund(tenantID, order.OrderNo, "partial-admission", order.Items[0].Price, []string{tickets[0].TicketCode}, "partial"); err != nil {
		t.Fatal(err)
	}
	var checkpoint model.CheckPoint
	var device model.Device
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("tenant_id = ? AND check_point_id = ?", tenantID, checkpoint.ID).First(&device).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&TicketService{}).Verify(tickets[1].TicketCode, checkpoint.ID, device.ID, tenantID); err != nil {
		t.Fatalf("remaining ticket was rejected: %v", err)
	}
}

func TestNoRefundPolicyIsSnapshottedAndEnforced(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	if err := model.DB.Model(&model.Product{}).Where("id = ?", productID).Update("refund_type", "no_refund").Error; err != nil {
		t.Fatal(err)
	}
	order := model.Order{TenantID: tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if order.Items[0].RefundType != "no_refund" {
		t.Fatalf("refund snapshot=%q", order.Items[0].RefundType)
	}
	if err := (&PaymentService{}).CreatePayment(tenantID, &model.Payment{OrderNo: order.OrderNo, Method: "cash"}); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (&RefundService{}).CreateCashRefund(tenantID, order.OrderNo, "no-refund", order.TotalAmount, []string{ticket.TicketCode}, "attempt"); err == nil {
		t.Fatal("no-refund product accepted a refund")
	}
}

func TestStockReleaseUsesSaleTimeStockType(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "total", 2)
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Product{}).Where("id = ?", productID).Update("stock_type", "unlimited").Error; err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).Cancel(order.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	var product model.Product
	if err := model.DB.First(&product, productID).Error; err != nil || product.DailyStock != 2 {
		t.Fatalf("released product=%+v err=%v", product, err)
	}
}

func TestDisabledDeviceHeartbeatDoesNotReactivate(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	var device model.Device
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&device).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&device).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if err := (&DeviceService{}).HeartbeatDirect(tenantID, device.ID, "127.0.0.1", "online"); err == nil {
		t.Fatal("disabled device heartbeat was accepted")
	}
	if err := model.DB.First(&device, device.ID).Error; err != nil || device.Status != "disabled" {
		t.Fatalf("device=%+v err=%v", device, err)
	}
}

func TestTeamCreationRejectsClientControlledDeposit(t *testing.T) {
	group := model.TourGroup{
		Name: "Unverified deposit", SupplierTenantID: 1, ScenicAreaID: 1,
		VisitDate: time.Now(), ExpectedCount: 1, DepositCents: 10000,
	}
	if err := (&TeamService{}).CreateGroup(1, &group); err == nil || !strings.Contains(err.Error(), "verified payment") {
		t.Fatalf("client-controlled deposit error=%v", err)
	}
}

func TestCtripDistributedOrderKeepsSupplierSettlement(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	account := model.ChannelAccount{TenantID: scenario.distributorID, Code: "ctrip-distributed", Type: "ctrip", Status: "active", Environment: "production"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&account).Error; err != nil {
			return err
		}
		return tx.Create(&model.ChannelProductMapping{
			ChannelAccountID: account.ID, ProductID: scenario.listingID, ExternalCode: "CTRIP-DIST",
			Status: "active", ChannelSaleCents: 12000, ChannelCostCents: 1000,
		}).Error
	}); err != nil {
		t.Fatal(err)
	}
	order := model.Order{
		TenantID: scenario.distributorID, Channel: "ota", ChannelAccountID: account.ID,
		Items: []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if got := moneyCents(order.Items[0].SettlementPrice); got != 6000 {
		t.Fatalf("distributed settlement=%d, want supplier offer 6000", got)
	}
	var capital model.CapitalAccount
	if err := model.DB.Where("owner_tenant_id = ? AND manager_tenant_id = ?", scenario.distributorID, scenario.supplierID).First(&capital).Error; err != nil {
		t.Fatal(err)
	}
	syncCapitalAccountCents(&capital)
	if capital.BalanceCents != 4000 {
		t.Fatalf("capital balance=%d, want 4000", capital.BalanceCents)
	}
}

func TestAfterSaleRefundReconcileRecoversMissingRefundLink(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	request := model.AfterSaleRequest{TenantID: tenantID, RequestNo: "AS-RECOVER", IdempotencyKey: "recover-refund", OrderNo: "ORDER-RECOVER", Type: "refund", Status: "processing", OperatorID: 7}
	refund := model.Refund{TenantID: tenantID, RefundNo: "REFUND-RECOVER", IdempotencyKey: "after-sale:recover-refund", OrderNo: request.OrderNo, PaymentID: 1, Status: "succeeded"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&request).Error; err != nil {
			return err
		}
		return tx.Create(&refund).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := (&AfterSaleService{}).ReconcileRefunds(); err != nil {
		t.Fatal(err)
	}
	var stored model.AfterSaleRequest
	if err := model.DB.First(&stored, request.ID).Error; err != nil || stored.Status != "completed" || stored.RefundID != refund.ID {
		t.Fatalf("request=%+v err=%v", stored, err)
	}
}
