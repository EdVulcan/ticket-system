package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

type AfterSaleController struct {
	Service service.AfterSaleService
}

type afterSaleRequestBody struct {
	OrderNo         string   `json:"order_no" binding:"required"`
	Type            string   `json:"type" binding:"required"`
	IdempotencyKey  string   `json:"idempotency_key" binding:"required"`
	TicketCodes     []string `json:"ticket_codes"`
	Reason          string   `json:"reason"`
	TargetDate      string   `json:"target_date"`
	TargetSlot      string   `json:"target_slot"`
	TargetProductID uint     `json:"target_product_id"`
	AmountCents     int64    `json:"amount_cents"`
	PaymentMethod   string   `json:"payment_method"`
	DeviceID        uint     `json:"device_id"`
	ShiftID         uint     `json:"shift_id"`
}

func (c *AfterSaleController) Create(ctx *gin.Context) {
	var body afterSaleRequestBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req := model.AfterSaleRequest{
		TenantID: ctx.GetUint("tenant_id"), OrderNo: body.OrderNo, Type: body.Type,
		IdempotencyKey: body.IdempotencyKey, Reason: strings.TrimSpace(body.Reason),
		TargetSlot: body.TargetSlot, TargetProductID: body.TargetProductID, AmountCents: body.AmountCents,
		PaymentMethod: body.PaymentMethod, DeviceID: body.DeviceID, ShiftID: body.ShiftID,
		OperatorID: ctx.GetUint("user_id"),
	}
	if strings.TrimSpace(body.TargetDate) != "" {
		value, err := time.Parse("2006-01-02", body.TargetDate)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "target_date must use YYYY-MM-DD"})
			return
		}
		req.TargetDate = &value
	}
	if err := c.Service.Create(&req, body.TicketCodes); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrDuplicateExternalOrder) {
			status = http.StatusConflict
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, req)
}

func (c *AfterSaleController) List(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	rows, total, err := c.Service.List(ctx.GetUint("tenant_id"), ctx.Query("status"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page})
}

func (c *AfterSaleController) Approve(ctx *gin.Context) {
	c.transition(ctx, true)
}

func (c *AfterSaleController) Reject(ctx *gin.Context) {
	c.transition(ctx, false)
}

func (c *AfterSaleController) transition(ctx *gin.Context, approve bool) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid after-sale id"})
		return
	}
	var body struct {
		Reason                    string `json:"reason"`
		SettlementException       bool   `json:"settlement_exception"`
		SettlementExceptionReason string `json:"settlement_exception_reason"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil && err.Error() != "EOF" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var req *model.AfterSaleRequest
	if approve {
		req, err = c.Service.ApproveWithOptions(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), body.Reason, body.SettlementException, body.SettlementExceptionReason)
	} else {
		req, err = c.Service.Reject(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), body.Reason)
	}
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, req)
}

func (c *AfterSaleController) Execute(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid after-sale id"})
		return
	}
	req, err := c.Service.Execute(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), ctx.GetString("role"))
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	status := http.StatusOK
	if req.Status == "processing" {
		status = http.StatusAccepted
	}
	ctx.JSON(status, req)
}

func (c *AfterSaleController) CollectDifference(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid after-sale id"})
		return
	}
	var body struct {
		Method            string `json:"method" binding:"required"`
		PayType           string `json:"pay_type"`
		AuthCode          string `json:"auth_code"`
		ShiftID           uint   `json:"shift_id"`
		DeviceID          uint   `json:"device_id"`
		CashTenderedCents int64  `json:"cash_tendered_cents"`
		IdempotencyKey    string `json:"idempotency_key" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.DeviceID > 0 {
		if err := service.RequireStaffResource(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), "device", body.DeviceID); err != nil {
			ctx.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	payment := model.Payment{
		Method: body.Method, PayType: body.PayType, AuthCode: body.AuthCode,
		ShiftID: body.ShiftID, DeviceID: body.DeviceID, TenderedCents: body.CashTenderedCents,
		IdempotencyKey: body.IdempotencyKey,
	}
	if err := c.Service.CollectExchangeDifference(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), &payment); err != nil {
		status := http.StatusConflict
		if errors.Is(err, service.ErrCashTenderInsufficient) {
			status = http.StatusBadRequest
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	status := http.StatusCreated
	if payment.Status == "pending" {
		status = http.StatusAccepted
	}
	ctx.JSON(status, payment)
}

func (c *AfterSaleController) Get(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid after-sale id"})
		return
	}
	req, err := c.Service.Get(ctx.GetUint("tenant_id"), uint(id))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "after-sale request not found"})
		return
	}
	ctx.JSON(http.StatusOK, req)
}
