package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"time"

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

func (c *DeviceController) PollCommand(ctx *gin.Context) {
	var req struct {
		SystemCode   string `json:"system_code" binding:"required"`
		SerialNumber string `json:"serial_number" binding:"required"`
		DeviceKey    string `json:"device_key" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	command, err := c.Service.PollHardwareCommand(req.SystemCode, req.SerialNumber, req.DeviceKey)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	if command == nil {
		ctx.JSON(http.StatusOK, gin.H{"command": nil})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"command": command, "ack_token": command.AckToken})
}

func (c *DeviceController) AckCommand(ctx *gin.Context) {
	var req service.HardwareAckRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.AckHardwareCommand(req); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": req.Status})
}

func (c *DeviceController) QueueCommand(ctx *gin.Context) {
	var body struct {
		DeviceID    uint   `json:"device_id" binding:"required"`
		Kind        string `json:"kind" binding:"required"`
		PayloadJSON string `json:"payload_json"`
		TTLSeconds  int    `json:"ttl_seconds"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	command, err := c.Service.QueueHardwareCommand(service.HardwareCommandRequest{TenantID: ctx.GetUint("tenant_id"), DeviceID: body.DeviceID, Kind: body.Kind, PayloadJSON: body.PayloadJSON, TTL: time.Duration(body.TTLSeconds) * time.Second})
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusAccepted, command)
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

	if err := c.Service.Create(&device, device.TenantID); err != nil {
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

	if err := c.Service.Update(uint(id), ctx.GetUint("tenant_id"), &device); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "updated successfully"})
}

func (c *DeviceController) Delete(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	if err := c.Service.Delete(uint(id), ctx.GetUint("tenant_id")); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "deleted successfully"})
}

func (c *DeviceController) List(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))
	devices, total, err := c.Service.List(page, pageSize, ctx.GetUint("tenant_id"))
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

func (c *DeviceController) RotateKey(ctx *gin.Context) {
	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil || id <= 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid device id"})
		return
	}
	key, err := c.Service.RotateKey(uint(id), ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "device not found"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"auth_key": key})
}
