package api

import (
	"fmt"
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type OTAController struct {
	OrderService   service.OrderService
	ProductService service.ProductService
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
	products, total, err := c.ProductService.List(page, pageSize, tenantID, "online")

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
	ProductID    uint   `json:"product_id" binding:"required"`
	Quantity     int    `json:"quantity" binding:"required"`
	Date         string `json:"date"` // Visit Date YYYY-MM-DD
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
	ExternalNo   string `json:"external_no"` // OTA's Order No
}

func (c *OTAController) CreateOrder(ctx *gin.Context) {
	var req OTACreateOrderRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		OTAResponse(ctx, err, nil)
		return
	}

	tenant := ctx.MustGet("tenant").(model.Tenant)
	tenantID := tenant.ID

	// Construct Order
	order := model.Order{
		TenantID:     tenantID,
		ContactName:  req.ContactName,
		ContactPhone: req.ContactPhone,
		Channel:      "ota", // Mark as OTA
		Items: []model.OrderItem{
			{
				ProductID: req.ProductID,
				Quantity:  req.Quantity,
				// UseDate:   nil, // Removed
			},
		},
		// Memo: fmt.Sprintf("OTA Order: %s", req.ExternalNo), // Removed
	}

	if err := c.OrderService.Create(&order); err != nil {
		OTAResponse(ctx, err, nil)
		return
	}

	OTAResponse(ctx, nil, gin.H{
		"order_no":     order.OrderNo,
		"order_status": order.Status,
		// Return first ticket info?
		// "ticket_codes": ...
	})
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
	order, err := c.OrderService.GetByOrderNo(req.OrderNo)
	if err != nil {
		OTAResponse(ctx, fmt.Errorf("order not found"), nil)
		return
	}

	tenantID := ctx.GetUint("tenant_id")
	if order.TenantID != tenantID {
		OTAResponse(ctx, fmt.Errorf("order not found"), nil) // Hide existence
		return
	}

	if err := c.OrderService.Cancel(req.OrderNo); err != nil {
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

	order, err := c.OrderService.GetByOrderNo(req.OrderNo)
	if err != nil {
		OTAResponse(ctx, fmt.Errorf("order not found"), nil)
		return
	}

	tenantID := ctx.GetUint("tenant_id")
	if order.TenantID != tenantID {
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
