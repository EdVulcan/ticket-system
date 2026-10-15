package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"ticket-backend/internal/authz"
	"ticket-backend/internal/middleware"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

func TestRestaurantDeliveryDetailsHTTPAuthorityAndNoDeparture(t *testing.T) {
	db := openCommerceBoundaryControllerDB(t)
	migrateCommerceStorefrontOrderTables(t, db)
	owner := createCommerceControllerTenant(t, db, "delivery details owner", "restaurant")
	foreign := createCommerceControllerTenant(t, db, "delivery details foreign", "restaurant")
	order := model.CommerceOrder{
		TenantID: owner.ID, OrderNo: "DELIVERY-DETAILS-HTTP", IdempotencyKey: "delivery-details-http",
		BusinessType: "restaurant", PaymentStatus: "paid", RefundStatus: "none", FulfillmentStatus: "preparing",
		LocationID: 1, OriginalAmountCents: 1200, TotalAmountCents: 1200,
	}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	fulfillment := model.RestaurantFulfillment{TenantID: owner.ID, OrderID: order.ID, LocationID: 1, Method: "delivery", Status: "preparing"}
	if err := db.Create(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}
	controller := CommerceOrderController{Service: service.CommerceOrderService{DB: db}}
	invoke := func(tenantID uint, role, id, body string) *httptest.ResponseRecorder {
		engine := gin.New()
		engine.Use(func(ctx *gin.Context) {
			ctx.Set("tenant_id", tenantID)
			ctx.Set("scope", "tenant")
			ctx.Set("role", role)
			ctx.Set("user_id", uint(17))
			ctx.Next()
		})
		engine.PUT("/orders/:id/restaurant-delivery", middleware.RequireTenantPermission(authz.PermissionOperationsWrite), controller.SaveRestaurantDelivery)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/orders/"+id+"/restaurant-delivery", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, request)
		return recorder
	}
	id := strconv.FormatUint(uint64(order.ID), 10)
	body := `{"delivery_provider":"courier_platform","courier_platform_name":"跑腿平台","courier_order_no":"RUNNER-123","courier_contact":"13800138000","tenant_id":999,"status":"delivering","actor_user_id":999,"actor_role":"platform_admin"}`
	for _, check := range []struct {
		name           string
		tenantID       uint
		role, id, body string
		status         int
	}{
		{"foreign tenant", foreign.ID, authz.RoleTenantAdmin, id, body, http.StatusNotFound},
		{"viewer", owner.ID, authz.RoleViewer, id, body, http.StatusForbidden},
		{"malformed body", owner.ID, authz.RoleTenantAdmin, id, "{", http.StatusBadRequest},
		{"invalid order", owner.ID, authz.RoleTenantAdmin, "invalid", body, http.StatusBadRequest},
	} {
		t.Run(check.name, func(t *testing.T) {
			response := invoke(check.tenantID, check.role, check.id, check.body)
			if response.Code != check.status {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), check.status)
			}
		})
	}
	var unchanged model.RestaurantFulfillment
	if err := db.First(&unchanged, fulfillment.ID).Error; err != nil || unchanged.DeliveryProvider != "" || unchanged.Status != "preparing" {
		t.Fatalf("rejected HTTP requests changed fulfillment: %+v err=%v", unchanged, err)
	}
	response := invoke(owner.ID, authz.RoleTenantAdmin, id, body)
	if response.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", response.Code, response.Body.String())
	}
	var saved model.RestaurantFulfillment
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.TenantID != owner.ID || saved.Status != "preparing" || saved.DeliveryStartedAt != nil || saved.CourierOrderNo != "RUNNER-123" {
		t.Fatalf("delivery save changed ownership or departure: %+v", saved)
	}
	var persisted model.CommerceOrder
	if err := db.First(&persisted, order.ID).Error; err != nil || persisted.FulfillmentStatus != "preparing" || persisted.TotalAmountCents != 1200 {
		t.Fatalf("delivery save changed order facts: %+v err=%v", persisted, err)
	}
	var audit model.AuditLog
	if err := db.Where("tenant_id = ? AND action = ?", owner.ID, "commerce.restaurant_delivery.update").First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if audit.ActorUserID != 17 || audit.ActorRole != authz.RoleTenantAdmin {
		t.Fatalf("audit accepted client actor fields: %+v", audit)
	}
	// A single dispatch request can reuse the saved plan while recording both
	// preparation completion and departure; the draft save itself did neither.
	dispatch := func(tenantID uint) *httptest.ResponseRecorder {
		return invokeCommerceController(t, http.MethodPost, "/orders/"+id+"/restaurant-fulfillment", tenantID, map[string]interface{}{
			"status": "delivering", "delivery_provider": saved.DeliveryProvider,
			"courier_platform_name": saved.CourierPlatformName, "courier_order_no": saved.CourierOrderNo,
			"courier_contact": saved.CourierContact, "tenant_id": foreign.ID,
			"actor_user_id": 999, "actor_role": "platform_admin",
		}, gin.Params{{Key: "id", Value: id}}, func(ctx *gin.Context) {
			ctx.Set("user_id", uint(17))
			ctx.Set("role", authz.RoleTenantAdmin)
			controller.RestaurantFulfillment(ctx)
		})
	}
	if response := dispatch(foreign.ID); response.Code != http.StatusNotFound {
		t.Fatalf("foreign dispatch status=%d body=%s", response.Code, response.Body.String())
	}
	response = dispatch(owner.ID)
	if response.Code != http.StatusOK {
		t.Fatalf("merged dispatch status=%d body=%s", response.Code, response.Body.String())
	}
	var dispatched model.RestaurantFulfillment
	if err := json.Unmarshal(response.Body.Bytes(), &dispatched); err != nil {
		t.Fatal(err)
	}
	if dispatched.Status != "delivering" || dispatched.ReadyAt == nil || dispatched.DeliveryStartedAt == nil || !dispatched.ReadyAt.Equal(*dispatched.DeliveryStartedAt) || dispatched.CourierOrderNo != "RUNNER-123" {
		t.Fatalf("merged dispatch lost preparation or delivery facts: %+v", dispatched)
	}
	var transitionAudit model.AuditLog
	if err := db.Where("tenant_id = ? AND action = ?", owner.ID, "commerce.restaurant_fulfillment.transition").First(&transitionAudit).Error; err != nil {
		t.Fatal(err)
	}
	if transitionAudit.ActorUserID != 17 || transitionAudit.ActorRole != authz.RoleTenantAdmin {
		t.Fatalf("dispatch audit accepted client actor fields: %+v", transitionAudit)
	}
}
