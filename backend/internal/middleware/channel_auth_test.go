//go:build cgo

package middleware

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestChannelSignatureCoversBodyAndRejectsConflict(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "channel.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Tenant{}, &model.TenantCapability{}, &model.ChannelAccount{}, &model.ChannelRequest{}); err != nil {
		t.Fatal(err)
	}
	model.DB = db
	model.InitWriter(db, 16, time.Second, 5*time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = model.CloseWriter(ctx)
		model.DB = nil
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	config.GlobalConfig.Security.EncryptionKey = strings.Repeat("a", 32)
	config.GlobalConfig.Security.OTAMaxClockSkewSeconds = 300
	tenant := model.Tenant{Name: "Channel Tenant", SystemCode: "CH-TENANT", SecretKey: "legacy-secret"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TenantCapability{TenantID: tenant.ID, Capability: "distributor", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	account := model.ChannelAccount{Code: "travel-test", Type: "travel-agency", PermissionsJSON: `["orders:create"]`, RateLimitPerMin: 1}
	secret, err := (&service.ChannelService{}).Create(tenant.ID, &account, "channel-secret")
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	handlerCalls := 0
	engine.POST("/channels/:code/orders/create", ChannelAuthMiddleware(), func(ctx *gin.Context) {
		handlerCalls++
		ctx.JSON(http.StatusOK, gin.H{"ok": true})
	})
	timestamp := fmt.Sprint(time.Now().Unix())
	nonce := "nonce-1"
	requestID := "request-1"
	body := []byte(`{"product_id":1}`)
	request := func(payload []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/channels/travel-test/orders/create", bytes.NewReader(payload))
		req.Header.Set("X-Channel-Timestamp", timestamp)
		req.Header.Set("X-Channel-Nonce", nonce)
		req.Header.Set("X-Channel-Request-Id", requestID)
		req.Header.Set("X-Channel-Signature", channelSignature(secret, timestamp, nonce, "POST", "/channels/travel-test/orders/create", payload))
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, req)
		return resp
	}
	if resp := request(body); resp.Code != http.StatusOK {
		t.Fatalf("first channel request status=%d body=%s", resp.Code, resp.Body.String())
	}
	if resp := request(body); resp.Code != http.StatusOK || handlerCalls != 1 {
		t.Fatalf("replayed channel request status=%d calls=%d body=%s", resp.Code, handlerCalls, resp.Body.String())
	}
	if resp := request([]byte(`{"product_id":2}`)); resp.Code != http.StatusConflict {
		t.Fatalf("conflicting channel request status=%d body=%s", resp.Code, resp.Body.String())
	}
	requestID = "request-rate-limited"
	nonce = "nonce-rate-limited"
	if resp := request(body); resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "rate limit") {
		t.Fatalf("rate-limited channel request status=%d body=%s", resp.Code, resp.Body.String())
	}
	if err := db.Model(&account).Update("allowed_ips_json", `["203.0.113.9/32"]`).Error; err != nil {
		t.Fatal(err)
	}
	requestID = "request-ip-denied"
	nonce = "nonce-ip-denied"
	if resp := request(body); resp.Code != http.StatusForbidden {
		t.Fatalf("IP-denied channel request status=%d body=%s", resp.Code, resp.Body.String())
	}
	if err := db.Model(&account).Update("allowed_ips_json", "").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&account).Update("rate_limit_per_min", 100).Error; err != nil {
		t.Fatal(err)
	}
	staleLock := time.Now().Add(-10 * time.Minute)
	bodyHash := sha256.Sum256(body)
	if err := db.Create(&model.ChannelRequest{
		ChannelAccountID: account.ID, RequestID: "request-stale", Endpoint: "/channels/travel-test/orders/create",
		BodyHash: hex.EncodeToString(bodyHash[:]), Status: "processing", LockedAt: &staleLock,
	}).Error; err != nil {
		t.Fatal(err)
	}
	requestID = "request-stale"
	nonce = "nonce-stale"
	if resp := request(body); resp.Code != http.StatusOK || handlerCalls != 2 {
		t.Fatalf("stale channel request status=%d calls=%d body=%s", resp.Code, handlerCalls, resp.Body.String())
	}
	var completed model.ChannelRequest
	if err := db.Where("channel_account_id = ? AND request_id = ?", account.ID, requestID).First(&completed).Error; err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" || completed.LockedAt != nil {
		t.Fatalf("stale request state=%+v", completed)
	}
	if err := db.Model(&tenant).Update("status", "frozen").Error; err != nil {
		t.Fatal(err)
	}
	timestamp = fmt.Sprint(time.Now().Unix())
	nonce = "nonce-frozen"
	requestID = "request-frozen"
	if resp := request(body); resp.Code != http.StatusUnauthorized {
		t.Fatalf("frozen tenant channel status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestChannelPermissionCoversRefundEndpoint(t *testing.T) {
	if got := channelPermissionForPath("/api/v1/channels/demo/orders/refund"); got != "orders:refund" {
		t.Fatalf("refund permission=%q", got)
	}
	if !channelPermissionAllows(`["orders:refund"]`, "orders:refund") {
		t.Fatal("refund permission was not accepted")
	}
}

func channelSignature(secret, timestamp, nonce, method, path string, body []byte) string {
	hash := sha256.Sum256(body)
	canonical := strings.Join([]string{timestamp, nonce, method, path, hex.EncodeToString(hash[:])}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}
