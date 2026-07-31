package api

import (
	"net/http"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type PlatformController struct{ Service service.PlatformService }

func (c *PlatformController) Overview(ctx *gin.Context) {
	result, err := c.Service.Overview()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := service.RecordAudit(platformActorID(ctx), 0, ctx.GetString("role"), "platform", "platform.overview.read", "platform", 0, "global operational overview", "", ""); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "overview loaded but audit logging failed"})
		return
	}
	ctx.JSON(http.StatusOK, result)
}
