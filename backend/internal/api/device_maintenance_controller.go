package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/config"
	"ticket-backend/internal/gatetunnel"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type DeviceMaintenanceController struct {
	Service *service.DeviceMaintenanceService
}

func NewDeviceMaintenanceController(s *service.DeviceMaintenanceService) *DeviceMaintenanceController {
	return &DeviceMaintenanceController{Service: s}
}

func (c *DeviceMaintenanceController) RotateCredential(ctx *gin.Context) {
	deviceID, ok := positiveID(ctx.Param("id"))
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid device id"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := c.Service.RotateCredential(ctx.GetUint("tenant_id"), deviceID, ctx.GetUint("user_id"), body.Reason)
	if err != nil {
		maintenanceError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, gin.H{
		"credential": result.Credential,
		"secret":     result.Secret,
		"warning":    "密钥只显示这一次，请保存到闸机现场配置；它与设备核销密钥不同",
	})
}

func (c *DeviceMaintenanceController) CredentialStatus(ctx *gin.Context) {
	deviceID, ok := positiveID(ctx.Param("id"))
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid device id"})
		return
	}
	credential, err := c.Service.CredentialStatus(ctx.GetUint("tenant_id"), deviceID)
	if err != nil {
		maintenanceError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, credential)
}

func (c *DeviceMaintenanceController) RevokeCredential(ctx *gin.Context) {
	deviceID, ok := positiveID(ctx.Param("id"))
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid device id"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.RevokeCredential(ctx.GetUint("tenant_id"), deviceID, ctx.GetUint("user_id"), body.Reason); err != nil {
		maintenanceError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "revoked"})
}

func (c *DeviceMaintenanceController) CreateSession(ctx *gin.Context) {
	deviceID, ok := positiveID(ctx.Param("id"))
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid device id"})
		return
	}
	var body struct {
		Reason     string `json:"reason"`
		TTLSeconds int    `json:"ttl_seconds"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := c.Service.CreateSession(service.MaintenanceSessionRequest{
		TenantID: ctx.GetUint("tenant_id"), DeviceID: deviceID, ActorUserID: ctx.GetUint("user_id"),
		Reason: body.Reason, TTL: durationSeconds(body.TTLSeconds),
	})
	if err != nil {
		maintenanceError(ctx, err)
		return
	}
	base := strings.TrimRight(strings.TrimSpace(config.GlobalConfig.Server.PublicBaseURL), "/")
	path := "/api/v1/hardware/maintenance/sessions/" + result.SessionID + "/ws"
	wsURL := path
	if base != "" {
		baseURL := base
		switch {
		case strings.HasPrefix(strings.ToLower(baseURL), "https://"):
			baseURL = "wss://" + baseURL[len("https://"):]
		case strings.HasPrefix(strings.ToLower(baseURL), "http://"):
			baseURL = "ws://" + baseURL[len("http://"):]
		}
		wsURL = strings.TrimRight(baseURL, "/") + path
	}
	ctx.JSON(http.StatusCreated, gin.H{
		"session":       result.Session,
		"session_id":    result.SessionID,
		"session_token": result.SessionToken,
		"websocket_url": wsURL,
		"ssh_target":    "127.0.0.1:22",
		"warning":       "仅允许闸机本机 127.0.0.1:22；令牌只显示这一次，过期或关闭后立即失效",
	})
}

func (c *DeviceMaintenanceController) ListSessions(ctx *gin.Context) {
	deviceID := uint(0)
	if raw := strings.TrimSpace(ctx.Param("id")); raw != "" {
		parsed, ok := positiveID(raw)
		if !ok {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid device id"})
			return
		}
		deviceID = parsed
	}
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	rows, total, err := c.Service.ListSessions(ctx.GetUint("tenant_id"), deviceID, page, pageSize)
	if err != nil {
		maintenanceError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *DeviceMaintenanceController) CloseSession(ctx *gin.Context) {
	sessionID, ok := positiveID(ctx.Param("sessionID"))
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return
	}
	deviceID, ok := positiveID(ctx.Param("id"))
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid device id"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.CloseSession(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), deviceID, sessionID, body.Reason); err != nil {
		maintenanceError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "closed"})
}

func durationSeconds(value int) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Second
}

func positiveID(value string) (uint, bool) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
	return uint(parsed), err == nil && parsed > 0
}

func maintenanceError(ctx *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, service.ErrMaintenanceReasonRequired):
		status = http.StatusBadRequest
	case errors.Is(err, service.ErrMaintenanceNotConfigured):
		status = http.StatusNotImplemented
	case errors.Is(err, service.ErrMaintenanceCredential), errors.Is(err, gatetunnel.ErrSessionNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		status = http.StatusNotFound
	case errors.Is(err, gatetunnel.ErrDeviceOffline), errors.Is(err, gatetunnel.ErrSessionAlreadyOpen), errors.Is(err, gatetunnel.ErrSessionExpired):
		status = http.StatusConflict
	}
	ctx.JSON(status, gin.H{"error": err.Error()})
}
