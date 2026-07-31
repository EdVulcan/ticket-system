package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type OrderController struct {
	Service service.OrderService
}

func (c *OrderController) Create(ctx *gin.Context) {
	var req model.Order
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Assign TenantID from context
	tenantID, _ := ctx.Get("tenant_id")
	req.TenantID = tenantID.(uint)
	req.Channel = "window"
	req.ExternalNo = nil

	if err := c.Service.Create(&req); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, req)
}

func (c *OrderController) List(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))
	status := ctx.DefaultQuery("status", "")
	channel := ctx.DefaultQuery("channel", "")
	startDate := ctx.DefaultQuery("start_date", "")
	endDate := ctx.DefaultQuery("end_date", "")
	search := ctx.DefaultQuery("search", "")

	orders, total, err := c.Service.List(page, pageSize, ctx.GetUint("tenant_id"), status, channel, startDate, endDate, search)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"data":  orders,
		"total": total,
		"page":  page,
	})
}

func (c *OrderController) Cancel(ctx *gin.Context) {
	if err := c.Service.Cancel(ctx.Param("orderNo"), ctx.GetUint("tenant_id")); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}
