package api

import (
	"errors"
	"net/http"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// XiaohongshuVoucherVerificationController exposes a deliberately narrow
// tenant-scoped manual exit for a provider verification whose result is
// unknown. The client supplies only the decision and evidence; the service
// derives every fulfillment identity from the durable coordinator record.
type XiaohongshuVoucherVerificationController struct {
	Service *service.DeviceService
}

func NewXiaohongshuVoucherVerificationController(s *service.DeviceService) *XiaohongshuVoucherVerificationController {
	return &XiaohongshuVoucherVerificationController{Service: s}
}

func (c *XiaohongshuVoucherVerificationController) Resolve(ctx *gin.Context) {
	sagaID, ok := positiveID(ctx.Param("id"))
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid voucher verification id"})
		return
	}
	var body struct {
		Decision         string `json:"decision" binding:"required"`
		Reason           string `json:"reason" binding:"required"`
		Evidence         string `json:"evidence" binding:"required"`
		ExternalVerifyID string `json:"external_verify_id"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "小红书券人工处理请求格式错误"})
		return
	}
	result, err := c.Service.ResolveXiaohongshuVoucherVerification(service.XiaohongshuVoucherVerificationResolutionRequest{
		TenantID: ctx.GetUint("tenant_id"), SagaID: sagaID, ActorUserID: ctx.GetUint("user_id"), ActorRole: ctx.GetString("role"),
		Decision: body.Decision, Reason: body.Reason, Evidence: body.Evidence, ExternalVerifyID: body.ExternalVerifyID,
	})
	if err != nil {
		xiaohongshuVoucherResolutionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func xiaohongshuVoucherResolutionError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrXiaohongshuVoucherResolutionPermission):
		ctx.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrXiaohongshuVoucherResolutionInvalid):
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrXiaohongshuVoucherResolutionNotResolvable):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		ctx.JSON(http.StatusNotFound, gin.H{"error": "小红书券核销记录不存在"})
	default:
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "小红书券人工处理失败，请稍后重试"})
	}
}
