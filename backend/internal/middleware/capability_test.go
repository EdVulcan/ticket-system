package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/testdb"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRequireAnyTenantCapability(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(&model.TenantCapability{}); err != nil {
		t.Fatal(err)
	}
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
	})
	if err := db.Create(&model.TenantCapability{TenantID: 1, Capability: "distributor", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().Add(-time.Hour)
	if err := db.Create(&model.TenantCapability{TenantID: 2, Capability: "supplier", Status: "active", ExpiresAt: &expiredAt}).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/supplier", tenantContext(1), RequireAnyTenantCapability("supplier"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/sales", tenantContext(1), RequireAnyTenantCapability("supplier", "distributor"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/expired", tenantContext(2), RequireAnyTenantCapability("supplier"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/without-database", tenantContext(1), func(c *gin.Context) { model.DB = nil; c.Next() }, RequireAnyTenantCapability("distributor"), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	assertCapabilityStatus(t, engine, "/supplier", http.StatusForbidden)
	assertCapabilityStatus(t, engine, "/sales", http.StatusNoContent)
	assertCapabilityStatus(t, engine, "/expired", http.StatusForbidden)
	assertCapabilityStatus(t, engine, "/without-database", http.StatusForbidden)
}

func TestSupplierBusinessTypeSeparatesScenicAndHotelOperations(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(&model.Tenant{}, &model.TenantCapability{}, &model.SupplierBusinessType{}); err != nil {
		t.Fatal(err)
	}
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	if err := db.Create(&[]model.Tenant{
		{Base: model.Base{ID: 11}, Name: "scenic supplier", SystemCode: "SCENIC-11", Status: "active"},
		{Base: model.Base{ID: 12}, Name: "hotel supplier", SystemCode: "HOTEL-12", Status: "active"},
		{Base: model.Base{ID: 13}, Name: "non supplier", SystemCode: "NON-SUPPLIER-13", Status: "active"},
		{Base: model.Base{ID: 14}, Name: "expired supplier", SystemCode: "EXPIRED-14", Status: "active"},
		{Base: model.Base{ID: 15}, Name: "suspended supplier", SystemCode: "SUSPENDED-15", Status: "active"},
		{Base: model.Base{ID: 16}, Name: "suspended scenic", SystemCode: "SUSPENDED-SCENIC-16", Status: "active"},
		{Base: model.Base{ID: 17}, Name: "frozen scenic supplier", SystemCode: "FROZEN-SCENIC-17", Status: "frozen"},
		{Base: model.Base{ID: 18}, Name: "rejected supplier", SystemCode: "REJECTED-18", Status: "active"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().Add(-time.Hour)
	if err := db.Create(&[]model.TenantCapability{
		{TenantID: 11, Capability: "supplier", Status: "active"},
		{TenantID: 12, Capability: "supplier", Status: "active"},
		{TenantID: 13, Capability: "distributor", Status: "active"},
		{TenantID: 14, Capability: "supplier", Status: "active", ExpiresAt: &expiredAt},
		{TenantID: 15, Capability: "supplier", Status: "suspended"},
		{TenantID: 16, Capability: "supplier", Status: "active"},
		{TenantID: 17, Capability: "supplier", Status: "active"},
		{TenantID: 18, Capability: "supplier", Status: "rejected"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]model.SupplierBusinessType{
		{TenantID: 11, BusinessType: "scenic", Status: "active"},
		{TenantID: 12, BusinessType: "hotel", Status: "active"},
		{TenantID: 13, BusinessType: "scenic", Status: "active"},
		{TenantID: 14, BusinessType: "scenic", Status: "active"},
		{TenantID: 15, BusinessType: "scenic", Status: "active"},
		{TenantID: 16, BusinessType: "scenic", Status: "suspended"},
		{TenantID: 17, BusinessType: "scenic", Status: "suspended"},
		{TenantID: 18, BusinessType: "scenic", Status: "suspended"},
	}).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/scenic", tenantContext(11), RequireAnySupplierBusinessType("scenic"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/hotel-on-scenic", tenantContext(12), RequireAnySupplierBusinessType("scenic"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/business-without-supplier", tenantContext(13), RequireAnySupplierBusinessType("scenic"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/expired-supplier", tenantContext(14), RequireAnySupplierBusinessType("scenic"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/suspended-supplier-live", tenantContext(15), RequireAnySupplierBusinessType("scenic"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/suspended-scenic-live", tenantContext(16), RequireAnySupplierBusinessType("scenic"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/expired-supplier-history", tenantContext(14), RequireConfiguredSupplierBusinessType("scenic"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/suspended-supplier-history", tenantContext(15), RequireConfiguredSupplierBusinessType("scenic"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/suspended-scenic-history", tenantContext(16), RequireConfiguredSupplierBusinessType("scenic"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/hotel-history", tenantContext(12), RequireConfiguredSupplierBusinessType("scenic"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/frozen-history", tenantContext(17), RequireConfiguredSupplierBusinessType("scenic"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/rejected-history", tenantContext(18), RequireConfiguredSupplierBusinessType("scenic"), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	assertCapabilityStatus(t, engine, "/scenic", http.StatusNoContent)
	assertCapabilityStatus(t, engine, "/hotel-on-scenic", http.StatusForbidden)
	assertCapabilityStatus(t, engine, "/business-without-supplier", http.StatusForbidden)
	assertCapabilityStatus(t, engine, "/expired-supplier", http.StatusForbidden)
	assertCapabilityStatus(t, engine, "/suspended-supplier-live", http.StatusForbidden)
	assertCapabilityStatus(t, engine, "/suspended-scenic-live", http.StatusForbidden)
	assertCapabilityStatus(t, engine, "/expired-supplier-history", http.StatusNoContent)
	assertCapabilityStatus(t, engine, "/suspended-supplier-history", http.StatusNoContent)
	assertCapabilityStatus(t, engine, "/suspended-scenic-history", http.StatusNoContent)
	assertCapabilityStatus(t, engine, "/hotel-history", http.StatusForbidden)
	assertCapabilityStatus(t, engine, "/frozen-history", http.StatusForbidden)
	assertCapabilityStatus(t, engine, "/rejected-history", http.StatusForbidden)
}

func tenantContext(tenantID uint) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("scope", "tenant")
		c.Set("tenant_id", tenantID)
		c.Next()
	}
}

func assertCapabilityStatus(t *testing.T, engine *gin.Engine, path string, expected int) {
	t.Helper()
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != expected {
		t.Fatalf("%s status=%d body=%s, want %d", path, response.Code, response.Body.String(), expected)
	}
}
