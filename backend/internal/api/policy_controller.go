package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type PolicyController struct {
	Service service.PolicyService
}

func (c *PolicyController) Create(ctx *gin.Context) {
	var policy model.Policy
	if err := ctx.ShouldBindJSON(&policy); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tenantID, _ := ctx.Get("tenant_id")
	policy.TenantID = tenantID.(uint)

	if err := c.Service.Create(&policy); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, policy)
}

func (c *PolicyController) Update(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	var policy model.Policy
	if err := ctx.ShouldBindJSON(&policy); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.Update(uint(id), ctx.GetUint("tenant_id"), &policy); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "updated successfully"})
}

func (c *PolicyController) Delete(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	if err := c.Service.Delete(uint(id), ctx.GetUint("tenant_id")); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "deleted successfully"})
}

func (c *PolicyController) List(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")
	category := ctx.Query("category")

	policies, err := c.Service.List(tenantID.(uint), category)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"data": policies})
}
