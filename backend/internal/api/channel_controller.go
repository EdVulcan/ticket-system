package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type ChannelController struct{ Service service.ChannelService }

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
