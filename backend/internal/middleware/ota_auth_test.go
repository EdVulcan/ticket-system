//go:build cgo

package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestOTASignatureTimestampAndReplayProtection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "ota.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Tenant{}, &model.TenantCapability{}, &model.OTANonce{}); err != nil {
		t.Fatal(err)
	}
	tenant := model.Tenant{Name: "OTA Tenant", SystemCode: "OTA-KEY", SecretKey: "OTA-SECRET"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TenantCapability{TenantID: tenant.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	model.DB = db
	model.InitWriter(db, 16, time.Second, 5*time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = model.CloseWriter(ctx)
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	config.GlobalConfig.Security.OTAMaxClockSkewSeconds = 300

	engine := gin.New()
	engine.POST("/ota", OTASignMiddleware(), func(ctx *gin.Context) {
		if ctx.GetUint("tenant_id") != tenant.ID {
			t.Error("OTA tenant was not added to request context")
		}
		ctx.Status(http.StatusNoContent)
	})

	request := func(timestamp int64, nonce string) int {
		params := map[string]string{
			"app_key": tenant.SystemCode, "timestamp": strconv.FormatInt(timestamp, 10), "nonce": nonce,
		}
		values := url.Values{}
		for key, value := range params {
			values.Set(key, value)
		}
		values.Set("sign", utils.SignParams(params, tenant.SecretKey))
		recorder := httptest.NewRecorder()
		httpRequest := httptest.NewRequest(http.MethodPost, "/ota?"+values.Encode(), nil)
		engine.ServeHTTP(recorder, httpRequest)
		return recorder.Code
	}

	now := time.Now().Unix()
	if status := request(now, "same-nonce"); status != http.StatusNoContent {
		t.Fatalf("valid request status = %d", status)
	}
	if status := request(now, "same-nonce"); status != http.StatusConflict {
		t.Fatalf("replayed request status = %d, want 409", status)
	}
	if status := request(now-600, fmt.Sprintf("old-%d", now)); status != http.StatusUnauthorized {
		t.Fatalf("expired request status = %d, want 401", status)
	}
	if err := db.Model(&tenant).Update("status", "frozen").Error; err != nil {
		t.Fatal(err)
	}
	if status := request(now, fmt.Sprintf("frozen-%d", now)); status != http.StatusUnauthorized {
		t.Fatalf("frozen tenant request status = %d, want 401", status)
	}
}
