package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type CheckPointController struct {
	Service service.CheckPointService
}

func (c *CheckPointController) Create(ctx *gin.Context) {
	var cp model.CheckPoint
	if err := ctx.ShouldBindJSON(&cp); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tenantID, _ := ctx.Get("tenant_id")
	cp.TenantID = tenantID.(uint)

	if err := c.Service.Create(&cp); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, cp)
}

func (c *CheckPointController) Update(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	var cp model.CheckPoint
	if err := ctx.ShouldBindJSON(&cp); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.Update(uint(id), ctx.GetUint("tenant_id"), &cp); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "updated successfully"})
}

func (c *CheckPointController) Delete(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	if err := c.Service.Delete(uint(id), ctx.GetUint("tenant_id")); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "deleted successfully"})
}

func (c *CheckPointController) List(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))
	scenicAreaID, _ := strconv.ParseUint(ctx.Query("scenic_area_id"), 10, 64)
	cps, total, err := c.Service.List(page, pageSize, ctx.GetUint("tenant_id"), uint(scenicAreaID))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"data":  cps,
		"total": total,
		"page":  page,
	})
}
