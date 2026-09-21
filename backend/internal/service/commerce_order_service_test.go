package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func TestCommerceOrderChannelIsNotClientWritable(t *testing.T) {
	var input CreateCommerceOrderInput
	if err := json.Unmarshal([]byte(`{"business_type":"restaurant","channel":"forged-channel"}`), &input); err != nil {
		t.Fatalf("decode order input: %v", err)
	}
	if input.Channel != "" {
		t.Fatalf("client supplied channel was accepted: %q", input.Channel)
	}
}

func commerceOrderFixture(t *testing.T) (tenantID, productID, skuID, locationID uint, now time.Time) {
	t.Helper()
	if err := model.DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&model.CommercePaymentReconciliationTask{}).Error; err != nil {
		t.Fatalf("reset commerce payment reconciliation tasks: %v", err)
	}
	tenantID = newCommerceTenant(t, "restaurant", "active")
	catalog := &CommerceCatalogService{}
	product, err := catalog.CreateProduct(tenantID, commerceProductInput("Order meal"))
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	if _, err := catalog.SetProductStatus(tenantID, product.ID, "online"); err != nil {
		t.Fatalf("publish product: %v", err)
	}
	ops := &CommerceOperationsService{}
	location, err := ops.CreateLocation(tenantID, CreateCommerceLocationInput{BusinessType: "restaurant", Name: "Main store", LocationType: "store"})
	if err != nil {
		t.Fatalf("create location: %v", err)
	}
	if _, err := ops.SetInventory(tenantID, CommerceInventoryInput{SkuID: product.SKUs[0].ID, LocationID: location.ID, AvailableQty: 20}); err != nil {
		t.Fatalf("set inventory: %v", err)
	}
	return tenantID, product.ID, product.SKUs[0].ID, location.ID, time.Now().UTC().Truncate(time.Microsecond)
}

func commerceOrderInput(productID, skuID, locationID uint, key string, expiresAt time.Time) CreateCommerceOrderInput {
	return CreateCommerceOrderInput{
		IdempotencyKey: key, BusinessType: "restaurant", Channel: "direct", CustomerID: "customer-1", LocationID: locationID,
		ContactName: "Customer", ContactPhone: "13800138000", ExpiresAt: &expiresAt,
		Items: []CommerceOrderItemInput{{ProductID: productID, SKUID: skuID, Quantity: 2}},
	}
}

func TestCommerceOrderPaymentAndRefundLifecycle(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	service := &CommerceOrderService{Clock: func() time.Time { return now }}
	input := commerceOrderInput(productID, skuID, locationID, "lifecycle", now.Add(15*time.Minute))
	input.PaymentReference = "original-payment-lifecycle"
	order, err := service.CreateOrder(tenantID, input)
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if len(order.OrderNo) != 28 || strings.Contains(order.OrderNo, "%!") {
		t.Fatalf("invalid generated order number %q", order.OrderNo)
	}
	if order.PaymentStatus != "unpaid" || order.Items[0].ReservationStatus != "reserved" || order.TotalAmountCents != 2000 {
		t.Fatalf("created order=%+v items=%+v", order, order.Items)
	}
	var stock model.CommerceInventory
	if err := model.DB.Where("tenant_id = ? AND sku_id = ? AND location_id = ?", tenantID, skuID, locationID).First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock.AvailableQty != 18 || stock.ReservedQty != 2 {
		t.Fatalf("reserved stock=%+v", stock)
	}
	paid, err := service.ConfirmPayment(tenantID, order.ID)
	if err != nil {
		t.Fatalf("confirm payment: %v", err)
	}
	if paid.PaymentStatus != "paid" || paid.PaidAt == nil {
		t.Fatalf("paid order=%+v", paid)
	}
	var item model.CommerceOrderItem
	if err := model.DB.Where("order_id = ? AND tenant_id = ?", order.ID, tenantID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.ReservationStatus != "sold" {
		t.Fatalf("paid item status=%s", item.ReservationStatus)
	}
	request, err := service.CreateRefundRequest(tenantID, order.ID, "refund-lifecycle", "customer request")
	if err != nil {
		t.Fatalf("create refund request: %v", err)
	}
	if _, err := service.TransitionRestaurantFulfillment(tenantID, order.ID, RestaurantFulfillmentTransitionInput{Status: "accepted"}); !errors.Is(err, ErrCommerceOrderState) {
		t.Fatalf("order with an open refund should not advance fulfillment, err=%v", err)
	}
	refunded, err := service.CompleteRefundAfterProviderConfirmation(tenantID, request.Request.ID, "refund-provider-lifecycle", order.TotalAmountCents)
	if err != nil {
		t.Fatalf("complete refund: %v", err)
	}
	if refunded.Order.PaymentStatus != "refunded" || refunded.Order.RefundStatus != "refunded" || refunded.Request.Status != "completed" {
		t.Fatalf("refunded result=%+v", refunded)
	}
	if refunded.Order.PaymentReference != "original-payment-lifecycle" {
		t.Fatalf("refund changed original payment reference: %q", refunded.Order.PaymentReference)
	}
	if refunded.Request.ProviderRefundReference != "refund-provider-lifecycle" || refunded.Request.ProviderRefundAmountCents != order.TotalAmountCents {
		t.Fatalf("provider refund facts=%+v", refunded.Request)
	}
	var refundedItem model.CommerceOrderItem
	if err := model.DB.Where("id = ?", item.ID).First(&refundedItem).Error; err != nil {
		t.Fatal(err)
	}
	if refundedItem.ReservationStatus != "refunded" {
		t.Fatalf("refunded item status=%s", refundedItem.ReservationStatus)
	}
	if err := model.DB.Where("id = ?", stock.ID).First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock.AvailableQty != 20 || stock.SoldQty != 0 {
		t.Fatalf("restored stock=%+v", stock)
	}
	if again, err := service.CompleteRefundAfterProviderConfirmation(tenantID, request.Request.ID, "refund-provider-lifecycle", order.TotalAmountCents); err != nil || again.Order.PaymentStatus != "refunded" {
		t.Fatalf("idempotent refund result=%+v err=%v", again, err)
	}
	if err := model.DB.Where("id = ?", stock.ID).First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock.AvailableQty != 20 || stock.SoldQty != 0 {
		t.Fatalf("duplicate refund changed stock=%+v", stock)
	}
	var eventCount int64
	if err := model.DB.Model(&model.CommerceAfterSaleEvent{}).Where("tenant_id = ? AND request_id = ? AND event_type = ?", tenantID, request.Request.ID, "refund_completed").Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("idempotent refund created %d completion events", eventCount)
	}
	if _, err := service.CompleteRefundAfterProviderConfirmation(tenantID, request.Request.ID, "different-refund", order.TotalAmountCents); !errors.Is(err, ErrCommercePaymentInvalid) {
		t.Fatalf("conflicting completed refund reference error=%v", err)
	}
	if _, err := service.CompleteRefundAfterProviderConfirmation(tenantID, request.Request.ID, "refund-provider-lifecycle", order.TotalAmountCents-1); !errors.Is(err, ErrCommercePaymentInvalid) {
		t.Fatalf("conflicting completed refund amount error=%v", err)
	}
}

func TestCommerceOrderSnapshotsProductMediaAtCreation(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	store := CommerceImageStore{Directory: t.TempDir(), PublicBaseURL: "https://tickets.example.com"}
	catalog := &CommerceCatalogService{Images: &store}
	coverURL, err := store.Save(tenantID, productID, CommerceProductMediaCover, commercePNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.AddProductMedia(tenantID, productID, CommerceProductMediaCover, coverURL); err != nil {
		t.Fatal(err)
	}
	orderService := &CommerceOrderService{Clock: func() time.Time { return now }}
	input := commerceOrderInput(productID, skuID, locationID, "media-snapshot", now.Add(15*time.Minute))
	order, err := orderService.CreateOrder(tenantID, input)
	if err != nil {
		t.Fatal(err)
	}
	var item model.CommerceOrderItem
	if err := model.DB.Where("tenant_id = ? AND order_id = ?", tenantID, order.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.MediaSnapshotJSON == "" || !strings.Contains(item.MediaSnapshotJSON, coverURL) {
		t.Fatalf("media snapshot=%q", item.MediaSnapshotJSON)
	}
	snapshot := item.MediaSnapshotJSON
	newURL, err := store.Save(tenantID, productID, CommerceProductMediaCover, commercePNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.AddProductMedia(tenantID, productID, CommerceProductMediaCover, newURL); err != nil {
		t.Fatal(err)
	}
	retry, err := orderService.CreateOrder(tenantID, input)
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != order.ID || retry.Items[0].MediaSnapshotJSON != snapshot {
		t.Fatalf("idempotent retry rewrote media snapshot: first=%q retry=%q", snapshot, retry.Items[0].MediaSnapshotJSON)
	}
	var persisted model.CommerceOrderItem
	if err := model.DB.Where("id = ?", item.ID).First(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.MediaSnapshotJSON != snapshot {
		t.Fatalf("persisted media snapshot changed: %q", persisted.MediaSnapshotJSON)
	}
}

func TestCommerceRefundProviderFactsRejectConflictOnPendingRequest(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	service := &CommerceOrderService{Clock: func() time.Time { return now }}
	input := commerceOrderInput(productID, skuID, locationID, "refund-provider-pending-conflict", now.Add(time.Minute))
	input.PaymentReference = "original-payment-pending-conflict"
	order, err := service.CreateOrder(tenantID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmPayment(tenantID, order.ID); err != nil {
		t.Fatal(err)
	}
	request, err := service.CreateRefundRequest(tenantID, order.ID, "refund-provider-pending-conflict-key", "customer request")
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.CommerceAfterSaleRequest{}).Where("id = ? AND tenant_id = ?", request.Request.ID, tenantID).Updates(map[string]interface{}{
		"provider_refund_reference":    "already-recorded-refund",
		"provider_refund_amount_cents": order.TotalAmountCents,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteRefundAfterProviderConfirmation(tenantID, request.Request.ID, "another-refund", order.TotalAmountCents); !errors.Is(err, ErrCommercePaymentInvalid) {
		t.Fatalf("pending request conflict error=%v", err)
	}
	var persisted model.CommerceAfterSaleRequest
	if err := model.DB.First(&persisted, request.Request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != "requested" || persisted.ProviderRefundReference != "already-recorded-refund" {
		t.Fatalf("pending request changed after conflict=%+v", persisted)
	}
}

func TestCommerceCompleteRefundRequiresProviderConfirmation(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	service := &CommerceOrderService{Clock: func() time.Time { return now }}
	order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "refund-provider-gate", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmPayment(tenantID, order.ID); err != nil {
		t.Fatal(err)
	}
	request, err := service.CreateRefundRequest(tenantID, order.ID, "refund-provider-gate-key", "customer request")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteRefund(tenantID, request.Request.ID); !errors.Is(err, ErrCommercePaymentUnavailable) {
		t.Fatalf("ordinary refund completion error=%v", err)
	}
	var persisted model.CommerceOrder
	if err := model.DB.First(&persisted, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.PaymentStatus != "paid" || persisted.RefundStatus != "requested" {
		t.Fatalf("ordinary completion changed order=%+v", persisted)
	}
}

func TestCommercePendingPaymentIsNotExpired(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	service := &CommerceOrderService{Clock: func() time.Time { return now }}
	order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "pending", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MarkPaymentPending(tenantID, order.ID); err != nil {
		t.Fatalf("mark pending: %v", err)
	}
	if count, err := service.ExpireUnpaidOrders(tenantID, now.Add(2*time.Minute)); err != nil || count != 0 {
		t.Fatalf("expired pending order count=%d err=%v", count, err)
	}
	var persisted model.CommerceOrder
	if err := model.DB.First(&persisted, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.PaymentStatus != "pending" {
		t.Fatalf("pending order status=%s", persisted.PaymentStatus)
	}
	paid, err := service.ConfirmPayment(tenantID, order.ID)
	if err != nil || paid.PaymentStatus != "paid" {
		t.Fatalf("pending order should remain payable: order=%+v err=%v", paid, err)
	}
}

func TestCommerceExpiredUnpaidCannotBecomePending(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	current := now
	service := &CommerceOrderService{Clock: func() time.Time { return current }}
	order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "expired-pending", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	current = now.Add(2 * time.Minute)
	if _, err := service.MarkPaymentPending(tenantID, order.ID); !errors.Is(err, ErrCommerceOrderState) {
		t.Fatalf("expired order should reject pending transition, err=%v", err)
	}
	var persisted model.CommerceOrder
	if err := model.DB.First(&persisted, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.PaymentStatus != "unpaid" {
		t.Fatalf("expired order was moved to %s", persisted.PaymentStatus)
	}
}

func TestCommerceUnpaidExpiryReleasesReservationOnce(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	service := &CommerceOrderService{Clock: func() time.Time { return now }}
	order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "expiry", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if count, err := service.ExpireUnpaidOrders(tenantID, now.Add(2*time.Minute)); err != nil || count != 1 {
		t.Fatalf("expired unpaid order count=%d err=%v", count, err)
	}
	if count, err := service.ExpireUnpaidOrders(tenantID, now.Add(3*time.Minute)); err != nil || count != 0 {
		t.Fatalf("repeated expiry count=%d err=%v", count, err)
	}
	var persisted model.CommerceOrder
	if err := model.DB.First(&persisted, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.PaymentStatus != "failed" || persisted.FulfillmentStatus != "cancelled" {
		t.Fatalf("expired order=%+v", persisted)
	}
	var fulfillment model.RestaurantFulfillment
	if err := model.DB.Where("tenant_id = ? AND order_id = ?", tenantID, order.ID).First(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}
	if fulfillment.Status != "cancelled" {
		t.Fatalf("expired child fulfillment status=%s", fulfillment.Status)
	}
	var item model.CommerceOrderItem
	if err := model.DB.Where("order_id = ?", order.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.ReservationStatus != "released" || item.ReleasedAt == nil {
		t.Fatalf("released item=%+v", item)
	}
	var stock model.CommerceInventory
	if err := model.DB.Where("tenant_id = ? AND sku_id = ? AND location_id = ?", tenantID, skuID, locationID).First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock.AvailableQty != 20 || stock.ReservedQty != 0 || stock.ReleasedQty != 2 {
		t.Fatalf("released stock=%+v", stock)
	}
}

func TestCommerceOrderValidationRequiresRetailShippingAddressAndBoundsQuantity(t *testing.T) {
	_, err := normalizeCommerceOrderInput(CreateCommerceOrderInput{
		IdempotencyKey: "retail-address", BusinessType: "retail", CustomerID: "customer", LocationID: 1,
		Items: []CommerceOrderItemInput{{ProductID: 1, SKUID: 1, Quantity: 1}},
	})
	if !errors.Is(err, ErrCommerceOrderInvalid) {
		t.Fatalf("retail order without shipping address err=%v", err)
	}
	_, err = normalizeCommerceOrderInput(CreateCommerceOrderInput{
		IdempotencyKey: "quantity-bound", BusinessType: "restaurant", CustomerID: "customer", LocationID: 1,
		Items: []CommerceOrderItemInput{{ProductID: 1, SKUID: 1, Quantity: 1001}},
	})
	if !errors.Is(err, ErrCommerceOrderInvalid) {
		t.Fatalf("oversized order quantity err=%v", err)
	}
}

func TestCommerceOrderIdempotencySerializesConcurrentCreate(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	input := commerceOrderInput(productID, skuID, locationID, "concurrent", now.Add(15*time.Minute))
	const attempts = 8
	orders := make([]*model.CommerceOrder, attempts)
	errs := make([]error, attempts)
	var wait sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			orders[index], errs[index] = (&CommerceOrderService{Clock: func() time.Time { return now }}).CreateOrder(tenantID, input)
		}(i)
	}
	wait.Wait()
	var firstID uint
	for i := range orders {
		if errs[i] != nil {
			t.Fatalf("concurrent create %d: %v", i, errs[i])
		}
		if orders[i] == nil || orders[i].ID == 0 {
			t.Fatalf("concurrent create %d returned %+v", i, orders[i])
		}
		if i == 0 {
			firstID = orders[i].ID
		} else if orders[i].ID != firstID {
			t.Fatalf("idempotency returned order ids %d and %d", firstID, orders[i].ID)
		}
	}
	var count int64
	if err := model.DB.Model(&model.CommerceOrder{}).Where("tenant_id = ? AND idempotency_key = ?", tenantID, input.IdempotencyKey).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("order count=%d", count)
	}
	var itemCount int64
	if err := model.DB.Model(&model.CommerceOrderItem{}).Where("order_id = ?", firstID).Count(&itemCount).Error; err != nil {
		t.Fatal(err)
	}
	if itemCount != 1 {
		t.Fatalf("order item count=%d", itemCount)
	}
	var stock model.CommerceInventory
	if err := model.DB.Where("tenant_id = ? AND sku_id = ? AND location_id = ?", tenantID, skuID, locationID).First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock.AvailableQty != 18 || stock.ReservedQty != 2 {
		t.Fatalf("concurrent stock=%+v", stock)
	}
	if _, err := (&CommerceOrderService{Clock: func() time.Time { return now }}).CreateOrder(tenantID, func() CreateCommerceOrderInput {
		conflict := input
		conflict.ContactName = "Different customer"
		return conflict
	}()); !errors.Is(err, ErrCommerceIdempotencyConflict) {
		t.Fatalf("conflicting retry err=%v", err)
	}
	if _, err := (&CommerceOrderService{Clock: func() time.Time { return now }}).CreateOrder(tenantID, func() CreateCommerceOrderInput {
		conflict := input
		conflict.FulfillmentMethod = "delivery"
		conflict.ShippingAddressJSON = `{"detail":"Main street 1"}`
		return conflict
	}()); !errors.Is(err, ErrCommerceIdempotencyConflict) {
		t.Fatalf("fulfillment method conflict err=%v", err)
	}
}

func TestCommerceOrderTenantLockRequiresExistingTenant(t *testing.T) {
	service := &CommerceOrderService{}
	_, err := service.CreateOrder(999999, CreateCommerceOrderInput{IdempotencyKey: fmt.Sprintf("missing-%d", time.Now().UnixNano()), BusinessType: "restaurant", CustomerID: "customer", LocationID: 1, Items: []CommerceOrderItemInput{{ProductID: 1, SKUID: 1, Quantity: 1}}})
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing tenant error=%v", err)
	}
}

func commerceRetailOrderFixture(t *testing.T) (tenantID, productID, skuID, locationID uint, now time.Time) {
	t.Helper()
	if err := model.DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&model.CommercePaymentReconciliationTask{}).Error; err != nil {
		t.Fatalf("reset commerce payment reconciliation tasks: %v", err)
	}
	tenantID = newCommerceTenant(t, "retail", "active")
	catalog := &CommerceCatalogService{}
	productInput := commerceProductInput("Retail item")
	productInput.BusinessType = "retail"
	product, err := catalog.CreateProduct(tenantID, productInput)
	if err != nil {
		t.Fatalf("create retail product: %v", err)
	}
	if _, err := catalog.SetProductStatus(tenantID, product.ID, "online"); err != nil {
		t.Fatalf("publish retail product: %v", err)
	}
	ops := &CommerceOperationsService{}
	location, err := ops.CreateLocation(tenantID, CreateCommerceLocationInput{BusinessType: "retail", Name: "Warehouse", LocationType: "warehouse"})
	if err != nil {
		t.Fatalf("create retail location: %v", err)
	}
	if _, err := ops.SetInventory(tenantID, CommerceInventoryInput{SkuID: product.SKUs[0].ID, LocationID: location.ID, AvailableQty: 20}); err != nil {
		t.Fatalf("set retail inventory: %v", err)
	}
	return tenantID, product.ID, product.SKUs[0].ID, location.ID, time.Now().UTC().Truncate(time.Microsecond)
}

func commerceRetailOrderInput(productID, skuID, locationID uint, key string, expiresAt time.Time) CreateCommerceOrderInput {
	input := commerceOrderInput(productID, skuID, locationID, key, expiresAt)
	input.BusinessType = "retail"
	input.ShippingAddressJSON = `{"recipient":"Customer","detail":"Main street 1"}`
	input.FulfillmentMethod = ""
	return input
}

func loadCommerceInventory(t *testing.T, tenantID, skuID, locationID uint) model.CommerceInventory {
	t.Helper()
	var stock model.CommerceInventory
	if err := model.DB.Where("tenant_id = ? AND sku_id = ? AND location_id = ?", tenantID, skuID, locationID).First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	return stock
}

func loadCommercePaymentTask(t *testing.T, tenantID, orderID uint) model.CommercePaymentReconciliationTask {
	t.Helper()
	var task model.CommercePaymentReconciliationTask
	if err := model.DB.Where("tenant_id = ? AND order_id = ?", tenantID, orderID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	return task
}

func TestCommerceApplyPaymentOutcomePendingCreatesReconciliationTask(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	service := &CommerceOrderService{Clock: func() time.Time { return now }}
	order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "payment-pending-task", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MarkPaymentPendingWithReference(tenantID, order.ID, "pay-pending"); err != nil {
		t.Fatalf("mark pending: %v", err)
	}
	if err := model.DB.Unscoped().Where("tenant_id = ? AND order_id = ?", tenantID, order.ID).Delete(&model.CommercePaymentReconciliationTask{}).Error; err != nil {
		t.Fatalf("remove pre-existing reconciliation task: %v", err)
	}
	updated, err := service.ApplyPaymentOutcome(tenantID, order.ID, CommercePaymentOutcome{Status: "pending", ProviderReference: "pay-pending", ProviderState: "PROCESSING"})
	if !errors.Is(err, ErrCommercePaymentManualReview) {
		t.Fatalf("pending outcome error=%v", err)
	}
	if updated == nil || updated.PaymentStatus != "pending" {
		t.Fatalf("pending outcome order=%+v", updated)
	}
	task := loadCommercePaymentTask(t, tenantID, order.ID)
	if task.Status != "manual_review" || task.LastProviderState != "PROCESSING" || task.LastError == "" || task.NextAttemptAt != nil {
		t.Fatalf("reconciliation task=%+v", task)
	}
	stock := loadCommerceInventory(t, tenantID, skuID, locationID)
	if stock.AvailableQty != 18 || stock.ReservedQty != 2 || stock.ReleasedQty != 0 || stock.SoldQty != 0 {
		t.Fatalf("pending outcome changed stock=%+v", stock)
	}
}

func TestCommercePaymentReconciliationClaimsPendingTask(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	orderService := &CommerceOrderService{Clock: func() time.Time { return now }}
	order, err := orderService.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "reconciliation-claim", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orderService.MarkPaymentPendingWithReference(tenantID, order.ID, "provider-reconciliation-claim"); err != nil {
		t.Fatalf("mark payment pending: %v", err)
	}

	reconciliation := &CommercePaymentReconciliationService{Clock: func() time.Time { return now }}
	processed, err := reconciliation.ProcessTasks(context.Background(), now, 1)
	if err != nil {
		t.Fatalf("process pending reconciliation task: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d, want 1", processed)
	}
	task := loadCommercePaymentTask(t, tenantID, order.ID)
	if task.Status != "manual_review" || task.LastError == "" {
		t.Fatalf("reconciliation task=%+v", task)
	}
}

func TestCommerceApplyPaymentOutcomeFailureReleasesReservationOnce(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	service := &CommerceOrderService{Clock: func() time.Time { return now }}
	order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "payment-failed", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MarkPaymentPendingWithReference(tenantID, order.ID, "pay-failed"); err != nil {
		t.Fatal(err)
	}
	failed, err := service.ApplyPaymentOutcome(tenantID, order.ID, CommercePaymentOutcome{Status: "failed", ProviderReference: "pay-failed", ProviderState: "CLOSED"})
	if err != nil || failed.PaymentStatus != "failed" || failed.FulfillmentStatus != "cancelled" {
		t.Fatalf("failed outcome=%+v err=%v", failed, err)
	}
	first := loadCommerceInventory(t, tenantID, skuID, locationID)
	if first.AvailableQty != 20 || first.ReservedQty != 0 || first.ReleasedQty != 2 || first.SoldQty != 0 {
		t.Fatalf("failure release stock=%+v", first)
	}
	if _, err := service.ApplyPaymentOutcome(tenantID, order.ID, CommercePaymentOutcome{Status: "failed", ProviderReference: "pay-failed", ProviderState: "CLOSED"}); err != nil {
		t.Fatalf("repeated failed outcome: %v", err)
	}
	second := loadCommerceInventory(t, tenantID, skuID, locationID)
	if second.AvailableQty != first.AvailableQty || second.ReservedQty != first.ReservedQty || second.ReleasedQty != first.ReleasedQty || second.SoldQty != first.SoldQty {
		t.Fatalf("repeated failure changed stock first=%+v second=%+v", first, second)
	}
	if task := loadCommercePaymentTask(t, tenantID, order.ID); task.Status != "completed" {
		t.Fatalf("failed payment task=%+v", task)
	}
}

func TestCommerceApplyPaymentOutcomeLateSuccessWithinWindow(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	current := now
	service := &CommerceOrderService{Clock: func() time.Time { return current }}
	order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "late-success", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	current = now.Add(2 * time.Minute)
	providerPaidAt := now.Add(30 * time.Second)
	paid, err := service.ApplyPaymentOutcome(tenantID, order.ID, CommercePaymentOutcome{Status: "success", ProviderReference: "late-success-ref", ProviderPaidAt: &providerPaidAt, ProviderAmountCents: order.TotalAmountCents})
	if err != nil || paid.PaymentStatus != "paid" {
		t.Fatalf("late in-window success=%+v err=%v", paid, err)
	}
	item := model.CommerceOrderItem{}
	if err := model.DB.Where("order_id = ? AND tenant_id = ?", order.ID, tenantID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.ReservationStatus != "sold" {
		t.Fatalf("late success item=%+v", item)
	}
	stock := loadCommerceInventory(t, tenantID, skuID, locationID)
	if stock.AvailableQty != 18 || stock.ReservedQty != 0 || stock.SoldQty != 2 {
		t.Fatalf("late success stock=%+v", stock)
	}
	if task := loadCommercePaymentTask(t, tenantID, order.ID); task.Status != "completed" || task.PaymentReference != "late-success-ref" {
		t.Fatalf("late success task=%+v", task)
	}
}

func TestCommerceApplyPaymentOutcomeLateSuccessRequiresManualReview(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	current := now
	service := &CommerceOrderService{Clock: func() time.Time { return current }}
	order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "late-review", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	current = now.Add(2 * time.Minute)
	providerPaidAt := now.Add(90 * time.Second)
	updated, err := service.ApplyPaymentOutcome(tenantID, order.ID, CommercePaymentOutcome{Status: "paid", ProviderReference: "late-review-ref", ProviderPaidAt: &providerPaidAt, ProviderAmountCents: order.TotalAmountCents})
	if !errors.Is(err, ErrCommercePaymentManualReview) {
		t.Fatalf("late out-of-window error=%v", err)
	}
	if updated == nil || updated.PaymentStatus != "pending" {
		t.Fatalf("late out-of-window order=%+v", updated)
	}
	stock := loadCommerceInventory(t, tenantID, skuID, locationID)
	if stock.AvailableQty != 18 || stock.ReservedQty != 2 || stock.SoldQty != 0 || stock.ReleasedQty != 0 {
		t.Fatalf("late out-of-window stock=%+v", stock)
	}
	task := loadCommercePaymentTask(t, tenantID, order.ID)
	if task.Status != "manual_review" || task.LastError == "" || task.NextAttemptAt != nil {
		t.Fatalf("late out-of-window task=%+v", task)
	}
}

func TestCommerceApplyPaymentOutcomeUnknownFreezesOrder(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	service := &CommerceOrderService{Clock: func() time.Time { return now }}
	order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "payment-unknown", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.ApplyPaymentOutcome(tenantID, order.ID, CommercePaymentOutcome{Status: "unknown", ProviderState: "QUERY_TIMEOUT"})
	if !errors.Is(err, ErrCommercePaymentManualReview) || updated == nil || updated.PaymentStatus != "pending" {
		t.Fatalf("unknown outcome=%+v err=%v", updated, err)
	}
	stock := loadCommerceInventory(t, tenantID, skuID, locationID)
	if stock.AvailableQty != 18 || stock.ReservedQty != 2 || stock.SoldQty != 0 || stock.ReleasedQty != 0 {
		t.Fatalf("unknown outcome changed stock=%+v", stock)
	}
	task := loadCommercePaymentTask(t, tenantID, order.ID)
	if task.Status != "manual_review" || task.LastProviderState != "QUERY_TIMEOUT" || task.LastError == "" {
		t.Fatalf("unknown outcome task=%+v", task)
	}
}

func TestCommerceApplyPaymentOutcomeRejectsAmountAndReferenceMismatch(t *testing.T) {
	t.Run("amount", func(t *testing.T) {
		tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
		service := &CommerceOrderService{Clock: func() time.Time { return now }}
		order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "amount-mismatch", now.Add(time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.MarkPaymentPendingWithReference(tenantID, order.ID, "amount-ref"); err != nil {
			t.Fatal(err)
		}
		if _, err := service.ApplyPaymentOutcome(tenantID, order.ID, CommercePaymentOutcome{Status: "paid", ProviderReference: "amount-ref", ProviderAmountCents: order.TotalAmountCents + 1}); !errors.Is(err, ErrCommercePaymentInvalid) {
			t.Fatalf("amount mismatch error=%v", err)
		}
		var persisted model.CommerceOrder
		if err := model.DB.First(&persisted, order.ID).Error; err != nil {
			t.Fatal(err)
		}
		if persisted.PaymentStatus != "pending" {
			t.Fatalf("amount mismatch changed order=%+v", persisted)
		}
		stock := loadCommerceInventory(t, tenantID, skuID, locationID)
		if stock.AvailableQty != 18 || stock.ReservedQty != 2 || stock.ReleasedQty != 0 {
			t.Fatalf("amount mismatch changed stock=%+v", stock)
		}
	})

	t.Run("reference", func(t *testing.T) {
		tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
		service := &CommerceOrderService{Clock: func() time.Time { return now }}
		order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "reference-mismatch", now.Add(time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.MarkPaymentPendingWithReference(tenantID, order.ID, "expected-ref"); err != nil {
			t.Fatal(err)
		}
		if _, err := service.ApplyPaymentOutcome(tenantID, order.ID, CommercePaymentOutcome{Status: "paid", ProviderReference: "wrong-ref", ProviderAmountCents: order.TotalAmountCents}); !errors.Is(err, ErrCommercePaymentInvalid) {
			t.Fatalf("reference mismatch error=%v", err)
		}
		var persisted model.CommerceOrder
		if err := model.DB.First(&persisted, order.ID).Error; err != nil {
			t.Fatal(err)
		}
		if persisted.PaymentStatus != "pending" || persisted.PaymentReference != "expected-ref" {
			t.Fatalf("reference mismatch changed order=%+v", persisted)
		}
	})
}

func TestCommerceApplyPaymentOutcomeIsIdempotent(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	service := &CommerceOrderService{Clock: func() time.Time { return now }}
	order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "payment-idempotent", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	outcome := CommercePaymentOutcome{Status: "paid", ProviderReference: "idempotent-ref", ProviderAmountCents: order.TotalAmountCents, ProviderPaidAt: &now}
	if _, err := service.ApplyPaymentOutcome(tenantID, order.ID, outcome); err != nil {
		t.Fatal(err)
	}
	first := loadCommerceInventory(t, tenantID, skuID, locationID)
	if _, err := service.ApplyPaymentOutcome(tenantID, order.ID, outcome); err != nil {
		t.Fatalf("repeated paid outcome: %v", err)
	}
	second := loadCommerceInventory(t, tenantID, skuID, locationID)
	if second.AvailableQty != first.AvailableQty || second.ReservedQty != first.ReservedQty || second.SoldQty != first.SoldQty || second.ReleasedQty != first.ReleasedQty {
		t.Fatalf("repeated paid outcome changed stock first=%+v second=%+v", first, second)
	}
	if task := loadCommercePaymentTask(t, tenantID, order.ID); task.Status != "completed" {
		t.Fatalf("idempotent payment task=%+v", task)
	}
}

func TestCommerceRefundRejectsFulfilledRestaurantAndRetailOrders(t *testing.T) {
	t.Run("restaurant accepted", func(t *testing.T) {
		tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
		service := &CommerceOrderService{Clock: func() time.Time { return now }}
		order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "refund-accepted", now.Add(time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.ConfirmPayment(tenantID, order.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := service.TransitionRestaurantFulfillment(tenantID, order.ID, RestaurantFulfillmentTransitionInput{Status: "accepted"}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.CreateRefundRequest(tenantID, order.ID, "refund-accepted-key", "too late"); !errors.Is(err, ErrCommerceRefundInvalid) {
			t.Fatalf("accepted restaurant refund error=%v", err)
		}
	})

	t.Run("retail shipped", func(t *testing.T) {
		tenantID, productID, skuID, locationID, now := commerceRetailOrderFixture(t)
		service := &CommerceOrderService{Clock: func() time.Time { return now }}
		order, err := service.CreateOrder(tenantID, commerceRetailOrderInput(productID, skuID, locationID, "refund-shipped", now.Add(time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.ConfirmPayment(tenantID, order.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := service.TransitionRetailFulfillment(tenantID, order.ID, RetailFulfillmentTransitionInput{Status: "shipped", Carrier: "Carrier", TrackingNo: "TRACK-1"}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.CreateRefundRequest(tenantID, order.ID, "refund-shipped-key", "too late"); !errors.Is(err, ErrCommerceRefundInvalid) {
			t.Fatalf("shipped retail refund error=%v", err)
		}
	})
}

func TestCommercePaidFulfillmentCannotBeCancelledDirectly(t *testing.T) {
	t.Run("restaurant", func(t *testing.T) {
		tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
		service := &CommerceOrderService{Clock: func() time.Time { return now }}
		order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "cancel-paid-restaurant", now.Add(time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.ConfirmPayment(tenantID, order.ID); err != nil {
			t.Fatal(err)
		}
		beforeStock := loadCommerceInventory(t, tenantID, skuID, locationID)
		var beforeFulfillment model.RestaurantFulfillment
		if err := model.DB.Where("tenant_id = ? AND order_id = ?", tenantID, order.ID).First(&beforeFulfillment).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := service.TransitionRestaurantFulfillment(tenantID, order.ID, RestaurantFulfillmentTransitionInput{Status: "cancelled"}); !errors.Is(err, ErrCommerceOrderState) {
			t.Fatalf("direct restaurant cancellation error=%v", err)
		}
		var persistedOrder model.CommerceOrder
		if err := model.DB.Where("tenant_id = ? AND id = ?", tenantID, order.ID).First(&persistedOrder).Error; err != nil {
			t.Fatal(err)
		}
		var persistedFulfillment model.RestaurantFulfillment
		if err := model.DB.Where("tenant_id = ? AND order_id = ?", tenantID, order.ID).First(&persistedFulfillment).Error; err != nil {
			t.Fatal(err)
		}
		persistedStock := loadCommerceInventory(t, tenantID, skuID, locationID)
		if persistedOrder.PaymentStatus != "paid" || persistedOrder.RefundStatus != "none" || persistedOrder.FulfillmentStatus != beforeFulfillment.Status {
			t.Fatalf("direct restaurant cancellation changed order=%+v", persistedOrder)
		}
		if persistedFulfillment.Status != beforeFulfillment.Status || persistedFulfillment.Status == "cancelled" {
			t.Fatalf("direct restaurant cancellation changed fulfillment=%+v", persistedFulfillment)
		}
		if persistedStock != beforeStock {
			t.Fatalf("direct restaurant cancellation changed inventory before=%+v after=%+v", beforeStock, persistedStock)
		}
	})

	t.Run("retail", func(t *testing.T) {
		tenantID, productID, skuID, locationID, now := commerceRetailOrderFixture(t)
		service := &CommerceOrderService{Clock: func() time.Time { return now }}
		order, err := service.CreateOrder(tenantID, commerceRetailOrderInput(productID, skuID, locationID, "cancel-paid-retail", now.Add(time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.ConfirmPayment(tenantID, order.ID); err != nil {
			t.Fatal(err)
		}
		beforeStock := loadCommerceInventory(t, tenantID, skuID, locationID)
		var beforeFulfillment model.RetailFulfillment
		if err := model.DB.Where("tenant_id = ? AND order_id = ?", tenantID, order.ID).First(&beforeFulfillment).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := service.TransitionRetailFulfillment(tenantID, order.ID, RetailFulfillmentTransitionInput{Status: "cancelled"}); !errors.Is(err, ErrCommerceOrderState) {
			t.Fatalf("direct retail cancellation error=%v", err)
		}
		var persistedOrder model.CommerceOrder
		if err := model.DB.Where("tenant_id = ? AND id = ?", tenantID, order.ID).First(&persistedOrder).Error; err != nil {
			t.Fatal(err)
		}
		var persistedFulfillment model.RetailFulfillment
		if err := model.DB.Where("tenant_id = ? AND order_id = ?", tenantID, order.ID).First(&persistedFulfillment).Error; err != nil {
			t.Fatal(err)
		}
		persistedStock := loadCommerceInventory(t, tenantID, skuID, locationID)
		if persistedOrder.PaymentStatus != "paid" || persistedOrder.RefundStatus != "none" || persistedOrder.FulfillmentStatus != beforeFulfillment.Status {
			t.Fatalf("direct retail cancellation changed order=%+v", persistedOrder)
		}
		if persistedFulfillment.Status != beforeFulfillment.Status || persistedFulfillment.Status == "cancelled" {
			t.Fatalf("direct retail cancellation changed fulfillment=%+v", persistedFulfillment)
		}
		if persistedStock != beforeStock {
			t.Fatalf("direct retail cancellation changed inventory before=%+v after=%+v", beforeStock, persistedStock)
		}
	})
}

func TestCommerceRefundRequestIdempotencyIsSerialized(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	service := &CommerceOrderService{DB: model.DB, Clock: func() time.Time { return now }}
	order, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "refund-concurrent", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmPayment(tenantID, order.ID); err != nil {
		t.Fatal(err)
	}
	const key = "refund-concurrent-key"
	start := make(chan struct{})
	results := make(chan *CommerceRefundResult, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, callErr := service.CreateRefundRequest(tenantID, order.ID, key, "customer request")
			results <- result
			errs <- callErr
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	var requestID uint
	for result := range results {
		if result == nil || result.Request == nil {
			t.Fatalf("concurrent refund result=%+v", result)
		}
		if requestID == 0 {
			requestID = result.Request.ID
		} else if result.Request.ID != requestID {
			t.Fatalf("concurrent requests created different rows: %d and %d", requestID, result.Request.ID)
		}
	}
	for callErr := range errs {
		if callErr != nil {
			t.Fatalf("concurrent idempotent request failed: %v", callErr)
		}
	}
	var count int64
	if err := model.DB.Model(&model.CommerceAfterSaleRequest{}).Where("tenant_id = ? AND order_id = ? AND idempotency_key = ?", tenantID, order.ID, key).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("concurrent refund request count=%d, want 1", count)
	}
}

func TestCommerceRefundIdempotencyConflictAcrossOrdersIsStable(t *testing.T) {
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	service := &CommerceOrderService{DB: model.DB, Clock: func() time.Time { return now }}
	first, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "refund-cross-order-a", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateOrder(tenantID, commerceOrderInput(productID, skuID, locationID, "refund-cross-order-b", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmPayment(tenantID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmPayment(tenantID, second.ID); err != nil {
		t.Fatal(err)
	}
	const key = "refund-cross-order-key"
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, orderID := range []uint{first.ID, second.ID} {
		orderID := orderID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, callErr := service.CreateRefundRequest(tenantID, orderID, key, "same key across orders")
			errs <- callErr
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	successes := 0
	conflicts := 0
	for callErr := range errs {
		switch {
		case callErr == nil:
			successes++
		case errors.Is(callErr, ErrCommerceIdempotencyConflict):
			conflicts++
		default:
			t.Fatalf("cross-order refund idempotency error=%v", callErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("cross-order refund idempotency successes=%d conflicts=%d", successes, conflicts)
	}
	var count int64
	if err := model.DB.Model(&model.CommerceAfterSaleRequest{}).Where("tenant_id = ? AND idempotency_key = ?", tenantID, key).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("cross-order refund request count=%d, want 1", count)
	}
}
