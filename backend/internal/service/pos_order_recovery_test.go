package service

import (
	"errors"
	"gorm.io/gorm"
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestWindowOrderClientRequestIsIdempotentAndPayloadBound(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	first := model.Order{
		TenantID: tenantID, Channel: "window", ClientRequestID: "window-retry-1",
		Items: []model.OrderItem{{ProductID: productID, Quantity: 1}},
	}
	if err := (&OrderService{}).Create(&first); err != nil {
		t.Fatalf("create first window order: %v", err)
	}
	retry := model.Order{
		TenantID: tenantID, Channel: "window", ClientRequestID: "window-retry-1",
		Items: []model.OrderItem{{ProductID: productID, Quantity: 1}},
	}
	if err := (&OrderService{}).Create(&retry); !errors.Is(err, ErrIdempotentWindowOrder) {
		t.Fatalf("retry error=%v, want idempotent marker", err)
	}
	if retry.OrderNo != first.OrderNo || retry.ID != first.ID {
		t.Fatalf("retry order=%+v, first=%+v", retry, first)
	}
	var count int64
	if err := model.DB.Model(&model.Order{}).Where("tenant_id = ? AND channel = ? AND client_request_id = ?", tenantID, "window", "window-retry-1").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("idempotent request created %d orders", count)
	}

	conflicting := model.Order{
		TenantID: tenantID, Channel: "window", ClientRequestID: "window-retry-1",
		Items: []model.OrderItem{{ProductID: productID, Quantity: 2}},
	}
	if err := (&OrderService{}).Create(&conflicting); err == nil || errors.Is(err, ErrIdempotentWindowOrder) {
		t.Fatalf("conflicting request was accepted: %v", err)
	}
}

func TestOrderDetailIncludesOperationalFactsWithinTenant(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		var area model.ScenicArea
		if err := tx.Where("tenant_id = ?", tenantID).First(&area).Error; err != nil {
			return err
		}
		device := model.Device{TenantID: tenantID, ScenicAreaID: area.ID, Name: "Detail POS", SerialNumber: "DETAIL-POS", Type: "pos", Status: "online"}
		if err := tx.Create(&device).Error; err != nil {
			return err
		}
		shift := model.POSShift{TenantID: tenantID, ScenicAreaID: area.ID, DeviceID: device.ID, OperatorID: 1, ShiftNo: "DETAIL-SHIFT", Status: "open", OpenedAt: time.Now()}
		if err := tx.Create(&shift).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.Payment{TenantID: tenantID, PaymentNo: "PAY-DETAIL-TEST", OrderNo: order.OrderNo, Amount: order.TotalAmount, AmountCents: moneyCents(order.TotalAmount), Method: "cash", Status: "paid"}).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.PrintJob{TenantID: tenantID, DeviceID: device.ID, OperatorID: 1, ShiftID: shift.ID, OrderNo: order.OrderNo, Status: "failed", LastError: "paper out", AttemptCount: 1, PaperWidthMM: 58, Orientation: "portrait", CopyCount: 1}).Error; err != nil {
			return err
		}
		return tx.Create(&model.AfterSaleRequest{TenantID: tenantID, RequestNo: "AS-DETAIL-TEST", IdempotencyKey: "AS-DETAIL-TEST", OrderNo: order.OrderNo, Type: "refund", Status: "pending", Reason: "detail test", OperatorID: 1}).Error
	}); err != nil {
		t.Fatal(err)
	}
	detail, err := (&OrderService{}).GetDetail(order.OrderNo, tenantID)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Payments) != 1 || len(detail.PrintJobs) != 1 || len(detail.AfterSales) != 1 {
		t.Fatalf("detail operational facts=%+v", detail)
	}
	if _, err := (&OrderService{}).GetDetail(order.OrderNo, tenantID+1); err == nil {
		t.Fatal("cross-tenant order detail was readable")
	}
}
