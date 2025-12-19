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

	// Mock TenantID
	// req.TenantID = ctx.GetUint("tenant_id")

	if err := c.Service.Create(&req); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, req)
}

func (c *OrderController) List(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))
	tenantID, _ := strconv.Atoi(ctx.DefaultQuery("tenant_id", "0"))
	status := ctx.DefaultQuery("status", "")

	orders, total, err := c.Service.List(page, pageSize, uint(tenantID), status)
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
