package api

import (
	"errors"
	"net/http"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type XiaohongshuWebhookController struct {
	Service service.XiaohongshuWebhookService
}

func (c XiaohongshuWebhookController) Verify(ctx *gin.Context) {
	echo, err := c.Service.VerifyURL(
		ctx.Param("appID"), ctx.Query("signature"), ctx.Query("timestamp"),
		ctx.Query("nonce"), ctx.Query("echostr"),
	)
	if err != nil {
		writeXiaohongshuWebhookError(ctx, err)
		return
	}
	ctx.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(echo))
}

func (c XiaohongshuWebhookController) Receive(ctx *gin.Context) {
	var body service.XiaohongshuWebhookMessage
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid xiaohongshu webhook message"})
		return
	}
	if err := c.Service.Receive(ctx.Request.Context(), ctx.Param("appID"), body); err != nil {
		writeXiaohongshuWebhookError(ctx, err)
		return
	}
	ctx.Data(http.StatusOK, "text/plain; charset=utf-8", []byte("success"))
}

func writeXiaohongshuWebhookError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrXiaohongshuWebhookNotConfigured):
		ctx.JSON(http.StatusNotFound, gin.H{"error": "xiaohongshu webhook is not configured"})
	case errors.Is(err, service.ErrXiaohongshuWebhookSignature):
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "invalid xiaohongshu webhook signature"})
	case errors.Is(err, service.ErrXiaohongshuWebhookPayload):
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid xiaohongshu webhook payload"})
	default:
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "xiaohongshu webhook failed"})
	}
}
