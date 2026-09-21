package api

import (
	"errors"
	"net/http"
	"strings"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// CommerceLogisticsController exposes the retail package timeline boundary.
// Admin methods derive tenant scope from JWT middleware. Storefront methods
// first resolve the order through the opaque customer session and only then
// pass the server-owned tenant/customer/order identity to the logistics
// service.
type CommerceLogisticsController struct {
	Service    service.CommerceLogisticsService
	Storefront *service.CommerceStorefrontService
}

func (c *CommerceLogisticsController) CreateShipment(ctx *gin.Context) {
	orderID, err := parseLogisticsPathID(ctx, "orderID")
	if err != nil {
		return
	}
	var input service.CreateCommerceShipmentInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "物流包裹信息格式不正确"})
		return
	}
	// The order identity is part of the authenticated route, not client JSON.
	input.OrderID = orderID
	// This is the tenant-operator endpoint. Provider identities are reserved for
	// authenticated carrier adapters, and the ordinary "confirm shipment" action
	// must create the package, first event, and order projection atomically.
	input.Source = "manual"
	input.MarkShipped = true
	shipment, err := c.Service.CreateShipment(ctx.GetUint("tenant_id"), input)
	if err != nil {
		commerceLogisticsError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, shipment)
}

func (c *CommerceLogisticsController) UpdateShipment(ctx *gin.Context) {
	shipmentID, err := parseLogisticsPathID(ctx, "shipmentID")
	if err != nil {
		return
	}
	var input service.UpdateCommerceShipmentInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "物流包裹信息格式不正确"})
		return
	}
	shipment, err := c.Service.UpdateShipment(ctx.GetUint("tenant_id"), shipmentID, input)
	if err != nil {
		commerceLogisticsError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, shipment)
}

func (c *CommerceLogisticsController) AppendManualEvent(ctx *gin.Context) {
	shipmentID, err := parseLogisticsPathID(ctx, "shipmentID")
	if err != nil {
		return
	}
	var input service.AppendCommerceShipmentEventInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "物流节点信息格式不正确"})
		return
	}
	// This endpoint is specifically for operator-entered nodes. The source is
	// forced server-side so a client cannot turn a manual write into a provider
	// event or bypass provider identity requirements. Provider event identities
	// are discarded as well; a manual node must be replayed with its own
	// idempotency key and must never claim an upstream event.
	input.Source = "manual"
	input.ProviderEventID = ""
	input.OccurredAt = time.Time{}
	input.PayloadHash = ""
	input.PayloadJSON = ""
	input.AllowExceptionRecovery = false
	event, err := c.Service.AppendShipmentEvent(ctx.GetUint("tenant_id"), shipmentID, input)
	if err != nil {
		commerceLogisticsError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, event)
}

func (c *CommerceLogisticsController) ShipmentTimeline(ctx *gin.Context) {
	shipmentID, err := parseLogisticsPathID(ctx, "shipmentID")
	if err != nil {
		return
	}
	timeline, err := c.Service.ListShipmentTimeline(ctx.GetUint("tenant_id"), shipmentID)
	if err != nil {
		commerceLogisticsError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, timeline)
}

func (c *CommerceLogisticsController) AdminOrderTimeline(ctx *gin.Context) {
	orderID, err := parseLogisticsPathID(ctx, "orderID")
	if err != nil {
		return
	}
	timeline, err := c.Service.AdminOrderTimeline(ctx.GetUint("tenant_id"), orderID)
	if err != nil {
		commerceLogisticsError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, timeline)
}

func (c *CommerceLogisticsController) CustomerOrderTimeline(ctx *gin.Context) {
	if c.Storefront == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "物流查询暂不可用"})
		return
	}
	token := commerceStorefrontBearerToken(ctx)
	orderNo := strings.TrimSpace(ctx.Param("orderNo"))
	order, err := c.Storefront.GetOrderByNo(token, orderNo)
	if err != nil {
		commerceStorefrontError(ctx, err)
		return
	}
	timeline, err := c.Service.ListCustomerShipmentTimeline(order.TenantID, order.CustomerID, order.ID)
	if err != nil {
		commerceLogisticsStorefrontError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, timeline)
}

func parseLogisticsPathID(ctx *gin.Context, name string) (uint, error) {
	id, err := parsePathID(ctx, name)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "无效的物流编号"})
		return 0, err
	}
	return id, nil
}

func commerceLogisticsError(ctx *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "物流操作失败"
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, message = http.StatusNotFound, "物流包裹不存在"
	case errors.Is(err, service.ErrCommerceLogisticsConflict):
		status, message = http.StatusConflict, "物流包裹或节点已存在且内容冲突"
	case errors.Is(err, service.ErrCommerceLogisticsInvalid), errors.Is(err, service.ErrCommerceLogisticsState):
		status, message = http.StatusBadRequest, "物流包裹或节点状态不符合要求"
	}
	ctx.JSON(status, gin.H{"error": message})
}

func commerceLogisticsStorefrontError(ctx *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "物流记录不存在"})
		return
	}
	commerceStorefrontError(ctx, err)
}
