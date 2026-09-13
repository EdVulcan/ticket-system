package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"ticket-backend/internal/service"
)

func (c *RefundController) CheckUpstream(ctx *gin.Context) {
	var input struct {
		OrderNo string `json:"order_no" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	err := c.Service.CheckUpstreamRefund(ctx.Request.Context(), ctx.GetUint("tenant_id"), input.OrderNo)
	if errors.Is(err, service.ErrUpstreamRefundUsed) || errors.Is(err, service.ErrUpstreamRefundUnknown) {
		ctx.JSON(http.StatusOK, gin.H{"requires_confirmation": true, "message": "供应商已使用、不可退或暂时无法确认状态。继续将按管理员特殊退款处理，不代表旧系统票码已经失效。"})
		return
	}
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"requires_confirmation": false})
}
func (c *RefundController) ConfirmUpstream(ctx *gin.Context) {
	var input struct {
		RefundID uint   `json:"refund_id" binding:"required"`
		Reason   string `json:"reason" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.ConfirmUpstreamRefund(service.RefundActor{TenantID: ctx.GetUint("tenant_id"), UserID: ctx.GetUint("user_id")}, input.RefundID, input.Reason); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "pending"})
}

func (c *RefundController) RecoverUpstreamFunding(ctx *gin.Context) {
	var input struct {
		RefundID uint   `json:"refund_id" binding:"required"`
		Reason   string `json:"reason" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := c.Service.RecoverUpstreamFunding(service.RefundActor{TenantID: ctx.GetUint("tenant_id"), UserID: ctx.GetUint("user_id")}, input.RefundID, input.Reason)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusAccepted, result)
}

func (c *OrderController) Upstream(ctx *gin.Context) {
	rows, err := service.GetUpstreamOrderView(ctx.GetUint("tenant_id"), ctx.Param("orderNo"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}
func (c *OrderController) RefreshUpstream(ctx *gin.Context) {
	if err := service.RefreshUpstreamOrder(ctx.Request.Context(), ctx.GetUint("tenant_id"), ctx.Param("orderNo")); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Upstream(ctx)
}

func (c *OrderController) RecoverUpstreamIssuance(ctx *gin.Context) {
	var input struct {
		Reason          string `json:"reason" binding:"required"`
		ConfirmedAbsent bool   `json:"confirmed_no_order"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := (&service.UpstreamSupplyWorker{}).RecoverIssuance(ctx.Request.Context(), ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.Param("orderNo"), input.Reason, input.ConfirmedAbsent); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusAccepted, gin.H{"status": "pending"})
}
