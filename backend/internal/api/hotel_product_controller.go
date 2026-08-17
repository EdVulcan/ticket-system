package api

import (
	"net/http"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

// HotelProductController exposes tenant-scoped accommodation product
// maintenance. Tenant identity is always derived from the authenticated Gin
// context; request JSON intentionally contains no tenant field.
type HotelProductController struct {
	Service service.HotelProductService
}

func (c *HotelProductController) List(ctx *gin.Context) {
	rows, err := c.Service.List(ctx.GetUint("tenant_id"))
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *HotelProductController) Create(ctx *gin.Context) {
	var input service.HotelProductInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		hotelError(ctx, err)
		return
	}
	row, err := c.Service.Create(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), input)
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *HotelProductController) Update(ctx *gin.Context) {
	id, ok := pathID(ctx, "hotelProductID")
	if !ok {
		return
	}
	var input service.HotelProductInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		hotelError(ctx, err)
		return
	}
	if err := c.Service.Update(ctx.GetUint("tenant_id"), id, ctx.GetUint("user_id"), input); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "hotel product updated"})
}

func (c *HotelProductController) Delete(ctx *gin.Context) {
	id, ok := pathID(ctx, "hotelProductID")
	if !ok {
		return
	}
	if err := c.Service.Delete(ctx.GetUint("tenant_id"), id, ctx.GetUint("user_id")); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "hotel product deleted"})
}

func (c *HotelProductController) ListCalendar(ctx *gin.Context) {
	id, ok := pathID(ctx, "hotelProductID")
	if !ok {
		return
	}
	rows, err := c.Service.ListCalendar(ctx.GetUint("tenant_id"), id, ctx.Query("start_date"), ctx.Query("end_date"))
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *HotelProductController) SetCalendar(ctx *gin.Context) {
	id, ok := pathID(ctx, "hotelProductID")
	if !ok {
		return
	}
	var body struct {
		Items []service.HotelProductCalendarPriceInput `json:"items"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		hotelError(ctx, err)
		return
	}
	if err := c.Service.SetCalendar(ctx.GetUint("tenant_id"), id, ctx.GetUint("user_id"), body.Items); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "hotel product calendar updated"})
}
