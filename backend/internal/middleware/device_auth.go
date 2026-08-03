package middleware

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/deviceauth"
	"ticket-backend/internal/model"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	deviceClockSkew = 5 * time.Minute
	deviceRateLimit = 600
	deviceBodyLimit = 64 << 10
)

// DeviceAuth authenticates a direct gate request. The raw device key never
// crosses the network; it only signs the exact method, path and request body.
func DeviceAuth() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		systemCode := strings.TrimSpace(ctx.GetHeader(deviceauth.HeaderSystemCode))
		serial := strings.TrimSpace(ctx.GetHeader(deviceauth.HeaderSerial))
		requestID := strings.TrimSpace(ctx.GetHeader(deviceauth.HeaderRequestID))
		timestamp := strings.TrimSpace(ctx.GetHeader(deviceauth.HeaderTimestamp))
		nonce := strings.TrimSpace(ctx.GetHeader(deviceauth.HeaderNonce))
		signature := strings.TrimSpace(ctx.GetHeader(deviceauth.HeaderSignature))
		if systemCode == "" || serial == "" || requestID == "" || timestamp == "" || nonce == "" || signature == "" || len(requestID) > 100 || len(nonce) > 100 {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "设备认证信息不完整"})
			return
		}
		unixTime, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil || time.Since(time.Unix(unixTime, 0)) > deviceClockSkew || time.Until(time.Unix(unixTime, 0)) > deviceClockSkew {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "设备时间戳无效或已过期"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(ctx.Request.Body, deviceBodyLimit+1))
		if err != nil || len(body) > deviceBodyLimit {
			ctx.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "设备请求正文过大"})
			return
		}
		ctx.Request.Body = io.NopCloser(bytes.NewReader(body))

		var tenant model.Tenant
		if err := model.DB.Where("system_code = ? AND status = ?", systemCode, "active").First(&tenant).Error; err != nil {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "设备所属商户不可用"})
			return
		}
		var capability model.TenantCapability
		if err := model.DB.Where("tenant_id = ? AND capability = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", tenant.ID, "supplier", "active", time.Now()).First(&capability).Error; err != nil {
			ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "景区供应能力不可用"})
			return
		}
		var device model.Device
		if err := model.DB.Where("tenant_id = ? AND serial_number = ?", tenant.ID, serial).First(&device).Error; err != nil {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "设备未登记"})
			return
		}
		key, err := deviceauth.DecodeStoredKey(device.AuthKeyHash)
		if err != nil || !deviceauth.Verify(key, signature, ctx.Request.Method, ctx.Request.URL.Path, timestamp, nonce, requestID, body) {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "设备签名无效"})
			return
		}

		now := time.Now()
		if err := model.Write(func(tx *gorm.DB) error {
			if err := tx.Where("device_id = ? AND expires_at < ?", device.ID, now).Delete(&model.DeviceRequestNonce{}).Error; err != nil {
				return err
			}
			var recent int64
			if err := tx.Model(&model.DeviceRequestNonce{}).Where("device_id = ? AND created_at >= ?", device.ID, now.Add(-time.Minute)).Count(&recent).Error; err != nil {
				return err
			}
			if recent >= deviceRateLimit {
				return errors.New("设备请求过于频繁")
			}
			return tx.Create(&model.DeviceRequestNonce{TenantID: tenant.ID, DeviceID: device.ID, Nonce: nonce, RequestID: requestID, Path: ctx.Request.URL.Path, ExpiresAt: now.Add(deviceClockSkew)}).Error
		}); err != nil {
			status := http.StatusConflict
			if strings.Contains(err.Error(), "过于频繁") {
				status = http.StatusTooManyRequests
			}
			ctx.AbortWithStatusJSON(status, gin.H{"error": "请求已处理或频率过高"})
			return
		}

		ctx.Set("tenant_id", tenant.ID)
		ctx.Set("device_id", device.ID)
		ctx.Set("scenic_area_id", device.ScenicAreaID)
		ctx.Set("check_point_id", uint(0))
		if device.CheckPointID != nil {
			ctx.Set("check_point_id", *device.CheckPointID)
		}
		ctx.Set("device_status", device.Status)
		ctx.Set("device_request_id", requestID)
		ctx.Set("device_request_hash", deviceauth.HashBody(body))
		ctx.Next()
	}
}
