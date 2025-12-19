package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type DeviceController struct {
	Service service.DeviceService
}

func (c *DeviceController) Create(ctx *gin.Context) {
	var device model.Device
	if err := ctx.ShouldBindJSON(&device); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.Create(&device); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, device)
}

func (c *DeviceController) Update(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	var device model.Device
	if err := ctx.ShouldBindJSON(&device); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.Update(uint(id), &device); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "updated successfully"})
}

func (c *DeviceController) Delete(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	if err := c.Service.Delete(uint(id)); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "deleted successfully"})
}

func (c *DeviceController) List(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))
	tenantID, _ := strconv.Atoi(ctx.DefaultQuery("tenant_id", "0"))

	devices, total, err := c.Service.List(page, pageSize, uint(tenantID))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"data":  devices,
		"total": total,
		"page":  page,
	})
}
