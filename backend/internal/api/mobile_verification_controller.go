package api

import (
	"errors"
	"net/http"
	"strings"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

const mobileSessionHeader = "X-Mobile-Session"

type MobileVerificationController struct {
	Service *service.MobileVerificationService
}

type mobileSessionRequest struct {
	CheckPointID uint `json:"check_point_id" binding:"required"`
	DeviceID     uint `json:"device_id" binding:"required"`
}

type mobileVerifyRequest struct {
	TicketCode string `json:"ticket_code" binding:"required"`
	RequestID  string `json:"request_id" binding:"required"`
}

func NewMobileVerificationController(s *service.MobileVerificationService) *MobileVerificationController {
	return &MobileVerificationController{Service: s}
}

func (c *MobileVerificationController) Targets(ctx *gin.Context) {
	result, err := c.Service.Targets(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"))
	if err != nil {
		writeMobileError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *MobileVerificationController) CreateSession(ctx *gin.Context) {
	var req mobileSessionRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := c.Service.CreateSession(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), req.CheckPointID, req.DeviceID)
	if err != nil {
		writeMobileError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, result)
}

func (c *MobileVerificationController) Heartbeat(ctx *gin.Context) {
	if err := c.Service.Heartbeat(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), mobileSessionToken(ctx)); err != nil {
		writeMobileError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "active"})
}

func (c *MobileVerificationController) Close(ctx *gin.Context) {
	if err := c.Service.Close(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), mobileSessionToken(ctx)); err != nil {
		writeMobileError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "closed"})
}

func (c *MobileVerificationController) Verify(ctx *gin.Context) {
	var req mobileVerifyRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := c.Service.Verify(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), mobileSessionToken(ctx), req.TicketCode, req.RequestID)
	if err != nil {
		if errors.Is(err, service.ErrVerificationProcessing) {
			ctx.JSON(http.StatusConflict, gin.H{"error": "该扫码请求正在处理中，请稍后重试"})
			return
		}
		writeMobileError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func mobileSessionToken(ctx *gin.Context) string {
	return strings.TrimSpace(ctx.GetHeader(mobileSessionHeader))
}

func writeMobileError(ctx *gin.Context, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, service.ErrMobileSessionInvalid) {
		status = http.StatusUnauthorized
	} else if errors.Is(err, service.ErrMobileTargetDenied) {
		status = http.StatusForbidden
	}
	ctx.JSON(status, gin.H{"error": err.Error()})
}
