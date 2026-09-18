package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// CommerceOrderController exposes the independent restaurant/retail order
// lifecycle. It never delegates to the scenic ticket order controller.
type CommerceOrderController struct {
	Service service.CommerceOrderService
}

func (c *CommerceOrderController) Create(ctx *gin.Context) {
	var input service.CreateCommerceOrderInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "商业订单信息格式不正确"})
		return
	}
	order, err := c.Service.CreateOrder(ctx.GetUint("tenant_id"), input)
	if err != nil {
		commerceOrderError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, order)
}

func (c *CommerceOrderController) List(ctx *gin.Context) {
	page, pageErr := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, pageSizeErr := strconv.Atoi(ctx.DefaultQuery("page_size", "50"))
	if pageErr != nil || page < 1 {
		page = 1
	}
	if pageSizeErr != nil || pageSize < 1 {
		pageSize = 50
	}
	rows, total, err := c.Service.ListOrdersPage(ctx.GetUint("tenant_id"), service.CommerceOrderListFilter{
		BusinessType: ctx.Query("business_type"), Search: ctx.Query("search"), PaymentStatus: ctx.Query("payment_status"),
		FulfillmentStatus: ctx.Query("fulfillment_status"), RefundStatus: ctx.Query("refund_status"),
		Page: page, PageSize: pageSize,
	})
	if err != nil {
		commerceOrderError(ctx, err)
		return
	}
	if !authz.HasTenantPermission(ctx.GetString("role"), authz.PermissionAfterSalesRead) {
		for i := range rows {
			rows[i].AfterSales = nil
		}
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *CommerceOrderController) Get(ctx *gin.Context) {
	id, err := parseCommerceOrderID(ctx)
	if err != nil {
		return
	}
	order, err := c.Service.GetOrder(ctx.GetUint("tenant_id"), id)
	if err != nil {
		commerceOrderError(ctx, err)
		return
	}
	if !authz.HasTenantPermission(ctx.GetString("role"), authz.PermissionAfterSalesRead) {
		order.AfterSales = nil
	}
	ctx.JSON(http.StatusOK, order)
}

// ConfirmPayment is a local payment-adapter callback boundary. A real
// payment adapter must call this only after verifying its provider response;
// the endpoint is kept tenant-admin controlled and does not accept an amount
// from the client.
func (c *CommerceOrderController) ConfirmPayment(ctx *gin.Context) {
	id, err := parseCommerceOrderID(ctx)
	if err != nil {
		return
	}
	order, err := c.Service.ConfirmPayment(ctx.GetUint("tenant_id"), id)
	if err != nil {
		commerceOrderError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, order)
}

func (c *CommerceOrderController) RequestRefund(ctx *gin.Context) {
	id, err := parseCommerceOrderID(ctx)
	if err != nil {
		return
	}
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		Reason         string `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "退款申请信息格式不正确"})
		return
	}
	if strings.TrimSpace(body.IdempotencyKey) == "" {
		body.IdempotencyKey = strings.TrimSpace(ctx.GetHeader("Idempotency-Key"))
	}
	result, err := c.Service.RequestRefund(ctx.GetUint("tenant_id"), id, body.IdempotencyKey, body.Reason)
	if err != nil {
		commerceOrderError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, result)
}

func (c *CommerceOrderController) CompleteRefund(ctx *gin.Context) {
	requestID, err := parsePathID(ctx, "requestID")
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "无效的售后申请编号"})
		return
	}
	result, err := c.Service.CompleteRefund(ctx.GetUint("tenant_id"), requestID)
	if err != nil {
		commerceOrderError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *CommerceOrderController) RestaurantFulfillment(ctx *gin.Context) {
	id, err := parseCommerceOrderID(ctx)
	if err != nil {
		return
	}
	var input service.RestaurantFulfillmentTransitionInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "餐饮履约状态格式不正确"})
		return
	}
	input.ActorUserID = ctx.GetUint("user_id")
	input.ActorRole = ctx.GetString("role")
	row, err := c.Service.TransitionRestaurantFulfillment(ctx.GetUint("tenant_id"), id, input)
	if err != nil {
		commerceOrderError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *CommerceOrderController) RetailFulfillment(ctx *gin.Context) {
	id, err := parseCommerceOrderID(ctx)
	if err != nil {
		return
	}
	var input service.RetailFulfillmentTransitionInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "电商履约状态格式不正确"})
		return
	}
	input.ActorUserID = ctx.GetUint("user_id")
	input.ActorRole = ctx.GetString("role")
	row, err := c.Service.TransitionRetailFulfillment(ctx.GetUint("tenant_id"), id, input)
	if err != nil {
		commerceOrderError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func parseCommerceOrderID(ctx *gin.Context) (uint, error) {
	return parsePathID(ctx, "id")
}

func parsePathID(ctx *gin.Context, name string) (uint, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(ctx.Param(name)), 10, 32)
	if err != nil || value == 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return uint(value), nil
}

func commerceOrderError(ctx *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "商业订单操作失败"
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, message = http.StatusNotFound, "商业订单不存在"
	case errors.Is(err, service.ErrBusinessCapabilityInactive), errors.Is(err, service.ErrCapabilityInactive), errors.Is(err, service.ErrTenantUnavailable):
		status, message = http.StatusForbidden, "当前商户未启用该商业能力"
	case errors.Is(err, service.ErrCommercePaymentUnavailable):
		status, message = http.StatusNotImplemented, "支付渠道尚未接入，退款等待支付渠道确认"
	case errors.Is(err, service.ErrCommercePaymentManualReview):
		status, message = http.StatusConflict, "支付结果需要人工复核"
	case errors.Is(err, service.ErrCommercePaymentInvalid):
		status, message = http.StatusBadRequest, "支付渠道返回结果不符合订单金额或凭据"
	case errors.Is(err, service.ErrCommerceOrderInvalid), errors.Is(err, service.ErrCommerceOrderState), errors.Is(err, service.ErrCommerceInventoryShortage), errors.Is(err, service.ErrCommerceRefundInvalid), errors.Is(err, service.ErrCommerceIdempotencyConflict):
		status, message = http.StatusBadRequest, "商业订单状态或信息不符合要求"
	}
	ctx.JSON(status, gin.H{"error": message})
}
