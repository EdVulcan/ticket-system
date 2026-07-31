package api

import (
	"net/http"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

type ReportController struct {
	Service service.ReportService
}

func (c *ReportController) GetSales(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")
	start := ctx.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	end := ctx.DefaultQuery("end_date", time.Now().AddDate(0, 0, 1).Format("2006-01-02"))

	stats, err := c.Service.GetSalesStats(tenantID.(uint), start, end)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": stats})
}

func (c *ReportController) GetProducts(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")
	start := ctx.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	end := ctx.DefaultQuery("end_date", time.Now().AddDate(0, 0, 1).Format("2006-01-02"))

	stats, err := c.Service.GetProductStats(tenantID.(uint), start, end)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": stats})
}

func (c *ReportController) GetChannels(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")
	start := ctx.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	end := ctx.DefaultQuery("end_date", time.Now().AddDate(0, 0, 1).Format("2006-01-02"))

	stats, err := c.Service.GetChannelStats(tenantID.(uint), start, end)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": stats})
}

func (c *ReportController) GetDaily(ctx *gin.Context) {
	tenantID := ctx.GetUint("tenant_id")
	start := ctx.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	end := ctx.DefaultQuery("end_date", time.Now().Format("2006-01-02"))
	report, err := c.Service.GetDailyReport(tenantID, start, end)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": report, "start_date": start, "end_date": end})
}

func (c *ReportController) GetOperations(ctx *gin.Context) {
	tenantID := ctx.GetUint("tenant_id")
	start := ctx.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	end := ctx.DefaultQuery("end_date", time.Now().Format("2006-01-02"))
	report, err := c.Service.GetOperationsReport(tenantID, start, end)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": report, "start_date": start, "end_date": end})
}
