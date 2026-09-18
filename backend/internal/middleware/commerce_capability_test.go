package middleware

import (
	"net/http"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/testdb"

	"github.com/gin-gonic/gin"
)

func TestTenantBusinessCapabilityMiddleware(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(&model.Tenant{}, &model.TenantBusinessCapability{}); err != nil {
		t.Fatal(err)
	}
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	if err := db.Create(&[]model.Tenant{
		{Base: model.Base{ID: 31}, Name: "restaurant", SystemCode: "COMMERCE-MW-31", Status: "active"},
		{Base: model.Base{ID: 32}, Name: "suspended capability", SystemCode: "COMMERCE-MW-32", Status: "active"},
		{Base: model.Base{ID: 33}, Name: "frozen", SystemCode: "COMMERCE-MW-33", Status: "frozen"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]model.TenantBusinessCapability{
		{TenantID: 31, BusinessType: "restaurant", Status: "active"},
		{TenantID: 32, BusinessType: "restaurant", Status: "suspended"},
		{TenantID: 33, BusinessType: "restaurant", Status: "active"},
	}).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/live", tenantContext(31), RequireTenantBusinessCapability("restaurant"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/suspended", tenantContext(32), RequireTenantBusinessCapability("restaurant"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/history", tenantContext(32), RequireConfiguredTenantBusinessCapability("restaurant"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/frozen", tenantContext(33), RequireTenantBusinessCapability("restaurant"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/wrong-domain", tenantContext(31), RequireTenantBusinessCapability("retail"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/wrong-scope", func(c *gin.Context) { c.Set("scope", "platform"); c.Set("tenant_id", uint(31)); c.Next() }, RequireTenantBusinessCapability("restaurant"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/without-db", tenantContext(31), func(c *gin.Context) { model.DB = nil; c.Next() }, RequireTenantBusinessCapability("restaurant"), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	assertCapabilityStatus(t, engine, "/live", http.StatusNoContent)
	assertCapabilityStatus(t, engine, "/suspended", http.StatusForbidden)
	assertCapabilityStatus(t, engine, "/history", http.StatusNoContent)
	assertCapabilityStatus(t, engine, "/frozen", http.StatusForbidden)
	assertCapabilityStatus(t, engine, "/wrong-domain", http.StatusForbidden)
	assertCapabilityStatus(t, engine, "/wrong-scope", http.StatusForbidden)
	assertCapabilityStatus(t, engine, "/without-db", http.StatusForbidden)
}
