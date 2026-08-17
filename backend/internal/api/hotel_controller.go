package api

import (
	"errors"
	"net/http"
	"strconv"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type HotelController struct {
	Service service.HotelService
}

func pathID(ctx *gin.Context, name string) (uint, bool) {
	value, err := strconv.ParseUint(ctx.Param(name), 10, 64)
	if err != nil || value == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid resource id"})
		return 0, false
	}
	return uint(value), true
}

func hotelError(ctx *gin.Context, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, gorm.ErrRecordNotFound) {
		status = http.StatusNotFound
	}
	ctx.JSON(status, gin.H{"error": err.Error()})
}

func (c *HotelController) ListProperties(ctx *gin.Context) {
	rows, err := c.Service.ListProperties(ctx.GetUint("tenant_id"))
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *HotelController) CreateProperty(ctx *gin.Context) {
	var input service.HotelPropertyInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		hotelError(ctx, err)
		return
	}
	row, err := c.Service.CreateProperty(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), input)
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *HotelController) UpdateProperty(ctx *gin.Context) {
	id, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	var input service.HotelPropertyInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		hotelError(ctx, err)
		return
	}
	if err := c.Service.UpdateProperty(ctx.GetUint("tenant_id"), id, ctx.GetUint("user_id"), input); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "hotel property updated"})
}

func (c *HotelController) DeleteProperty(ctx *gin.Context) {
	id, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	if err := c.Service.DeleteProperty(ctx.GetUint("tenant_id"), id, ctx.GetUint("user_id")); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "hotel property deleted"})
}

func (c *HotelController) ListRoomTypes(ctx *gin.Context) {
	hotelID, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	rows, err := c.Service.ListRoomTypes(ctx.GetUint("tenant_id"), hotelID)
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *HotelController) CreateRoomType(ctx *gin.Context) {
	hotelID, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	var input service.HotelRoomTypeInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		hotelError(ctx, err)
		return
	}
	row, err := c.Service.CreateRoomType(ctx.GetUint("tenant_id"), hotelID, ctx.GetUint("user_id"), input)
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *HotelController) UpdateRoomType(ctx *gin.Context) {
	hotelID, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	roomTypeID, ok := pathID(ctx, "roomTypeID")
	if !ok {
		return
	}
	var input service.HotelRoomTypeInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		hotelError(ctx, err)
		return
	}
	if err := c.Service.UpdateRoomType(ctx.GetUint("tenant_id"), hotelID, roomTypeID, ctx.GetUint("user_id"), input); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "hotel room type updated"})
}

func (c *HotelController) DeleteRoomType(ctx *gin.Context) {
	hotelID, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	roomTypeID, ok := pathID(ctx, "roomTypeID")
	if !ok {
		return
	}
	if err := c.Service.DeleteRoomType(ctx.GetUint("tenant_id"), hotelID, roomTypeID, ctx.GetUint("user_id")); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "hotel room type deleted"})
}

func (c *HotelController) ListRatePlans(ctx *gin.Context) {
	hotelID, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	roomTypeID, ok := pathID(ctx, "roomTypeID")
	if !ok {
		return
	}
	rows, err := c.Service.ListRatePlans(ctx.GetUint("tenant_id"), hotelID, roomTypeID)
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *HotelController) CreateRatePlan(ctx *gin.Context) {
	hotelID, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	roomTypeID, ok := pathID(ctx, "roomTypeID")
	if !ok {
		return
	}
	var input service.HotelRatePlanInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		hotelError(ctx, err)
		return
	}
	row, err := c.Service.CreateRatePlan(ctx.GetUint("tenant_id"), hotelID, roomTypeID, ctx.GetUint("user_id"), input)
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *HotelController) UpdateRatePlan(ctx *gin.Context) {
	hotelID, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	roomTypeID, ok := pathID(ctx, "roomTypeID")
	if !ok {
		return
	}
	ratePlanID, ok := pathID(ctx, "ratePlanID")
	if !ok {
		return
	}
	var input service.HotelRatePlanInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		hotelError(ctx, err)
		return
	}
	if err := c.Service.UpdateRatePlan(ctx.GetUint("tenant_id"), hotelID, roomTypeID, ratePlanID, ctx.GetUint("user_id"), input); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "hotel rate plan updated"})
}

func (c *HotelController) DeleteRatePlan(ctx *gin.Context) {
	hotelID, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	roomTypeID, ok := pathID(ctx, "roomTypeID")
	if !ok {
		return
	}
	ratePlanID, ok := pathID(ctx, "ratePlanID")
	if !ok {
		return
	}
	if err := c.Service.DeleteRatePlan(ctx.GetUint("tenant_id"), hotelID, roomTypeID, ratePlanID, ctx.GetUint("user_id")); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "hotel rate plan deleted"})
}

func (c *HotelController) ListRatePlanCalendar(ctx *gin.Context) {
	hotelID, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	roomTypeID, ok := pathID(ctx, "roomTypeID")
	if !ok {
		return
	}
	ratePlanID, ok := pathID(ctx, "ratePlanID")
	if !ok {
		return
	}
	rows, err := c.Service.ListRatePlanCalendar(ctx.GetUint("tenant_id"), hotelID, roomTypeID, ratePlanID, ctx.Query("start_date"), ctx.Query("end_date"))
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *HotelController) SetRatePlanCalendar(ctx *gin.Context) {
	hotelID, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	roomTypeID, ok := pathID(ctx, "roomTypeID")
	if !ok {
		return
	}
	ratePlanID, ok := pathID(ctx, "ratePlanID")
	if !ok {
		return
	}
	var body struct {
		Items []service.HotelRatePlanPriceInput `json:"items"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		hotelError(ctx, err)
		return
	}
	if err := c.Service.SetRatePlanCalendar(ctx.GetUint("tenant_id"), hotelID, roomTypeID, ratePlanID, ctx.GetUint("user_id"), body.Items); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "hotel rate plan calendar updated"})
}

func (c *HotelController) ListInventory(ctx *gin.Context) {
	hotelID, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	roomTypeID, ok := pathID(ctx, "roomTypeID")
	if !ok {
		return
	}
	rows, err := c.Service.ListInventory(ctx.GetUint("tenant_id"), hotelID, roomTypeID, ctx.Query("start_date"), ctx.Query("end_date"))
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *HotelController) SetInventory(ctx *gin.Context) {
	hotelID, ok := pathID(ctx, "hotelID")
	if !ok {
		return
	}
	roomTypeID, ok := pathID(ctx, "roomTypeID")
	if !ok {
		return
	}
	var body struct {
		Items []service.HotelInventoryInput `json:"items"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		hotelError(ctx, err)
		return
	}
	if err := c.Service.SetInventory(ctx.GetUint("tenant_id"), hotelID, roomTypeID, ctx.GetUint("user_id"), body.Items); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "hotel inventory updated"})
}
