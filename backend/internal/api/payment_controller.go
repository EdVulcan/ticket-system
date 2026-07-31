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
		OrderNo  string `json:"order_no" binding:"required"`
		Method   string `json:"method" binding:"required"`
		PayType  string `json:"pay_type"`
		AuthCode string `json:"auth_code"`
		ShiftID  uint   `json:"shift_id"`
		DeviceID uint   `json:"device_id"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req := model.Payment{OrderNo: body.OrderNo, Method: body.Method, PayType: body.PayType, AuthCode: body.AuthCode, ShiftID: body.ShiftID, DeviceID: body.DeviceID, OperatorID: ctx.GetUint("user_id")}

	tenantID := ctx.GetUint("tenant_id")
	if err := c.Service.CreatePayment(tenantID, &req); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, req)
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
	refund, err := c.Service.CreateCashRefund(ctx.GetUint("tenant_id"), body.OrderNo, body.IdempotencyKey, body.Amount, body.TicketCodes, body.Reason)
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
	refund, err := c.Service.CreateDigitalRefund(ctx.GetUint("tenant_id"), body.OrderNo, body.IdempotencyKey, body.Amount, body.TicketCodes, body.Reason)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusAccepted, gin.H{"refund": refund, "message": "refund request recorded; provider confirmation pending"})
}
