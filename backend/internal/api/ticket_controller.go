package api

import (
	"net/http"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type TicketController struct {
	Service service.TicketService
}

type VerifyRequest struct {
	Code         string `json:"code" binding:"required"`
	CheckPointID uint   `json:"check_point_id" binding:"required"`
	DeviceID     uint   `json:"device_id"` // Optional
}

func (c *TicketController) Verify(ctx *gin.Context) {
	var req VerifyRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.Verify(req.Code, req.CheckPointID, req.DeviceID); err != nil {
		// Return 400 for business logic errors (invalid ticket, expired, etc.)
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "核销成功", "result": "success"})
}
