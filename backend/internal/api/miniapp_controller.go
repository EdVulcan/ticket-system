package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
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

func (c *MiniappController) ListOrders(ctx *gin.Context) {
	customer, err := c.authenticate(ctx)
	if err != nil {
		return
	}
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))
	result, err := c.Service.ListXiaohongshuOrders(customer, page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "订单加载失败，请稍后重试"})
		return
	}
	ctx.JSON(http.StatusOK, result)
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

func (c *MiniappController) BookPackage(ctx *gin.Context) {
	customer, err := c.authenticate(ctx)
	if err != nil {
		return
	}
	var body service.MiniappPackageBookingInput
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请填写完整的预约信息"})
		return
	}
	result, err := c.Service.BookXiaohongshuPackage(ctx.Request.Context(), customer, ctx.Param("orderNo"), body)
	if err != nil {
		status := http.StatusConflict
		message := miniappPackageBookingError(err)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, service.ErrMiniappUnavailable) {
			status = http.StatusServiceUnavailable
		} else {
			var platformError *xiaohongshu.APIError
			if errors.As(err, &platformError) {
				status = http.StatusBadGateway
				message = "小红书预约服务暂时不可用，请稍后重试"
			}
		}
		ctx.JSON(status, gin.H{"error": message})
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *MiniappController) CancelPackageBooking(ctx *gin.Context) {
	customer, err := c.authenticate(ctx)
	if err != nil {
		return
	}
	result, err := c.Service.CancelXiaohongshuPackageBooking(ctx.Request.Context(), customer, ctx.Param("orderNo"), ctx.Param("entitlementNo"))
	if err != nil {
		status := http.StatusConflict
		message := miniappPackageBookingError(err)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, service.ErrMiniappUnavailable) {
			status = http.StatusServiceUnavailable
		} else {
			var platformError *xiaohongshu.APIError
			if errors.As(err, &platformError) {
				status = http.StatusBadGateway
				message = "小红书预约服务暂时不可用，请稍后重试"
			}
		}
		ctx.JSON(status, gin.H{"error": message})
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func miniappPackageBookingError(err error) string {
	if err == nil {
		return "预约失败，请稍后重试"
	}
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return "订单或套餐预约权益不存在"
	case errors.Is(err, service.ErrMiniappUnavailable):
		return "小程序服务暂时不可用，请稍后重试"
	case strings.Contains(message, "validity") || strings.Contains(message, "expired"):
		return "该套餐已超过预约有效期"
	case strings.Contains(message, "advance"):
		return "所选日期不满足提前预约要求"
	case strings.Contains(message, "hotel inventory") || strings.Contains(message, "hotel rooms"):
		return "所选日期房量不足或尚未开放"
	case strings.Contains(message, "stock"):
		return "所选日期门票库存不足"
	case strings.Contains(message, "reschedule"):
		return "该套餐已达到允许改约次数"
	case strings.Contains(message, "pending refund"):
		return "该套餐正在退款，暂不能预约或改约"
	case strings.Contains(message, "bookable") || strings.Contains(message, "awaiting booking"):
		return "该套餐当前不可预约"
	default:
		return "预约处理失败，请稍后重试或联系景区客服"
	}
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
