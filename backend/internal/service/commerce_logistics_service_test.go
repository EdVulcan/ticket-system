package service

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"ticket-backend/internal/model"
)

type commerceLogisticsFixture struct {
	service  *CommerceLogisticsService
	tenantID uint
	orderID  uint
	location uint
	customer string
	now      time.Time
}

func newCommerceLogisticsFixture(t *testing.T, businessCustomer string) commerceLogisticsFixture {
	t.Helper()
	if model.DB == nil {
		t.Fatal("database is not initialized")
	}
	if err := model.DB.AutoMigrate(&model.RetailFulfillment{}, &model.CommerceShipment{}, &model.CommerceShipmentEvent{}); err != nil {
		t.Fatalf("migrate logistics models: %v", err)
	}
	tenantID := newCommerceTenant(t, "retail", "active")
	location := &model.CommerceFulfillmentLocation{
		TenantID:     tenantID,
		BusinessType: "retail",
		Name:         "Logistics warehouse " + time.Now().Format("150405.000000000"),
		LocationType: "warehouse",
		Status:       "active",
	}
	if err := model.DB.Create(location).Error; err != nil {
		t.Fatalf("create fixture location: %v", err)
	}
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	order := &model.CommerceOrder{TenantID: tenantID, OrderNo: "LOG-" + time.Now().Format("150405.000000000"), BusinessType: "retail", CustomerID: businessCustomer, LocationID: location.ID, PaymentStatus: "paid", FulfillmentStatus: "pending", TotalAmountCents: 1000, ShippingAddressJSON: `{"recipient":"Customer","detail":"Address"}`}
	if err := model.DB.Create(order).Error; err != nil {
		t.Fatalf("create fixture order: %v", err)
	}
	fulfillment := &model.RetailFulfillment{TenantID: tenantID, OrderID: order.ID, LocationID: order.LocationID, Status: "pending_shipment"}
	if err := model.DB.Create(fulfillment).Error; err != nil {
		t.Fatalf("create fixture fulfillment: %v", err)
	}
	return commerceLogisticsFixture{service: &CommerceLogisticsService{Clock: func() time.Time { return now }}, tenantID: tenantID, orderID: order.ID, location: location.ID, customer: businessCustomer, now: now}
}

func createLogisticsOrder(t *testing.T, tenantID uint, customer string, location uint) uint {
	t.Helper()
	order := &model.CommerceOrder{TenantID: tenantID, OrderNo: "LOG-" + time.Now().Format("150405.000000000") + "-" + customer, BusinessType: "retail", CustomerID: customer, LocationID: location, PaymentStatus: "paid", FulfillmentStatus: "pending", TotalAmountCents: 1000, ShippingAddressJSON: `{"detail":"Other address"}`}
	if err := model.DB.Create(order).Error; err != nil {
		t.Fatalf("create second fixture order: %v", err)
	}
	if err := model.DB.Create(&model.RetailFulfillment{TenantID: tenantID, OrderID: order.ID, LocationID: location, Status: "pending_shipment"}).Error; err != nil {
		t.Fatalf("create second fulfillment: %v", err)
	}
	return order.ID
}

func TestCommerceLogisticsCreateShipmentDerivesOrderLocation(t *testing.T) {
	f := newCommerceLogisticsFixture(t, "location-source-customer")
	shipment, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{
		OrderID:    f.orderID,
		ShipmentNo: "SHP-LOG-DERIVED-LOCATION",
		Source:     "manual",
	})
	if err != nil {
		t.Fatalf("create shipment with omitted location: %v", err)
	}
	if shipment.LocationID != f.location {
		t.Fatalf("shipment location=%d, want order location %d", shipment.LocationID, f.location)
	}
}

func TestCommerceLogisticsCreateShipmentRejectsForgedLocation(t *testing.T) {
	f := newCommerceLogisticsFixture(t, "forged-location-customer")
	otherLocation := &model.CommerceFulfillmentLocation{
		TenantID:     f.tenantID,
		BusinessType: "retail",
		Name:         "Other logistics location " + time.Now().Format("150405.000000000"),
		LocationType: "warehouse",
		Status:       "active",
	}
	if err := model.DB.Create(otherLocation).Error; err != nil {
		t.Fatalf("create other location: %v", err)
	}
	if _, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{
		OrderID:    f.orderID,
		LocationID: otherLocation.ID,
		ShipmentNo: "SHP-LOG-FORGED-LOCATION",
		Source:     "manual",
	}); !errors.Is(err, ErrCommerceLogisticsConflict) {
		t.Fatalf("forged location error=%v, want ErrCommerceLogisticsConflict", err)
	}
	var shipmentCount int64
	if err := model.DB.Model(&model.CommerceShipment{}).Where("tenant_id = ? AND order_id = ?", f.tenantID, f.orderID).Count(&shipmentCount).Error; err != nil {
		t.Fatal(err)
	}
	if shipmentCount != 0 {
		t.Fatalf("forged location created %d shipments", shipmentCount)
	}
}

func TestCommerceLogisticsCreateShipmentMarkShippedProjectsAtomically(t *testing.T) {
	f := newCommerceLogisticsFixture(t, "atomic-shipped-customer")
	shipment, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{
		OrderID:     f.orderID,
		ShipmentNo:  "SHP-LOG-ATOMIC-SHIPPED",
		CarrierCode: "carrier",
		TrackingNo:  "track-atomic-shipped",
		MarkShipped: true,
	})
	if err != nil {
		t.Fatalf("create and mark shipped: %v", err)
	}
	if shipment.Status != "shipped" || shipment.ShippedAt == nil || !shipment.ShippedAt.Equal(f.now) {
		t.Fatalf("shipment projection=%+v", shipment)
	}
	var event model.CommerceShipmentEvent
	if err := model.DB.Where("tenant_id = ? AND shipment_id = ?", f.tenantID, shipment.ID).First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.Source != "manual" || event.Status != "shipped" || event.IdempotencyKey == nil || *event.IdempotencyKey != "shipment-created:"+fmt.Sprint(f.tenantID)+":"+fmt.Sprint(f.orderID) {
		t.Fatalf("initial shipped event=%+v", event)
	}
	var fulfillment model.RetailFulfillment
	if err := model.DB.Where("tenant_id = ? AND order_id = ?", f.tenantID, f.orderID).First(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}
	if fulfillment.Status != "shipped" || fulfillment.Carrier != "carrier" || fulfillment.TrackingNo != "track-atomic-shipped" || fulfillment.ShippedAt == nil || !fulfillment.ShippedAt.Equal(f.now) {
		t.Fatalf("fulfillment projection=%+v", fulfillment)
	}
	var order model.CommerceOrder
	if err := model.DB.First(&order, f.orderID).Error; err != nil {
		t.Fatal(err)
	}
	if order.FulfillmentStatus != "shipped" {
		t.Fatalf("order fulfillment status=%q, want shipped", order.FulfillmentStatus)
	}
}

func TestCommerceLogisticsCreateShipmentMarkShippedRequiresCarrierAndTracking(t *testing.T) {
	tests := []struct {
		name  string
		input CreateCommerceShipmentInput
	}{
		{name: "missing carrier", input: CreateCommerceShipmentInput{ShipmentNo: "SHP-LOG-MISSING-CARRIER", TrackingNo: "track-missing-carrier", MarkShipped: true}},
		{name: "missing tracking", input: CreateCommerceShipmentInput{ShipmentNo: "SHP-LOG-MISSING-TRACKING", CarrierCode: "carrier", MarkShipped: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newCommerceLogisticsFixture(t, "missing-shipped-field-"+tt.name)
			tt.input.OrderID = f.orderID
			if _, err := f.service.CreateShipment(f.tenantID, tt.input); !errors.Is(err, ErrCommerceLogisticsInvalid) {
				t.Fatalf("error=%v, want ErrCommerceLogisticsInvalid", err)
			}
		})
	}
}

func TestCommerceLogisticsCreateShipmentRejectsDuplicateOrderShipment(t *testing.T) {
	f := newCommerceLogisticsFixture(t, "duplicate-order-shipment-customer")
	if _, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{OrderID: f.orderID, ShipmentNo: "SHP-LOG-DUPLICATE-1"}); err != nil {
		t.Fatalf("create first shipment: %v", err)
	}
	if _, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{OrderID: f.orderID, ShipmentNo: "SHP-LOG-DUPLICATE-2"}); !errors.Is(err, ErrCommerceLogisticsConflict) {
		t.Fatalf("duplicate order shipment error=%v, want ErrCommerceLogisticsConflict", err)
	}
	var shipmentCount int64
	if err := model.DB.Model(&model.CommerceShipment{}).Where("tenant_id = ? AND order_id = ? AND deleted_at IS NULL", f.tenantID, f.orderID).Count(&shipmentCount).Error; err != nil {
		t.Fatal(err)
	}
	if shipmentCount != 1 {
		t.Fatalf("active shipment count=%d, want 1", shipmentCount)
	}
}

func TestCommerceLogisticsCreateShipmentRejectsActiveRefund(t *testing.T) {
	f := newCommerceLogisticsFixture(t, "refund-locked-create-customer")
	if err := model.DB.Model(&model.CommerceOrder{}).Where("id = ? AND tenant_id = ?", f.orderID, f.tenantID).Update("refund_status", "requested").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{
		OrderID: f.orderID, ShipmentNo: "SHP-LOG-REFUND-LOCKED", CarrierCode: "carrier", TrackingNo: "refund-locked", MarkShipped: true,
	}); !errors.Is(err, ErrCommerceLogisticsState) {
		t.Fatalf("active refund shipment error=%v, want ErrCommerceLogisticsState", err)
	}
	var shipmentCount int64
	if err := model.DB.Model(&model.CommerceShipment{}).Where("tenant_id = ? AND order_id = ?", f.tenantID, f.orderID).Count(&shipmentCount).Error; err != nil {
		t.Fatal(err)
	}
	if shipmentCount != 0 {
		t.Fatalf("active refund created %d shipments", shipmentCount)
	}
}

func TestCommerceLogisticsCreateShipmentPendingFlowRemainsUnchanged(t *testing.T) {
	f := newCommerceLogisticsFixture(t, "pending-shipment-customer")
	shipment, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{OrderID: f.orderID, ShipmentNo: "SHP-LOG-PENDING"})
	if err != nil {
		t.Fatalf("create pending shipment: %v", err)
	}
	if shipment.Status != "pending_shipment" || shipment.ShippedAt != nil {
		t.Fatalf("pending shipment=%+v", shipment)
	}
	var fulfillment model.RetailFulfillment
	if err := model.DB.Where("tenant_id = ? AND order_id = ?", f.tenantID, f.orderID).First(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}
	if fulfillment.Status != "pending_shipment" || fulfillment.ShippedAt != nil {
		t.Fatalf("pending fulfillment=%+v", fulfillment)
	}
	var order model.CommerceOrder
	if err := model.DB.First(&order, f.orderID).Error; err != nil {
		t.Fatal(err)
	}
	if order.FulfillmentStatus != "pending" {
		t.Fatalf("pending order fulfillment status=%q, want pending", order.FulfillmentStatus)
	}
	var eventCount int64
	if err := model.DB.Model(&model.CommerceShipmentEvent{}).Where("shipment_id = ?", shipment.ID).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatalf("pending shipment event count=%d, want 0", eventCount)
	}
}

func TestCommerceLogisticsAdminOrderTimelineResolvesShipmentByOrder(t *testing.T) {
	f := newCommerceLogisticsFixture(t, "admin-order-timeline-customer")
	shipment, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{
		OrderID:     f.orderID,
		ShipmentNo:  "SHP-LOG-ADMIN-ORDER",
		CarrierCode: "carrier",
		TrackingNo:  "track-admin-order",
		MarkShipped: true,
	})
	if err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	timeline, err := f.service.AdminOrderTimeline(f.tenantID, f.orderID)
	if err != nil {
		t.Fatalf("admin order timeline: %v", err)
	}
	if timeline.Shipment.ID != shipment.ID || timeline.Shipment.OrderID != f.orderID || len(timeline.Events) != 1 {
		t.Fatalf("admin order timeline=%+v", timeline)
	}
	foreign := newCommerceLogisticsFixture(t, "admin-order-timeline-customer")
	if _, err := foreign.service.AdminOrderTimeline(foreign.tenantID, f.orderID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant admin timeline error=%v", err)
	}
}

func TestCommerceLogisticsProviderEventsAreIdempotentAndAppendOnly(t *testing.T) {
	f := newCommerceLogisticsFixture(t, "logistics-customer")
	shipment, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{OrderID: f.orderID, LocationID: f.location, ShipmentNo: "SHP-LOG-1", CarrierCode: "carrier", TrackingNo: "track-1", Source: "provider"})
	if err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	var fulfillment model.RetailFulfillment
	if err := model.DB.Where("order_id = ? AND tenant_id = ?", f.orderID, f.tenantID).First(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}
	if fulfillment.Status != "pending_shipment" || fulfillment.Carrier != "carrier" || fulfillment.TrackingNo != "track-1" {
		t.Fatalf("shipment creation did not sync fulfillment metadata: %+v", fulfillment)
	}
	shippedAt := f.now.Add(time.Hour)
	first, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{Source: "provider", ProviderEventID: "provider-1", Status: "shipped", OccurredAt: shippedAt, Description: "accepted"})
	if err != nil {
		t.Fatalf("append shipped event: %v", err)
	}
	duplicate, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{Source: "provider", ProviderEventID: "provider-1", Status: "shipped", OccurredAt: shippedAt, Description: "accepted"})
	if err != nil || duplicate.ID != first.ID {
		t.Fatalf("duplicate provider event result=%+v err=%v", duplicate, err)
	}
	if err := model.DB.Where("order_id = ? AND tenant_id = ?", f.orderID, f.tenantID).First(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}
	if fulfillment.Status != "shipped" || fulfillment.ShippedAt == nil || !fulfillment.ShippedAt.Equal(shippedAt) {
		t.Fatalf("shipped event did not sync fulfillment: %+v", fulfillment)
	}
	if _, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{Source: "provider", ProviderEventID: "provider-1", Status: "in_transit", OccurredAt: shippedAt.Add(time.Minute)}); !errors.Is(err, ErrCommerceLogisticsConflict) {
		t.Fatalf("conflicting duplicate error=%v", err)
	}
	inTransitAt := shippedAt.Add(time.Hour)
	if _, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{Source: "provider", ProviderEventID: "provider-2", Status: "in_transit", OccurredAt: inTransitAt}); err != nil {
		t.Fatalf("append in-transit event: %v", err)
	}
	if err := model.DB.Where("order_id = ? AND tenant_id = ?", f.orderID, f.tenantID).First(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}
	if fulfillment.Status != "in_transit" {
		t.Fatalf("in-transit event did not sync fulfillment: %+v", fulfillment)
	}
	deliveredAt := shippedAt.Add(2 * time.Hour)
	if _, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{Source: "provider", ProviderEventID: "provider-3", Status: "delivered", OccurredAt: deliveredAt}); err != nil {
		t.Fatalf("append delivered event: %v", err)
	}
	var stored model.CommerceShipment
	if err := model.DB.First(&stored, shipment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "delivered" || stored.DeliveredAt == nil || !stored.DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("delivered projection=%+v", stored)
	}
	if err := model.DB.Where("order_id = ? AND tenant_id = ?", f.orderID, f.tenantID).First(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}
	if fulfillment.Status != "delivered" || fulfillment.DeliveredAt == nil || !fulfillment.DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("delivered event did not sync fulfillment: %+v", fulfillment)
	}
	if _, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{Source: "provider", ProviderEventID: "provider-3", Status: "delivered", OccurredAt: deliveredAt}); err != nil {
		t.Fatalf("duplicate delivered event: %v", err)
	}
	var afterDuplicate model.RetailFulfillment
	if err := model.DB.Where("order_id = ? AND tenant_id = ?", f.orderID, f.tenantID).First(&afterDuplicate).Error; err != nil {
		t.Fatal(err)
	}
	if afterDuplicate.Status != "delivered" || afterDuplicate.DeliveredAt == nil || !afterDuplicate.DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("duplicate delivered event drifted fulfillment: %+v", afterDuplicate)
	}
	var count int64
	if err := model.DB.Model(&model.CommerceShipmentEvent{}).Where("shipment_id = ?", shipment.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("append-only event count=%d", count)
	}
	if _, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{Source: "provider", ProviderEventID: "provider-4", Status: "in_transit", OccurredAt: deliveredAt.Add(time.Minute)}); !errors.Is(err, ErrCommerceLogisticsState) {
		t.Fatalf("delivered regression error=%v", err)
	}
}

func TestCommerceLogisticsManualRetryUsesIdempotencyAndRejectsRegression(t *testing.T) {
	f := newCommerceLogisticsFixture(t, "manual-customer")
	shipment, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{OrderID: f.orderID, LocationID: f.location, ShipmentNo: "SHP-LOG-2", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	input := AppendCommerceShipmentEventInput{Source: "manual", IdempotencyKey: "manual-1", Status: "shipped", Description: "manual handoff", OccurredAt: f.now.Add(time.Hour)}
	one, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	two, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, input)
	if err != nil || two.ID != one.ID {
		t.Fatalf("manual retry result=%+v err=%v", two, err)
	}
	if _, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{Source: "manual", IdempotencyKey: "manual-2", Status: "pending_shipment", OccurredAt: f.now.Add(2 * time.Hour)}); !errors.Is(err, ErrCommerceLogisticsState) {
		t.Fatalf("invalid regression error=%v", err)
	}
	if _, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{Source: "manual", IdempotencyKey: "manual-1", Status: "in_transit", OccurredAt: f.now.Add(2 * time.Hour)}); !errors.Is(err, ErrCommerceLogisticsConflict) {
		t.Fatalf("conflicting retry error=%v", err)
	}
}

func TestCommerceLogisticsAppendEventRejectsActiveRefundWithoutProjection(t *testing.T) {
	f := newCommerceLogisticsFixture(t, "refund-locked-event-customer")
	shipment, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{OrderID: f.orderID, ShipmentNo: "SHP-LOG-REFUND-EVENT"})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.CommerceOrder{}).Where("id = ? AND tenant_id = ?", f.orderID, f.tenantID).Update("refund_status", "processing").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{
		Source: "provider", ProviderEventID: "refund-locked-provider-event", Status: "shipped", OccurredAt: f.now.Add(time.Minute),
	}); !errors.Is(err, ErrCommerceLogisticsState) {
		t.Fatalf("active refund event error=%v, want ErrCommerceLogisticsState", err)
	}
	var eventCount int64
	if err := model.DB.Model(&model.CommerceShipmentEvent{}).Where("tenant_id = ? AND shipment_id = ?", f.tenantID, shipment.ID).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatalf("active refund wrote %d shipment events", eventCount)
	}
	var fulfillment model.RetailFulfillment
	if err := model.DB.Where("tenant_id = ? AND order_id = ?", f.tenantID, f.orderID).First(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}
	if fulfillment.Status != "pending_shipment" {
		t.Fatalf("active refund projected fulfillment status=%q", fulfillment.Status)
	}
}

func TestCommerceLogisticsRefundAndShipmentCreationAreMutuallyExclusive(t *testing.T) {
	for iteration := 0; iteration < 3; iteration++ {
		f := newCommerceLogisticsFixture(t, fmt.Sprintf("refund-create-race-%d", iteration))
		orders := &CommerceOrderService{DB: model.DB, Clock: func() time.Time { return f.now }}
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		results := make(chan error, 2)
		go func() {
			defer wait.Done()
			<-start
			_, err := orders.CreateRefundRequest(f.tenantID, f.orderID, fmt.Sprintf("refund-create-race-%d", iteration), "race test")
			results <- err
		}()
		go func() {
			defer wait.Done()
			<-start
			_, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{
				OrderID: f.orderID, ShipmentNo: fmt.Sprintf("SHP-REFUND-CREATE-RACE-%d", iteration),
				CarrierCode: "carrier", TrackingNo: fmt.Sprintf("race-%d", iteration), MarkShipped: true,
			})
			results <- err
		}()
		close(start)
		wait.Wait()
		close(results)
		successes := 0
		for err := range results {
			if err == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Fatalf("iteration %d successes=%d, want exactly one", iteration, successes)
		}
		var order model.CommerceOrder
		if err := model.DB.First(&order, f.orderID).Error; err != nil {
			t.Fatal(err)
		}
		var shipmentCount, refundCount int64
		if err := model.DB.Model(&model.CommerceShipment{}).Where("tenant_id = ? AND order_id = ?", f.tenantID, f.orderID).Count(&shipmentCount).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.DB.Model(&model.CommerceAfterSaleRequest{}).Where("tenant_id = ? AND order_id = ?", f.tenantID, f.orderID).Count(&refundCount).Error; err != nil {
			t.Fatal(err)
		}
		if shipmentCount+refundCount != 1 || order.RefundStatus == "requested" && shipmentCount != 0 || order.FulfillmentStatus == "shipped" && refundCount != 0 {
			t.Fatalf("iteration %d order=%+v shipments=%d refunds=%d", iteration, order, shipmentCount, refundCount)
		}
	}
}

func TestCommerceLogisticsRefundAndFirstShipmentEventAreMutuallyExclusive(t *testing.T) {
	for iteration := 0; iteration < 3; iteration++ {
		f := newCommerceLogisticsFixture(t, fmt.Sprintf("refund-event-race-%d", iteration))
		shipment, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{
			OrderID: f.orderID, ShipmentNo: fmt.Sprintf("SHP-REFUND-EVENT-RACE-%d", iteration),
		})
		if err != nil {
			t.Fatal(err)
		}
		orders := &CommerceOrderService{DB: model.DB, Clock: func() time.Time { return f.now }}
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		results := make(chan error, 2)
		go func() {
			defer wait.Done()
			<-start
			_, err := orders.CreateRefundRequest(f.tenantID, f.orderID, fmt.Sprintf("refund-event-race-%d", iteration), "race test")
			results <- err
		}()
		go func() {
			defer wait.Done()
			<-start
			_, err := f.service.AppendShipmentEvent(f.tenantID, shipment.ID, AppendCommerceShipmentEventInput{
				Source: "provider", ProviderEventID: fmt.Sprintf("provider-race-%d", iteration),
				Status: "shipped", OccurredAt: f.now.Add(time.Minute),
			})
			results <- err
		}()
		close(start)
		wait.Wait()
		close(results)
		successes := 0
		for resultErr := range results {
			if resultErr == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Fatalf("iteration %d successes=%d, want exactly one", iteration, successes)
		}
		var order model.CommerceOrder
		if err := model.DB.First(&order, f.orderID).Error; err != nil {
			t.Fatal(err)
		}
		var eventCount, refundCount int64
		if err := model.DB.Model(&model.CommerceShipmentEvent{}).Where("tenant_id = ? AND shipment_id = ?", f.tenantID, shipment.ID).Count(&eventCount).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.DB.Model(&model.CommerceAfterSaleRequest{}).Where("tenant_id = ? AND order_id = ?", f.tenantID, f.orderID).Count(&refundCount).Error; err != nil {
			t.Fatal(err)
		}
		if eventCount+refundCount != 1 || order.RefundStatus == "requested" && eventCount != 0 || order.FulfillmentStatus == "shipped" && refundCount != 0 {
			t.Fatalf("iteration %d order=%+v events=%d refunds=%d", iteration, order, eventCount, refundCount)
		}
	}
}

func TestCommerceLogisticsTrackingIdentityAndTenantCustomerIsolation(t *testing.T) {
	f := newCommerceLogisticsFixture(t, "owner-customer")
	if _, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{OrderID: f.orderID, LocationID: f.location, ShipmentNo: "SHP-LOG-3", CarrierCode: "carrier", TrackingNo: "same-track"}); err != nil {
		t.Fatal(err)
	}
	secondOrder := createLogisticsOrder(t, f.tenantID, "other-customer", f.location)
	if _, err := f.service.CreateShipment(f.tenantID, CreateCommerceShipmentInput{OrderID: secondOrder, LocationID: f.location, ShipmentNo: "SHP-LOG-4", CarrierCode: "carrier", TrackingNo: "same-track"}); !errors.Is(err, ErrCommerceLogisticsConflict) {
		t.Fatalf("tracking conflict error=%v", err)
	}
	if _, err := f.service.ListCustomerShipmentTimeline(f.tenantID, "other-customer", f.orderID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("customer isolation error=%v", err)
	}
	foreign := newCommerceLogisticsFixture(t, "owner-customer")
	if _, err := foreign.service.ListShipmentTimeline(foreign.tenantID, 1); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("tenant isolation error=%v", err)
	}
	foreignShipment, err := foreign.service.CreateShipment(foreign.tenantID, CreateCommerceShipmentInput{OrderID: foreign.orderID, LocationID: foreign.location, ShipmentNo: "SHP-LOG-FOREIGN", CarrierCode: "carrier", TrackingNo: "foreign-track"})
	if err != nil {
		t.Fatalf("create foreign shipment: %v", err)
	}
	if _, err := f.service.AppendShipmentEvent(f.tenantID, foreignShipment.ID, AppendCommerceShipmentEventInput{Source: "manual", IdempotencyKey: "cross-tenant-event", Status: "shipped"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant append error=%v", err)
	}
}
