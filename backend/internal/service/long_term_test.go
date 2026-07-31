//go:build cgo

package service

import (
	"context"
	"errors"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type refundProviderFunc func(context.Context, *model.Refund, *model.Payment) (RefundProviderResult, error)

func (f refundProviderFunc) Process(ctx context.Context, refund *model.Refund, payment *model.Payment) (RefundProviderResult, error) {
	return f(ctx, refund, payment)
}

func TestLedgerRecordsDistributionReservationAndRelease(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	order := model.Order{TenantID: scenario.distributorID, Channel: "online", Items: []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	var entries []model.LedgerEntry
	if err := model.DB.Where("owner_tenant_id = ?", scenario.distributorID).Order("id").Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].EntryType != "reservation_cash" || entries[0].AmountCents != -6000 {
		t.Fatalf("ledger after reservation=%+v", entries)
	}
	if err := (&OrderService{}).Cancel(order.OrderNo, scenario.distributorID); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("owner_tenant_id = ?", scenario.distributorID).Order("id").Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[1].EntryType != "release_cash" || entries[1].AmountCents != 6000 {
		t.Fatalf("ledger after release=%+v", entries)
	}
}

func TestSoldOrderCapturesProductRevision(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	var item model.OrderItem
	if err := model.DB.Where("order_id = ?", order.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.ProductRevisionID == 0 {
		t.Fatal("order item has no product revision snapshot")
	}
	var revision model.ProductRevision
	if err := model.DB.First(&revision, item.ProductRevisionID).Error; err != nil {
		t.Fatal(err)
	}
	if revision.ProductID != productID || revision.PriceCents != 9950 {
		t.Fatalf("revision=%+v", revision)
	}
}

func TestSettlementUsesFulfillmentSnapshot(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	order := model.Order{TenantID: scenario.distributorID, Channel: "window", Items: []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, scenario.distributorID); err != nil {
		t.Fatal(err)
	}
	periodStart, periodEnd := time.Now().Add(-time.Hour).Truncate(time.Second), time.Now().Add(time.Hour).Truncate(time.Second)
	statement, err := (&SettlementService{}).GenerateStatement(scenario.supplierID, scenario.supplierID, scenario.distributorID, periodStart, periodEnd)
	if err != nil {
		t.Fatal(err)
	}
	if statement.GrossCents != 6000 || statement.NetCents != 6000 {
		t.Fatalf("statement=%+v", statement)
	}
	var line model.SettlementLine
	if err := model.DB.Where("statement_id = ?", statement.ID).First(&line).Error; err != nil {
		t.Fatal(err)
	}
	if line.GrossCents != 6000 {
		t.Fatalf("settlement line=%+v", line)
	}
	settlements := &SettlementService{}
	repeated, err := settlements.GenerateStatement(scenario.supplierID, scenario.supplierID, scenario.distributorID, periodStart, periodEnd)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.ID != statement.ID {
		t.Fatalf("period retry created statement %d instead of returning %d", repeated.ID, statement.ID)
	}
	if err := settlements.SetStatus(scenario.distributorID, statement.ID, "supplier_confirmed", ""); err == nil {
		t.Fatal("distributor performed supplier confirmation")
	}
	if err := settlements.SetStatus(scenario.supplierID, statement.ID, "supplier_confirmed", ""); err != nil {
		t.Fatal(err)
	}
	if err := settlements.SetStatus(scenario.supplierID, statement.ID, "confirmed", ""); err == nil {
		t.Fatal("supplier performed distributor confirmation")
	}
	if err := settlements.SetStatus(scenario.distributorID, statement.ID, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	if err := settlements.SetStatus(scenario.distributorID, statement.ID, "paid", ""); err == nil {
		t.Fatal("settlement was paid without proof")
	}
	if err := settlements.SetStatus(scenario.supplierID, statement.ID, "paid", "bank-slip"); err == nil {
		t.Fatal("supplier marked settlement paid")
	}
	if err := settlements.SetStatus(scenario.distributorID, statement.ID, "paid", "bank-slip"); err != nil {
		t.Fatal(err)
	}
}

func TestDigitalRefundIsPendingAndIdempotent(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{TenantID: tenantID, OrderNo: order.OrderNo, PaymentNo: "PAY-DIGITAL-1", Amount: order.TotalAmount, Method: "wechat", Status: "paid"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&payment).Error; err != nil {
			return err
		}
		return tx.Model(&model.Order{}).Where("id = ?", order.ID).Update("status", "paid").Error
	}); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	refund, err := (&RefundService{}).CreateDigitalRefund(tenantID, order.OrderNo, "refund-key-1", order.TotalAmount, []string{ticket.TicketCode}, "customer request")
	if err != nil {
		t.Fatal(err)
	}
	if refund.Status != "pending" {
		t.Fatalf("refund status=%s, want pending", refund.Status)
	}
	again, err := (&RefundService{}).CreateDigitalRefund(tenantID, order.OrderNo, "refund-key-1", order.TotalAmount, []string{ticket.TicketCode}, "customer request")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != refund.ID {
		t.Fatalf("idempotent refund ids=%d/%d", again.ID, refund.ID)
	}
	var tasks []model.DigitalRefundTask
	if err := model.DB.Where("refund_id = ?", refund.ID).Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Status != "pending" {
		t.Fatalf("refund tasks=%+v", tasks)
	}
}

func TestDigitalRefundWorkerCompletesTicketScopedFacts(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{TenantID: tenantID, OrderNo: order.OrderNo, PaymentNo: "PAY-DIGITAL-WORKER", Amount: order.TotalAmount, Method: "wechat", Status: "paid"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&payment).Error; err != nil {
			return err
		}
		return tx.Model(&model.Order{}).Where("id = ?", order.ID).Update("status", "paid").Error
	}); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	refund, err := (&RefundService{}).CreateDigitalRefund(tenantID, order.OrderNo, "digital-worker", order.TotalAmount, []string{ticket.TicketCode}, "customer request")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	worker := &RefundService{Provider: refundProviderFunc(func(_ context.Context, got *model.Refund, gotPayment *model.Payment) (RefundProviderResult, error) {
		calls++
		if got.RefundNo != refund.RefundNo || gotPayment.PaymentNo != payment.PaymentNo {
			t.Fatalf("provider got refund=%s payment=%s", got.RefundNo, gotPayment.PaymentNo)
		}
		return RefundProviderResult{Status: "succeeded", ProviderRefundID: "WX-REFUND-1"}, nil
	})}
	if processed, err := worker.ProcessDigitalRefundTasks(context.Background(), time.Now().Add(time.Second), 10); err != nil || processed != 1 {
		t.Fatalf("processed=%d err=%v", processed, err)
	}
	if _, err := worker.ProcessDigitalRefundTasks(context.Background(), time.Now().Add(time.Minute), 10); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider calls=%d, want 1", calls)
	}
	if err := model.DB.First(&ticket, ticket.ID).Error; err != nil || ticket.Status != "refunded" {
		t.Fatalf("ticket=%+v err=%v", ticket, err)
	}
	if err := model.DB.First(&payment, payment.ID).Error; err != nil || payment.Status != "refunded" || payment.RefundedAmount != order.TotalAmount {
		t.Fatalf("payment=%+v err=%v", payment, err)
	}
	if err := model.DB.First(&refund, refund.ID).Error; err != nil || refund.Status != "succeeded" || refund.ProviderRefundID != "WX-REFUND-1" {
		t.Fatalf("refund=%+v err=%v", refund, err)
	}
}

func TestDigitalPartialRefundUpdatesPaymentAndOrderState(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: productID, Quantity: 2}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{TenantID: tenantID, OrderNo: order.OrderNo, PaymentNo: "PAY-DIGITAL-PARTIAL", Amount: order.TotalAmount, Method: "wechat", Status: "paid"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&payment).Error; err != nil {
			return err
		}
		return tx.Model(&model.Order{}).Where("id = ?", order.ID).Update("status", "paid").Error
	}); err != nil {
		t.Fatal(err)
	}
	var tickets []model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).Find(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 2 {
		t.Fatalf("tickets=%d, want 2", len(tickets))
	}
	refund, err := (&RefundService{}).CreateDigitalRefund(tenantID, order.OrderNo, "digital-partial", 99.50, []string{tickets[0].TicketCode}, "partial request")
	if err != nil {
		t.Fatal(err)
	}
	worker := &RefundService{Provider: refundProviderFunc(func(context.Context, *model.Refund, *model.Payment) (RefundProviderResult, error) {
		return RefundProviderResult{Status: "succeeded", ProviderRefundID: "WX-PARTIAL"}, nil
	})}
	if _, err := worker.ProcessDigitalRefundTasks(context.Background(), time.Now().Add(time.Second), 10); err != nil {
		t.Fatal(err)
	}
	var storedPayment model.Payment
	if err := model.DB.First(&storedPayment, payment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedPayment.Status != "partial_refunded" || storedPayment.RefundedAmount != 99.50 {
		t.Fatalf("payment=%+v", storedPayment)
	}
	var storedOrder model.Order
	if err := model.DB.First(&storedOrder, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedOrder.Status != "partial_refunded" {
		t.Fatalf("order status=%s", storedOrder.Status)
	}
	if err := model.DB.First(&refund, refund.ID).Error; err != nil || refund.Status != "succeeded" {
		t.Fatalf("refund=%+v err=%v", refund, err)
	}
}

func TestPOSShiftsAndPrintJobsStayTerminalScoped(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	var area model.ScenicArea
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&area).Error; err != nil {
		t.Fatal(err)
	}
	checkpoint := model.CheckPoint{TenantID: tenantID, ScenicAreaID: area.ID, Name: "POS checkpoint"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&checkpoint).Error }); err != nil {
		t.Fatal(err)
	}
	checkpointID := checkpoint.ID
	devices := []model.Device{
		{TenantID: tenantID, ScenicAreaID: area.ID, CheckPointID: &checkpointID, Name: "POS 1", SerialNumber: "POS-1", Type: "pos", Status: "online"},
		{TenantID: tenantID, ScenicAreaID: area.ID, CheckPointID: &checkpointID, Name: "POS 2", SerialNumber: "POS-2", Type: "pos", Status: "online"},
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&devices).Error }); err != nil {
		t.Fatal(err)
	}
	ops := &OperationsService{}
	shift1, err := ops.OpenShift(tenantID, devices[0].ID, 101, 1000)
	if err != nil {
		t.Fatal(err)
	}
	shift2, err := ops.OpenShift(tenantID, devices[1].ID, 202, 2000)
	if err != nil {
		t.Fatal(err)
	}
	makeSale := func(shift *model.POSShift, deviceID, operatorID uint) (model.Order, model.Payment, model.Ticket) {
		order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
		if err := (&OrderService{}).Create(&order); err != nil {
			t.Fatal(err)
		}
		payment := model.Payment{OrderNo: order.OrderNo, Method: "cash", ShiftID: shift.ID, DeviceID: deviceID, OperatorID: operatorID}
		if err := (&PaymentService{}).CreatePayment(tenantID, &payment); err != nil {
			t.Fatal(err)
		}
		var ticket model.Ticket
		if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
			t.Fatal(err)
		}
		return order, payment, ticket
	}
	order1, _, ticket1 := makeSale(shift1, devices[0].ID, 101)
	order2, _, ticket2 := makeSale(shift2, devices[1].ID, 202)
	if _, err := (&RefundService{}).CreateCashRefund(tenantID, order1.OrderNo, "pos-refund", order1.TotalAmount, []string{ticket1.TicketCode}, "same shift refund"); err != nil {
		t.Fatal(err)
	}
	closed1, err := ops.CloseShift(tenantID, shift1.ID, 1000, "")
	if err != nil {
		t.Fatal(err)
	}
	closed2, err := ops.CloseShift(tenantID, shift2.ID, 2000+moneyCents(order2.TotalAmount), "")
	if err != nil {
		t.Fatal(err)
	}
	if closed1.ExpectedCents != 1000 || closed2.ExpectedCents != 2000+moneyCents(order2.TotalAmount) {
		t.Fatalf("shift expected cents=%d/%d", closed1.ExpectedCents, closed2.ExpectedCents)
	}
	today := time.Now().Format("2006-01-02")
	productStats, err := (&ReportService{}).GetProductStats(tenantID, today, today)
	if err != nil || len(productStats) != 1 || productStats[0].TotalSold != 1 || moneyCents(productStats[0].TotalAmount) != moneyCents(order2.TotalAmount) {
		t.Fatalf("product stats=%+v err=%v", productStats, err)
	}
	shift3, err := ops.OpenShift(tenantID, devices[0].ID, 101, 0)
	if err != nil {
		t.Fatal(err)
	}
	job, err := ops.QueuePrint(tenantID, devices[0].ID, 101, shift3.ID, order2.OrderNo, ticket2.TicketCode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ops.StartPrint(tenantID, job.ID, devices[0].ID, 101); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.FailPrint(tenantID, job.ID, devices[0].ID, 101, "paper out"); err != nil {
		t.Fatal(err)
	}
	restarted := &OperationsService{}
	failed, err := restarted.ListPrintJobs(tenantID, devices[0].ID, "failed")
	if err != nil || len(failed) != 1 {
		t.Fatalf("failed jobs=%+v err=%v", failed, err)
	}
	if _, err := restarted.StartPrint(tenantID, job.ID, devices[0].ID, 101); err != nil {
		t.Fatal(err)
	}
	printed, err := restarted.CompletePrint(tenantID, job.ID, devices[0].ID, 101)
	if err != nil || printed.Status != "printed" || printed.AttemptCount != 2 {
		t.Fatalf("printed=%+v err=%v", printed, err)
	}
}

func TestStaffResourceScopesFailClosed(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	var area model.ScenicArea
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&area).Error; err != nil {
		t.Fatal(err)
	}
	staff := model.Staff{TenantID: tenantID, Name: "Checker", JobNumber: "CHECK-1", Password: "hash", Roles: "checker", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&staff).Error }); err != nil {
		t.Fatal(err)
	}
	if err := RequireStaffResource(tenantID, staff.ID, "checker", "scenic_area", area.ID); !errors.Is(err, ErrResourceScopeDenied) {
		t.Fatalf("missing scope error=%v", err)
	}
	if err := ReplaceStaffResourceScopes(tenantID, staff.ID, []model.StaffResourceScope{{ResourceType: "scenic_area", ResourceID: area.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := RequireStaffResource(tenantID, staff.ID, "checker", "scenic_area", area.ID); err != nil {
		t.Fatal(err)
	}
	if err := RequireStaffResource(tenantID+1, staff.ID, "checker", "scenic_area", area.ID); !errors.Is(err, ErrResourceScopeDenied) {
		t.Fatalf("cross-tenant scope error=%v", err)
	}
}

func TestTeamRosterAndEntryStayTenantScoped(t *testing.T) {
	resetBusinessData(t)
	var travel, supplier, other model.Tenant
	if err := model.Write(func(tx *gorm.DB) error {
		travel = model.Tenant{Name: "Travel T", SystemCode: "TRAVEL-T", SecretKey: "t"}
		supplier = model.Tenant{Name: "Supplier S", SystemCode: "SUP-S", SecretKey: "s"}
		other = model.Tenant{Name: "Other", SystemCode: "OTHER", SecretKey: "o"}
		if err := tx.Create(&travel).Error; err != nil {
			return err
		}
		if err := tx.Create(&supplier).Error; err != nil {
			return err
		}
		if err := tx.Create(&other).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.TenantCapability{TenantID: travel.ID, Capability: "travel_agency", Status: "active"}).Error; err != nil {
			return err
		}
		return tx.Create(&model.TenantCapability{TenantID: supplier.ID, Capability: "supplier", Status: "active"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	var area model.ScenicArea
	if err := model.Write(func(tx *gorm.DB) error {
		area = model.ScenicArea{TenantID: supplier.ID, Code: "MAIN", Name: "Main", Status: "active"}
		if err := tx.Create(&area).Error; err != nil {
			return err
		}
		return tx.Create(&model.DistributorRelationship{AgentTenantID: travel.ID, SupplierTenantID: supplier.ID, Status: "active"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	group := model.TourGroup{Name: "Team 1", SupplierTenantID: supplier.ID, ScenicAreaID: area.ID, VisitDate: time.Now().AddDate(0, 0, 1)}
	if err := (&TeamService{}).CreateGroup(travel.ID, &group); err != nil {
		t.Fatal(err)
	}
	if _, err := (&TeamService{}).AddMembers(travel.ID, group.ID, []model.TourGroupMember{{Name: "A"}, {Name: "B"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&TeamService{}).ListMembers(other.ID, group.ID); err == nil {
		t.Fatal("other tenant read team members")
	}
	members, err := (&TeamService{}).ListMembers(travel.ID, group.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("members=%+v err=%v", members, err)
	}
	if _, err := (&TeamService{}).EnterBatch(travel.ID, group.ID, 0, 7, []uint{members[0].ID, members[1].ID}); err == nil {
		t.Fatal("travel tenant admitted a team without paid order, tickets or supplier device")
	}
	var stored model.TourGroup
	if err := model.DB.First(&stored, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "draft" {
		t.Fatalf("group status=%s", stored.Status)
	}
}

func TestDeviceOfflineCreatesOneAlert(t *testing.T) {
	resetBusinessData(t)
	var tenant model.Tenant
	if err := model.Write(func(tx *gorm.DB) error {
		tenant = model.Tenant{Name: "Device Tenant", SystemCode: "DEV-T", SecretKey: "d"}
		return tx.Create(&tenant).Error
	}); err != nil {
		t.Fatal(err)
	}
	device := model.Device{Name: "Gate", SerialNumber: "G-1", Type: "gate", TenantID: tenant.ID, Status: "online"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&device).Error }); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Minute)
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&device).Updates(map[string]interface{}{"status": "online", "last_heartbeat": old}).Error
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewDeviceService(model.DB, &TicketService{})
	count, err := svc.MarkOffline(time.Now(), time.Minute)
	if err != nil || count != 1 {
		t.Fatalf("offline count=%d err=%v", count, err)
	}
	count, err = svc.MarkOffline(time.Now(), time.Minute)
	if err != nil || count != 0 {
		t.Fatalf("second offline count=%d err=%v", count, err)
	}
	var alerts []model.DeviceAlert
	if err := model.DB.Where("device_id = ?", device.ID).Find(&alerts).Error; err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].Type != "offline" {
		t.Fatalf("alerts=%+v", alerts)
	}
}

func TestDistributedCashRefundRestoresStockFundsAndLedger(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	order := model.Order{TenantID: scenario.distributorID, Channel: "online", Items: []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{OrderNo: order.OrderNo, Method: "cash"}
	if err := (&PaymentService{}).CreatePayment(scenario.distributorID, &payment); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	refund, err := (&RefundService{}).CreateCashRefund(scenario.distributorID, order.OrderNo, "dist-refund-1", order.TotalAmount, []string{ticket.TicketCode}, "customer request")
	if err != nil {
		t.Fatal(err)
	}
	if refund.Status != "succeeded" {
		t.Fatalf("refund status=%s", refund.Status)
	}
	var account model.CapitalAccount
	if err := model.DB.Where("owner_tenant_id = ? AND manager_tenant_id = ?", scenario.distributorID, scenario.supplierID).First(&account).Error; err != nil {
		t.Fatal(err)
	}
	if account.Balance != 100 || account.UsedCredit != 0 {
		t.Fatalf("account after refund balance=%v used_credit=%v", account.Balance, account.UsedCredit)
	}
	var source model.Product
	if err := model.DB.First(&source, scenario.sourceProductID).Error; err != nil {
		t.Fatal(err)
	}
	if source.DailyStock != 1 {
		t.Fatalf("stock after refund=%d, want 1", source.DailyStock)
	}
	var refundLedgerCount int64
	if err := model.DB.Model(&model.LedgerEntry{}).Where("account_id = ? AND entry_type = ? AND amount_cents = ?", account.ID, "refund_cash", 6000).Count(&refundLedgerCount).Error; err != nil {
		t.Fatal(err)
	}
	if refundLedgerCount != 1 {
		t.Fatalf("refund cash ledger count=%d", refundLedgerCount)
	}
	if _, err := (&RefundService{}).CreateCashRefund(scenario.distributorID, order.OrderNo, "dist-refund-1", order.TotalAmount, []string{ticket.TicketCode}, "customer request"); err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	if err := model.DB.Model(&model.LedgerEntry{}).Where("account_id = ? AND entry_type = ?", account.ID, "refund_cash").Count(&refundLedgerCount).Error; err != nil {
		t.Fatal(err)
	}
	if refundLedgerCount != 1 {
		t.Fatalf("idempotent refund ledger count=%d", refundLedgerCount)
	}
}

func TestDistributionCreditReservationAndReleaseAreReconstructable(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	if err := model.DB.Model(&model.CapitalAccount{}).
		Where("owner_tenant_id = ? AND manager_tenant_id = ?", scenario.distributorID, scenario.supplierID).
		Updates(map[string]interface{}{"balance": 0, "credit_line": 100, "used_credit": 0}).Error; err != nil {
		t.Fatal(err)
	}
	order := model.Order{TenantID: scenario.distributorID, Channel: "window", Items: []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).Cancel(order.OrderNo, scenario.distributorID); err != nil {
		t.Fatal(err)
	}
	var entries []model.LedgerEntry
	if err := model.DB.Where("related_order_no = ?", order.OrderNo).Order("id").Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].EntryType != "reservation_credit" || entries[0].AmountCents != 6000 || entries[1].EntryType != "release_credit" || entries[1].AmountCents != -6000 {
		t.Fatalf("credit ledger=%+v", entries)
	}
}

func TestDistributorCannotCreateOrImportWithoutSupplierOffer(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	if _, err := (&DistributionService{}).CreateOffer(scenario.distributorID, scenario.distributorID, scenario.sourceProductID, 1, 0, "window", nil, nil); err == nil {
		t.Fatal("distributor created an authoritative supplier offer")
	}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Where("seller_tenant_id = ?", scenario.distributorID).Delete(&model.SellerListing{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", scenario.listingID).Delete(&model.Product{}).Error; err != nil {
			return err
		}
		return tx.Where("supplier_tenant_id = ? AND distributor_tenant_id = ?", scenario.supplierID, scenario.distributorID).Delete(&model.ProductOffer{}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := (&DistributionService{}).ImportProduct(scenario.distributorID, scenario.sourceProductID, "Unauthorized", 1, "online"); err == nil {
		t.Fatal("distributor imported a product without a supplier offer")
	}
}

func TestProductUpdateCreatesNewRevisionForNewOrders(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	first := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&first); err != nil {
		t.Fatal(err)
	}
	var product model.Product
	if err := model.DB.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Where("id = ? AND tenant_id = ?", productID, tenantID).First(&product).Error; err != nil {
		t.Fatal(err)
	}
	firstRevision := first.Items[0].ProductRevisionID
	product.Price = 109.50
	product.Name = "Adult Ticket Revised"
	rule := product.Rule
	if err := (&ProductService{}).Update(product.ID, tenantID, &product, &rule); err != nil {
		t.Fatal(err)
	}
	second := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&second); err != nil {
		t.Fatal(err)
	}
	if second.Items[0].ProductRevisionID == firstRevision {
		t.Fatalf("new order reused revision %d", firstRevision)
	}
	if first.Items[0].Price != 99.50 || second.Items[0].Price != 109.50 {
		t.Fatalf("order prices first=%v second=%v", first.Items[0].Price, second.Items[0].Price)
	}
	var revisions []model.ProductRevision
	if err := model.DB.Where("product_id = ?", productID).Order("version").Find(&revisions).Error; err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 || revisions[0].Status != "expired" || revisions[1].Status != "active" {
		t.Fatalf("product revisions=%+v", revisions)
	}
}

func TestTeamMultiBatchAdmissionUsesSupplierTicketsAndDevice(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&model.TenantCapability{TenantID: scenario.distributorID, Capability: "travel_agency", Status: "active"}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Product{}).Where("id = ?", scenario.sourceProductID).Update("daily_stock", 2).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.CapitalAccount{}).Where("owner_tenant_id = ? AND manager_tenant_id = ?", scenario.distributorID, scenario.supplierID).Update("balance", 200).Error; err != nil {
			return err
		}
		return tx.Create(&model.User{Username: "supplier-operator", Password: "test", Role: "admin", TenantID: scenario.supplierID}).Error
	}); err != nil {
		t.Fatal(err)
	}
	var source model.Product
	if err := model.DB.First(&source, scenario.sourceProductID).Error; err != nil {
		t.Fatal(err)
	}
	order := model.Order{TenantID: scenario.distributorID, Channel: "window", Items: []model.OrderItem{{ProductID: scenario.listingID, Quantity: 2}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, scenario.distributorID); err != nil {
		t.Fatal(err)
	}
	group := model.TourGroup{Name: "Paid Team", SupplierTenantID: scenario.supplierID, ScenicAreaID: source.ScenicAreaID, VisitDate: time.Now()}
	teamService := &TeamService{}
	if err := teamService.CreateGroup(scenario.distributorID, &group); err != nil {
		t.Fatal(err)
	}
	if _, err := teamService.AddMembers(scenario.distributorID, group.ID, []model.TourGroupMember{{Name: "A"}, {Name: "B"}}); err != nil {
		t.Fatal(err)
	}
	if err := teamService.AttachOrder(scenario.distributorID, group.ID, order.ID); err != nil {
		t.Fatal(err)
	}
	members, err := teamService.ListMembers(scenario.distributorID, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	var operator model.User
	if err := model.DB.Where("tenant_id = ?", scenario.supplierID).First(&operator).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := teamService.EnterBatch(scenario.supplierID, group.ID, scenario.supplierDeviceID, operator.ID, []uint{members[0].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := teamService.EnterBatch(scenario.supplierID, group.ID, scenario.supplierDeviceID, operator.ID, []uint{members[1].ID}); err != nil {
		t.Fatal(err)
	}
	var stored model.TourGroup
	if err := model.DB.First(&stored, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "entered" {
		t.Fatalf("group status=%s", stored.Status)
	}
	var checkIns int64
	if err := model.DB.Model(&model.CheckInRecord{}).Where("scenic_area_id = ? AND device_id = ? AND message = ?", source.ScenicAreaID, scenario.supplierDeviceID, "team admission").Count(&checkIns).Error; err != nil {
		t.Fatal(err)
	}
	if checkIns != 2 {
		t.Fatalf("team check-in facts=%d, want 2", checkIns)
	}
}
