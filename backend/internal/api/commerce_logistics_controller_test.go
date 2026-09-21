package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func migrateCommerceLogisticsControllerTables(t *testing.T, db interface{ AutoMigrate(...interface{}) error }) {
	t.Helper()
	// Kept as a small adapter so the fixture remains usable with the gorm DB
	// returned by the shared commerce controller test setup.
	if err := db.AutoMigrate(
		&model.CommerceOrder{}, &model.CommerceOrderItem{}, &model.CommerceFulfillmentLocation{},
		&model.CommerceOrderAdjustment{}, &model.CommerceAfterSaleRequest{},
		&model.RestaurantFulfillment{}, &model.RetailFulfillment{}, &model.CommerceShipment{}, &model.CommerceShipmentEvent{},
	); err != nil {
		t.Fatalf("migrate logistics controller tables: %v", err)
	}
}

func createLogisticsControllerOrder(t *testing.T, db *gorm.DB, tenantID, locationID uint, orderNo, customerID, businessType, channel, paymentStatus string) model.CommerceOrder {
	t.Helper()
	order := model.CommerceOrder{
		TenantID: tenantID, OrderNo: orderNo, IdempotencyKey: "idempotency-" + orderNo,
		BusinessType: businessType, Channel: channel, CustomerID: customerID, LocationID: locationID,
		OriginalAmountCents: 1000, TotalAmountCents: 1000, PaymentStatus: paymentStatus,
		FulfillmentStatus: "pending_shipment", RefundStatus: "none", ContactName: "物流测试",
		ContactPhone: "13800138000", ShippingAddressJSON: `{"detail":"测试地址"}`,
	}
	if err := db.Create(&order).Error; err != nil {
		t.Fatalf("create logistics order: %v", err)
	}
	if businessType == "retail" {
		if err := db.Create(&model.RetailFulfillment{TenantID: tenantID, OrderID: order.ID, LocationID: locationID, Status: "pending_shipment"}).Error; err != nil {
			t.Fatalf("create retail fulfillment: %v", err)
		}
	}
	return order
}

func TestCommerceLogisticsControllerAdminIsolationAndManualIdentity(t *testing.T) {
	db := openCommerceBoundaryControllerDB(t)
	migrateCommerceLogisticsControllerTables(t, db)
	owner := createCommerceControllerTenant(t, db, "logistics owner", "retail")
	foreign := createCommerceControllerTenant(t, db, "logistics foreign", "retail")
	ownerLocation := model.CommerceFulfillmentLocation{TenantID: owner.ID, BusinessType: "retail", Name: "Owner warehouse", LocationType: "warehouse", Status: "active"}
	foreignLocation := model.CommerceFulfillmentLocation{TenantID: foreign.ID, BusinessType: "retail", Name: "Foreign warehouse", LocationType: "warehouse", Status: "active"}
	if err := db.Create(&ownerLocation).Error; err != nil {
		t.Fatalf("create owner location: %v", err)
	}
	if err := db.Create(&foreignLocation).Error; err != nil {
		t.Fatalf("create foreign location: %v", err)
	}
	order := createLogisticsControllerOrder(t, db, owner.ID, ownerLocation.ID, "RET-LOG-OWNER-1", "owner-customer", "retail", "direct", "paid")
	controller := &CommerceLogisticsController{Service: service.CommerceLogisticsService{DB: db}}
	createResponse := invokeCommerceController(t, http.MethodPost, "/commerce/orders/"+strconv.FormatUint(uint64(order.ID), 10)+"/shipments", owner.ID, map[string]interface{}{
		"tenant_id": foreign.ID, "order_id": 999999, "location_id": ownerLocation.ID,
		"shipment_no": "SHP-OWNER-1", "carrier_code": "carrier", "tracking_no": "TRACK-OWNER-1",
		"source": "provider", "address_snapshot": `{"detail":"下单地址"}`,
	}, gin.Params{{Key: "orderID", Value: strconv.FormatUint(uint64(order.ID), 10)}}, controller.CreateShipment)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create shipment status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var shipment model.CommerceShipment
	if err := json.Unmarshal(createResponse.Body.Bytes(), &shipment); err != nil {
		t.Fatalf("decode created shipment: %v", err)
	}
	if shipment.TenantID != owner.ID || shipment.OrderID != order.ID || shipment.Source != "manual" || shipment.Status != "shipped" || shipment.ShippedAt == nil {
		t.Fatalf("request body widened shipment ownership: %+v", shipment)
	}
	var initialEvents int64
	if err := db.Model(&model.CommerceShipmentEvent{}).Where("tenant_id = ? AND shipment_id = ? AND status = ?", owner.ID, shipment.ID, "shipped").Count(&initialEvents).Error; err != nil || initialEvents != 1 {
		t.Fatalf("atomic shipped event count=%d err=%v, want 1", initialEvents, err)
	}
	adminOrderRead := invokeCommerceController(t, http.MethodGet, "/commerce/orders/"+strconv.FormatUint(uint64(order.ID), 10)+"/shipment", owner.ID, nil, gin.Params{{Key: "orderID", Value: strconv.FormatUint(uint64(order.ID), 10)}}, controller.AdminOrderTimeline)
	if adminOrderRead.Code != http.StatusOK {
		t.Fatalf("admin order timeline status=%d body=%s", adminOrderRead.Code, adminOrderRead.Body.String())
	}
	foreignOrderRead := invokeCommerceController(t, http.MethodGet, "/commerce/orders/"+strconv.FormatUint(uint64(order.ID), 10)+"/shipment", foreign.ID, nil, gin.Params{{Key: "orderID", Value: strconv.FormatUint(uint64(order.ID), 10)}}, controller.AdminOrderTimeline)
	if foreignOrderRead.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant order timeline status=%d body=%s, want 404", foreignOrderRead.Code, foreignOrderRead.Body.String())
	}

	shipmentID := strconv.FormatUint(uint64(shipment.ID), 10)
	foreignRead := invokeCommerceController(t, http.MethodGet, "/commerce/shipments/"+shipmentID+"/timeline", foreign.ID, nil, gin.Params{{Key: "shipmentID", Value: shipmentID}}, controller.ShipmentTimeline)
	if foreignRead.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant shipment read status=%d body=%s, want 404", foreignRead.Code, foreignRead.Body.String())
	}

	updateResponse := invokeCommerceController(t, http.MethodPut, "/commerce/shipments/"+shipmentID, owner.ID, map[string]interface{}{
		"shipment_no": "SHP-OWNER-1-UPDATED", "carrier_code": "carrier-2", "carrier_name": "承运商",
		"tracking_no": "TRACK-OWNER-1-UPDATED", "address_snapshot": `{"detail":"更新地址"}`,
		"tenant_id": foreign.ID, "order_id": 999999, "location_id": foreignLocation.ID, "status": "delivered", "source": "provider",
	}, gin.Params{{Key: "shipmentID", Value: shipmentID}}, controller.UpdateShipment)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update shipment status=%d body=%s", updateResponse.Code, updateResponse.Body.String())
	}
	var updated model.CommerceShipment
	if err := json.Unmarshal(updateResponse.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated shipment: %v", err)
	}
	if updated.TenantID != owner.ID || updated.OrderID != order.ID || updated.LocationID != ownerLocation.ID || updated.Status != "shipped" || updated.Source != "manual" {
		t.Fatalf("mutable update changed server-owned facts: %+v", updated)
	}

	manualResponse := invokeCommerceController(t, http.MethodPost, "/commerce/shipments/"+shipmentID+"/events", owner.ID, map[string]interface{}{
		"source": "provider", "provider_event_id": "provider-forged-event", "idempotency_key": "manual-event-1",
		"status": "in_transit", "description": "运输中", "occurred_at": time.Date(2026, 9, 21, 1, 2, 3, 0, time.UTC),
		"payload_hash": "forged-hash", "payload": `{"provider":"forged"}`, "allow_exception_recovery": true,
	}, gin.Params{{Key: "shipmentID", Value: shipmentID}}, controller.AppendManualEvent)
	if manualResponse.Code != http.StatusCreated {
		t.Fatalf("manual event status=%d body=%s", manualResponse.Code, manualResponse.Body.String())
	}
	var event model.CommerceShipmentEvent
	if err := json.Unmarshal(manualResponse.Body.Bytes(), &event); err != nil {
		t.Fatalf("decode manual event: %v", err)
	}
	if event.Source != "manual" || event.ProviderEventID != nil || event.PayloadHash != "" || event.PayloadJSON != "" || event.OccurredAt.Equal(time.Date(2026, 9, 21, 1, 2, 3, 0, time.UTC)) {
		t.Fatalf("manual endpoint preserved provider-owned facts: %+v", event)
	}

	secondOrder := createLogisticsControllerOrder(t, db, owner.ID, ownerLocation.ID, "RET-LOG-OWNER-2", "owner-customer", "retail", "direct", "paid")
	duplicateResponse := invokeCommerceController(t, http.MethodPost, "/commerce/orders/"+strconv.FormatUint(uint64(secondOrder.ID), 10)+"/shipments", owner.ID, map[string]interface{}{
		"order_id": secondOrder.ID, "location_id": ownerLocation.ID, "shipment_no": "SHP-OWNER-2",
		"carrier_code": "carrier-2", "tracking_no": "TRACK-OWNER-1-UPDATED",
	}, gin.Params{{Key: "orderID", Value: strconv.FormatUint(uint64(secondOrder.ID), 10)}}, controller.CreateShipment)
	if duplicateResponse.Code != http.StatusConflict {
		t.Fatalf("duplicate tracking status=%d body=%s, want 409", duplicateResponse.Code, duplicateResponse.Body.String())
	}
}

func TestCommerceLogisticsControllerStorefrontIsSessionAndBusinessScoped(t *testing.T) {
	fixture := newCommerceStorefrontControllerFixture(t)
	migrateCommerceLogisticsControllerTables(t, fixture.db)
	if err := fixture.db.Create(&model.TenantBusinessCapability{TenantID: fixture.tenant.ID, BusinessType: "retail", Status: "active"}).Error; err != nil {
		t.Fatalf("create retail capability: %v", err)
	}
	retailLocation := model.CommerceFulfillmentLocation{TenantID: fixture.tenant.ID, BusinessType: "retail", Name: "Retail warehouse", LocationType: "warehouse", Status: "active"}
	if err := fixture.db.Create(&retailLocation).Error; err != nil {
		t.Fatalf("create retail location: %v", err)
	}
	if err := fixture.db.Create(&model.CommerceStorefrontBinding{TenantID: fixture.tenant.ID, ChannelAccountID: fixture.account.ID, BusinessType: "retail", LocationID: retailLocation.ID, Status: "active"}).Error; err != nil {
		t.Fatalf("create retail binding: %v", err)
	}

	loginResponse := invokeCommerceStorefrontController(t, http.MethodPost, "/storefront/wechat/session", "", map[string]interface{}{"app_id": fixture.account.AppID, "code": "logistics-customer"}, nil, fixture.control.Login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("storefront login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	var login service.CommerceStorefrontLoginResult
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &login); err != nil {
		t.Fatalf("decode storefront login: %v", err)
	}
	var session model.CommerceCustomerSession
	if err := fixture.db.Where("tenant_id = ? AND channel_account_id = ? AND status = ?", fixture.tenant.ID, fixture.account.ID, "active").First(&session).Error; err != nil {
		t.Fatalf("load storefront session: %v", err)
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("wechat-miniapp:%d:%s", fixture.account.ID, session.SubjectHash)))
	customerID := hex.EncodeToString(hash[:])
	order := createLogisticsControllerOrder(t, fixture.db, fixture.tenant.ID, retailLocation.ID, "RET-LOG-STOREFRONT-1", customerID, "retail", "wechat_miniapp", "paid")
	logistics := service.CommerceLogisticsService{DB: fixture.db}
	shipment, err := logistics.CreateShipment(fixture.tenant.ID, service.CreateCommerceShipmentInput{OrderID: order.ID, LocationID: retailLocation.ID, ShipmentNo: "SHP-STOREFRONT-1", CarrierCode: "carrier", TrackingNo: "TRACK-STOREFRONT-1"})
	if err != nil {
		t.Fatalf("create storefront shipment: %v", err)
	}
	if _, err := logistics.AppendShipmentEvent(fixture.tenant.ID, shipment.ID, service.AppendCommerceShipmentEventInput{Source: "manual", IdempotencyKey: "storefront-event-1", Status: "shipped", Description: "已发货"}); err != nil {
		t.Fatalf("append storefront event: %v", err)
	}
	controller := &CommerceLogisticsController{Service: logistics, Storefront: &fixture.control.Service}
	path := "/storefront/wechat/orders/" + order.OrderNo + "/shipment"
	response := invokeCommerceStorefrontController(t, http.MethodGet, path, login.Token, nil, gin.Params{{Key: "orderNo", Value: order.OrderNo}}, controller.CustomerOrderTimeline)
	if response.Code != http.StatusOK {
		t.Fatalf("storefront timeline status=%d body=%s", response.Code, response.Body.String())
	}
	var timeline service.CommerceShipmentTimeline
	if err := json.Unmarshal(response.Body.Bytes(), &timeline); err != nil {
		t.Fatalf("decode storefront timeline: %v", err)
	}
	if timeline.Shipment.ID != shipment.ID || timeline.Shipment.TenantID != fixture.tenant.ID || len(timeline.Events) != 1 {
		t.Fatalf("storefront timeline crossed identity boundary: %+v", timeline)
	}

	otherLoginResponse := invokeCommerceStorefrontController(t, http.MethodPost, "/storefront/wechat/session", "", map[string]interface{}{"app_id": fixture.account.AppID, "code": "other-logistics-customer"}, nil, fixture.control.Login)
	var otherLogin service.CommerceStorefrontLoginResult
	if err := json.Unmarshal(otherLoginResponse.Body.Bytes(), &otherLogin); err != nil {
		t.Fatalf("decode other storefront login: %v", err)
	}
	response = invokeCommerceStorefrontController(t, http.MethodGet, path, otherLogin.Token, nil, gin.Params{{Key: "orderNo", Value: order.OrderNo}}, controller.CustomerOrderTimeline)
	if response.Code != http.StatusNotFound {
		t.Fatalf("other customer timeline status=%d body=%s, want 404", response.Code, response.Body.String())
	}

	nonRetail := createLogisticsControllerOrder(t, fixture.db, fixture.tenant.ID, fixture.location.ID, "REST-LOG-STOREFRONT-1", customerID, "restaurant", "wechat_miniapp", "paid")
	nonRetailPath := "/storefront/wechat/orders/" + nonRetail.OrderNo + "/shipment"
	response = invokeCommerceStorefrontController(t, http.MethodGet, nonRetailPath, login.Token, nil, gin.Params{{Key: "orderNo", Value: nonRetail.OrderNo}}, controller.CustomerOrderTimeline)
	if response.Code != http.StatusNotFound {
		t.Fatalf("non-retail timeline status=%d body=%s, want 404", response.Code, response.Body.String())
	}

	response = invokeCommerceStorefrontController(t, http.MethodGet, path, "", nil, gin.Params{{Key: "orderNo", Value: order.OrderNo}}, controller.CustomerOrderTimeline)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated timeline status=%d body=%s, want 401", response.Code, response.Body.String())
	}
}
