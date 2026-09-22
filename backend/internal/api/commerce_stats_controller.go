package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

type CommerceStatsController struct{ Service service.CommerceStatsService }

func (c *CommerceStatsController) Get(ctx *gin.Context) {
	query := service.CommerceStatsQuery{TenantID: ctx.GetUint("tenant_id"), BusinessType: strings.TrimSpace(ctx.Query("business_type"))}
	if value := strings.TrimSpace(ctx.Query("location_id")); value != "" {
		id, err := strconv.ParseUint(value, 10, 32)
		if err != nil || id == 0 {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "门店编号不正确"})
			return
		}
		query.LocationID = uint(id)
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "统计时区不可用"})
		return
	}
	start, end, dateErr := parseCommerceStatsDates(ctx, location)
	if dateErr != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": dateErr.Error()})
		return
	}
	query.StartAt, query.EndAt = start, end
	result, err := c.Service.Get(query)
	if err != nil {
		commerceStatsError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func parseCommerceStatsDates(ctx *gin.Context, location *time.Location) (*time.Time, *time.Time, error) {
	startText, endText := strings.TrimSpace(ctx.Query("start_date")), strings.TrimSpace(ctx.Query("end_date"))
	if startText == "" && endText == "" {
		return nil, nil, nil
	}
	parse := func(value string) (*time.Time, error) {
		if value == "" {
			return nil, nil
		}
		parsed, err := time.ParseInLocation("2006-01-02", value, location)
		if err != nil {
			return nil, errors.New("日期必须使用 YYYY-MM-DD 格式")
		}
		return &parsed, nil
	}
	start, err := parse(startText)
	if err != nil {
		return nil, nil, err
	}
	endDay, err := parse(endText)
	if err != nil {
		return nil, nil, err
	}
	var end *time.Time
	if endDay != nil {
		next := endDay.AddDate(0, 0, 1)
		end = &next
	}
	if start != nil && end != nil && !end.After(*start) {
		return nil, nil, errors.New("结束日期不能早于开始日期")
	}
	return start, end, nil
}

func commerceStatsError(ctx *gin.Context, err error) {
	status, message := http.StatusInternalServerError, "经营统计查询失败"
	switch {
	case errors.Is(err, service.ErrCommerceStatsInvalid):
		status, message = http.StatusBadRequest, "统计条件不正确"
	case errors.Is(err, service.ErrBusinessCapabilityInactive), errors.Is(err, service.ErrCapabilityInactive), errors.Is(err, service.ErrTenantUnavailable):
		status, message = http.StatusForbidden, "当前商户未启用该商业能力"
	}
	ctx.JSON(status, gin.H{"error": message})
}
