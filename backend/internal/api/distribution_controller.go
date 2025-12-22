package api

import (
	"net/http"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type DistributionController struct {
	Service service.DistributionService
}

func (c *DistributionController) Search(ctx *gin.Context) {
	code := ctx.Query("code")
	if code == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "必须提供系统编号"})
		return
	}

	tenant, err := c.Service.GetSupplierByCode(code)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Only return public info
	ctx.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"name":    tenant.Name,
			"contact": tenant.Contact,
			"code":    tenant.SystemCode,
		},
	})
}

func (c *DistributionController) Apply(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")

	var req struct {
		SystemCode string `json:"system_code" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.ApplyAgent(tenantID.(uint), req.SystemCode); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "申请已提交"})
}

func (c *DistributionController) ListSuppliers(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")

	list, err := c.Service.ListSuppliers(tenantID.(uint))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"data": list})
}
