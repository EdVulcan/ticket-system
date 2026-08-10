package api

import (
	"errors"
	"net/http"
	"strings"
	"ticket-backend/internal/service"
	"ticket-backend/internal/xiaohongshu"

	"github.com/gin-gonic/gin"
)

type MiniappController struct {
	Service service.MiniappService
}

func (c *MiniappController) LoginXiaohongshu(ctx *gin.Context) {
	var body struct {
		AppID string `json:"app_id" binding:"required"`
		Code  string `json:"code" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "小程序登录参数不完整"})
		return
	}
	result, err := c.Service.LoginXiaohongshu(ctx.Request.Context(), body.AppID, body.Code)
	if err != nil {
		if errors.Is(err, service.ErrMiniappUnavailable) {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "当前小程序暂未开放"})
			return
		}
		var platformError *xiaohongshu.APIError
		if errors.As(err, &platformError) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "小红书登录凭证已失效，请重新进入小程序"})
			return
		}
		ctx.JSON(http.StatusBadGateway, gin.H{"error": "小红书登录校验失败，请稍后重试"})
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *MiniappController) ListCatalog(ctx *gin.Context) {
	token := bearerToken(ctx.GetHeader("Authorization"))
	customer, err := c.Service.Authenticate(token)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "登录状态已失效，请重新进入小程序"})
		return
	}
	catalog, err := c.Service.ListCatalog(customer)
	if err != nil {
		if errors.Is(err, service.ErrMiniappUnavailable) {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "当前小程序暂未开放"})
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "票种加载失败，请稍后重试"})
		return
	}
	ctx.JSON(http.StatusOK, catalog)
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}
