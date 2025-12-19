package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type TenantController struct {
	Service service.TenantService
}

func (c *TenantController) Create(ctx *gin.Context) {
	var tenant model.Tenant
	if err := ctx.ShouldBindJSON(&tenant); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.Create(&tenant); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, tenant)
}

func (c *TenantController) Update(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	var tenant model.Tenant
	if err := ctx.ShouldBindJSON(&tenant); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.Update(uint(id), &tenant); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "updated successfully"})
}

func (c *TenantController) Delete(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	if err := c.Service.Delete(uint(id)); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "deleted successfully"})
}

func (c *TenantController) List(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))

	tenants, total, err := c.Service.List(page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"data":  tenants,
		"total": total,
		"page":  page,
	})
}
