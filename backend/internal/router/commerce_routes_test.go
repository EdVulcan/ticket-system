package router

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"ticket-backend/internal/testdb"

	"github.com/gin-gonic/gin"
)

func TestCommerceOrderRoutesRegisterWithoutWildcardConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	InitRouter(engine)

	routes := map[string]bool{}
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, expected := range []string{
		"PUT /api/v1/commerce/locations/:locationID",
		"GET /api/v1/commerce/orders/:id",
		"GET /api/v1/commerce/orders/:id/shipment",
		"POST /api/v1/commerce/orders/:id/shipments",
		"PUT /api/v1/commerce/orders/:id/restaurant-delivery",
	} {
		if !routes[expected] {
			t.Fatalf("route %q is not registered", expected)
		}
	}
}

func TestCommerceLocationUpdateRouteRequiresCatalogWriteAndActiveCapability(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(&model.Tenant{}, &model.User{}, &model.TenantBusinessCapability{}, &model.CommerceFulfillmentLocation{}); err != nil {
		t.Fatal(err)
	}
	previousDB, previousSecret := model.DB, config.GlobalConfig.Security.JWTSecret
	model.DB = db
	model.InitWriter(db, 5*time.Second)
	config.GlobalConfig.Security.JWTSecret = "01234567890123456789012345678901"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = model.CloseWriter(ctx)
		model.DB = previousDB
		config.GlobalConfig.Security.JWTSecret = previousSecret
	})
	tenant := model.Tenant{Name: "Location editor", SystemCode: "LOCATION-EDITOR", SecretKey: "unused", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TenantBusinessCapability{TenantID: tenant.ID, BusinessType: "retail", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	location := model.CommerceFulfillmentLocation{TenantID: tenant.ID, BusinessType: "retail", Name: "Original warehouse", LocationType: "warehouse", Status: "active"}
	if err := db.Create(&location).Error; err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for _, role := range []string{"viewer", "product_operator"} {
		user := model.User{TenantID: tenant.ID, Username: role, Password: "unused", Role: role, TokenVersion: 1}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		token, err := (&service.AuthService{}).GenerateToken(&user)
		if err != nil {
			t.Fatal(err)
		}
		tokens[role] = token
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	InitRouter(engine)
	request := func(token string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/commerce/locations/"+strconv.FormatUint(uint64(location.ID), 10), bytes.NewBufferString(`{"business_type":"retail","name":"Edited warehouse","location_type":"pickup","status":"inactive"}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		engine.ServeHTTP(recorder, req)
		return recorder
	}
	for _, check := range []struct {
		name     string
		token    string
		wantCode int
	}{
		{"unauthenticated", "", http.StatusUnauthorized},
		{"catalog reader", tokens["viewer"], http.StatusForbidden},
		{"catalog writer", tokens["product_operator"], http.StatusOK},
	} {
		response := request(check.token)
		if response.Code != check.wantCode {
			t.Fatalf("%s status=%d body=%s, want %d", check.name, response.Code, response.Body.String(), check.wantCode)
		}
	}
	if err := db.Model(&model.TenantBusinessCapability{}).Where("tenant_id = ? AND business_type = ?", tenant.ID, "retail").Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	if response := request(tokens["product_operator"]); response.Code != http.StatusForbidden {
		t.Fatalf("suspended business status=%d body=%s, want 403", response.Code, response.Body.String())
	}
}
