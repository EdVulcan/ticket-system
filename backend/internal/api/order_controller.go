package api

import (
	"errors"
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type OrderController struct {
	Service service.OrderService
}

type windowOrderItemRequest struct {
	ProductID       uint                 `json:"product_id"`
	BundleProductID uint                 `json:"bundle_product_id"`
	Quantity        int                  `json:"quantity"`
	UseDate         *time.Time           `json:"use_date"`
	StockSlot       string               `json:"stock_slot"`
	VisitorName     string               `json:"visitor_name"`
	VisitorPhone    string               `json:"visitor_phone"`
	VisitorID       string               `json:"visitor_id"`
	VisitorRegion   string               `json:"visitor_region"`
	Visitors        []model.VisitorInput `json:"visitors"`
}

type windowOrderRequest struct {
	ContactName   string                   `json:"contact_name"`
	ContactPhone  string                   `json:"contact_phone"`
	VisitorID     string                   `json:"visitor_id"`
	VisitorRegion string                   `json:"visitor_region"`
	Items         []windowOrderItemRequest `json:"items" binding:"required,min=1"`
}

func (c *OrderController) Create(ctx *gin.Context) {
	var body windowOrderRequest
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req := model.Order{
		TenantID: ctx.GetUint("tenant_id"), Channel: "window",
		ContactName: body.ContactName, ContactPhone: body.ContactPhone,
		VisitorID: body.VisitorID, VisitorRegion: body.VisitorRegion,
		Items: make([]model.OrderItem, len(body.Items)),
	}
	for i := range body.Items {
		item := body.Items[i]
		req.Items[i] = model.OrderItem{
			ProductID: item.ProductID, BundleProductID: item.BundleProductID, Quantity: item.Quantity,
			UseDate: item.UseDate, StockSlot: item.StockSlot,
			VisitorName: item.VisitorName, VisitorPhone: item.VisitorPhone,
			VisitorID: item.VisitorID, VisitorRegion: item.VisitorRegion, Visitors: item.Visitors,
		}
	}

	if err := c.Service.Create(&req); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, req)
}

func (c *OrderController) List(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))
	status := ctx.DefaultQuery("status", "")
	channel := ctx.DefaultQuery("channel", "")
	startDate := ctx.DefaultQuery("start_date", "")
	endDate := ctx.DefaultQuery("end_date", "")
	search := ctx.DefaultQuery("search", "")

	orders, total, err := c.Service.List(page, pageSize, ctx.GetUint("tenant_id"), status, channel, startDate, endDate, search)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"data":  orders,
		"total": total,
		"page":  page,
	})
}

func (c *OrderController) Get(ctx *gin.Context) {
	detail, err := c.Service.GetDetail(ctx.Param("orderNo"), ctx.GetUint("tenant_id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
		} else {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load order detail"})
		}
		return
	}
	ctx.JSON(http.StatusOK, detail)
}

func (c *OrderController) Cancel(ctx *gin.Context) {
	if err := c.Service.Cancel(ctx.Param("orderNo"), ctx.GetUint("tenant_id")); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}
