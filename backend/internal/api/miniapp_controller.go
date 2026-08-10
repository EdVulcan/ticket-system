package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"ticket-backend/internal/xiaohongshu"
	"ticket-backend/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
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
			if logger.Log != nil {
				logger.Log.Warn("xiaohongshu miniapp login rejected",
					zap.String("app_id", strings.TrimSpace(body.AppID)),
					zap.Int("platform_code", platformError.Code),
					zap.String("platform_message", platformError.Message),
				)
			}
			ctx.JSON(http.StatusUnauthorized, gin.H{
				"error":         xiaohongshuLoginError(platformError),
				"platform_code": platformError.Code,
			})
			return
		}
		ctx.JSON(http.StatusBadGateway, gin.H{"error": "小红书登录校验失败，请稍后重试"})
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func xiaohongshuLoginError(platformError *xiaohongshu.APIError) string {
	if platformError == nil || platformError.Code == 0 {
		return "小红书平台拒绝登录，请检查小程序配置后重试"
	}
	return fmt.Sprintf("小红书平台拒绝登录（错误码 %d），请检查 AppID、AppSecret 与当前环境是否匹配", platformError.Code)
}

func (c *MiniappController) ListCatalog(ctx *gin.Context) {
	customer, err := c.authenticate(ctx)
	if err != nil {
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

func (c *MiniappController) CreateOrder(ctx *gin.Context) {
	customer, err := c.authenticate(ctx)
	if err != nil {
		return
	}
	var body service.MiniappOrderCreateInput
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请选择票种和购买数量"})
		return
	}
	result, err := c.Service.CreateXiaohongshuOrder(ctx.Request.Context(), customer, body)
	if err != nil {
		var platformError *xiaohongshu.APIError
		if errors.As(err, &platformError) {
			ctx.JSON(http.StatusBadGateway, gin.H{"error": "小红书暂时无法创建支付订单，请稍后重试", "platform_code": platformError.Code})
			return
		}
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, result)
}

func (c *MiniappController) GetOrder(ctx *gin.Context) {
	customer, err := c.authenticate(ctx)
	if err != nil {
		return
	}
	result, err := c.Service.GetXiaohongshuOrder(ctx.Request.Context(), customer, ctx.Param("orderNo"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "订单不存在"})
		return
	}
	if err != nil {
		ctx.JSON(http.StatusBadGateway, gin.H{"error": "订单状态查询失败，请稍后重试"})
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *MiniappController) authenticate(ctx *gin.Context) (*model.MiniappCustomer, error) {
	token := bearerToken(ctx.GetHeader("Authorization"))
	customer, err := c.Service.Authenticate(token)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "登录状态已失效，请重新进入小程序"})
		return nil, err
	}
	return customer, nil
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}
