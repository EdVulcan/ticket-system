package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type DeviceController struct {
	Service *service.DeviceService
}

func NewDeviceController(s *service.DeviceService) *DeviceController {
	return &DeviceController{Service: s}
}

func (c *DeviceController) Heartbeat(ctx *gin.Context) {
	var req service.HeartbeatRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.Heartbeat(req); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()}) // Or 404 if not found? Service returns generic error.
		// Proposal says: return {command: "none"}. Even if error? Maybe log error but return success to keep device happy?
		// But checking if device registered is part of security. If not registered, return error.
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"command": "none"})
}

func (c *DeviceController) Verify(ctx *gin.Context) {
	var req service.VerifyRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := c.Service.Verify(req)
	if err != nil {
		// If service error (DB error), 500.
		// If deny (logic), Verify returns resp with 403 but err=nil?
		// My Service implementation returns resp, err. If err != nil it means DB error mostly.
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Returns the response structure directly (VerifyResponse)
	ctx.JSON(http.StatusOK, resp)
}

// --- CRUD Methods ---

func (c *DeviceController) Create(ctx *gin.Context) {
	var device model.Device
	if err := ctx.ShouldBindJSON(&device); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set TenantID from context
	if tid, exists := ctx.Get("tenant_id"); exists {
		device.TenantID = tid.(uint)
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
