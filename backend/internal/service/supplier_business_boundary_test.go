package service

import (
	"errors"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

func TestScenicSuspensionBlocksNewSalesButPreservesHistoryAndShutdown(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	var account model.ChannelAccount
	if err := model.Write(func(tx *gorm.DB) error {
		account = model.ChannelAccount{TenantID: tenantID, Code: "SCENIC-PAUSE-CHANNEL", Type: "custom", Status: "active", Environment: "production"}
		return tx.Create(&account).Error
	}); err != nil {
		t.Fatal(err)
	}
	cancelOrder := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	payOrder := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	refundOrder := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	for _, order := range []*model.Order{&cancelOrder, &payOrder, &refundOrder} {
		if err := (&OrderService{}).Create(order); err != nil {
			t.Fatalf("create historical order: %v", err)
		}
	}
	if err := (&OrderService{}).MarkAsPaid(refundOrder.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.Payment{
		TenantID: tenantID, PaymentNo: "PAID-BEFORE-SCENIC-SUSPENSION", IdempotencyKey: "paid-before-suspension",
		OrderNo: refundOrder.OrderNo, Purpose: "order", Amount: refundOrder.TotalAmount,
		AmountCents: moneyCents(refundOrder.TotalAmount), Method: "cash", Status: "paid",
	}).Error; err != nil {
		t.Fatal(err)
	}
	activeProducts, activeTotal, err := (&ProductService{}).ListChannelProducts(1, 10, tenantID, []uint{productID})
	if err != nil || activeTotal != 1 || len(activeProducts) != 1 {
		t.Fatalf("active supplier channel catalog products=%d total=%d err=%v", len(activeProducts), activeTotal, err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.SupplierBusinessType{}).
			Where("tenant_id = ? AND business_type = ?", tenantID, "scenic").
			Update("status", "suspended").Error
	}); err != nil {
		t.Fatal(err)
	}

	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); !errors.Is(err, ErrSupplierBusinessTypeInactive) {
		t.Fatalf("new order err=%v, want inactive scenic business", err)
	}
	if _, err := (&ChannelWorkflowService{}).Reserve(tenantID, account.ID, "custom", productID, "PAUSED-RESERVATION", 1, nil, "", time.Minute); !errors.Is(err, ErrSupplierBusinessTypeInactive) {
		t.Fatalf("new reservation err=%v, want inactive scenic business", err)
	}
	if err := (&OrderService{}).Cancel(cancelOrder.OrderNo, tenantID); err != nil {
		t.Fatalf("historical unpaid order cancellation failed: %v", err)
	}
	if err := (&OrderService{}).MarkAsPaid(payOrder.OrderNo, tenantID); err != nil {
		t.Fatalf("historical payment completion failed: %v", err)
	}
	if len(refundOrder.Items) != 1 || len(refundOrder.Items[0].Tickets) != 1 {
		t.Fatalf("historical refund order tickets=%+v", refundOrder.Items)
	}
	if _, err := (&RefundService{}).CreateCashRefund(tenantID, refundOrder.OrderNo, "refund-after-scenic-suspension", refundOrder.TotalAmount,
		[]string{refundOrder.Items[0].Tickets[0].TicketCode}, "supplier business suspended"); err != nil {
		t.Fatalf("historical order refund failed: %v", err)
	}
	products, total, err := (&ProductService{}).List(1, 10, tenantID, "")
	if err != nil || total != 1 || len(products) != 1 {
		t.Fatalf("historical products=%d total=%d err=%v", len(products), total, err)
	}
	channelProducts, channelTotal, err := (&ProductService{}).ListChannelProducts(1, 10, tenantID, []uint{productID})
	if err != nil || channelTotal != 0 || len(channelProducts) != 0 {
		t.Fatalf("paused supplier leaked to channel catalog: products=%d total=%d err=%v", len(channelProducts), channelTotal, err)
	}
	if err := (&ProductService{}).UpdateStatus(productID, tenantID, "offline"); err != nil {
		t.Fatalf("safe product shutdown failed: %v", err)
	}
	if err := (&ProductService{}).UpdateStatus(productID, tenantID, "online"); !errors.Is(err, ErrSupplierBusinessTypeInactive) {
		t.Fatalf("product reactivation err=%v, want inactive scenic business", err)
	}
	if err := (&ProductService{}).Delete(productID, tenantID); !errors.Is(err, ErrSupplierBusinessTypeInactive) {
		t.Fatalf("product deletion err=%v, want inactive scenic business", err)
	}
}

func TestTravelPartnershipRejectsHotelOnlySupplier(t *testing.T) {
	resetBusinessData(t)
	var hotel, travel model.Tenant
	if err := model.Write(func(tx *gorm.DB) error {
		hotel = model.Tenant{Name: "Hotel Supplier", SystemCode: "HOTEL-ONLY-SUPPLIER", Status: "active"}
		travel = model.Tenant{Name: "Travel Agency", SystemCode: "SCENIC-TRAVEL-AGENCY", Status: "active"}
		if err := tx.Create(&hotel).Error; err != nil {
			return err
		}
		if err := tx.Create(&travel).Error; err != nil {
			return err
		}
		if err := tx.Create(&[]model.TenantCapability{
			{TenantID: hotel.ID, Capability: "supplier", Status: "active"},
			{TenantID: travel.ID, Capability: "travel_agency", Status: "active"},
		}).Error; err != nil {
			return err
		}
		return tx.Create(&model.SupplierBusinessType{TenantID: hotel.ID, BusinessType: "hotel", Status: "active"}).Error
	}); err != nil {
		t.Fatal(err)
	}

	team := &TeamService{}
	if _, err := team.SearchSupplierPartner(travel.ID, hotel.SystemCode); err == nil {
		t.Fatal("hotel-only supplier was returned by scenic travel supplier search")
	}
	if err := team.ApplySupplierPartner(travel.ID, hotel.SystemCode); err == nil {
		t.Fatal("hotel-only supplier accepted a scenic travel partnership")
	}
	var relationships int64
	if err := model.DB.Model(&model.DistributorRelationship{}).Count(&relationships).Error; err != nil || relationships != 0 {
		t.Fatalf("unexpected travel relationship count=%d err=%v", relationships, err)
	}
}
