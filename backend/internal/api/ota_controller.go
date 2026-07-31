package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

type OTAController struct {
	OrderService   service.OrderService
	ProductService service.ProductService
	Gateway        *service.ChannelGatewayService
}

// Response Wrapper
func OTAResponse(ctx *gin.Context, err error, data interface{}) {
	if err != nil {
		ctx.JSON(http.StatusOK, gin.H{
			"code": 500,
			"msg":  err.Error(),
			"data": nil,
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "success",
		"data": data,
	})
}

// 1. List Products
func (c *OTAController) ListProducts(ctx *gin.Context) {
	// OTA sees products of the Tenant (identified by app_key)
	tenantID := ctx.GetUint("tenant_id")

	// Default paging
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "50"))

	// Reuse ProductService List
	// We only show "online" products (Ticket Type)
	var products []model.Product
	var total int64
	channelAccountID := ctx.GetUint("channel_account_id")
	var err error
	if channelAccountID > 0 {
		var productIDs []uint
		if err = model.DB.Table("channel_product_mappings").Where("channel_account_id = ? AND status = ?", channelAccountID, "active").Pluck("product_id", &productIDs).Error; err == nil && len(productIDs) > 0 {
			query := model.DB.Where("tenant_id = ? AND status = ? AND id IN ?", tenantID, "online", productIDs)
			err = query.Count(&total).Error
			if err == nil {
				err = query.Order("id ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&products).Error
			}
		} else if err == nil {
			products = []model.Product{}
		}
	} else {
		products, total, err = c.ProductService.List(page, pageSize, tenantID, "online")
	}

	if err != nil {
		OTAResponse(ctx, err, nil)
		return
	}

	// Transform to Simplified OTA Model
	var list []gin.H
	for _, p := range products {
		list = append(list, gin.H{
			"product_id":    p.ID,
			"name":          p.Name,
			"price":         p.Price,
			"stock":         p.DailyStock, // Simplified
			"validity_type": p.ValidityType,
			"validity_days": p.ValidityDays,
		})
	}

	OTAResponse(ctx, nil, gin.H{
		"list":  list,
		"total": total,
	})
}

// 2. Create Order
type OTACreateOrderRequest struct {
	ProductID           uint   `json:"product_id"`
	ExternalProductCode string `json:"external_product_code"`
	Quantity            int    `json:"quantity" binding:"required"`
	Date                string `json:"date"` // Visit Date YYYY-MM-DD
	ContactName         string `json:"contact_name"`
	ContactPhone        string `json:"contact_phone"`
	ExternalNo          string `json:"external_no" binding:"required"` // OTA's Order No
}

func (c *OTAController) CreateOrder(ctx *gin.Context) {
	var req OTACreateOrderRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		OTAResponse(ctx, err, nil)
		return
	}

	tenantID := ctx.GetUint("tenant_id")
	if tenantID == 0 {
		tenant := ctx.MustGet("tenant").(model.Tenant)
		tenantID = tenant.ID
	}
	if ctx.GetUint("channel_account_id") > 0 {
		if req.ExternalProductCode == "" {
			OTAResponse(ctx, fmt.Errorf("external_product_code is required for channel orders"), nil)
			return
		}
		var mapping model.ChannelProductMapping
		if err := model.DB.Where("channel_account_id = ? AND external_code = ? AND status = ?", ctx.GetUint("channel_account_id"), req.ExternalProductCode, "active").First(&mapping).Error; err != nil {
			OTAResponse(ctx, fmt.Errorf("external product is not mapped"), nil)
			return
		}
		req.ProductID = mapping.ProductID
	}
	if req.ProductID == 0 {
		OTAResponse(ctx, fmt.Errorf("product_id or external_product_code is required"), nil)
		return
	}
	channel := "ota"
	if value := ctx.GetString("channel_code"); value != "" {
		channel = value
	}

	var useDate *time.Time
	if req.Date != "" {
		parsed, err := time.ParseInLocation("2006-01-02", req.Date, time.Local)
		if err != nil {
			OTAResponse(ctx, fmt.Errorf("invalid visit date"), nil)
			return
		}
		useDate = &parsed
	}
	externalNo := req.ExternalNo
	if channelAccountID := ctx.GetUint("channel_account_id"); channelAccountID > 0 {
		if c.Gateway == nil {
			OTAResponse(ctx, errors.New("channel gateway is not configured"), nil)
			return
		}
		response, err := c.Gateway.CreateOrder(ctx, ctx.GetString("channel_type"), service.ChannelCreateOrderRequest{
			TenantID: tenantID, AccountID: channelAccountID, Channel: channel, ExternalNo: req.ExternalNo,
			ExternalProductCode: req.ExternalProductCode, Quantity: req.Quantity, UseDate: useDate,
			ContactName: req.ContactName, ContactPhone: req.ContactPhone, TTL: service.DefaultOrderReservationTTL,
		})
		if err != nil {
			OTAResponse(ctx, err, nil)
			return
		}
		if response.Order == nil {
			OTAResponse(ctx, errors.New("channel adapter returned an empty order"), nil)
			return
		}
		OTAResponse(ctx, nil, gin.H{"order_no": response.Order.OrderNo, "order_status": response.Status, "ticket_codes": response.TicketCodes, "external_no": response.ExternalNo})
		return
	}
	order := model.Order{
		TenantID:         tenantID,
		ContactName:      req.ContactName,
		ContactPhone:     req.ContactPhone,
		Channel:          channel,
		ChannelAccountID: ctx.GetUint("channel_account_id"),
		ExternalNo:       &externalNo,
		Items: []model.OrderItem{
			{
				ProductID: req.ProductID,
				Quantity:  req.Quantity,
				UseDate:   useDate,
			},
		},
	}

	if err := c.OrderService.Create(&order); err != nil {
		if errors.Is(err, service.ErrDuplicateExternalOrder) {
			existing, findErr := c.OrderService.GetByExternalNo(req.ExternalNo, channel, tenantID)
			if findErr != nil {
				OTAResponse(ctx, findErr, nil)
				return
			}
			if !sameExternalOrder(existing, &order) {
				OTAResponse(ctx, fmt.Errorf("external order number was already used with different order data"), nil)
				return
			}
			order = *existing
		} else {
			OTAResponse(ctx, err, nil)
			return
		}
	}
	// OTA orders are prepaid by the upstream channel. Repeating the same external
	// number is idempotent and can safely complete a prior interrupted request.
	if err := c.OrderService.MarkAsPaid(order.OrderNo, tenantID); err != nil {
		OTAResponse(ctx, err, nil)
		return
	}
	order.Status = "paid"

	OTAResponse(ctx, nil, gin.H{
		"order_no":     order.OrderNo,
		"order_status": order.Status,
		// Return first ticket info?
		// "ticket_codes": ...
	})
}

func sameExternalOrder(existing, requested *model.Order) bool {
	if existing == nil || requested == nil || existing.ContactName != requested.ContactName || existing.ContactPhone != requested.ContactPhone || len(existing.Items) != len(requested.Items) {
		return false
	}
	for i := range existing.Items {
		a, b := existing.Items[i], requested.Items[i]
		if a.ProductID != b.ProductID || a.Quantity != b.Quantity {
			return false
		}
		if (a.UseDate == nil) != (b.UseDate == nil) {
			return false
		}
		if a.UseDate != nil && !startOfDayAPI(*a.UseDate).Equal(startOfDayAPI(*b.UseDate)) {
			return false
		}
	}
	return true
}

func startOfDayAPI(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
}

// 3. Cancel Order
type OTACancelRequest struct {
	OrderNo string `json:"order_no" binding:"required"`
}

func (c *OTAController) CancelOrder(ctx *gin.Context) {
	var req OTACancelRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		OTAResponse(ctx, err, nil)
		return
	}

	// Verify Tenant matches Order?
	tenantID := ctx.GetUint("tenant_id")
	channel := ctx.GetString("channel_code")
	if channel == "" {
		channel = "ota"
	}
	if accountID := ctx.GetUint("channel_account_id"); accountID > 0 {
		if c.Gateway == nil {
			OTAResponse(ctx, errors.New("channel gateway is not configured"), nil)
			return
		}
		if err := c.Gateway.CancelOrder(ctx, ctx.GetString("channel_type"), service.ChannelCancelRequest{TenantID: tenantID, AccountID: accountID, Channel: channel, ExternalNo: req.OrderNo, Reason: "channel request"}); err != nil {
			OTAResponse(ctx, err, nil)
			return
		}
		OTAResponse(ctx, nil, gin.H{"external_no": req.OrderNo, "status": "cancelled"})
		return
	}
	order, err := c.OrderService.GetByOrderNo(req.OrderNo, tenantID)
	if err != nil {
		OTAResponse(ctx, fmt.Errorf("order not found"), nil)
		return
	}
	if order.Channel != channel {
		OTAResponse(ctx, fmt.Errorf("order not found"), nil)
		return
	}

	if err := c.OrderService.Cancel(req.OrderNo, tenantID); err != nil {
		OTAResponse(ctx, err, nil)
		return
	}

	OTAResponse(ctx, nil, "cancelled")
}

// 4. Query Order
func (c *OTAController) QueryOrder(ctx *gin.Context) {
	// Can be POST with body or GET with param. Plan said POST.
	var req OTACancelRequest // Reuse struct { OrderNo }
	if err := ctx.ShouldBindJSON(&req); err != nil {
		OTAResponse(ctx, err, nil)
		return
	}

	tenantID := ctx.GetUint("tenant_id")
	channel := ctx.GetString("channel_code")
	if channel == "" {
		channel = "ota"
	}
	if accountID := ctx.GetUint("channel_account_id"); accountID > 0 {
		if c.Gateway == nil {
			OTAResponse(ctx, errors.New("channel gateway is not configured"), nil)
			return
		}
		response, err := c.Gateway.QueryOrder(ctx, ctx.GetString("channel_type"), service.ChannelQueryRequest{TenantID: tenantID, AccountID: accountID, Channel: channel, ExternalNo: req.OrderNo})
		if err != nil {
			OTAResponse(ctx, fmt.Errorf("order not found"), nil)
			return
		}
		if response.Order == nil {
			OTAResponse(ctx, errors.New("channel adapter returned an empty order"), nil)
			return
		}
		OTAResponse(ctx, nil, gin.H{"order_no": response.Order.OrderNo, "external_no": response.ExternalNo, "status": response.Status, "ticket_codes": response.TicketCodes})
		return
	}
	order, err := c.OrderService.GetByOrderNo(req.OrderNo, tenantID)
	if err != nil {
		OTAResponse(ctx, fmt.Errorf("order not found"), nil)
		return
	}
	if order.Channel != channel {
		OTAResponse(ctx, fmt.Errorf("order not found"), nil)
		return
	}

	// Format Response
	tickets := []gin.H{}
	for _, item := range order.Items {
		for _, t := range item.Tickets {
			tickets = append(tickets, gin.H{
				"code":   t.TicketCode,
				"status": t.Status,
			})
		}
	}

	OTAResponse(ctx, nil, gin.H{
		"order_no": order.OrderNo,
		"status":   order.Status,
		"tickets":  tickets,
	})
}

// RefundOrder delegates refund semantics to the configured external channel
// adapter. The core adapter intentionally rejects this call until an upstream
// refund protocol is configured, avoiding a false local success.
func (c *OTAController) RefundOrder(ctx *gin.Context) {
	var req struct {
		OrderNo     string `json:"order_no" binding:"required"`
		AmountCents int64  `json:"amount_cents" binding:"required"`
		Reason      string `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		OTAResponse(ctx, err, nil)
		return
	}
	if ctx.GetUint("channel_account_id") == 0 || c.Gateway == nil {
		OTAResponse(ctx, errors.New("channel refund requires an authenticated channel adapter"), nil)
		return
	}
	response, err := c.Gateway.RefundOrder(ctx, ctx.GetString("channel_type"), service.ChannelRefundRequest{
		TenantID: ctx.GetUint("tenant_id"), AccountID: ctx.GetUint("channel_account_id"), Channel: ctx.GetString("channel_code"),
		ExternalNo: req.OrderNo, AmountCents: req.AmountCents, Reason: req.Reason,
	})
	if err != nil {
		OTAResponse(ctx, err, nil)
		return
	}
	OTAResponse(ctx, nil, response)
}
