package service

import (
	"errors"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/testdb"
	"time"

	"gorm.io/gorm"
)

func openCommerceStatsDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testdb.Open(t)
	if err := db.AutoMigrate(
		&model.Tenant{}, &model.TenantBusinessCapability{}, &model.CommerceOrder{}, &model.CommerceOrderItem{},
		&model.CommerceAfterSaleRequest{}, &model.RestaurantFulfillment{}, &model.RetailFulfillment{},
		&model.CommerceAssistCampaign{}, &model.CommerceAssistSession{}, &model.CommerceMerchantNotification{},
	); err != nil {
		t.Fatalf("migrate commerce stats tables: %v", err)
	}
	return db
}

func createCommerceStatsTenant(t *testing.T, db *gorm.DB, name, businessType, status string) model.Tenant {
	t.Helper()
	tenant := model.Tenant{Name: name, SystemCode: "STATS-" + name + "-" + time.Now().Format("150405.000000"), SecretKey: "stats-secret", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TenantBusinessCapability{TenantID: tenant.ID, BusinessType: businessType, Status: status}).Error; err != nil {
		t.Fatal(err)
	}
	return tenant
}

func TestCommerceStatsAggregatesFactsWithoutJoinMultiplicationAndIsolatesTenant(t *testing.T) {
	db := openCommerceStatsDB(t)
	owner := createCommerceStatsTenant(t, db, "owner", "restaurant", "active")
	foreign := createCommerceStatsTenant(t, db, "foreign", "restaurant", "active")
	locationID := uint(41)
	created := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	order := model.CommerceOrder{TenantID: owner.ID, OrderNo: "STATS-OWNER-1", IdempotencyKey: "stats-key-1", BusinessType: "restaurant", Channel: "direct", CustomerID: "c1", LocationID: locationID, OriginalAmountCents: 1200, DiscountCents: 200, TotalAmountCents: 1000, PaymentStatus: "paid", FulfillmentStatus: "preparing", RefundStatus: "none", ContactName: "张三", ContactPhone: "13800138000", Base: model.Base{CreatedAt: created, UpdatedAt: created}}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CommerceOrderItem{TenantID: owner.ID, OrderID: order.ID, ProductID: 7, SkuID: 8, ProductNameSnapshot: "套餐", SkuNameSnapshot: "标准", Quantity: 2, OriginalUnitPriceCents: 600, UnitPriceCents: 500, DiscountCents: 200, LineAmountCents: 1000, ReservationStatus: "sold"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.RestaurantFulfillment{TenantID: owner.ID, OrderID: order.ID, LocationID: locationID, Method: "pickup", Status: "preparing"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CommerceAfterSaleRequest{TenantID: owner.ID, OrderID: order.ID, RequestNo: "STATS-REFUND-1", IdempotencyKey: "stats-refund-key-1", Status: "completed", AmountCents: 300, ProviderRefundAmountCents: 300}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CommerceAfterSaleRequest{TenantID: owner.ID, OrderID: order.ID, RequestNo: "STATS-REFUND-2", IdempotencyKey: "stats-refund-key-2", ProviderRefundReference: "provider-refund-2", Status: "completed", AmountCents: 50, ProviderRefundAmountCents: 50}).Error; err != nil {
		t.Fatal(err)
	}
	refundedOrder := model.CommerceOrder{TenantID: owner.ID, OrderNo: "STATS-OWNER-REFUNDED", IdempotencyKey: "stats-key-refunded", BusinessType: "restaurant", Channel: "direct", CustomerID: "c3", LocationID: locationID, OriginalAmountCents: 500, DiscountCents: 100, TotalAmountCents: 400, PaymentStatus: "refunded", FulfillmentStatus: "cancelled", RefundStatus: "refunded", ContactName: "退款订单", ContactPhone: "13800138001", Base: model.Base{CreatedAt: created, UpdatedAt: created}}
	if err := db.Create(&refundedOrder).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CommerceOrderItem{TenantID: owner.ID, OrderID: refundedOrder.ID, ProductID: 7, SkuID: 8, ProductNameSnapshot: "套餐", SkuNameSnapshot: "标准", Quantity: 1, OriginalUnitPriceCents: 500, UnitPriceCents: 400, DiscountCents: 100, LineAmountCents: 400, ReservationStatus: "refunded"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CommerceAfterSaleRequest{TenantID: owner.ID, OrderID: refundedOrder.ID, RequestNo: "STATS-REFUND-3", IdempotencyKey: "stats-refund-key-3", ProviderRefundReference: "provider-refund-3", Status: "completed", AmountCents: 400, ProviderRefundAmountCents: 400}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.Create(&model.CommerceMerchantNotification{TenantID: owner.ID, EventKey: "STATS-EVENT-" + string(rune('a'+i)), EventType: "order_paid", OrderID: order.ID, OrderNo: order.OrderNo, BusinessType: "restaurant", LocationID: locationID, Title: "新订单", Body: "新订单待处理", Status: "unread", Base: model.Base{CreatedAt: created}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	foreignOrder := model.CommerceOrder{TenantID: foreign.ID, OrderNo: "STATS-FOREIGN-1", IdempotencyKey: "stats-foreign-key-1", BusinessType: "restaurant", Channel: "direct", CustomerID: "foreign", LocationID: locationID, OriginalAmountCents: 9999, TotalAmountCents: 9999, PaymentStatus: "paid", FulfillmentStatus: "preparing", RefundStatus: "none", Base: model.Base{CreatedAt: created}}
	if err := db.Create(&foreignOrder).Error; err != nil {
		t.Fatal(err)
	}
	unpaidOrder := model.CommerceOrder{TenantID: owner.ID, OrderNo: "STATS-OWNER-UNPAID", IdempotencyKey: "stats-key-unpaid", BusinessType: "restaurant", Channel: "direct", CustomerID: "c2", LocationID: locationID, OriginalAmountCents: 5000, TotalAmountCents: 5000, PaymentStatus: "unpaid", FulfillmentStatus: "pending", RefundStatus: "none", Base: model.Base{CreatedAt: created}}
	if err := db.Create(&unpaidOrder).Error; err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	stats, err := (&CommerceStatsService{DB: db}).Get(CommerceStatsQuery{TenantID: owner.ID, BusinessType: "restaurant", LocationID: locationID, StartAt: &start, EndAt: &end})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.TotalOrders != 3 || stats.PaidOrders != 2 || stats.PendingOrders != 1 || stats.CancelledOrders != 1 {
		t.Fatalf("order aggregates=%+v", stats)
	}
	if stats.OriginalAmountCents != 1700 || stats.DiscountCents != 300 || stats.TotalAmountCents != 1400 {
		t.Fatalf("amount aggregates=%+v", stats)
	}
	if stats.RefundedAmountCents != 750 || stats.NetAmountCents != 650 || stats.RefundedOrderCount != 2 {
		t.Fatalf("refund aggregates=%+v", stats)
	}
	if stats.NewOrderNotificationCount != 2 || stats.UnreadOrderNotificationCount != 2 {
		t.Fatalf("notification aggregates=%+v", stats)
	}
	if len(stats.FulfillmentMethods) != 1 || stats.FulfillmentMethods[0].Method != "pickup" || stats.FulfillmentMethods[0].Count != 1 {
		t.Fatalf("fulfillment=%+v", stats.FulfillmentMethods)
	}
	if len(stats.TopProducts) != 1 || stats.TopProducts[0].Quantity != 3 || stats.TopProducts[0].AmountCents != 1400 {
		t.Fatalf("top products=%+v", stats.TopProducts)
	}
	if stats.ProductQuantity != 3 {
		t.Fatalf("product quantity=%d", stats.ProductQuantity)
	}

	foreignStats, err := (&CommerceStatsService{DB: db}).Get(CommerceStatsQuery{TenantID: foreign.ID, BusinessType: "restaurant", StartAt: &start, EndAt: &end})
	if err != nil {
		t.Fatalf("foreign stats: %v", err)
	}
	if foreignStats.TotalOrders != 1 || foreignStats.TotalAmountCents != 9999 {
		t.Fatalf("cross-tenant leakage=%+v", foreignStats)
	}
}

func TestCommerceStatsDateBoundaryAndSuspendedRead(t *testing.T) {
	db := openCommerceStatsDB(t)
	tenant := createCommerceStatsTenant(t, db, "suspended", "retail", "suspended")
	created := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	order := model.CommerceOrder{TenantID: tenant.ID, OrderNo: "STATS-BOUNDARY-1", IdempotencyKey: "stats-boundary-key", BusinessType: "retail", Channel: "direct", CustomerID: "c", LocationID: 1, OriginalAmountCents: 100, TotalAmountCents: 100, PaymentStatus: "paid", FulfillmentStatus: "completed", RefundStatus: "none", Base: model.Base{CreatedAt: created}}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	start := created
	end := created.Add(24 * time.Hour)
	stats, err := (&CommerceStatsService{DB: db}).Get(CommerceStatsQuery{TenantID: tenant.ID, BusinessType: "retail", StartAt: &start, EndAt: &end})
	if err != nil {
		t.Fatalf("suspended historical stats: %v", err)
	}
	if stats.TotalOrders != 1 {
		t.Fatalf("date boundary excluded order: %+v", stats)
	}
	badEnd := start
	if _, err := (&CommerceStatsService{DB: db}).Get(CommerceStatsQuery{TenantID: tenant.ID, BusinessType: "retail", StartAt: &start, EndAt: &badEnd}); !errors.Is(err, ErrCommerceStatsInvalid) {
		t.Fatalf("invalid range error=%v", err)
	}
}
