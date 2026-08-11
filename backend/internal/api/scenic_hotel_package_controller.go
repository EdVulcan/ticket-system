package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type ScenicHotelPackageController struct {
	Service service.ScenicHotelPackageService
}

func (c *ScenicHotelPackageController) List(ctx *gin.Context) {
	rows, err := c.Service.List(ctx.GetUint("tenant_id"))
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *ScenicHotelPackageController) Create(ctx *gin.Context) {
	var input service.ScenicHotelPackageInput
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

func (c *ScenicHotelPackageController) Update(ctx *gin.Context) {
	id, ok := pathID(ctx, "packageID")
	if !ok {
		return
	}
	var input service.ScenicHotelPackageInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		hotelError(ctx, err)
		return
	}
	if err := c.Service.Update(ctx.GetUint("tenant_id"), id, ctx.GetUint("user_id"), input); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "scenic hotel package updated"})
}

func (c *ScenicHotelPackageController) Delete(ctx *gin.Context) {
	id, ok := pathID(ctx, "packageID")
	if !ok {
		return
	}
	if err := c.Service.Delete(ctx.GetUint("tenant_id"), id, ctx.GetUint("user_id")); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "scenic hotel package deleted"})
}

func (c *ScenicHotelPackageController) ListReservations(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	result, err := c.Service.ListReservations(ctx.GetUint("tenant_id"), ctx.Query("status"), ctx.Query("order_no"), page, pageSize)
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}
