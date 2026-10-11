package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type CommerceDeliveryController struct {
	Service    service.CommerceDeliveryService
	Storefront *service.CommerceStorefrontService
}

func deliveryBusinessType(ctx *gin.Context) string {
	return strings.TrimSpace(ctx.Query("business_type"))
}

func deliveryLocationID(ctx *gin.Context) (uint, bool) {
	id, err := parsePathID(ctx, "locationID")
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "履约地点编号不正确"})
		return 0, false
	}
	return id, true
}

func deliveryEntityID(ctx *gin.Context, name, message string) (uint, bool) {
	id, err := parsePathID(ctx, name)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": message})
		return 0, false
	}
	return id, true
}

func (c *CommerceDeliveryController) GetConfig(ctx *gin.Context) {
	locationID, ok := deliveryLocationID(ctx)
	if !ok {
		return
	}
	row, err := c.Service.GetLocationConfig(ctx.GetUint("tenant_id"), locationID, deliveryBusinessType(ctx))
	if err != nil {
		commerceDeliveryError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *CommerceDeliveryController) SaveConfig(ctx *gin.Context) {
	locationID, ok := deliveryLocationID(ctx)
	if !ok {
		return
	}
	var input service.CommerceLocationServiceConfigInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "服务配置格式不正确"})
		return
	}
	queryBusiness := deliveryBusinessType(ctx)
	if input.BusinessType == "" {
		input.BusinessType = queryBusiness
	} else if queryBusiness != "" && input.BusinessType != queryBusiness {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "业务类型不一致"})
		return
	}
	row, err := c.Service.UpsertLocationConfig(ctx.GetUint("tenant_id"), locationID, input)
	if err != nil {
		commerceDeliveryError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *CommerceDeliveryController) ListZones(ctx *gin.Context) {
	locationID, ok := deliveryLocationID(ctx)
	if !ok {
		return
	}
	rows, err := c.Service.ListZones(ctx.GetUint("tenant_id"), locationID, deliveryBusinessType(ctx))
	if err != nil {
		commerceDeliveryError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": len(rows)})
}

func (c *CommerceDeliveryController) CreateZone(ctx *gin.Context) {
	locationID, ok := deliveryLocationID(ctx)
	if !ok {
		return
	}
	var input service.CommerceDeliveryZoneInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "配送区域格式不正确"})
		return
	}
	row, err := c.Service.CreateZone(ctx.GetUint("tenant_id"), locationID, deliveryBusinessType(ctx), input)
	if err != nil {
		commerceDeliveryError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *CommerceDeliveryController) UpdateZone(ctx *gin.Context) {
	locationID, ok := deliveryLocationID(ctx)
	if !ok {
		return
	}
	zoneID, ok := deliveryEntityID(ctx, "zoneID", "配送区域编号不正确")
	if !ok {
		return
	}
	var input service.CommerceDeliveryZoneInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "配送区域格式不正确"})
		return
	}
	row, err := c.Service.UpdateZone(ctx.GetUint("tenant_id"), locationID, zoneID, deliveryBusinessType(ctx), input)
	if err != nil {
		commerceDeliveryError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *CommerceDeliveryController) ListSlots(ctx *gin.Context) {
	locationID, ok := deliveryLocationID(ctx)
	if !ok {
		return
	}
	var zoneID *uint
	if raw := strings.TrimSpace(ctx.Query("zone_id")); raw != "" {
		value, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || value == 0 {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "配送区域编号不正确"})
			return
		}
		parsed := uint(value)
		zoneID = &parsed
	}
	rows, err := c.Service.ListSlots(ctx.GetUint("tenant_id"), locationID, deliveryBusinessType(ctx), zoneID)
	if err != nil {
		commerceDeliveryError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": len(rows)})
}

func (c *CommerceDeliveryController) CreateSlot(ctx *gin.Context) {
	locationID, ok := deliveryLocationID(ctx)
	if !ok {
		return
	}
	var input service.CommerceDeliverySlotInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "配送时段格式不正确"})
		return
	}
	row, err := c.Service.CreateSlot(ctx.GetUint("tenant_id"), locationID, deliveryBusinessType(ctx), input)
	if err != nil {
		commerceDeliveryError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *CommerceDeliveryController) UpdateSlot(ctx *gin.Context) {
	locationID, ok := deliveryLocationID(ctx)
	if !ok {
		return
	}
	slotID, ok := deliveryEntityID(ctx, "slotID", "配送时段编号不正确")
	if !ok {
		return
	}
	var input service.CommerceDeliverySlotInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "配送时段格式不正确"})
		return
	}
	row, err := c.Service.UpdateSlot(ctx.GetUint("tenant_id"), locationID, slotID, deliveryBusinessType(ctx), input)
	if err != nil {
		commerceDeliveryError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *CommerceDeliveryController) StorefrontOptions(ctx *gin.Context) {
	if c.Storefront == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "配送服务暂不可用"})
		return
	}
	scope, err := c.Storefront.ResolveCustomerScope(commerceStorefrontBearerToken(ctx), commerceStorefrontBusinessType(ctx))
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	config, err := c.Service.GetLocationConfig(scope.TenantID, scope.LocationID, scope.BusinessType)
	if err != nil {
		commerceDeliveryError(ctx, err)
		return
	}
	if config.Status != "active" {
		ctx.JSON(http.StatusConflict, gin.H{"error": "当前门店暂未开放配送或发货服务"})
		return
	}
	activeZones := []model.CommerceDeliveryZone{}
	activeSlots := []model.CommerceDeliverySlot{}
	if scope.BusinessType == "restaurant" {
		zones, err := c.Service.ListZones(scope.TenantID, scope.LocationID, scope.BusinessType)
		if err != nil {
			commerceDeliveryError(ctx, err)
			return
		}
		slots, err := c.Service.ListSlots(scope.TenantID, scope.LocationID, scope.BusinessType, nil)
		if err != nil {
			commerceDeliveryError(ctx, err)
			return
		}
		for _, zone := range zones {
			if zone.Status == "active" {
				activeZones = append(activeZones, zone)
			}
		}
		for _, slot := range slots {
			if slot.Status == "active" {
				activeSlots = append(activeSlots, slot)
			}
		}
	}
	ctx.JSON(http.StatusOK, gin.H{"config": config, "zones": activeZones, "slots": activeSlots})
}

func (c *CommerceDeliveryController) CreateStorefrontQuote(ctx *gin.Context) {
	if c.Storefront == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "配送服务暂不可用"})
		return
	}
	var input service.CommerceStorefrontQuoteInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "结算报价参数不正确"})
		return
	}
	result, err := c.Storefront.CreateCheckoutQuote(commerceStorefrontBearerToken(ctx), input, commerceStorefrontBusinessType(ctx))
	if err != nil {
		commerceDeliveryError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, result)
}

func commerceDeliveryError(ctx *gin.Context, err error) {
	status, message := http.StatusInternalServerError, "配送服务处理失败"
	code := "DELIVERY_SERVICE_ERROR"
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, message, code = http.StatusNotFound, "配送配置不存在，请联系商家", "DELIVERY_CONFIG_NOT_FOUND"
	case errors.Is(err, service.ErrCommerceQuoteExpired), errors.Is(err, service.ErrCommerceQuoteConflict):
		status, message, code = http.StatusConflict, "结算报价已失效，请重新确认", "CHECKOUT_QUOTE_EXPIRED"
	case errors.Is(err, service.ErrCommerceDeliveryInvalid), errors.Is(err, service.ErrCommerceQuoteInvalid), errors.Is(err, service.ErrCommerceStorefrontInvalid), errors.Is(err, service.ErrCommerceStorefrontAddressInvalid):
		status, message, code = http.StatusBadRequest, "配送或结算参数不符合要求", "CHECKOUT_QUOTE_INVALID"
		// Keep the detailed reason safe and actionable without exposing database
		// identifiers or internal error text to the storefront client.
		detail := err.Error()
		switch {
		case strings.Contains(detail, "delivery address, zone, slot and date are required"):
			message, code = "请重新选择完整的收货地址、配送区域和配送时段", "DELIVERY_SELECTION_REQUIRED"
		case strings.Contains(detail, "address does not match delivery zone"):
			message, code = "当前收货地址不在所选配送区域，请更换地址", "ADDRESS_ZONE_MISMATCH"
		case strings.Contains(detail, "slot does not match delivery zone"):
			message, code = "所选配送时段不属于当前配送区域，请重新选择", "SLOT_ZONE_MISMATCH"
		case strings.Contains(detail, "selected date does not match slot weekday"):
			message, code = "所选配送日期与配送时段不匹配，请重新选择", "SLOT_DATE_MISMATCH"
		case strings.Contains(detail, "slot cutoff has passed"):
			message, code = "所选配送时段已截止，请重新选择", "SLOT_CUTOFF_PASSED"
		case strings.Contains(detail, "delivery slot is full"):
			message, code = "所选配送时段已满，请重新选择", "SLOT_FULL"
		case strings.Contains(detail, "goods subtotal is below minimum"):
			message, code = "商品金额未达到起送金额", "MIN_AMOUNT_NOT_REACHED"
		case strings.Contains(detail, "delivery is disabled"):
			message, code = "当前门店暂未开放配送", "DELIVERY_DISABLED"
		case strings.Contains(detail, "pickup is disabled"):
			message, code = "当前门店暂未开放到店自取", "PICKUP_DISABLED"
		case strings.Contains(detail, "shipping address is required"):
			message, code = "请先选择收货地址", "ADDRESS_REQUIRED"
		}
	case errors.Is(err, service.ErrTenantUnavailable), errors.Is(err, service.ErrBusinessCapabilityInactive), errors.Is(err, service.ErrCapabilityInactive):
		status, message, code = http.StatusForbidden, "当前业务未授权使用配送服务", "DELIVERY_NOT_AUTHORIZED"
	case errors.Is(err, service.ErrCommerceStorefrontUnauthenticated):
		status, message, code = http.StatusUnauthorized, "小程序登录已失效", "STOREFRONT_SESSION_EXPIRED"
	case errors.Is(err, service.ErrCommerceStorefrontOwnership):
		status, message, code = http.StatusNotFound, "地址或购物车不存在", "CHECKOUT_RESOURCE_NOT_FOUND"
	}
	ctx.JSON(status, gin.H{"code": code, "error": message})
}
