package api

import (
	"errors"
	"net/http"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type PlatformAIController struct {
	Service service.PlatformAIService
}

func (c *PlatformAIController) GetConfig(ctx *gin.Context) {
	config, err := c.Service.GetConfig()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, config)
}

func (c *PlatformAIController) SaveConfig(ctx *gin.Context) {
	var input service.PlatformAIConfigInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	config, err := c.Service.SaveConfig(input, ctx.GetUint("platform_user_id"), ctx.GetString("role"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, config)
}

func (c *PlatformAIController) TestConfig(ctx *gin.Context) {
	var input service.PlatformAIConfigInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.TestConfig(ctx.Request.Context(), input); err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, service.ErrAIUnavailable) {
			status = http.StatusServiceUnavailable
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
}
