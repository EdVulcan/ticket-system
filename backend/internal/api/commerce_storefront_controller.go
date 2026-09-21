package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"ticket-backend/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// CommerceStorefrontController is the public customer boundary for the
// independent commercial domain. It has no JWT middleware and never accepts
// tenant, customer, channel, business, location, amount, or payment-status
// authority from request JSON.
type CommerceStorefrontController struct {
	Service service.CommerceStorefrontService
	Payment service.CommercePaymentService
}

func (c *CommerceStorefrontController) ListChannelAccounts(ctx *gin.Context) {
	rows, err := c.Service.ListChannelAccounts(ctx.GetUint("tenant_id"))
	if err != nil {
		commerceStorefrontAdminError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": len(rows)})
}

// ListBindings and SaveBinding are tenant-admin configuration endpoints. They
// are registered behind JWT/capability middleware; they are deliberately kept
// separate from the public bearer-session handlers below.
func (c *CommerceStorefrontController) ListBindings(ctx *gin.Context) {
	rows, err := c.Service.ListBindings(ctx.GetUint("tenant_id"), ctx.Query("business_type"))
	if err != nil {
		commerceStorefrontAdminError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": len(rows)})
}

func (c *CommerceStorefrontController) SaveBinding(ctx *gin.Context) {
	var input service.CommerceStorefrontBindingInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "小程序发布配置格式不正确"})
		return
	}
	if raw := strings.TrimSpace(ctx.Param("bindingID")); raw != "" {
		bindingID, parseErr := strconv.ParseUint(raw, 10, 32)
		if parseErr != nil || bindingID == 0 {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "无效的小程序发布配置编号"})
			return
		}
		input.ID = uint(bindingID)
	}
	row, err := c.Service.SaveBinding(ctx.GetUint("tenant_id"), input, ctx.GetUint("user_id"), ctx.GetString("role"))
	if err != nil {
		commerceStorefrontAdminError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": row})
}

func (c *CommerceStorefrontController) Login(ctx *gin.Context) {
	var input service.CommerceStorefrontLoginInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "小程序登录参数不完整"})
		return
	}
	result, err := c.Service.Login(ctx.Request.Context(), input)
	if err != nil {
		if logger.Log != nil {
			logger.Log.Warn("wechat storefront session rejected",
				zap.String("app_id", strings.TrimSpace(input.AppID)),
				zap.String("remote_addr", ctx.Request.RemoteAddr),
				zap.String("error", err.Error()),
			)
		}
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *CommerceStorefrontController) Catalog(ctx *gin.Context) {
	catalog, err := c.Service.ListCatalog(commerceStorefrontBearerToken(ctx), commerceStorefrontBusinessType(ctx))
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, catalog)
}

func (c *CommerceStorefrontController) GetCart(ctx *gin.Context) {
	var (
		cart *model.CommerceCart
		err  error
	)
	if raw := strings.TrimSpace(ctx.Param("cartID")); raw != "" {
		cartID, parseErr := parseStorefrontID(ctx, "cartID")
		if parseErr != nil {
			return
		}
		cart, err = c.Service.GetCartByID(commerceStorefrontBearerToken(ctx), cartID, commerceStorefrontBusinessType(ctx))
	} else {
		cart, err = c.Service.GetCart(commerceStorefrontBearerToken(ctx), commerceStorefrontBusinessType(ctx))
	}
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, cart)
}

func (c *CommerceStorefrontController) ListAddresses(ctx *gin.Context) {
	rows, err := c.Service.ListAddresses(commerceStorefrontBearerToken(ctx))
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *CommerceStorefrontController) SaveAddress(ctx *gin.Context) {
	var input service.CommerceStorefrontAddressInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "地址信息格式不正确"})
		return
	}
	if raw := strings.TrimSpace(ctx.Param("addressID")); raw != "" {
		addressID, parseErr := parseStorefrontPathID(ctx, "addressID", "无效的地址编号")
		if parseErr != nil {
			return
		}
		input.ID = addressID
	}
	row, err := c.Service.SaveAddress(commerceStorefrontBearerToken(ctx), input)
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": row})
}

func (c *CommerceStorefrontController) DeleteAddress(ctx *gin.Context) {
	addressID, err := parseStorefrontPathID(ctx, "addressID", "无效的地址编号")
	if err != nil {
		return
	}
	if err := c.Service.DeleteAddress(commerceStorefrontBearerToken(ctx), addressID); err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	// Write the empty response immediately. Calling Status alone only sets the
	// pending writer code; direct handler tests and some middleware chains can
	// otherwise flush the default 200 before the response is committed.
	ctx.AbortWithStatus(http.StatusNoContent)
}

func (c *CommerceStorefrontController) SetDefaultAddress(ctx *gin.Context) {
	addressID, err := parseStorefrontPathID(ctx, "addressID", "无效的地址编号")
	if err != nil {
		return
	}
	row, err := c.Service.SetDefaultAddress(commerceStorefrontBearerToken(ctx), addressID)
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": row})
}

func (c *CommerceStorefrontController) AddCartItem(ctx *gin.Context) {
	var input service.CommerceStorefrontCartItemInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "购物车商品信息格式不正确"})
		return
	}
	var (
		cart *model.CommerceCart
		err  error
	)
	if raw := strings.TrimSpace(ctx.Param("cartID")); raw != "" {
		cartID, parseErr := parseStorefrontID(ctx, "cartID")
		if parseErr != nil {
			return
		}
		cart, err = c.Service.AddCartItemToCart(commerceStorefrontBearerToken(ctx), cartID, input, commerceStorefrontBusinessType(ctx))
	} else {
		cart, err = c.Service.AddCartItem(commerceStorefrontBearerToken(ctx), input, commerceStorefrontBusinessType(ctx))
	}
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, cart)
}

func (c *CommerceStorefrontController) UpdateCartItem(ctx *gin.Context) {
	itemID, err := parseStorefrontID(ctx, "itemID")
	if err != nil {
		return
	}
	var input struct {
		Quantity int `json:"quantity"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "购物车数量格式不正确"})
		return
	}
	var cart *model.CommerceCart
	if raw := strings.TrimSpace(ctx.Param("cartID")); raw != "" {
		cartID, parseErr := parseStorefrontID(ctx, "cartID")
		if parseErr != nil {
			return
		}
		cart, err = c.Service.UpdateCartItemToCart(commerceStorefrontBearerToken(ctx), cartID, itemID, input.Quantity, commerceStorefrontBusinessType(ctx))
	} else {
		cart, err = c.Service.UpdateCartItem(commerceStorefrontBearerToken(ctx), itemID, input.Quantity, commerceStorefrontBusinessType(ctx))
	}
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, cart)
}

func (c *CommerceStorefrontController) RemoveCartItem(ctx *gin.Context) {
	itemID, err := parseStorefrontID(ctx, "itemID")
	if err != nil {
		return
	}
	var removeErr error
	if raw := strings.TrimSpace(ctx.Param("cartID")); raw != "" {
		cartID, parseErr := parseStorefrontID(ctx, "cartID")
		if parseErr != nil {
			return
		}
		removeErr = c.Service.RemoveCartItemFromCart(commerceStorefrontBearerToken(ctx), cartID, itemID, commerceStorefrontBusinessType(ctx))
	} else {
		removeErr = c.Service.RemoveCartItem(commerceStorefrontBearerToken(ctx), itemID, commerceStorefrontBusinessType(ctx))
	}
	if removeErr != nil {
		commerceStorefrontError(ctx, removeErr)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func (c *CommerceStorefrontController) Checkout(ctx *gin.Context) {
	var input service.CommerceStorefrontCheckoutInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "结算信息格式不正确"})
		return
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		input.IdempotencyKey = strings.TrimSpace(ctx.GetHeader("Idempotency-Key"))
	}
	var (
		order *model.CommerceOrder
		err   error
	)
	if raw := strings.TrimSpace(ctx.Param("cartID")); raw != "" {
		cartID, parseErr := parseStorefrontID(ctx, "cartID")
		if parseErr != nil {
			return
		}
		order, err = c.Service.CheckoutCartByID(commerceStorefrontBearerToken(ctx), cartID, input, commerceStorefrontBusinessType(ctx))
	} else {
		order, err = c.Service.Checkout(commerceStorefrontBearerToken(ctx), input, commerceStorefrontBusinessType(ctx))
	}
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, order)
}

func (c *CommerceStorefrontController) ListOrders(ctx *gin.Context) {
	page := storefrontQueryInt(ctx, "page", 1)
	pageSize := storefrontQueryInt(ctx, "page_size", 20)
	result, err := c.Service.ListOrders(commerceStorefrontBearerToken(ctx), page, pageSize, commerceStorefrontBusinessType(ctx))
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *CommerceStorefrontController) GetOrder(ctx *gin.Context) {
	orderNo := strings.TrimSpace(ctx.Param("orderNo"))
	order, err := c.Service.GetOrderByNo(commerceStorefrontBearerToken(ctx), orderNo)
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, order)
}

func (c *CommerceStorefrontController) CancelOrder(ctx *gin.Context) {
	order, err := c.Service.CancelOrder(commerceStorefrontBearerToken(ctx), ctx.Param("orderNo"))
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, order)
}

func (c *CommerceStorefrontController) ConfirmReceipt(ctx *gin.Context) {
	order, err := c.Service.ConfirmReceipt(commerceStorefrontBearerToken(ctx), ctx.Param("orderNo"))
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, order)
}

func (c *CommerceStorefrontController) RequestRefund(ctx *gin.Context) {
	var input service.CommerceStorefrontRefundInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "退款申请信息格式不正确"})
		return
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		input.IdempotencyKey = strings.TrimSpace(ctx.GetHeader("Idempotency-Key"))
	}
	result, err := c.Service.RequestRefund(commerceStorefrontBearerToken(ctx), ctx.Param("orderNo"), input)
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	// Starting the provider refund is a separate idempotent step. A missing or
	// temporarily unavailable payment provider leaves the request visible as
	// requested and recoverable by the worker; it must never be reported as a
	// completed refund from this customer endpoint.
	if result != nil && result.Request != nil {
		_, _ = c.Payment.StartWechatRefund(ctx.Request.Context(), result.Request.TenantID, result.Request.ID)
	}
	ctx.JSON(http.StatusAccepted, result)
}

func (c *CommerceStorefrontController) CreatePayment(ctx *gin.Context) {
	var input service.CommercePaymentRequest
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "支付参数格式不正确"})
		return
	}
	if strings.TrimSpace(input.OrderNo) == "" {
		input.OrderNo = strings.TrimSpace(ctx.Param("orderNo"))
	}
	if strings.TrimSpace(input.ClientRequestID) == "" {
		input.ClientRequestID = strings.TrimSpace(ctx.GetHeader("Idempotency-Key"))
	}
	result, err := c.Payment.CreateWechatPayment(ctx.Request.Context(), commerceStorefrontBearerToken(ctx), input)
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *CommerceStorefrontController) QueryPayment(ctx *gin.Context) {
	result, err := c.Payment.GetPaymentStatus(ctx.Request.Context(), commerceStorefrontBearerToken(ctx), ctx.Param("orderNo"))
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *CommerceStorefrontController) WechatPaymentNotify(ctx *gin.Context) {
	if err := c.Payment.HandleWechatNotify(ctx.Request.Context(), parseTenantPathID(ctx), ctx.Request, false); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "成功"})
}

func (c *CommerceStorefrontController) WechatRefundNotify(ctx *gin.Context) {
	if err := c.Payment.HandleWechatNotify(ctx.Request.Context(), parseTenantPathID(ctx), ctx.Request, true); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "成功"})
}

func parseTenantPathID(ctx *gin.Context) uint {
	value, err := strconv.ParseUint(strings.TrimSpace(ctx.Param("tenantID")), 10, 32)
	if err != nil || value == 0 {
		return 0
	}
	return uint(value)
}

func commerceStorefrontBearerToken(ctx *gin.Context) string {
	header := strings.TrimSpace(ctx.GetHeader("Authorization"))
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// commerceStorefrontBusinessType selects the business context for a shared
// storefront session. It is a routing hint only; the service must verify that
// the account session is authorized for the selected business and resolve the
// tenant/location server-side. The query parameter is the public contract;
// the header is a convenient fallback for clients that centralize request
// metadata outside the URL.
func commerceStorefrontBusinessType(ctx *gin.Context) string {
	if value := strings.TrimSpace(ctx.Query("business_type")); value != "" {
		return value
	}
	return strings.TrimSpace(ctx.GetHeader("X-Commerce-Business-Type"))
}

func parseStorefrontID(ctx *gin.Context, name string) (uint, error) {
	return parseStorefrontPathID(ctx, name, "无效的购物车商品编号")
}

func parseStorefrontPathID(ctx *gin.Context, name, message string) (uint, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(ctx.Param(name)), 10, 32)
	if err != nil || value == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": message})
		return 0, gorm.ErrRecordNotFound
	}
	return uint(value), nil
}

func storefrontQueryInt(ctx *gin.Context, name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(ctx.Query(name)))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func commerceStorefrontError(ctx *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "商业店铺暂时不可用"
	switch {
	case errors.Is(err, service.ErrCommerceStorefrontUnauthenticated):
		status, message = http.StatusUnauthorized, "登录状态已失效，请重新进入小程序"
	case errors.Is(err, service.ErrCommerceStorefrontAmbiguous):
		status, message = http.StatusConflict, "该小程序同时发布了多个业务，请指定业务类型"
	case errors.Is(err, service.ErrCommerceStorefrontUnavailable):
		status, message = http.StatusServiceUnavailable, "当前小程序暂未开放"
	case errors.Is(err, service.ErrTenantUnavailable), errors.Is(err, service.ErrCapabilityInactive), errors.Is(err, service.ErrBusinessCapabilityInactive):
		status, message = http.StatusForbidden, "当前商户未启用该商业能力"
	case errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, service.ErrCommerceStorefrontOwnership):
		status, message = http.StatusNotFound, "商业资源不存在"
	case errors.Is(err, service.ErrCommerceIdempotencyConflict):
		status, message = http.StatusConflict, "重复请求的参数不一致"
	case errors.Is(err, service.ErrCommerceStorefrontInvalid), errors.Is(err, service.ErrCommerceStorefrontAddressInvalid), errors.Is(err, service.ErrCommerceCartInvalid), errors.Is(err, service.ErrCommerceOrderInvalid), errors.Is(err, service.ErrCommerceOrderState), errors.Is(err, service.ErrCommerceRefundInvalid), errors.Is(err, service.ErrCommerceInventoryShortage):
		status, message = http.StatusBadRequest, "商业订单或购物车信息不符合要求"
	case errors.Is(err, service.ErrCommercePaymentUnavailable):
		status, message = http.StatusNotImplemented, "支付渠道尚未接入"
	case errors.Is(err, service.ErrCommercePaymentInvalid):
		status, message = http.StatusBadRequest, "支付凭据或金额校验失败"
	case errors.Is(err, service.ErrCommercePaymentManualReview):
		status, message = http.StatusConflict, "支付结果正在核实，请稍后刷新订单"
	}
	ctx.JSON(status, gin.H{"error": message})
}

func commerceStorefrontAdminError(ctx *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "小程序发布配置保存失败"
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, message = http.StatusNotFound, "小程序发布配置不存在"
	case errors.Is(err, service.ErrTenantUnavailable), errors.Is(err, service.ErrBusinessCapabilityInactive), errors.Is(err, service.ErrCapabilityInactive):
		status, message = http.StatusForbidden, "当前商户未启用该商业能力"
	case errors.Is(err, service.ErrCommerceStorefrontBindingInvalid), errors.Is(err, service.ErrCommerceStorefrontBindingAccount):
		status, message = http.StatusBadRequest, "小程序渠道账号、业务能力或履约地点配置不正确"
	}
	ctx.JSON(status, gin.H{"error": message})
}
