package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type AgentAliasController struct {
	Service service.AgentTaskService
}

func (c *AgentAliasController) List(ctx *gin.Context) {
	rows, err := c.Service.ListBusinessAliases(ctx.GetUint("tenant_id"))
	if err != nil {
		writeAgentTaskError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *AgentAliasController) Save(ctx *gin.Context) {
	var input service.AgentBusinessAliasInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.SaveBusinessAlias(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), input)
	if err != nil {
		writeAgentTaskError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *AgentAliasController) Delete(ctx *gin.Context) {
	aliasID, err := strconv.ParseUint(ctx.Param("aliasID"), 10, 64)
	if err != nil || aliasID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid alias id"})
		return
	}
	if err := c.Service.DeleteBusinessAlias(ctx.GetUint("tenant_id"), uint(aliasID)); err != nil {
		writeAgentTaskError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}
