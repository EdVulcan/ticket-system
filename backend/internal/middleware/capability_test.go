//go:build cgo

package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRequireAnyTenantCapability(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "capability.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.TenantCapability{}); err != nil {
		t.Fatal(err)
	}
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
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
