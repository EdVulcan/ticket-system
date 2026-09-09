package api

import (
	"errors"
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type MiniappPromotionController struct {
	Miniapp   service.MiniappService
	Promotion service.MiniappPromotionService
}

func (c *MiniappPromotionController) Opportunity(ctx *gin.Context) {
	customer, err := c.authenticate(ctx)
	if err != nil {
		return
	}
	result, err := c.Promotion.AcquireOpportunity(customer)
	if err != nil {
		miniappPromotionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *MiniappPromotionController) Quote(ctx *gin.Context) {
	customer, err := c.authenticate(ctx)
	if err != nil {
		return
	}
	var body struct {
		MappingID uint `json:"mapping_id"`
		Quantity  int  `json:"quantity"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil || body.MappingID == 0 || body.Quantity <= 0 || body.Quantity > 100 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请选择有效的票种和购买数量"})
		return
	}
	quote, err := c.Promotion.QuoteForCustomer(customer, body.MappingID, body.Quantity)
	if err != nil {
		miniappPromotionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, quote)
}

func (c *MiniappPromotionController) GetConfig(ctx *gin.Context) {
	accountID, ok := miniappPromotionAccountID(ctx)
	if !ok {
		return
	}
	result, err := c.Promotion.GetConfig(ctx.GetUint("tenant_id"), accountID)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "小红书渠道账号不存在或不可用"})
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *MiniappPromotionController) SaveConfig(ctx *gin.Context) {
	accountID, ok := miniappPromotionAccountID(ctx)
	if !ok {
		return
	}
	var body service.MiniappInstantDiscountConfig
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "优惠活动配置无效"})
		return
	}
	body.ActorUserID = ctx.GetUint("user_id")
	result, err := c.Promotion.SaveConfig(ctx.GetUint("tenant_id"), accountID, body)
	if err != nil {
		if errors.Is(err, service.ErrMiniappPromotionUnavailable) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": "小红书渠道账号不存在或不可用"})
			return
		}
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *MiniappPromotionController) authenticate(ctx *gin.Context) (*model.MiniappCustomer, error) {
	customer, err := c.Miniapp.Authenticate(bearerToken(ctx.GetHeader("Authorization")))
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "登录状态已失效，请重新进入小程序"})
		return nil, err
	}
	return customer, nil
}

func miniappPromotionAccountID(ctx *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return 0, false
	}
	return uint(id), true
}

func miniappPromotionError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrMiniappUnauthenticated):
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "登录状态已失效，请重新进入小程序"})
	case errors.Is(err, service.ErrMiniappPromotionQuote), errors.Is(err, service.ErrMiniappPromotionMapping):
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "票种当前不可购买"})
	case errors.Is(err, service.ErrMiniappPromotionUnavailable), errors.Is(err, service.ErrMiniappUnavailable):
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "当前小程序暂未开放"})
	default:
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	}
}
