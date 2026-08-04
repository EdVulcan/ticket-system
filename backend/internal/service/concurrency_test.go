//go:build cgo

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestExternalOrderUniqueViolationDetection(t *testing.T) {
	externalOrderConflict := fmt.Errorf("create order: %w", &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "idx_order_external",
	})
	if !isExternalOrderUniqueViolation(externalOrderConflict) {
		t.Fatal("external order unique violation was not detected")
	}
	otherConflict := &pgconn.PgError{Code: "23505", ConstraintName: "uni_orders_order_no"}
	if isExternalOrderUniqueViolation(otherConflict) {
		t.Fatal("unrelated unique violation was detected as a duplicate external order")
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("TICKET_TEST_POSTGRES") == "1" {
		config.GlobalConfig.Database = config.DatabaseConfig{
			Driver: "postgres", Host: "127.0.0.1", Port: 5432, Name: "ticket_system_test", User: "postgres",
			Password: os.Getenv("PGPASSWORD"), SSLMode: "disable", TimeZone: "Asia/Shanghai",
			MaxOpenConnections: 30, MaxIdleConnections: 5, ConnMaxLifetimeMinutes: 5,
			WriteQueueSize: 256, WriteTimeoutSeconds: 10, EnqueueTimeoutSeconds: 2,
		}
		if err := model.InitDB(); err != nil {
			panic(err)
		}
		model.DB.Logger = logger.Default.LogMode(logger.Silent)
		code := m.Run()
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = model.CloseWriter(closeContext)
		cancel()
		if sqlDB, dbErr := model.DB.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
		os.Exit(code)
	}

	databasePath, err := os.CreateTemp("", "ticket-service-test-*.db")
	if err != nil {
		panic(err)
	}
	path := databasePath.Name()
	_ = databasePath.Close()

	db, err := gorm.Open(sqlite.Open("file:"+path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		panic(err)
	}
	if err := db.AutoMigrate(
		&model.Tenant{}, &model.TenantCapability{}, &model.User{}, &model.Staff{}, &model.ScenicArea{}, &model.PlatformUser{}, &model.CheckPoint{}, &model.Device{}, &model.TicketRule{}, &model.RuleGroup{}, &model.RuleItem{},
		&model.Product{}, &model.ProductOffer{}, &model.SellerListing{}, &model.ProductInventory{}, &model.Order{}, &model.OrderItem{}, &model.Ticket{}, &model.OrderVisitor{}, &model.FulfillmentOrder{}, &model.TicketEntitlement{},
		&model.CheckInRecord{}, &model.DistributorRelationship{}, &model.CapitalAccount{}, &model.TransactionRecord{},
		&model.LedgerEntry{}, &model.DigitalRefundTask{}, &model.ChannelAccount{}, &model.ChannelProductMapping{}, &model.ChannelRequest{}, &model.CtripOrderLink{}, &model.CtripOrderItem{},
		&model.Policy{}, &model.Payment{}, &model.Refund{}, &model.PaymentConfig{}, &model.PaymentReconciliationTask{}, &model.AuditLog{},
		&model.TravelContract{}, &model.TravelAgent{}, &model.TourGuide{}, &model.TravelVehicle{}, &model.TourGroup{}, &model.TourGroupMember{}, &model.TourEntryBatch{}, &model.TourGroupConfirmation{}, &model.TourGroupMemberChange{},
		&model.POSShift{}, &model.POSShiftCorrection{}, &model.PrintJob{}, &model.DeviceAlert{}, &model.POSHold{},
		&model.ProductRevision{}, &model.SettlementStatement{}, &model.SettlementLine{}, &model.SettlementAdjustment{}, &model.StaffResourceScope{},
		&model.AfterSaleRequest{}, &model.AfterSaleEvent{}, &model.HardwareCommand{}, &model.HardwareEvent{}, &model.DeviceRequestNonce{}, &model.DeviceVerification{}, &model.ChannelReservation{}, &model.FinancialDocument{}, &model.TeamSettlementStatement{}, &model.TeamSettlementAdjustment{},
		&model.ChannelBillRecord{}, &model.ChannelReconciliation{}, &model.ChannelReconciliationLine{},
		&model.BundleProduct{}, &model.BundleVersion{}, &model.BundleComponent{},
	); err != nil {
		panic(err)
	}
	model.DB = db
	model.InitWriter(db, 256, 2*time.Second, 10*time.Second)

	code := m.Run()
	closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = model.CloseWriter(closeContext)
	cancel()
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		_ = sqlDB.Close()
	}
	_ = os.Remove(path)
	os.Exit(code)
}

func resetBusinessData(t *testing.T) {
	t.Helper()
	err := model.Write(func(tx *gorm.DB) error {
		for _, table := range []interface{}{
			&model.StaffResourceScope{}, &model.User{}, &model.Staff{},
			&model.Policy{}, &model.Payment{}, &model.Refund{}, &model.PaymentConfig{}, &model.PaymentReconciliationTask{}, &model.AuditLog{}, &model.LedgerEntry{}, &model.DigitalRefundTask{},
			&model.CtripOrderItem{}, &model.CtripOrderLink{}, &model.ChannelAccount{}, &model.ChannelProductMapping{}, &model.ChannelRequest{}, &model.ChannelReservation{}, &model.TourGroupConfirmation{}, &model.TourGroupMemberChange{}, &model.TourGroupMember{}, &model.TourEntryBatch{}, &model.TourGroup{}, &model.TravelContract{}, &model.TravelAgent{}, &model.TourGuide{}, &model.TravelVehicle{}, &model.POSShiftCorrection{}, &model.POSShift{}, &model.PrintJob{}, &model.DeviceAlert{}, &model.POSHold{},
			&model.AfterSaleEvent{}, &model.AfterSaleRequest{}, &model.HardwareEvent{}, &model.HardwareCommand{}, &model.DeviceRequestNonce{}, &model.DeviceVerification{}, &model.FinancialDocument{}, &model.TeamSettlementAdjustment{}, &model.TeamSettlementStatement{},
			&model.ChannelReconciliationLine{}, &model.ChannelBillRecord{}, &model.ChannelReconciliation{},
			&model.BundleComponent{}, &model.BundleProduct{}, &model.BundleVersion{},
			&model.ProductRevision{}, &model.SettlementLine{}, &model.SettlementAdjustment{}, &model.SettlementStatement{},
			&model.CheckInRecord{}, &model.OrderVisitor{}, &model.Ticket{}, &model.OrderItem{}, &model.Order{}, &model.ProductInventory{},
			&model.Product{}, &model.RuleItem{}, &model.RuleGroup{}, &model.TicketRule{}, &model.Device{}, &model.CheckPoint{},
			&model.TransactionRecord{}, &model.CapitalAccount{}, &model.DistributorRelationship{}, &model.TenantCapability{}, &model.ScenicArea{}, &model.PlatformUser{}, &model.TicketEntitlement{}, &model.FulfillmentOrder{}, &model.SellerListing{}, &model.ProductOffer{}, &model.Tenant{},
		} {
			if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(table).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reset database: %v", err)
	}
}

func verificationDeviceID(t *testing.T, tenantID, checkpointID uint) uint {
	t.Helper()
	var device model.Device
	if err := model.DB.Where("tenant_id = ? AND check_point_id = ?", tenantID, checkpointID).First(&device).Error; err != nil {
		t.Fatalf("load verification device: %v", err)
	}
	return device.ID
}

func TestConcurrentExternalOrderIsIdempotent(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	externalNo := "OTA-SAME-ORDER"
	var created atomic.Int32
	var duplicates atomic.Int32
	var unexpected atomic.Int32
	var wait sync.WaitGroup
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			order := model.Order{
				TenantID: tenantID, Channel: "ota", ExternalNo: &externalNo,
				Items: []model.OrderItem{{ProductID: productID, Quantity: 1}},
			}
			err := (&OrderService{}).Create(&order)
			switch {
			case err == nil:
				created.Add(1)
			case errors.Is(err, ErrDuplicateExternalOrder):
				duplicates.Add(1)
			default:
				unexpected.Add(1)
			}
		}()
	}
	wait.Wait()
	if created.Load() != 1 || duplicates.Load() != 11 || unexpected.Load() != 0 {
		t.Fatalf("created=%d duplicates=%d unexpected=%d", created.Load(), duplicates.Load(), unexpected.Load())
	}
	var count int64
	if err := model.DB.Model(&model.Order{}).Where("tenant_id = ? AND channel = ? AND external_no = ?", tenantID, "ota", externalNo).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("stored external orders = %d, want 1", count)
	}
}

func TestConcurrentOrdersRespectDailyStock(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "daily", 5)
	visitDate := startOfDay(time.Now().AddDate(0, 0, 1))
	var successes atomic.Int32
	var wait sync.WaitGroup
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			date := visitDate
			order := model.Order{
				TenantID: tenantID, Channel: "window",
				Items: []model.OrderItem{{ProductID: productID, Quantity: 1, UseDate: &date}},
			}
			if err := (&OrderService{}).Create(&order); err == nil {
				successes.Add(1)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 5 {
		t.Fatalf("successful daily-stock orders = %d, want 5", successes.Load())
	}
	var inventory model.ProductInventory
	if err := model.DB.Where("tenant_id = ? AND product_id = ? AND stock_date = ?", tenantID, productID, visitDate).First(&inventory).Error; err != nil {
		t.Fatal(err)
	}
	if inventory.Sold != 5 || inventory.Capacity != 5 {
		t.Fatalf("inventory sold=%d capacity=%d, want 5/5", inventory.Sold, inventory.Capacity)
	}
}

func TestMultiPersonOrderTicketRemainsActiveUntilBenefitsExhausted(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.Product{}).Where("id = ?", productID).Update("code_mode", "order").Error
	}); err != nil {
		t.Fatal(err)
	}
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 2}}}
	orderService := &OrderService{}
	if err := orderService.Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := orderService.MarkAsPaid(order.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	var checkpoint model.CheckPoint
	var ticket model.Ticket
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	service := &TicketService{}
	deviceID := verificationDeviceID(t, tenantID, checkpoint.ID)
	if err := service.Verify(ticket.TicketCode, checkpoint.ID, deviceID, tenantID); err != nil {
		t.Fatalf("first admission: %v", err)
	}
	if err := model.DB.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.Status != "active" || ticket.CheckInCount != 1 {
		t.Fatalf("after first admission status=%s count=%d", ticket.Status, ticket.CheckInCount)
	}
	if err := service.Verify(ticket.TicketCode, checkpoint.ID, deviceID, tenantID); err != nil {
		t.Fatalf("second admission: %v", err)
	}
	if err := model.DB.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.Status != "used" || ticket.CheckInCount != 2 {
		t.Fatalf("after second admission status=%s count=%d", ticket.Status, ticket.CheckInCount)
	}
	if err := service.Verify(ticket.TicketCode, checkpoint.ID, deviceID, tenantID); !errors.Is(err, ErrTicketUnavailable) {
		t.Fatalf("third admission error = %v, want unavailable", err)
	}
}

func TestCashPaymentUsesStoredAmountAndTenantScope(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{OrderNo: order.OrderNo, Amount: 0.01, Method: "cash"}
	if err := (&PaymentService{}).CreatePayment(tenantID, &payment); err != nil {
		t.Fatal(err)
	}
	if payment.Amount != 99.50 || payment.Status != "paid" {
		t.Fatalf("payment amount=%v status=%s, want 99.50/paid", payment.Amount, payment.Status)
	}
	var storedOrder model.Order
	if err := model.DB.Where("order_no = ?", order.OrderNo).First(&storedOrder).Error; err != nil {
		t.Fatal(err)
	}
	if storedOrder.Status != "paid" {
		t.Fatalf("stored order status=%s, want paid", storedOrder.Status)
	}
	if _, err := (&PaymentService{}).GetStatus(payment.ID, tenantID+999); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant payment lookup error=%v, want not found", err)
	}
}

func TestCashRefundIsTicketScopedAndIdempotent(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{OrderNo: order.OrderNo, Method: "cash"}
	if err := (&PaymentService{}).CreatePayment(tenantID, &payment); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_item_id IN (SELECT id FROM order_items WHERE order_id = ?)", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	refundService := &RefundService{}
	refund, err := refundService.CreateCashRefund(tenantID, order.OrderNo, "REFUND-1", order.TotalAmount, []string{ticket.TicketCode}, "visitor request")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := refundService.CreateCashRefund(tenantID, order.OrderNo, "REFUND-1", order.TotalAmount, []string{ticket.TicketCode}, "visitor request")
	if err != nil {
		t.Fatal(err)
	}
	if refund.ID != duplicate.ID || refund.Status != "succeeded" {
		t.Fatalf("refund idempotency IDs=%d/%d status=%s", refund.ID, duplicate.ID, refund.Status)
	}
	var storedOrder model.Order
	if err := model.DB.Where("order_no = ?", order.OrderNo).First(&storedOrder).Error; err != nil {
		t.Fatal(err)
	}
	if storedOrder.Status != "refunded" {
		t.Fatalf("order status=%s, want refunded", storedOrder.Status)
	}
	var storedTicket model.Ticket
	if err := model.DB.First(&storedTicket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedTicket.Status != "refunded" {
		t.Fatalf("ticket status=%s, want refunded", storedTicket.Status)
	}
	var storedPayment model.Payment
	if err := model.DB.First(&storedPayment, payment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedPayment.RefundedAmount != storedPayment.Amount || storedPayment.RefundedAmountCents != storedPayment.AmountCents {
		t.Fatalf("refunded amount=%v paid=%v", storedPayment.RefundedAmount, storedPayment.Amount)
	}
	stats, err := (&ReportService{}).GetSalesStats(tenantID, time.Now().AddDate(0, 0, -1).Format("2006-01-02"), time.Now().Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) == 0 || stats[len(stats)-1].RefundedAmount != storedPayment.Amount || stats[len(stats)-1].NetAmount != 0 {
		t.Fatalf("refund report=%+v, want refunded=%v net=0", stats, storedPayment.Amount)
	}
	if _, err := refundService.CreateCashRefund(tenantID, order.OrderNo, "REFUND-2", order.TotalAmount, []string{ticket.TicketCode}, "duplicate"); err == nil {
		t.Fatal("refunded ticket was refunded again")
	}
}

func TestPaymentNotificationCompletesOrderIdempotently(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{
		TenantID: tenantID,
		Channel:  "window",
		Items:    []model.OrderItem{{ProductID: productID, Quantity: 1}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{
		TenantID:  tenantID,
		PaymentNo: "PAY-NOTIFY-IDEMPOTENT",
		OrderNo:   order.OrderNo,
		Amount:    order.TotalAmount,
		Method:    "wechat",
		Status:    "pending",
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&payment).Error }); err != nil {
		t.Fatal(err)
	}

	service := &PaymentService{}
	if err := service.CompleteNotification(tenantID, payment.PaymentNo, "wechat", "WX-TXN-1", order.TotalAmount); err != nil {
		t.Fatalf("first notification: %v", err)
	}
	if err := service.CompleteNotification(tenantID, payment.PaymentNo, "wechat", "WX-TXN-1", order.TotalAmount); err != nil {
		t.Fatalf("duplicate notification: %v", err)
	}
	var storedPayment model.Payment
	if err := model.DB.Where("payment_no = ? AND tenant_id = ?", payment.PaymentNo, tenantID).First(&storedPayment).Error; err != nil {
		t.Fatal(err)
	}
	if storedPayment.Status != "paid" || storedPayment.TransactionID != "WX-TXN-1" {
		t.Fatalf("payment status=%s transaction=%s", storedPayment.Status, storedPayment.TransactionID)
	}
	var storedOrder model.Order
	if err := model.DB.Where("order_no = ? AND tenant_id = ?", order.OrderNo, tenantID).First(&storedOrder).Error; err != nil {
		t.Fatal(err)
	}
	if storedOrder.Status != "paid" {
		t.Fatalf("order status=%s, want paid", storedOrder.Status)
	}
}

func TestPaymentNotificationRejectsAmountAndTenantMismatch(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{
		TenantID:  tenantID,
		PaymentNo: "PAY-NOTIFY-MISMATCH",
		OrderNo:   order.OrderNo,
		Amount:    order.TotalAmount,
		Method:    "alipay",
		Status:    "pending",
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&payment).Error }); err != nil {
		t.Fatal(err)
	}
	service := &PaymentService{}
	if err := service.CompleteNotification(tenantID, payment.PaymentNo, "alipay", "ALI-TXN-1", order.TotalAmount+1); err == nil {
		t.Fatal("amount mismatch was accepted")
	}
	if err := service.CompleteNotification(tenantID+999, payment.PaymentNo, "alipay", "ALI-TXN-1", order.TotalAmount); err == nil {
		t.Fatal("wrong tenant notification was accepted")
	}
	var storedPayment model.Payment
	if err := model.DB.Where("payment_no = ?", payment.PaymentNo).First(&storedPayment).Error; err != nil {
		t.Fatal(err)
	}
	if storedPayment.Status != "pending" {
		t.Fatalf("payment status=%s, want pending", storedPayment.Status)
	}
	var storedOrder model.Order
	if err := model.DB.Where("order_no = ?", order.OrderNo).First(&storedOrder).Error; err != nil {
		t.Fatal(err)
	}
	if storedOrder.Status != "unpaid" {
		t.Fatalf("order status=%s, want unpaid", storedOrder.Status)
	}
}

func TestUnavailablePaymentAdapterDoesNotCancelOrder(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{OrderNo: order.OrderNo, Method: "wechat", PayType: "cscanb"}
	if err := (&PaymentService{OrderService: &OrderService{}}).CreatePayment(tenantID, &payment); err == nil {
		t.Fatal("unconfigured payment unexpectedly succeeded")
	}
	var storedOrder model.Order
	if err := model.DB.Where("id = ? AND tenant_id = ?", order.ID, tenantID).First(&storedOrder).Error; err != nil {
		t.Fatal(err)
	}
	if storedOrder.Status != "unpaid" {
		t.Fatalf("order status=%s, want unpaid so cashier can choose another method", storedOrder.Status)
	}
}

func TestFailedPaymentNotificationReleasesDistributionReservation(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	order := model.Order{
		TenantID: scenario.distributorID,
		Channel:  "window",
		Items:    []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{
		TenantID:  scenario.distributorID,
		PaymentNo: "PAY-NOTIFY-FAIL",
		OrderNo:   order.OrderNo,
		Amount:    order.TotalAmount,
		Method:    "alipay",
		Status:    "pending",
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&payment).Error }); err != nil {
		t.Fatal(err)
	}
	if err := (&PaymentService{}).FailNotification(scenario.distributorID, payment.PaymentNo, "alipay", "TRADE_CLOSED"); err != nil {
		t.Fatalf("failure notification: %v", err)
	}
	if err := (&PaymentService{}).FailNotification(scenario.distributorID, payment.PaymentNo, "alipay", "TRADE_CLOSED"); err != nil {
		t.Fatalf("duplicate failure notification: %v", err)
	}
	var storedOrder model.Order
	if err := model.DB.Where("order_no = ?", order.OrderNo).First(&storedOrder).Error; err != nil {
		t.Fatal(err)
	}
	if storedOrder.Status != "cancelled" {
		t.Fatalf("order status=%s, want cancelled", storedOrder.Status)
	}
	var source model.Product
	if err := model.DB.First(&source, scenario.sourceProductID).Error; err != nil {
		t.Fatal(err)
	}
	if source.DailyStock != 1 {
		t.Fatalf("supplier stock=%d, want 1 after release", source.DailyStock)
	}
	var account model.CapitalAccount
	if err := model.DB.Where("owner_tenant_id = ? AND manager_tenant_id = ?", scenario.distributorID, scenario.supplierID).First(&account).Error; err != nil {
		t.Fatal(err)
	}
	if account.Balance != 100 || account.UsedCredit != 0 || account.FrozenAmount != 0 {
		t.Fatalf("account after release balance=%v used_credit=%v frozen=%v", account.Balance, account.UsedCredit, account.FrozenAmount)
	}
}

func TestPaymentReconciliationRepairsMissingTaskAfterRestart(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{
		TenantID:  tenantID,
		PaymentNo: "PAY-RECOVERY-MISSING-TASK",
		OrderNo:   order.OrderNo,
		Amount:    order.TotalAmount,
		Method:    "wechat",
		Status:    "pending",
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&payment).Error }); err != nil {
		t.Fatal(err)
	}
	service := &PaymentService{OrderService: &OrderService{}}
	now := time.Now()
	if err := service.EnsurePaymentReconciliationTasks(now); err != nil {
		t.Fatal(err)
	}
	if err := service.EnsurePaymentReconciliationTasks(now); err != nil {
		t.Fatal(err)
	}
	var tasks []model.PaymentReconciliationTask
	if err := model.DB.Where("payment_id = ?", payment.ID).Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("reconciliation tasks=%d, want 1", len(tasks))
	}
	// A payment already completed before the crash is reconciled without any
	// provider call and its task is closed on the next startup pass.
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.Payment{}).Where("id = ?", payment.ID).Update("status", "paid").Error
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProcessPaymentReconciliationTasks(context.Background(), now, 1); err != nil {
		t.Fatal(err)
	}
	var task model.PaymentReconciliationTask
	if err := model.DB.First(&task, tasks[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "completed" {
		t.Fatalf("task status=%s, want completed", task.Status)
	}
}

func TestPaymentReconciliationBacksOffProviderErrors(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{
		TenantID:  tenantID,
		PaymentNo: "PAY-RECOVERY-RETRY",
		OrderNo:   order.OrderNo,
		Amount:    order.TotalAmount,
		Method:    "unsupported",
		Status:    "pending",
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&payment).Error }); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Create(&model.PaymentReconciliationTask{
			TenantID: tenantID, PaymentID: payment.ID, PaymentNo: payment.PaymentNo,
			Status: "pending", NextRunAt: time.Now().Add(-time.Second),
		}).Error
	}); err != nil {
		t.Fatal(err)
	}
	service := &PaymentService{}
	if _, err := service.ProcessPaymentReconciliationTasks(context.Background(), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	var task model.PaymentReconciliationTask
	if err := model.DB.Where("payment_id = ?", payment.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "pending" || task.Attempts != 1 || task.LastError == "" || !task.NextRunAt.After(time.Now()) {
		t.Fatalf("task retry state status=%s attempts=%d next=%s error=%q", task.Status, task.Attempts, task.NextRunAt, task.LastError)
	}
}

func TestTenantLifecycleAndCapabilitiesAreIndependent(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	tenantService := &TenantService{}
	hashedPassword, err := HashPassword("platform-pass")
	if err != nil {
		t.Fatal(err)
	}
	platformUser := model.PlatformUser{Username: "platform-admin", Password: hashedPassword, Role: "platform_admin", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&platformUser).Error }); err != nil {
		t.Fatal(err)
	}
	if token, user, err := (&AuthService{}).PlatformLogin("platform-admin", "platform-pass"); err != nil || token == "" || user.ID != platformUser.ID {
		t.Fatalf("platform login token=%q user=%v err=%v", token, user, err)
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Model(&platformUser).Update("status", "frozen").Error }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (&AuthService{}).PlatformLogin("platform-admin", "platform-pass"); err == nil {
		t.Fatal("frozen platform user was allowed to login")
	}
	if err := tenantService.SetCapability(tenantID, "supplier", "active", "verified supplier"); err != nil {
		t.Fatal(err)
	}
	if err := tenantService.SetCapability(tenantID, "distributor", "active", "verified distributor"); err != nil {
		t.Fatal(err)
	}
	if err := tenantService.UpdateStatus(tenantID, "frozen"); err != nil {
		t.Fatal(err)
	}
	var tenant model.Tenant
	if err := model.DB.First(&tenant, tenantID).Error; err != nil {
		t.Fatal(err)
	}
	if tenant.Status != "frozen" {
		t.Fatalf("tenant status=%s, want frozen", tenant.Status)
	}
	var capabilities []model.TenantCapability
	if err := model.DB.Where("tenant_id = ?", tenantID).Order("capability").Find(&capabilities).Error; err != nil {
		t.Fatal(err)
	}
	if len(capabilities) != 2 || capabilities[0].Status != "active" || capabilities[1].Status != "active" {
		t.Fatalf("capabilities=%+v, want independent active capabilities", capabilities)
	}
	if err := tenantService.SetCapability(tenantID, "distributor", "suspended", "agreement expired"); err != nil {
		t.Fatal(err)
	}
	var distributorCapability model.TenantCapability
	if err := model.DB.Where("tenant_id = ? AND capability = ?", tenantID, "distributor").First(&distributorCapability).Error; err != nil {
		t.Fatal(err)
	}
	if distributorCapability.Status != "suspended" {
		t.Fatalf("distributor capability=%s, want suspended", distributorCapability.Status)
	}
}

func TestTicketCannotCrossScenicAreaWithinSameSupplier(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	var firstArea, secondArea model.ScenicArea
	var firstCheckpoint model.CheckPoint
	if err := model.Write(func(tx *gorm.DB) error {
		firstArea = model.ScenicArea{TenantID: tenantID, Code: "A", Name: "Park A", Status: "active"}
		if err := tx.Create(&firstArea).Error; err != nil {
			return err
		}
		secondArea = model.ScenicArea{TenantID: tenantID, Code: "B", Name: "Park B", Status: "active"}
		if err := tx.Create(&secondArea).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.CheckPoint{}).Where("tenant_id = ?", tenantID).First(&firstCheckpoint).Error; err != nil {
			return err
		}
		if err := tx.Model(&firstCheckpoint).Update("scenic_area_id", firstArea.ID).Error; err != nil {
			return err
		}
		return tx.Model(&model.Product{}).Where("id = ?", productID).Update("scenic_area_id", firstArea.ID).Error
	}); err != nil {
		t.Fatal(err)
	}
	secondCheckpoint := model.CheckPoint{Name: "Park B Gate", TenantID: tenantID, ScenicAreaID: secondArea.ID}
	var secondDevice model.Device
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&secondCheckpoint).Error; err != nil {
			return err
		}
		checkpointID := secondCheckpoint.ID
		secondDevice = model.Device{Name: "Park B Device", SerialNumber: fmt.Sprintf("PARK-B-%d", time.Now().UnixNano()), Type: "gate", Status: "online", TenantID: tenantID, ScenicAreaID: secondArea.ID, CheckPointID: &checkpointID, AuthKeyHash: hashDeviceKey("test-device-key")}
		return tx.Create(&secondDevice).Error
	}); err != nil {
		t.Fatal(err)
	}
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_item_id IN (SELECT id FROM order_items WHERE order_id = ?)", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.FulfillmentScenicAreaID != firstArea.ID {
		t.Fatalf("ticket scenic area=%d, want %d", ticket.FulfillmentScenicAreaID, firstArea.ID)
	}
	if err := (&TicketService{}).Verify(ticket.TicketCode, secondCheckpoint.ID, secondDevice.ID, tenantID); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("cross-area verification error=%v, want invalid ticket", err)
	}
}

func TestScenicAreaServiceKeepsTenantScopeAndProtectsReferencedAreas(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	otherTenantID, _ := seedSellableProduct(t, "unlimited", 0)
	service := &ScenicAreaService{}
	area := &model.ScenicArea{Code: "NORTH", Name: "North Park"}
	if err := service.Create(tenantID, area); err != nil {
		t.Fatal(err)
	}
	if err := service.Update(area.ID, otherTenantID, &model.ScenicArea{Code: "NORTH", Name: "Hijack", Status: "active"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant area update error=%v, want not found", err)
	}
	if err := service.Delete(area.ID, tenantID); err != nil {
		t.Fatal(err)
	}
	var checkpoint model.CheckPoint
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	areaWithReference := &model.ScenicArea{Code: "SOUTH", Name: "South Park"}
	if err := service.Create(tenantID, areaWithReference); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&checkpoint).Update("scenic_area_id", areaWithReference.ID).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(areaWithReference.ID, tenantID); err == nil {
		t.Fatal("referenced scenic area was deleted")
	}
}

func seedSellableProduct(t *testing.T, stockType string, stock int) (uint, uint) {
	t.Helper()
	var tenantID, productID uint
	err := model.Write(func(tx *gorm.DB) error {
		tenant := model.Tenant{Name: "Test Tenant", SystemCode: fmt.Sprintf("TEST-%d", time.Now().UnixNano()), SecretKey: "test-secret"}
		if err := tx.Create(&tenant).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.TenantCapability{TenantID: tenant.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
			return err
		}
		area := model.ScenicArea{TenantID: tenant.ID, Code: fmt.Sprintf("AREA-%d", tenant.ID), Name: "Main Park", Status: "active"}
		if err := tx.Create(&area).Error; err != nil {
			return err
		}
		checkpoint := model.CheckPoint{Name: "Main Gate", TenantID: tenant.ID, ScenicAreaID: area.ID}
		if err := tx.Create(&checkpoint).Error; err != nil {
			return err
		}
		checkpointID := checkpoint.ID
		if err := tx.Create(&model.Device{Name: "Main Gate Device", SerialNumber: fmt.Sprintf("DEV-%d", time.Now().UnixNano()), Type: "gate", Status: "online", TenantID: tenant.ID, ScenicAreaID: area.ID, CheckPointID: &checkpointID, AuthKeyHash: hashDeviceKey("test-device-key")}).Error; err != nil {
			return err
		}
		rule := model.TicketRule{Name: "Single Entry", TenantID: tenant.ID, ValidityType: "date"}
		if err := tx.Create(&rule).Error; err != nil {
			return err
		}
		group := model.RuleGroup{RuleID: rule.ID, GroupName: "Admission", MaxTotalCheckIn: 1}
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.RuleItem{GroupID: group.ID, CheckPointID: checkpoint.ID, MaxPerCheckIn: 1}).Error; err != nil {
			return err
		}
		product := model.Product{
			Name: "Adult Ticket", Price: 99.50, SettlementPrice: 60,
			TenantID: tenant.ID, ScenicAreaID: area.ID, RuleID: rule.ID, Type: "online", Status: "online",
			ValidityType: "date", StockType: stockType, DailyStock: stock, CodeMode: "ticket",
		}
		if err := tx.Omit("Rule").Create(&product).Error; err != nil {
			return err
		}
		tenantID, productID = tenant.ID, product.ID
		return nil
	})
	if err != nil {
		t.Fatalf("seed product: %v", err)
	}
	return tenantID, productID
}

func TestOrderCreateUsesServerPrice(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{
		TenantID: tenantID, TotalAmount: 0.01, Status: "paid", Channel: "window",
		Items: []model.OrderItem{{ProductID: productID, Quantity: 2, Price: 0.01}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if order.TotalAmount != 199.00 || order.Items[0].Price != 99.50 {
		t.Fatalf("server price not applied: total=%v item=%v", order.TotalAmount, order.Items[0].Price)
	}
	if order.Status != "unpaid" {
		t.Fatalf("client supplied status was trusted: %s", order.Status)
	}
}

func TestConcurrentOrdersRespectTotalStock(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "total", 10)

	var successes atomic.Int32
	var lockErrors atomic.Int32
	var wait sync.WaitGroup
	for i := 0; i < 30; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
			err := (&OrderService{}).Create(&order)
			if err == nil {
				successes.Add(1)
			} else if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
				lockErrors.Add(1)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 10 {
		t.Fatalf("successful orders = %d, want 10", successes.Load())
	}
	if lockErrors.Load() != 0 {
		t.Fatalf("encountered %d database lock errors", lockErrors.Load())
	}
	var product model.Product
	if err := model.DB.First(&product, productID).Error; err != nil {
		t.Fatal(err)
	}
	if product.DailyStock != 0 {
		t.Fatalf("remaining total stock = %d, want 0", product.DailyStock)
	}
}

func TestConcurrentVerificationConsumesOneAdmission(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	orderService := &OrderService{}
	if err := orderService.Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := orderService.MarkAsPaid(order.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	var checkpoint model.CheckPoint
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}

	var successes atomic.Int32
	var lockErrors atomic.Int32
	var wait sync.WaitGroup
	deviceID := verificationDeviceID(t, tenantID, checkpoint.ID)
	for i := 0; i < 20; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			err := (&TicketService{}).Verify(ticket.TicketCode, checkpoint.ID, deviceID, tenantID)
			if err == nil {
				successes.Add(1)
			} else if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
				lockErrors.Add(1)
			} else if !errors.Is(err, ErrTicketUnavailable) && !errors.Is(err, ErrPointLimitReached) {
				t.Errorf("unexpected verification error: %v", err)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 1 {
		t.Fatalf("successful verifications = %d, want 1", successes.Load())
	}
	if lockErrors.Load() != 0 {
		t.Fatalf("encountered %d database lock errors", lockErrors.Load())
	}
	var successfulRecords int64
	if err := model.DB.Model(&model.CheckInRecord{}).Where("ticket_id = ? AND result = ?", ticket.ID, "success").Count(&successfulRecords).Error; err != nil {
		t.Fatal(err)
	}
	if successfulRecords != 1 {
		t.Fatalf("successful check-in records = %d, want 1", successfulRecords)
	}
}

func TestDirectDeviceVerificationReplaysResultAndTracksGateOpen(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	orders := &OrderService{}
	if err := orders.Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := orders.MarkAsPaid(order.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	var checkpoint model.CheckPoint
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	deviceID := verificationDeviceID(t, tenantID, checkpoint.ID)
	svc := NewDeviceService(model.DB, &TicketService{})
	req := DirectVerifyRequest{TenantID: tenantID, DeviceID: deviceID, CheckPointID: checkpoint.ID, RequestID: "scan-1", RequestHash: "body-hash", TicketCode: ticket.TicketCode}
	first, err := svc.VerifyDirect(req)
	if err != nil || first.Result != "allow" {
		t.Fatalf("first verification=%+v err=%v", first, err)
	}
	second, err := svc.VerifyDirect(req)
	if err != nil || second.Result != "allow" || second.DisplayText != first.DisplayText {
		t.Fatalf("replayed verification=%+v err=%v", second, err)
	}
	var successful int64
	if err := model.DB.Model(&model.CheckInRecord{}).Where("device_id = ? AND device_request_id = ? AND result = ?", deviceID, req.RequestID, "success").Count(&successful).Error; err != nil || successful != 1 {
		t.Fatalf("successful records=%d err=%v", successful, err)
	}
	open := OpenResultRequest{VerificationRequestID: req.RequestID, Status: "opened", OccurredAt: time.Now().Format(time.RFC3339)}
	if err := svc.ReportOpenResult(tenantID, deviceID, open); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReportOpenResult(tenantID, deviceID, open); err != nil {
		t.Fatalf("idempotent open report: %v", err)
	}
	if err := svc.ReportOpenResult(tenantID, deviceID, OpenResultRequest{VerificationRequestID: req.RequestID, Status: "failed"}); err == nil {
		t.Fatal("conflicting open result should be rejected")
	}
	var events int64
	if err := model.DB.Model(&model.HardwareEvent{}).Where("device_id = ? AND command_no = ? AND event_type = ?", deviceID, "VERIFY:"+req.RequestID, "gate_opened").Count(&events).Error; err != nil || events != 1 {
		t.Fatalf("open events=%d err=%v", events, err)
	}
}

func TestGateVoiceIsSnapshottedPerSoldTicket(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	var product model.Product
	if err := model.DB.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Where("id = ? AND tenant_id = ?", productID, tenantID).First(&product).Error; err != nil {
		t.Fatal(err)
	}
	product.GateVoiceCode = "adult_ticket"
	if err := (&ProductService{}).Update(product.ID, tenantID, &product, &product.Rule); err != nil {
		t.Fatal(err)
	}
	firstOrder := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&firstOrder); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(firstOrder.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	var firstTicket model.Ticket
	if err := model.DB.Where("order_id = ?", firstOrder.ID).First(&firstTicket).Error; err != nil {
		t.Fatal(err)
	}
	if firstTicket.GateVoiceCode != "adult_ticket" {
		t.Fatalf("first ticket voice=%q", firstTicket.GateVoiceCode)
	}

	if err := model.DB.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Where("id = ? AND tenant_id = ?", productID, tenantID).First(&product).Error; err != nil {
		t.Fatal(err)
	}
	product.GateVoiceCode = "vip_ticket"
	if err := (&ProductService{}).Update(product.ID, tenantID, &product, &product.Rule); err != nil {
		t.Fatal(err)
	}
	firstTicket = model.Ticket{}
	if err := model.DB.Where("order_id = ?", firstOrder.ID).First(&firstTicket).Error; err != nil {
		t.Fatal(err)
	}
	if firstTicket.GateVoiceCode != "adult_ticket" {
		t.Fatalf("sold ticket voice changed to %q", firstTicket.GateVoiceCode)
	}

	secondOrder := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&secondOrder); err != nil {
		t.Fatal(err)
	}
	var secondTicket model.Ticket
	if err := model.DB.Where("order_id = ?", secondOrder.ID).First(&secondTicket).Error; err != nil {
		t.Fatal(err)
	}
	if secondTicket.GateVoiceCode != "vip_ticket" {
		t.Fatalf("second ticket voice=%q", secondTicket.GateVoiceCode)
	}

	var checkpoint model.CheckPoint
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	deviceID := verificationDeviceID(t, tenantID, checkpoint.ID)
	resp, err := NewDeviceService(model.DB, &TicketService{}).VerifyDirect(DirectVerifyRequest{TenantID: tenantID, DeviceID: deviceID, CheckPointID: checkpoint.ID, RequestID: "voice-scan-1", RequestHash: "voice-body", TicketCode: firstTicket.TicketCode})
	if err != nil || resp.Result != "allow" || resp.VoiceCode != "adult_ticket" {
		t.Fatalf("verification=%+v err=%v", resp, err)
	}
}

type distributionScenario struct {
	supplierID           uint
	distributorID        uint
	sourceProductID      uint
	supplierCheckpointID uint
	supplierDeviceID     uint
	listingID            uint
}

func seedDistributionScenario(t *testing.T) distributionScenario {
	t.Helper()
	var scenario distributionScenario
	err := model.Write(func(tx *gorm.DB) error {
		supplier := model.Tenant{Name: "Supplier A", SystemCode: fmt.Sprintf("SUP-%d", time.Now().UnixNano()), SecretKey: "supplier-secret"}
		if err := tx.Create(&supplier).Error; err != nil {
			return err
		}
		distributor := model.Tenant{Name: "Distributor D", SystemCode: fmt.Sprintf("DIST-%d", time.Now().UnixNano()), SecretKey: "distributor-secret"}
		if err := tx.Create(&distributor).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.TenantCapability{TenantID: supplier.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.TenantCapability{TenantID: distributor.ID, Capability: "distributor", Status: "active"}).Error; err != nil {
			return err
		}
		area := model.ScenicArea{TenantID: supplier.ID, Code: fmt.Sprintf("SUP-AREA-%d", supplier.ID), Name: "Supplier Park", Status: "active"}
		if err := tx.Create(&area).Error; err != nil {
			return err
		}
		checkpoint := model.CheckPoint{Name: "Supplier Gate", TenantID: supplier.ID, ScenicAreaID: area.ID}
		if err := tx.Create(&checkpoint).Error; err != nil {
			return err
		}
		checkpointID := checkpoint.ID
		device := model.Device{Name: "Supplier Gate Device", SerialNumber: fmt.Sprintf("SUP-DEV-%d", time.Now().UnixNano()), Type: "gate", Status: "online", TenantID: supplier.ID, ScenicAreaID: area.ID, CheckPointID: &checkpointID, AuthKeyHash: hashDeviceKey("test-device-key")}
		if err := tx.Create(&device).Error; err != nil {
			return err
		}
		rule := model.TicketRule{Name: "Supplier Rule", TenantID: supplier.ID, ValidityType: "date"}
		if err := tx.Create(&rule).Error; err != nil {
			return err
		}
		group := model.RuleGroup{RuleID: rule.ID, GroupName: "Admission", MaxTotalCheckIn: 1}
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.RuleItem{GroupID: group.ID, CheckPointID: checkpoint.ID, MaxPerCheckIn: 1}).Error; err != nil {
			return err
		}
		source := model.Product{
			Name: "Supplier Ticket", Price: 100, SettlementPrice: 60,
			TenantID: supplier.ID, ScenicAreaID: area.ID, RuleID: rule.ID, Type: "online", Status: "online",
			IsDistributable: true, ValidityType: "date", StockType: "total", DailyStock: 1, CodeMode: "ticket",
		}
		if err := tx.Omit("Rule").Create(&source).Error; err != nil {
			return err
		}
		relationship := model.DistributorRelationship{AgentTenantID: distributor.ID, SupplierTenantID: supplier.ID, Status: "active"}
		if err := tx.Create(&relationship).Error; err != nil {
			return err
		}
		account := model.CapitalAccount{OwnerTenantID: distributor.ID, ManagerTenantID: supplier.ID, Balance: 100, Status: "active"}
		if err := tx.Create(&account).Error; err != nil {
			return err
		}
		scenario.supplierID = supplier.ID
		scenario.distributorID = distributor.ID
		scenario.sourceProductID = source.ID
		scenario.supplierCheckpointID = checkpoint.ID
		scenario.supplierDeviceID = device.ID
		return nil
	})
	if err != nil {
		t.Fatalf("seed distribution scenario: %v", err)
	}
	if _, err := (&DistributionService{}).CreateOffer(scenario.supplierID, scenario.distributorID, scenario.sourceProductID, 60, 0, "window,online,ota", nil, nil); err != nil {
		t.Fatalf("create supplier offer: %v", err)
	}
	if err := (&DistributionService{}).ImportProduct(scenario.distributorID, scenario.sourceProductID, "Distributed Ticket", 80, "online"); err != nil {
		t.Fatalf("import distributed product: %v", err)
	}
	var listing model.Product
	if err := model.DB.Where("tenant_id = ? AND source_product_id = ?", scenario.distributorID, scenario.sourceProductID).First(&listing).Error; err != nil {
		t.Fatalf("load imported listing: %v", err)
	}
	if listing.ProductOfferID == 0 {
		t.Fatal("imported listing has no server-owned product offer")
	}
	scenario.listingID = listing.ID
	return scenario
}

func TestDistributedOrderVerifiesAtSupplierOnly(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	var listingRule model.TicketRule
	if err := model.DB.Preload("Groups.Items").Where("id = (SELECT rule_id FROM products WHERE id = ?)", scenario.listingID).First(&listingRule).Error; err != nil {
		t.Fatal(err)
	}
	if len(listingRule.Groups) != 0 {
		t.Fatalf("distributed listing copied %d supplier rule groups", len(listingRule.Groups))
	}

	order := model.Order{TenantID: scenario.distributorID, Channel: "window", ContactName: "Visitor", Items: []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatalf("create distributed order: %v", err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, scenario.distributorID); err != nil {
		t.Fatal(err)
	}
	var item model.OrderItem
	if err := model.DB.Where("order_id = ?", order.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.FulfillmentProductID != scenario.sourceProductID || item.FulfillmentTenantID != scenario.supplierID {
		t.Fatalf("order fulfillment snapshot = %d/%d, want %d/%d", item.FulfillmentProductID, item.FulfillmentTenantID, scenario.sourceProductID, scenario.supplierID)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_item_id = ?", item.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.TenantID != scenario.distributorID || ticket.FulfillmentTenantID != scenario.supplierID {
		t.Fatalf("ticket ownership = sales %d/fulfillment %d", ticket.TenantID, ticket.FulfillmentTenantID)
	}
	var fulfillment model.FulfillmentOrder
	if err := model.DB.Where("sales_order_id = ? AND supplier_tenant_id = ?", order.ID, scenario.supplierID).First(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}
	if fulfillment.Status != "paid" || fulfillment.SalesTenantID != scenario.distributorID {
		t.Fatalf("fulfillment status=%s sales_tenant=%d", fulfillment.Status, fulfillment.SalesTenantID)
	}
	var entitlement model.TicketEntitlement
	if err := model.DB.Where("ticket_id = ?", ticket.ID).First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	if entitlement.SupplierTenantID != scenario.supplierID || entitlement.SalesTenantID != scenario.distributorID {
		t.Fatalf("entitlement ownership sales=%d supplier=%d", entitlement.SalesTenantID, entitlement.SupplierTenantID)
	}

	if err := (&TicketService{}).Verify(ticket.TicketCode, scenario.supplierCheckpointID, scenario.supplierDeviceID, scenario.supplierID); err != nil {
		t.Fatalf("supplier checkpoint rejected distributed ticket: %v", err)
	}
	if err := (&TicketService{}).Verify(ticket.TicketCode, scenario.supplierCheckpointID, scenario.supplierDeviceID, scenario.distributorID); err == nil {
		t.Fatal("distributor tenant verified supplier ticket")
	}

	var source model.Product
	if err := model.DB.First(&source, scenario.sourceProductID).Error; err != nil {
		t.Fatal(err)
	}
	if source.DailyStock != 0 {
		t.Fatalf("supplier stock = %d, want 0", source.DailyStock)
	}
}

func TestSoldTicketUsesRuleSnapshotAfterProductRetirement(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	order := model.Order{TenantID: scenario.distributorID, Channel: "window", Items: []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, scenario.distributorID); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	var item model.OrderItem
	if err := model.DB.Where("order_id = ?", order.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("order_item_id = ?", item.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.RuleSnapshot == "" {
		t.Fatal("sold ticket has no rule snapshot")
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Where("id = ?", scenario.sourceProductID).Delete(&model.Product{}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := (&TicketService{}).Verify(ticket.TicketCode, scenario.supplierCheckpointID, scenario.supplierDeviceID, scenario.supplierID); err != nil {
		t.Fatalf("snapshot ticket failed after source product retirement: %v", err)
	}
}

func TestDistributedListingCannotChangeSettlementPrice(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	var listing model.Product
	if err := model.DB.First(&listing, scenario.listingID).Error; err != nil {
		t.Fatal(err)
	}
	listing.SettlementPrice = 0
	if err := (&ProductService{}).Update(listing.ID, scenario.distributorID, &listing, &model.TicketRule{}); err != nil {
		t.Fatalf("update distributed listing: %v", err)
	}
	if err := model.DB.First(&listing, scenario.listingID).Error; err != nil {
		t.Fatal(err)
	}
	if listing.SettlementPrice != 60 {
		t.Fatalf("listing settlement price = %v, want supplier price 60", listing.SettlementPrice)
	}
	order := model.Order{TenantID: scenario.distributorID, Channel: "window", Items: []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatalf("create order after settlement tamper: %v", err)
	}
	var account model.CapitalAccount
	if err := model.DB.Where("owner_tenant_id = ? AND manager_tenant_id = ?", scenario.distributorID, scenario.supplierID).First(&account).Error; err != nil {
		t.Fatal(err)
	}
	if account.Balance != 40 {
		t.Fatalf("supplier-managed account balance = %v, want 40", account.Balance)
	}
	var item model.OrderItem
	if err := model.DB.Where("order_id = ?", order.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.SettlementPrice != 60 {
		t.Fatalf("order settlement price = %v, want 60", item.SettlementPrice)
	}
}

func TestDistributedCancellationReleasesSupplierStockAndFunds(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	order := model.Order{TenantID: scenario.distributorID, Channel: "window", Items: []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatalf("create distributed order: %v", err)
	}
	if err := (&OrderService{}).Cancel(order.OrderNo, scenario.distributorID); err != nil {
		t.Fatalf("cancel distributed order: %v", err)
	}
	var source model.Product
	if err := model.DB.First(&source, scenario.sourceProductID).Error; err != nil {
		t.Fatal(err)
	}
	if source.DailyStock != 1 {
		t.Fatalf("supplier stock after cancellation = %d, want 1", source.DailyStock)
	}
	var account model.CapitalAccount
	if err := model.DB.Where("owner_tenant_id = ? AND manager_tenant_id = ?", scenario.distributorID, scenario.supplierID).First(&account).Error; err != nil {
		t.Fatal(err)
	}
	if account.Balance != 100 || account.UsedCredit != 0 {
		t.Fatalf("supplier-managed account after cancellation = balance %v credit %v, want 100/0", account.Balance, account.UsedCredit)
	}
	var storedOrder model.Order
	if err := model.DB.Where("order_no = ?", order.OrderNo).First(&storedOrder).Error; err != nil {
		t.Fatal(err)
	}
	if storedOrder.Status != "cancelled" {
		t.Fatalf("order status after cancellation = %s, want cancelled", storedOrder.Status)
	}
}

func TestExpiredUnpaidOrderReleasesReservationExactlyOnce(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	order := model.Order{TenantID: scenario.distributorID, Channel: "window", Items: []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		past := time.Now().Add(-time.Minute)
		return tx.Model(&model.Order{}).Where("id = ?", order.ID).Update("expires_at", past).Error
	}); err != nil {
		t.Fatal(err)
	}
	service := &OrderService{}
	count, err := service.ExpireUnpaid(time.Now())
	if err != nil || count != 1 {
		t.Fatalf("expired order count=%d err=%v, want 1/nil", count, err)
	}
	count, err = service.ExpireUnpaid(time.Now().Add(time.Minute))
	if err != nil || count != 0 {
		t.Fatalf("second expiry count=%d err=%v, want 0/nil", count, err)
	}
	var source model.Product
	if err := model.DB.First(&source, scenario.sourceProductID).Error; err != nil {
		t.Fatal(err)
	}
	if source.DailyStock != 1 {
		t.Fatalf("supplier stock after expiry = %d, want 1", source.DailyStock)
	}
	var account model.CapitalAccount
	if err := model.DB.Where("owner_tenant_id = ? AND manager_tenant_id = ?", scenario.distributorID, scenario.supplierID).First(&account).Error; err != nil {
		t.Fatal(err)
	}
	if account.Balance != 100 {
		t.Fatalf("account balance after expiry = %v, want 100", account.Balance)
	}
}

func TestExpiredReservationReleasesAfterListingAndSourceRetirement(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	order := model.Order{TenantID: scenario.distributorID, Channel: "window", Items: []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", scenario.listingID).Delete(&model.Product{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", scenario.sourceProductID).Delete(&model.Product{}).Error; err != nil {
			return err
		}
		past := time.Now().Add(-time.Minute)
		return tx.Model(&model.Order{}).Where("id = ?", order.ID).Update("expires_at", past).Error
	}); err != nil {
		t.Fatal(err)
	}
	if count, err := (&OrderService{}).ExpireUnpaid(time.Now()); err != nil || count != 1 {
		t.Fatalf("expired order count=%d err=%v, want 1/nil", count, err)
	}
	var account model.CapitalAccount
	if err := model.DB.Where("owner_tenant_id = ? AND manager_tenant_id = ?", scenario.distributorID, scenario.supplierID).First(&account).Error; err != nil {
		t.Fatal(err)
	}
	if account.Balance != 100 {
		t.Fatalf("account balance after retired listing release=%v, want 100", account.Balance)
	}
}

func TestDistributedOrderRechecksSupplierAuthorization(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.DistributorRelationship{}).
			Where("agent_tenant_id = ? AND supplier_tenant_id = ?", scenario.distributorID, scenario.supplierID).
			Update("status", "suspended").Error
	}); err != nil {
		t.Fatal(err)
	}
	order := model.Order{TenantID: scenario.distributorID, Channel: "window", Items: []model.OrderItem{{ProductID: scenario.listingID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err == nil {
		t.Fatal("order succeeded after supplier relationship was suspended")
	}
	var source model.Product
	if err := model.DB.First(&source, scenario.sourceProductID).Error; err != nil {
		t.Fatal(err)
	}
	if source.DailyStock != 1 {
		t.Fatalf("supplier stock after rejected order = %d, want 1", source.DailyStock)
	}
}

func TestGenericProductCannotForgeFulfillmentSource(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	product := model.Product{
		Name: "Forged", Price: 1, SettlementPrice: 1, TenantID: tenantID,
		Type: "online", Status: "online", SourceProductID: 999, SourceTenantID: tenantID + 1,
		ValidityType: "date", StockType: "unlimited", CodeMode: "ticket",
	}
	rule := model.TicketRule{Name: "Forged Rule", TenantID: tenantID}
	if err := (&ProductService{}).Create(&product, &rule); err == nil {
		t.Fatal("forged fulfillment source was accepted")
	}
}

func TestReportsRetainCompletedOrders(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	first := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	second := model.Order{TenantID: tenantID, Channel: "ota", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&first); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).Create(&second); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(first.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(second.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.Order{}).Where("order_no = ?", first.OrderNo).Update("status", "completed").Error
	}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	end := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	stats, err := (&ReportService{}).GetSalesStats(tenantID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	var orders int
	var amount float64
	for _, stat := range stats {
		orders += stat.OrderCount
		amount += stat.TotalAmount
	}
	if orders != 2 || amount != 199 {
		t.Fatalf("report orders=%d amount=%v, want 2/199", orders, amount)
	}
}

func TestTenantCapabilityFailsClosedForSupplierOperations(t *testing.T) {
	resetBusinessData(t)
	tenant := model.Tenant{Name: "Capability Tenant", SystemCode: "CAP-T", SecretKey: "secret", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&tenant).Error }); err != nil {
		t.Fatal(err)
	}
	service := &ScenicAreaService{}
	createArea := func(code string) error {
		return service.Create(tenant.ID, &model.ScenicArea{Code: code, Name: code})
	}
	if err := createArea("MISSING"); !errors.Is(err, ErrCapabilityInactive) {
		t.Fatalf("missing capability error=%v", err)
	}
	capability := model.TenantCapability{TenantID: tenant.ID, Capability: "supplier", Status: "pending"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&capability).Error }); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"pending", "suspended"} {
		if err := model.DB.Model(&capability).Updates(map[string]interface{}{"status": status, "expires_at": nil}).Error; err != nil {
			t.Fatal(err)
		}
		if err := createArea(strings.ToUpper(status)); !errors.Is(err, ErrCapabilityInactive) {
			t.Fatalf("%s capability error=%v", status, err)
		}
	}
	expired := time.Now().Add(-time.Minute)
	if err := model.DB.Model(&capability).Updates(map[string]interface{}{"status": "active", "expires_at": expired}).Error; err != nil {
		t.Fatal(err)
	}
	if err := createArea("EXPIRED"); !errors.Is(err, ErrCapabilityInactive) {
		t.Fatalf("expired capability error=%v", err)
	}
	future := time.Now().Add(time.Hour)
	if err := model.DB.Model(&capability).Update("expires_at", future).Error; err != nil {
		t.Fatal(err)
	}
	if err := createArea("ACTIVE"); err != nil {
		t.Fatalf("active capability rejected: %v", err)
	}
}

func TestCheckpointRequiresUnambiguousScenicArea(t *testing.T) {
	resetBusinessData(t)
	tenant := model.Tenant{Name: "Scenic Boundary Tenant", SystemCode: "SCENIC-T", SecretKey: "secret", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&tenant).Error; err != nil {
			return err
		}
		return tx.Create(&model.TenantCapability{TenantID: tenant.ID, Capability: "supplier", Status: "active"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	checkpointService := &CheckPointService{}
	if err := checkpointService.Create(&model.CheckPoint{Name: "No Area", TenantID: tenant.ID}); err == nil {
		t.Fatal("checkpoint without an active scenic area was created")
	}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&model.ScenicArea{TenantID: tenant.ID, Code: "A", Name: "A", Status: "active"}).Error; err != nil {
			return err
		}
		return tx.Create(&model.ScenicArea{TenantID: tenant.ID, Code: "B", Name: "B", Status: "active"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := checkpointService.Create(&model.CheckPoint{Name: "Ambiguous", TenantID: tenant.ID}); err == nil {
		t.Fatal("checkpoint with ambiguous scenic area was created")
	}
}

func TestVerificationRequiresOwnedDeviceAndNonzeroScenicArea(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	var checkpoint model.CheckPoint
	var ticket model.Ticket
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	deviceID := verificationDeviceID(t, tenantID, checkpoint.ID)
	if err := (&TicketService{}).Verify(ticket.TicketCode, checkpoint.ID, 0, tenantID); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("missing device error=%v", err)
	}
	if err := (&TicketService{}).Verify(ticket.TicketCode, checkpoint.ID, deviceID+999999, tenantID); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("foreign device error=%v", err)
	}
	corruptionErr := model.Write(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Ticket{}).Where("id = ?", ticket.ID).Updates(map[string]interface{}{"scenic_area_id": 0, "fulfillment_scenic_area_id": 0}).Error; err != nil {
			return err
		}
		return tx.Model(&model.OrderItem{}).Where("id = ?", ticket.OrderItemID).Update("fulfillment_scenic_area_id", 0).Error
	})
	if model.DB.Dialector.Name() == "postgres" {
		if corruptionErr == nil {
			t.Fatal("PostgreSQL ownership guard accepted a zero-scenic ticket")
		}
		return
	}
	if corruptionErr != nil {
		t.Fatal(corruptionErr)
	}
	if err := (&TicketService{}).Verify(ticket.TicketCode, checkpoint.ID, deviceID, tenantID); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("zero scenic ticket error=%v", err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Ticket{}).Where("id = ?", ticket.ID).Updates(map[string]interface{}{"scenic_area_id": checkpoint.ScenicAreaID, "fulfillment_scenic_area_id": checkpoint.ScenicAreaID}).Error; err != nil {
			return err
		}
		return tx.Model(&model.OrderItem{}).Where("id = ?", ticket.OrderItemID).Update("fulfillment_scenic_area_id", checkpoint.ScenicAreaID).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := (&TicketService{}).Verify(ticket.TicketCode, checkpoint.ID, deviceID, tenantID); err != nil {
		t.Fatalf("valid device verification failed: %v", err)
	}
	var record model.CheckInRecord
	if err := model.DB.Where("ticket_id = ? AND result = ?", ticket.ID, "success").First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.ScenicAreaID != checkpoint.ScenicAreaID {
		t.Fatalf("check-in scenic area=%d, want %d", record.ScenicAreaID, checkpoint.ScenicAreaID)
	}
}

func TestPendingProviderPaymentSurvivesOrderExpiryAndLateSuccess(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "total", 1)
	order := model.Order{TenantID: tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	payment := model.Payment{TenantID: tenantID, OrderNo: order.OrderNo, PaymentNo: "PAY-LATE-SUCCESS", Method: "wechat", PayType: "cscanb", Amount: order.TotalAmount, Status: "pending"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&payment).Error; err != nil {
			return err
		}
		return tx.Model(&order).Update("expires_at", time.Now().Add(-time.Minute)).Error
	}); err != nil {
		t.Fatal(err)
	}
	count, err := (&OrderService{}).ExpireUnpaid(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expired pending-payment orders=%d, want 0", count)
	}
	if err := (&PaymentService{}).CompleteNotification(tenantID, payment.PaymentNo, payment.Method, "WX-LATE", payment.Amount); err != nil {
		t.Fatalf("late payment callback failed: %v", err)
	}
	if err := model.DB.First(&order, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != "paid" {
		t.Fatalf("late-success order status=%s", order.Status)
	}
	var product model.Product
	if err := model.DB.First(&product, productID).Error; err != nil {
		t.Fatal(err)
	}
	if product.DailyStock != 0 {
		t.Fatalf("reserved stock was released: %d", product.DailyStock)
	}
}

func TestMoneyCentsRoundsAtCentBoundary(t *testing.T) {
	for value, want := range map[float64]int64{0.29: 29, 1.01: 101, 1.005: 101, 0.1 + 0.2: 30, 12.34: 1234, 99.99: 9999, -1.005: -101} {
		if got := moneyCents(value); got != want {
			t.Fatalf("moneyCents(%v)=%d, want %d", value, got, want)
		}
	}
}
