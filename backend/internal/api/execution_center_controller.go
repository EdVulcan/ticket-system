package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type ExecutionCenterController struct {
	Service service.ExecutionCenterService
}

func (c *ExecutionCenterController) List(ctx *gin.Context) {
	limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "50"))
	view, err := c.Service.List(ctx.GetUint("tenant_id"), ctx.Query("category"), ctx.Query("severity"), limit)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, view)
}
