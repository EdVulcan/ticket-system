//go:build cgo

package middleware

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"ticket-backend/internal/deviceauth"
	"ticket-backend/internal/model"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDeviceAuthRejectsReplay(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "device-auth.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Tenant{}, &model.TenantCapability{}, &model.Device{}, &model.DeviceRequestNonce{}); err != nil {
		t.Fatal(err)
	}
	previousDB := model.DB
	model.DB = db
	model.InitWriter(db, 16, time.Second, 5*time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = model.CloseWriter(ctx)
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
		model.DB = previousDB
	})
	tenant := model.Tenant{Name: "景区", SystemCode: "SCENIC", Status: "active"}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.TenantCapability{TenantID: tenant.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	device := model.Device{Name: "一号闸机", SerialNumber: "GATE-1", Type: "gate", Status: "online", TenantID: tenant.ID, ScenicAreaID: 1, AuthKeyHash: fmt.Sprintf("%x", deviceauth.DeriveKey("secret"))}
	if err := model.DB.Create(&device).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/hardware/heartbeat", DeviceAuth(), func(ctx *gin.Context) { ctx.Status(http.StatusOK) })
	request := func() *httptest.ResponseRecorder {
		body := []byte(`{"status":"online"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/hardware/heartbeat", bytes.NewReader(body))
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		req.Header.Set(deviceauth.HeaderSystemCode, tenant.SystemCode)
		req.Header.Set(deviceauth.HeaderSerial, device.SerialNumber)
		req.Header.Set(deviceauth.HeaderRequestID, "heartbeat-1")
		req.Header.Set(deviceauth.HeaderTimestamp, timestamp)
		req.Header.Set(deviceauth.HeaderNonce, "nonce-1")
		req.Header.Set(deviceauth.HeaderSignature, deviceauth.Sign(deviceauth.DeriveKey("secret"), http.MethodPost, req.URL.Path, timestamp, "nonce-1", "heartbeat-1", body))
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		return resp
	}
	if resp := request(); resp.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", resp.Code, resp.Body.String())
	}
	if resp := request(); resp.Code != http.StatusConflict {
		t.Fatalf("replay status=%d body=%s", resp.Code, resp.Body.String())
	}
}
