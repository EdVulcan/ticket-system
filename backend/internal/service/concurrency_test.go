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
	"ticket-backend/internal/model"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMain(m *testing.M) {
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
		&model.Tenant{}, &model.CheckPoint{}, &model.TicketRule{}, &model.RuleGroup{}, &model.RuleItem{},
		&model.Product{}, &model.ProductInventory{}, &model.Order{}, &model.OrderItem{}, &model.Ticket{},
		&model.CheckInRecord{}, &model.DistributorRelationship{}, &model.CapitalAccount{}, &model.TransactionRecord{},
		&model.Payment{}, &model.PaymentConfig{},
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
			&model.Payment{}, &model.PaymentConfig{},
			&model.CheckInRecord{}, &model.Ticket{}, &model.OrderItem{}, &model.Order{}, &model.ProductInventory{},
			&model.Product{}, &model.RuleItem{}, &model.RuleGroup{}, &model.TicketRule{}, &model.CheckPoint{},
			&model.TransactionRecord{}, &model.CapitalAccount{}, &model.DistributorRelationship{}, &model.Tenant{},
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
	if err := service.Verify(ticket.TicketCode, checkpoint.ID, 1, tenantID); err != nil {
		t.Fatalf("first admission: %v", err)
	}
	if err := model.DB.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.Status != "active" || ticket.CheckInCount != 1 {
		t.Fatalf("after first admission status=%s count=%d", ticket.Status, ticket.CheckInCount)
	}
	if err := service.Verify(ticket.TicketCode, checkpoint.ID, 1, tenantID); err != nil {
		t.Fatalf("second admission: %v", err)
	}
	if err := model.DB.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.Status != "used" || ticket.CheckInCount != 2 {
		t.Fatalf("after second admission status=%s count=%d", ticket.Status, ticket.CheckInCount)
	}
	if err := service.Verify(ticket.TicketCode, checkpoint.ID, 1, tenantID); !errors.Is(err, ErrTicketUnavailable) {
		t.Fatalf("third admission error = %v, want unavailable", err)
	}
}

func TestCashPaymentUsesStoredAmountAndTenantScope(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
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

func seedSellableProduct(t *testing.T, stockType string, stock int) (uint, uint) {
	t.Helper()
	var tenantID, productID uint
	err := model.Write(func(tx *gorm.DB) error {
		tenant := model.Tenant{Name: "Test Tenant", SystemCode: fmt.Sprintf("TEST-%d", time.Now().UnixNano()), SecretKey: "test-secret"}
		if err := tx.Create(&tenant).Error; err != nil {
			return err
		}
		checkpoint := model.CheckPoint{Name: "Main Gate", TenantID: tenant.ID}
		if err := tx.Create(&checkpoint).Error; err != nil {
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
			TenantID: tenant.ID, RuleID: rule.ID, Type: "online", Status: "online",
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
	for i := 0; i < 20; i++ {
		wait.Add(1)
		go func(deviceID uint) {
			defer wait.Done()
			err := (&TicketService{}).Verify(ticket.TicketCode, checkpoint.ID, deviceID, tenantID)
			if err == nil {
				successes.Add(1)
			} else if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
				lockErrors.Add(1)
			} else if !errors.Is(err, ErrTicketUnavailable) && !errors.Is(err, ErrPointLimitReached) {
				t.Errorf("unexpected verification error: %v", err)
			}
		}(uint(i + 1))
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
