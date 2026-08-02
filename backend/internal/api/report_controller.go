package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

type ReportController struct {
	Service service.ReportService
}

func reportFilter(ctx *gin.Context) service.FormalReportFilter {
	scenicAreaID, _ := strconv.ParseUint(ctx.Query("scenic_area_id"), 10, 64)
	return service.FormalReportFilter{
		StartDate:    ctx.DefaultQuery("start_date", time.Now().AddDate(0, 0, -29).Format("2006-01-02")),
		EndDate:      ctx.DefaultQuery("end_date", time.Now().Format("2006-01-02")),
		Channel:      ctx.Query("channel"),
		Method:       ctx.Query("method"),
		OrderNo:      ctx.Query("order_no"),
		ProductName:  ctx.Query("product_name"),
		ScenicAreaID: uint(scenicAreaID),
	}
}

func reportPage(ctx *gin.Context) (int, int) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	return page, pageSize
}

func (c *ReportController) GetBusinessSummary(ctx *gin.Context) {
	rows, err := c.Service.GetBusinessSummary(ctx.GetUint("tenant_id"), reportFilter(ctx))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *ReportController) GetBusinessDetails(ctx *gin.Context) {
	page, pageSize := reportPage(ctx)
	rows, total, err := c.Service.GetBusinessDetails(ctx.GetUint("tenant_id"), reportFilter(ctx), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total})
}

func (c *ReportController) GetVerificationSummary(ctx *gin.Context) {
	rows, err := c.Service.GetVerificationSummary(ctx.GetUint("tenant_id"), reportFilter(ctx))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *ReportController) GetVerificationDetails(ctx *gin.Context) {
	page, pageSize := reportPage(ctx)
	rows, total, err := c.Service.GetVerificationDetails(ctx.GetUint("tenant_id"), reportFilter(ctx), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total})
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
