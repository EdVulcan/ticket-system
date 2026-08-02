package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type PaymentController struct {
	Service service.PaymentService
}

type RefundController struct {
	Service service.RefundService
}

func (c *PaymentController) Pay(ctx *gin.Context) {
	var body struct {
		OrderNo           string `json:"order_no" binding:"required"`
		Method            string `json:"method" binding:"required"`
		PayType           string `json:"pay_type"`
		AuthCode          string `json:"auth_code"`
		ShiftID           uint   `json:"shift_id"`
		DeviceID          uint   `json:"device_id"`
		AmountCents       int64  `json:"amount_cents"`
		CashTenderedCents int64  `json:"cash_tendered_cents"`
		IdempotencyKey    string `json:"idempotency_key"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var order model.Order
	if err := model.DB.Where("order_no = ? AND tenant_id = ?", body.OrderNo, ctx.GetUint("tenant_id")).First(&order).Error; err == nil && order.Channel == "window" {
		if err := service.RequireStaffResource(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), "device", body.DeviceID); err != nil {
			ctx.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	req := model.Payment{OrderNo: body.OrderNo, Method: body.Method, PayType: body.PayType, AuthCode: body.AuthCode, ShiftID: body.ShiftID, DeviceID: body.DeviceID, OperatorID: ctx.GetUint("user_id"), AmountCents: body.AmountCents, TenderedCents: body.CashTenderedCents, IdempotencyKey: body.IdempotencyKey}

	tenantID := ctx.GetUint("tenant_id")
	if err := c.Service.CreatePayment(tenantID, &req); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrCashTenderInsufficient) {
			status = http.StatusBadRequest
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, req)
}

func (c *PaymentController) OrderProgress(ctx *gin.Context) {
	progress, err := c.Service.GetOrderPaymentProgress(ctx.GetUint("tenant_id"), ctx.Param("orderNo"))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, progress)
}

func (c *PaymentController) CancelPartialCash(ctx *gin.Context) {
	var body struct {
		ShiftID  uint   `json:"shift_id" binding:"required"`
		DeviceID uint   `json:"device_id" binding:"required"`
		Reason   string `json:"reason" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.RequireStaffResource(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), "device", body.DeviceID); err != nil {
		ctx.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.CancelPartialCashPayment(ctx.GetUint("tenant_id"), ctx.Param("orderNo"), body.ShiftID, body.DeviceID, ctx.GetUint("user_id"), ctx.GetString("role"), body.Reason); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.Status(http.StatusNoContent)
}

func (c *PaymentController) Query(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	payment, err := c.Service.GetStatus(uint(id), ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "Payment not found"})
		return
	}
	ctx.JSON(http.StatusOK, payment)
}

func (c *PaymentController) WeChatNotify(ctx *gin.Context) {
	tenantID, err := strconv.ParseUint(ctx.Param("tenantID"), 10, 32)
	if err != nil || tenantID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": "invalid tenant"})
		return
	}
	if err := c.Service.HandleWeChatNotify(uint(tenantID), ctx.Request); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "成功"})
}

func (c *PaymentController) AlipayNotify(ctx *gin.Context) {
	tenantID, err := strconv.ParseUint(ctx.Param("tenantID"), 10, 32)
	if err != nil || tenantID == 0 {
		ctx.String(http.StatusBadRequest, "fail")
		return
	}
	if err := c.Service.HandleAlipayNotify(uint(tenantID), ctx.Request); err != nil {
		ctx.String(http.StatusBadRequest, fmt.Sprintf("fail: %v", err))
		return
	}
	ctx.String(http.StatusOK, "success")
}

func (c *RefundController) CreateCash(ctx *gin.Context) {
	var body struct {
		OrderNo        string   `json:"order_no" binding:"required"`
		IdempotencyKey string   `json:"idempotency_key" binding:"required"`
		Amount         float64  `json:"amount" binding:"required"`
		TicketCodes    []string `json:"ticket_codes" binding:"required"`
		Reason         string   `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	refund, err := c.Service.CreateCashRefundAs(service.RefundActor{TenantID: ctx.GetUint("tenant_id"), UserID: ctx.GetUint("user_id")}, body.OrderNo, body.IdempotencyKey, body.Amount, body.TicketCodes, body.Reason)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrDigitalRefundNotConfigured) {
			status = http.StatusNotImplemented
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if err := service.RecordAudit(ctx.GetUint("user_id"), ctx.GetUint("tenant_id"), ctx.GetString("role"), "tenant", "payment.refund.cash", "refund", refund.ID, body.Reason, "", `{"refund_no":"`+refund.RefundNo+`","amount":`+fmt.Sprintf("%.2f", refund.Amount)+`}`); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "refund completed but audit logging failed"})
		return
	}
	ctx.JSON(http.StatusCreated, refund)
}

func (c *RefundController) CreateMixed(ctx *gin.Context) {
	var body struct {
		OrderNo        string   `json:"order_no" binding:"required"`
		IdempotencyKey string   `json:"idempotency_key" binding:"required"`
		Amount         float64  `json:"amount" binding:"required"`
		TicketCodes    []string `json:"ticket_codes" binding:"required"`
		Reason         string   `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	refund, err := c.Service.CreateMixedRefundAs(service.RefundActor{TenantID: ctx.GetUint("tenant_id"), UserID: ctx.GetUint("user_id")}, body.OrderNo, body.IdempotencyKey, body.Amount, body.TicketCodes, body.Reason)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	status := http.StatusCreated
	if refund.Status == "group_pending" {
		status = http.StatusAccepted
	}
	ctx.JSON(status, refund)
}

func (c *RefundController) GetGroup(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid refund id"})
		return
	}
	group, err := c.Service.GetRefundGroup(ctx.GetUint("tenant_id"), uint(id))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "refund not found"})
		return
	}
	ctx.JSON(http.StatusOK, group)
}

func (c *RefundController) CreateDigital(ctx *gin.Context) {
	var body struct {
		OrderNo        string   `json:"order_no" binding:"required"`
		IdempotencyKey string   `json:"idempotency_key" binding:"required"`
		Amount         float64  `json:"amount" binding:"required"`
		TicketCodes    []string `json:"ticket_codes" binding:"required"`
		Reason         string   `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	refund, err := c.Service.CreateDigitalRefundAs(service.RefundActor{TenantID: ctx.GetUint("tenant_id"), UserID: ctx.GetUint("user_id")}, body.OrderNo, body.IdempotencyKey, body.Amount, body.TicketCodes, body.Reason)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusAccepted, gin.H{"refund": refund, "message": "refund request recorded; provider confirmation pending"})
}

func (c *RefundController) ListDigitalTasks(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	tasks, total, err := c.Service.ListDigitalRefundTasks(ctx.GetUint("tenant_id"), ctx.Query("status"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": tasks, "total": total, "page": page, "page_size": pageSize})
}

func (c *RefundController) RetryDigitalTask(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid refund task id"})
		return
	}
	var body struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.RetryDigitalRefundTask(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), ctx.GetString("role"), body.Reason); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusAccepted, gin.H{"status": "pending"})
}
