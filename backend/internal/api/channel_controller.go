package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

type ChannelController struct {
	Service service.ChannelService
	Gateway *service.ChannelGatewayService
}

func (c *ChannelController) List(ctx *gin.Context) {
	rows, err := c.Service.List(ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *ChannelController) Create(ctx *gin.Context) {
	var body struct {
		model.ChannelAccount
		Secret string `json:"secret"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	secret, err := c.Service.Create(ctx.GetUint("tenant_id"), &body.ChannelAccount, body.Secret)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	body.Secret = secret
	body.SecretCiphertext = ""
	body.VerifyKeyCiphertext = ""
	ctx.JSON(http.StatusCreated, body)
}

func (c *ChannelController) SetStatus(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.SetStatus(ctx.GetUint("tenant_id"), uint(id), body.Status); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": body.Status})
}

func (c *ChannelController) RotateSecret(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	secret, err := c.Service.RotateSecret(ctx.GetUint("tenant_id"), uint(id))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "channel not found"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"secret": secret})
}

func (c *ChannelController) AddMapping(ctx *gin.Context) {
	var mapping model.ChannelProductMapping
	if err := ctx.ShouldBindJSON(&mapping); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.AddMapping(ctx.GetUint("tenant_id"), &mapping); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, mapping)
}

func (c *ChannelController) ListMappings(ctx *gin.Context) {
	accountID, _ := strconv.ParseUint(ctx.Query("channel_account_id"), 10, 32)
	rows, err := c.Service.ListMappings(ctx.GetUint("tenant_id"), uint(accountID))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *ChannelController) ListRequests(ctx *gin.Context) {
	accountID, _ := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if accountID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	rows, total, err := c.Service.ListRequests(ctx.GetUint("tenant_id"), uint(accountID), ctx.Query("status"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *ChannelController) AuthorizeRequestRetry(ctx *gin.Context) {
	accountID, accountErr := strconv.ParseUint(ctx.Param("id"), 10, 32)
	requestID, requestErr := strconv.ParseUint(ctx.Param("requestId"), 10, 32)
	if accountErr != nil || requestErr != nil || accountID == 0 || requestID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel or request id"})
		return
	}
	var body struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.AuthorizeRequestRetry(ctx.GetUint("tenant_id"), uint(accountID), uint(requestID), ctx.GetUint("user_id"), ctx.GetString("role"), body.Reason); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "retryable"})
}

func (c *ChannelController) ImportBill(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	var body struct {
		IdempotencyKey string                     `json:"idempotency_key" binding:"required"`
		Records        []service.ChannelBillInput `json:"records" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	report, err := c.Service.ImportBill(ctx.GetUint("tenant_id"), uint(id), body.IdempotencyKey, body.Records)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, report)
}

func (c *ChannelController) ListReconciliations(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	rows, total, err := c.Service.ListReconciliations(ctx.GetUint("tenant_id"), uint(id), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *ChannelController) GetReconciliation(ctx *gin.Context) {
	accountID, accountErr := strconv.ParseUint(ctx.Param("id"), 10, 32)
	reconciliationID, reconciliationErr := strconv.ParseUint(ctx.Param("reconciliationId"), 10, 32)
	if accountErr != nil || reconciliationErr != nil || accountID == 0 || reconciliationID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel or reconciliation id"})
		return
	}
	row, err := c.Service.GetReconciliation(ctx.GetUint("tenant_id"), uint(accountID), uint(reconciliationID))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "channel reconciliation not found"})
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *ChannelController) Reserve(ctx *gin.Context) {
	var body struct {
		ExternalProductCode string `json:"external_product_code" binding:"required"`
		ExternalNo          string `json:"external_no" binding:"required"`
		Quantity            int    `json:"quantity" binding:"required"`
		Date                string `json:"date"`
		StockSlot           string `json:"stock_slot"`
		TTLSeconds          int    `json:"ttl_seconds"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var useDate *time.Time
	if body.Date != "" {
		value, err := time.ParseInLocation("2006-01-02", body.Date, time.Local)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid visit date"})
			return
		}
		useDate = &value
	}
	if c.Gateway == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "channel gateway is not configured"})
		return
	}
	reservation, err := c.Gateway.CreateReservation(ctx, ctx.GetString("channel_type"), service.ChannelReservationRequest{
		TenantID: ctx.GetUint("tenant_id"), AccountID: ctx.GetUint("channel_account_id"), Channel: ctx.GetString("channel_code"),
		ExternalProductCode: body.ExternalProductCode, ExternalNo: body.ExternalNo, Quantity: body.Quantity,
		UseDate: useDate, StockSlot: body.StockSlot, TTL: time.Duration(body.TTLSeconds) * time.Second,
	})
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, reservation)
}

func (c *ChannelController) Confirm(ctx *gin.Context) {
	var body struct {
		ReservationID uint   `json:"reservation_id" binding:"required"`
		ContactName   string `json:"contact_name"`
		ContactPhone  string `json:"contact_phone"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if c.Gateway == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "channel gateway is not configured"})
		return
	}
	order, err := c.Gateway.ConfirmOrder(ctx, ctx.GetString("channel_type"), service.ChannelConfirmRequest{
		TenantID: ctx.GetUint("tenant_id"), AccountID: ctx.GetUint("channel_account_id"), Channel: ctx.GetString("channel_code"),
		ReservationID: body.ReservationID, ContactName: body.ContactName, ContactPhone: body.ContactPhone,
	})
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"order_no": order.Order.OrderNo, "status": order.Status, "items": order.Order.Items, "ticket_codes": order.TicketCodes})
}

func (c *ChannelController) Release(ctx *gin.Context) {
	var body struct {
		ReservationID uint `json:"reservation_id" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if c.Gateway == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "channel gateway is not configured"})
		return
	}
	if err := c.Gateway.ReleaseReservation(ctx, ctx.GetString("channel_type"), service.ChannelReleaseRequest{
		TenantID: ctx.GetUint("tenant_id"), AccountID: ctx.GetUint("channel_account_id"), Channel: ctx.GetString("channel_code"),
		ReservationID: body.ReservationID, Reason: "channel request",
	}); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "released"})
}
