package api

import (
	"net/http"
	"strconv"
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

func (c *PlatformController) ListOrders(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	tenantID, _ := strconv.ParseUint(ctx.Query("tenant_id"), 10, 32)
	rows, total, err := c.Service.ListOrders(uint(tenantID), ctx.Query("status"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := service.RecordAudit(platformActorID(ctx), 0, ctx.GetString("role"), "platform", "platform.orders.read", "platform", 0, "global order worklist", "", ""); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "orders loaded but audit logging failed"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *PlatformController) ListIssues(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	tenantID, _ := strconv.ParseUint(ctx.Query("tenant_id"), 10, 32)
	rows, total, err := c.Service.ListIssues(uint(tenantID), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := service.RecordAudit(platformActorID(ctx), 0, ctx.GetString("role"), "platform", "platform.issues.read", "platform", 0, "global issue worklist", "", ""); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "issues loaded but audit logging failed"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}
