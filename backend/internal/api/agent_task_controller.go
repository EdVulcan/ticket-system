package api

import (
	"errors"
	"net/http"
	"strconv"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type AgentTaskController struct {
	Service service.AgentTaskService
}

func (c *AgentTaskController) Submit(ctx *gin.Context) {
	var req service.AgentTaskRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	view, err := c.Service.Submit(ctx.Request.Context(), ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), req)
	if err != nil {
		writeAgentTaskError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, view)
}

func (c *AgentTaskController) Get(ctx *gin.Context) {
	taskID, err := strconv.ParseUint(ctx.Param("taskID"), 10, 64)
	if err != nil || taskID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid task id"})
		return
	}
	view, err := c.Service.Get(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), uint(taskID))
	if err != nil {
		writeAgentTaskError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, view)
}

func (c *AgentTaskController) Cancel(ctx *gin.Context) {
	taskID, err := strconv.ParseUint(ctx.Param("taskID"), 10, 64)
	if err != nil || taskID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid task id"})
		return
	}
	view, err := c.Service.Cancel(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), uint(taskID))
	if err != nil {
		writeAgentTaskError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, view)
}

func (c *AgentTaskController) Confirm(ctx *gin.Context) {
	taskID, err := strconv.ParseUint(ctx.Param("taskID"), 10, 64)
	if err != nil || taskID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid task id"})
		return
	}
	view, err := c.Service.Confirm(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), uint(taskID))
	if err != nil {
		writeAgentTaskError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, view)
}

func writeAgentTaskError(ctx *gin.Context, err error) {
	var taskErr *service.AgentTaskError
	if errors.As(err, &taskErr) {
		ctx.JSON(taskErr.HTTPStatus, gin.H{"error": taskErr.Message, "code": taskErr.Code})
		return
	}
	if errors.Is(err, service.ErrAIBudgetExceeded) {
		ctx.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error(), "code": "ai_budget_exceeded"})
		return
	}
	if errors.Is(err, service.ErrAIUnavailable) {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "code": "ai_unavailable"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
