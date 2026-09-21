package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// CommercePromotionController exposes the promotion administration boundary
// and the customer-facing WeChat storefront boundary. The latter never takes
// tenant, channel, business, or customer identity from JSON; all of it comes
// from CommerceStorefrontService.ResolveCustomerScope.
type CommercePromotionController struct {
	Promotion  service.CommercePromotionService
	Storefront service.CommerceStorefrontService
}

func (c *CommercePromotionController) CreateCouponTemplate(ctx *gin.Context) {
	var input service.CreateCommerceCouponTemplateInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "优惠券模板信息格式不正确"})
		return
	}
	scope, ok := adminPromotionScope(ctx, input.ChannelAccountID, input.BusinessType)
	if !ok {
		return
	}
	row, err := c.Promotion.CreateCouponTemplateForScope(scope, input)
	if err != nil {
		writeCommercePromotionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *CommercePromotionController) ListCouponTemplates(ctx *gin.Context) {
	scope, ok := adminPromotionScopeQuery(ctx)
	if !ok {
		return
	}
	rows, err := c.Promotion.ListCouponTemplatesForScope(scope, strings.TrimSpace(ctx.Query("status")))
	if err != nil {
		writeCommercePromotionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": len(rows)})
}

func (c *CommercePromotionController) UpdateCouponTemplate(ctx *gin.Context) {
	templateID, ok := promotionPathID(ctx, "templateID")
	if !ok {
		return
	}
	scope, ok := adminPromotionScopeQuery(ctx)
	if !ok {
		return
	}
	var input service.UpdateCommerceCouponTemplateInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "优惠券模板信息格式不正确"})
		return
	}
	row, err := c.Promotion.UpdateCouponTemplateForScope(scope, templateID, input)
	if err != nil {
		writeCommercePromotionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *CommercePromotionController) CreateAssistCampaign(ctx *gin.Context) {
	var input service.CreateCommerceAssistCampaignInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "助力活动信息格式不正确"})
		return
	}
	scope, ok := adminPromotionScope(ctx, input.ChannelAccountID, input.BusinessType)
	if !ok {
		return
	}
	row, err := c.Promotion.CreateAssistCampaignForScope(scope, input)
	if err != nil {
		writeCommercePromotionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *CommercePromotionController) ListAssistCampaigns(ctx *gin.Context) {
	scope, ok := adminPromotionScopeQuery(ctx)
	if !ok {
		return
	}
	rows, err := c.Promotion.ListAssistCampaignsForScope(scope, strings.TrimSpace(ctx.Query("status")))
	if err != nil {
		writeCommercePromotionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": len(rows)})
}

func (c *CommercePromotionController) UpdateAssistCampaign(ctx *gin.Context) {
	campaignID, ok := promotionPathID(ctx, "campaignID")
	if !ok {
		return
	}
	scope, ok := adminPromotionScopeQuery(ctx)
	if !ok {
		return
	}
	var input service.UpdateCommerceAssistCampaignInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "助力活动信息格式不正确"})
		return
	}
	row, err := c.Promotion.UpdateAssistCampaignForScope(scope, campaignID, input)
	if err != nil {
		writeCommercePromotionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *CommercePromotionController) ListAvailableCoupons(ctx *gin.Context) {
	scope, customerID, ok := c.storefrontPromotionScope(ctx)
	if !ok {
		return
	}
	rows, err := c.Promotion.ListAvailableCouponsForScope(*scope, customerID)
	if err != nil {
		writeCommercePromotionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": len(rows)})
}

func (c *CommercePromotionController) ListAvailableAssistCampaigns(ctx *gin.Context) {
	scope, _, ok := c.storefrontPromotionScope(ctx)
	if !ok {
		return
	}
	rows, err := c.Promotion.ListAvailableAssistCampaignsForScope(*scope)
	if err != nil {
		writeCommercePromotionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": len(rows)})
}

func (c *CommercePromotionController) CreateAssistSession(ctx *gin.Context) {
	scope, customerID, ok := c.storefrontPromotionScope(ctx)
	if !ok {
		return
	}
	var input struct {
		CampaignID     uint   `json:"campaign_id"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "助力活动请求格式不正确"})
		return
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(ctx.GetHeader("Idempotency-Key"))
	}
	result, err := c.Promotion.CreateAssistSessionForScope(*scope, input.CampaignID, customerID, input.IdempotencyKey)
	if err != nil {
		writeCommercePromotionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, result)
}

func (c *CommercePromotionController) GetAssistSession(ctx *gin.Context) {
	scope, customerID, ok := c.storefrontPromotionScope(ctx)
	if !ok {
		return
	}
	view, err := c.Promotion.GetAssistSessionForScope(*scope, ctx.Param("token"), customerID)
	if err != nil {
		writeCommercePromotionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, view)
}

func (c *CommercePromotionController) HelpAssist(ctx *gin.Context) {
	scope, _, ok := c.storefrontPromotionScope(ctx)
	if !ok {
		return
	}
	// The helper identity is always derived from the bearer session. A body
	// field, if sent by an old client, is intentionally ignored.
	resolved, err := c.Storefront.ResolveCustomerScope(commerceStorefrontBearerToken(ctx), promotionBusinessHint(ctx))
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	result, err := c.Promotion.HelpAssistForScope(*scope, ctx.Param("token"), resolved.CustomerID)
	if err != nil {
		writeCommercePromotionError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *CommercePromotionController) storefrontPromotionScope(ctx *gin.Context) (*service.CommercePromotionScope, string, bool) {
	token := commerceStorefrontBearerToken(ctx)
	hint := promotionBusinessHint(ctx)
	var (
		resolved *service.CommerceStorefrontCustomerScope
		err      error
	)
	if hint == "" {
		resolved, err = c.Storefront.ResolveCustomerScope(token)
	} else {
		resolved, err = c.Storefront.ResolveCustomerScope(token, hint)
	}
	if err != nil {
		commerceStorefrontError(ctx, err)
		return nil, "", false
	}
	return &service.CommercePromotionScope{TenantID: resolved.TenantID, ChannelAccountID: resolved.ChannelAccountID, BusinessType: resolved.BusinessType, LocationID: resolved.LocationID}, resolved.CustomerID, true
}

func adminPromotionScope(ctx *gin.Context, channelID uint, businessType string) (service.CommercePromotionScope, bool) {
	if channelID == 0 || strings.TrimSpace(businessType) == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "必须选择微信小程序渠道和业务类型"})
		return service.CommercePromotionScope{}, false
	}
	return service.CommercePromotionScope{TenantID: ctx.GetUint("tenant_id"), ChannelAccountID: channelID, BusinessType: strings.TrimSpace(businessType)}, true
}

func adminPromotionScopeQuery(ctx *gin.Context) (service.CommercePromotionScope, bool) {
	channelID, err := strconv.ParseUint(strings.TrimSpace(ctx.Query("channel_account_id")), 10, 32)
	if err != nil || channelID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "必须选择微信小程序渠道"})
		return service.CommercePromotionScope{}, false
	}
	return adminPromotionScope(ctx, uint(channelID), ctx.Query("business_type"))
}

func promotionPathID(ctx *gin.Context, name string) (uint, bool) {
	id, err := strconv.ParseUint(strings.TrimSpace(ctx.Param(name)), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "编号无效"})
		return 0, false
	}
	return uint(id), true
}

func promotionBusinessHint(ctx *gin.Context) string {
	return commerceStorefrontBusinessType(ctx)
}

func writeCommercePromotionError(ctx *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "促销服务暂时不可用"
	switch {
	case errors.Is(err, service.ErrPromotionScopeDenied), errors.Is(err, service.ErrTenantUnavailable), errors.Is(err, service.ErrBusinessCapabilityInactive), errors.Is(err, service.ErrCapabilityInactive):
		status, message = http.StatusForbidden, "当前小程序或业务未授权使用该促销活动"
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, message = http.StatusNotFound, "促销活动不存在"
	case errors.Is(err, service.ErrCommercePromotionInvalid):
		status, message = http.StatusBadRequest, "促销参数不符合要求"
	case errors.Is(err, service.ErrPromotionTemplateImmutable):
		status, message = http.StatusConflict, "已发放优惠的奖励条款不能修改，请停用后创建新版本"
	case errors.Is(err, service.ErrPromotionUnavailable):
		status, message = http.StatusConflict, "促销活动当前不可用"
	case errors.Is(err, service.ErrAssistSelfHelp):
		status, message = http.StatusConflict, "发起人不能为自己的助力"
	case errors.Is(err, service.ErrAssistAlreadyHelped):
		status, message = http.StatusConflict, "你已经助力过该活动"
	case errors.Is(err, service.ErrAssistSessionLimit):
		status, message = http.StatusConflict, "已达到可发起的活动次数上限"
	case errors.Is(err, service.ErrCouponNotOwned), errors.Is(err, service.ErrCouponAlreadyReserved), errors.Is(err, service.ErrCouponNotApplicable):
		status, message = http.StatusConflict, "优惠券当前不可使用"
	}
	ctx.JSON(status, gin.H{"error": message})
}
