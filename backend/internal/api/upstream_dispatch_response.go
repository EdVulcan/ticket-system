package api

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"ticket-backend/internal/service"
	"time"
)

func respondUpstreamDeferred(ctx *gin.Context, err error) bool {
	retry, queued := service.UpstreamDispatchRetryAt(err)
	if !queued {
		return false
	}
	seconds := int(time.Until(retry).Seconds()) + 1
	if seconds < 1 {
		seconds = 1
	}
	ctx.Header("Retry-After", strconv.Itoa(seconds))
	ctx.JSON(http.StatusTooManyRequests, gin.H{"reason_code": "upstream_waiting", "requires_confirmation": false, "retry_at": retry, "error": "供应商查询正在排队或冷却，退款尚未提交，请稍后重试"})
	return true
}
