package api

import (
	"errors"
	"net/http"
	"strings"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// DeviceProvisioningController exposes the one-time installer handshake. The
// claim endpoint is intentionally public because a fresh Linux controller has
// no device credentials yet; all ownership is selected from the server-side
// activation lease.
type DeviceProvisioningController struct {
	Service *service.DeviceProvisioningService
}

func NewDeviceProvisioningController(s *service.DeviceProvisioningService) *DeviceProvisioningController {
	return &DeviceProvisioningController{Service: s}
}

func (c *DeviceProvisioningController) CreateLease(ctx *gin.Context) {
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
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "安装绑定请求格式错误"})
		return
	}
	result, err := c.Service.CreateLease(service.ProvisioningLeaseRequest{
		TenantID: ctx.GetUint("tenant_id"), DeviceID: deviceID, ActorUserID: ctx.GetUint("user_id"),
		Reason: body.Reason, TTL: durationSeconds(body.TTLSeconds),
	})
	if err != nil {
		provisioningError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, gin.H{
		"lease_id":        result.Lease.ID,
		"activation_code": result.ActivationCode,
		"expires_at":      result.Lease.ExpiresAt,
		"status":          result.Lease.Status,
		"warning":         "绑定码只显示这一次，请在闸机安装器中通过 HTTPS 输入；不要放入 URL、命令行、环境变量、截图或日志",
	})
}

func (c *DeviceProvisioningController) Claim(ctx *gin.Context) {
	var body service.ProvisioningClaimRequest
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "安装绑定请求格式错误"})
		return
	}
	result, err := c.Service.Claim(body)
	if err != nil {
		provisioningError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *DeviceProvisioningController) Confirm(ctx *gin.Context) {
	var body service.ProvisioningConfirmRequest
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "安装确认请求格式错误"})
		return
	}
	if err := c.Service.Confirm(ctx.GetUint("tenant_id"), ctx.GetUint("device_id"), body); err != nil {
		provisioningError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "completed"})
}

func (c *DeviceProvisioningController) RevokeLease(ctx *gin.Context) {
	deviceID, ok := positiveID(ctx.Param("id"))
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid device id"})
		return
	}
	leaseID, ok := positiveID(ctx.Param("leaseID"))
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid provisioning lease id"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "安装绑定请求格式错误"})
		return
	}
	if err := c.Service.RevokeLease(ctx.GetUint("tenant_id"), deviceID, leaseID, ctx.GetUint("user_id"), body.Reason); err != nil {
		provisioningError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "revoked"})
}

func provisioningError(ctx *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "安装绑定失败，请稍后重试"
	switch {
	case errors.Is(err, service.ErrProvisioningLeaseInvalid), errors.Is(err, gorm.ErrRecordNotFound):
		status = http.StatusBadRequest
		message = "安装绑定码无效或已失效"
	case errors.Is(err, service.ErrProvisioningDeviceOnline), errors.Is(err, service.ErrProvisioningLeaseExists):
		status = http.StatusConflict
		message = err.Error()
	case errors.Is(err, service.ErrProvisioningNotReady):
		status = http.StatusServiceUnavailable
		message = "安装绑定服务尚未配置 HTTPS 公网地址"
	case errors.Is(err, service.ErrProvisioningReasonRequired):
		status = http.StatusBadRequest
		message = err.Error()
	case strings.Contains(err.Error(), "required"):
		status = http.StatusBadRequest
		message = "安装绑定参数不完整"
	}
	ctx.JSON(status, gin.H{"error": message})
}
