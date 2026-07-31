package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type FinanceController struct {
	Service service.FinanceService
}

func (c *FinanceController) ListAccounts(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")

	accounts, err := c.Service.ListAccounts(tenantID.(uint))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"data": accounts})
}

func (c *FinanceController) ListTransactions(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))

	filters := make(map[string]interface{})
	if v := ctx.Query("supplier_id"); v != "" {
		filters["supplier_id"] = v
	}
	if v := ctx.Query("type"); v != "" {
		filters["type"] = v
	}
	if v := ctx.Query("start_date"); v != "" {
		filters["start_date"] = v
	}
	if v := ctx.Query("end_date"); v != "" {
		filters["end_date"] = v
	}

	records, total, err := c.Service.ListTransactions(tenantID.(uint), page, pageSize, filters)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"data":  records,
		"total": total,
		"page":  page,
	})
}

func (c *FinanceController) ListLedger(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	accountID, _ := strconv.ParseUint(ctx.Query("account_id"), 10, 32)
	entries, total, err := c.Service.ListLedger(ctx.GetUint("tenant_id"), page, pageSize, uint(accountID))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": entries, "total": total, "page": page, "page_size": pageSize})
}

func (c *FinanceController) ListDocuments(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	rows, total, err := c.Service.ListDocuments(ctx.GetUint("tenant_id"), ctx.Query("status"), ctx.Query("type"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *FinanceController) CreateDocument(ctx *gin.Context) {
	var document model.FinancialDocument
	if err := ctx.ShouldBindJSON(&document); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.CreateDocument(ctx.GetUint("tenant_id"), &document); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, document)
}

func (c *FinanceController) ApproveDocument(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid financial document id"})
		return
	}
	var body struct {
		Evidence string `json:"evidence"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil && err.Error() != "EOF" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	document, err := c.Service.ApproveDocument(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), body.Evidence)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, document)
}
