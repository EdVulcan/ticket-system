package api

import (
	"io"
	"net/http"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

const maxCtripOrderRequestBytes = 2 << 20

type CtripController struct {
	Service service.CtripProtocolService
}

func (c *CtripController) HandleOrder(ctx *gin.Context) {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxCtripOrderRequestBytes)
	raw, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"header": gin.H{"resultCode": "0001", "resultMessage": "报文过大或读取失败"}})
		return
	}
	response, err := c.Service.Handle(raw, ctx.ClientIP())
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"header": gin.H{"resultCode": "1100", "resultMessage": "系统处理失败，请稍后重试"}})
		return
	}
	ctx.Data(http.StatusOK, "application/json; charset=utf-8", response)
}
