package api

import (
	"errors"
	"net/http"
	"strconv"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type CatalogBatchChangeController struct {
	Service service.CatalogBatchChangeService
}

func (c *CatalogBatchChangeController) AIPreview(ctx *gin.Context) {
	var req service.CatalogAIPreviewRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	preview, err := c.Service.PreviewWithAI(ctx.Request.Context(), ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), req)
	if err != nil {
		writeCatalogBatchError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, preview)
}

func (c *CatalogBatchChangeController) AIStatus(ctx *gin.Context) {
	status, err := (&service.PlatformAIService{}).Availability(ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, status)
}

func (c *CatalogBatchChangeController) Preview(ctx *gin.Context) {
	var req service.CatalogBatchChangePreviewRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	preview, err := c.Service.Preview(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), req)
	if err != nil {
		writeCatalogBatchError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, preview)
}

func (c *CatalogBatchChangeController) Get(ctx *gin.Context) {
	planID, err := strconv.ParseUint(ctx.Param("planID"), 10, 64)
	if err != nil || planID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid plan id"})
		return
	}
	preview, err := c.Service.Get(ctx.GetUint("tenant_id"), uint(planID))
	if err != nil {
		writeCatalogBatchError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, preview)
}

func (c *CatalogBatchChangeController) Confirm(ctx *gin.Context) {
	planID, err := strconv.ParseUint(ctx.Param("planID"), 10, 64)
	if err != nil || planID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid plan id"})
		return
	}
	var req struct {
		PlanHash string `json:"plan_hash"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	preview, err := c.Service.Confirm(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), uint(planID), req.PlanHash)
	if err != nil {
		writeCatalogBatchError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, preview)
}

func writeCatalogBatchError(ctx *gin.Context, err error) {
	var batchErr *service.CatalogBatchError
	if errors.As(err, &batchErr) {
		ctx.JSON(batchErr.HTTPStatus, gin.H{"error": batchErr.Message, "code": batchErr.Code})
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
