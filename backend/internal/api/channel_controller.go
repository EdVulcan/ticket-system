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
	Service  service.ChannelService
	Workflow service.ChannelWorkflowService
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
	var mapping model.ChannelProductMapping
	if err := model.DB.Where("channel_account_id = ? AND external_code = ? AND status = ?", ctx.GetUint("channel_account_id"), body.ExternalProductCode, "active").First(&mapping).Error; err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "external product is not mapped"})
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
	reservation, err := c.Workflow.Reserve(ctx.GetUint("tenant_id"), ctx.GetUint("channel_account_id"), ctx.GetString("channel_code"), mapping.ProductID, body.ExternalNo, body.Quantity, useDate, body.StockSlot, time.Duration(body.TTLSeconds)*time.Second)
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
	order, err := c.Workflow.Confirm(ctx.GetUint("tenant_id"), ctx.GetUint("channel_account_id"), ctx.GetString("channel_code"), body.ReservationID, body.ContactName, body.ContactPhone)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"order_no": order.OrderNo, "status": order.Status, "items": order.Items})
}

func (c *ChannelController) Release(ctx *gin.Context) {
	var body struct {
		ReservationID uint `json:"reservation_id" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Workflow.Release(ctx.GetUint("tenant_id"), ctx.GetUint("channel_account_id"), body.ReservationID, "channel request"); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "released"})
}
