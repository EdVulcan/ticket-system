package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

type SettlementController struct{ Service service.SettlementService }

func (c *SettlementController) List(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	rows, total, err := c.Service.List(ctx.GetUint("tenant_id"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *SettlementController) Generate(ctx *gin.Context) {
	var body struct {
		SupplierTenantID    uint   `json:"supplier_tenant_id" binding:"required"`
		DistributorTenantID uint   `json:"distributor_tenant_id" binding:"required"`
		StartDate           string `json:"start_date" binding:"required"`
		EndDate             string `json:"end_date" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.SupplierTenantID != ctx.GetUint("tenant_id") {
		ctx.JSON(http.StatusForbidden, gin.H{"error": "settlement scope denied"})
		return
	}
	start, err := time.ParseInLocation("2006-01-02", body.StartDate, time.Local)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid start_date"})
		return
	}
	end, err := time.ParseInLocation("2006-01-02", body.EndDate, time.Local)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid end_date"})
		return
	}
	statement, err := c.Service.GenerateStatement(ctx.GetUint("tenant_id"), body.SupplierTenantID, body.DistributorTenantID, start, end.Add(24*time.Hour))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, statement)
}

func (c *SettlementController) SetStatus(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid statement id"})
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
		Detail string `json:"detail"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.SetStatus(ctx.GetUint("tenant_id"), uint(id), body.Status, body.Detail); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": body.Status})
}
