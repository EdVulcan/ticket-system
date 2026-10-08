package api

import (
	"net/http"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

// XiaohongshuSupplierVerificationController provides the narrow, audited
// tenant-admin route for resolving an uncertain supplier-side consume report.
type XiaohongshuSupplierVerificationController struct{}

func (c *XiaohongshuSupplierVerificationController) Resolve(ctx *gin.Context) {
	taskID, ok := positiveID(ctx.Param("id"))
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid supplier verification id"})
		return
	}
	var body struct {
		Decision         string `json:"decision" binding:"required"`
		Reason           string `json:"reason" binding:"required"`
		Evidence         string `json:"evidence" binding:"required"`
		ExternalVerifyID string `json:"external_verify_id"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "小红书供应商核销处置请求格式错误"})
		return
	}
	result, err := service.ResolveXiaohongshuSupplierVerification(service.XiaohongshuSupplierVerificationResolutionRequest{
		TenantID: ctx.GetUint("tenant_id"), TaskID: taskID, ActorUserID: ctx.GetUint("user_id"), ActorRole: ctx.GetString("role"),
		Decision: body.Decision, Reason: body.Reason, Evidence: body.Evidence, ExternalVerifyID: body.ExternalVerifyID,
	})
	if err != nil {
		xiaohongshuVoucherResolutionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}
