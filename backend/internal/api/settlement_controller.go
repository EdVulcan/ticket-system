package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

type SettlementController struct{ Service service.SettlementService }

func (c *SettlementController) Get(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid statement id"})
		return
	}
	statement, err := c.Service.GetStatement(ctx.GetUint("tenant_id"), uint(id))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "settlement statement not found"})
		return
	}
	ctx.JSON(http.StatusOK, statement)
}

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
		SupplierTenantID    uint   `json:"supplier_tenant_id"`
		DistributorTenantID uint   `json:"distributor_tenant_id" binding:"required"`
		StartDate           string `json:"start_date" binding:"required"`
		EndDate             string `json:"end_date" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	body.SupplierTenantID = ctx.GetUint("tenant_id")
	if body.SupplierTenantID == 0 {
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

func (c *SettlementController) Adjust(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid statement id"})
		return
	}
	var body struct {
		AmountCents int64  `json:"amount_cents" binding:"required"`
		Reason      string `json:"reason" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.AdjustDisputed(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), body.AmountCents, body.Reason); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "draft"})
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
	if err := c.Service.SetStatus(ctx.GetUint("tenant_id"), uint(id), body.Status, body.Detail, ctx.GetUint("user_id")); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": body.Status})
}
