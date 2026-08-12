package api

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
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
	hotelID, ok := optionalHotelID(ctx)
	if !ok {
		return
	}
	result, err := c.Service.ListReservations(ctx.GetUint("tenant_id"), hotelID, ctx.Query("status"), ctx.Query("order_no"), page, pageSize)
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *ScenicHotelPackageController) SetReservationStatus(ctx *gin.Context) {
	id, ok := pathID(ctx, "reservationID")
	if !ok {
		return
	}
	var input struct {
		Status string `json:"status" binding:"required"`
		Reason string `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		hotelError(ctx, err)
		return
	}
	if err := c.Service.SetReservationStatus(ctx.GetUint("tenant_id"), id, ctx.GetUint("user_id"), input.Status, input.Reason); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "hotel reservation fulfillment updated"})
}

func (c *ScenicHotelPackageController) BusinessSummary(ctx *gin.Context) {
	hotelID, ok := optionalHotelID(ctx)
	if !ok {
		return
	}
	result, err := c.Service.BusinessSummary(ctx.GetUint("tenant_id"), hotelID, ctx.Query("start_date"), ctx.Query("end_date"))
	if err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *ScenicHotelPackageController) ExportReservations(ctx *gin.Context) {
	hotelID, ok := optionalHotelID(ctx)
	if !ok {
		return
	}
	rows, err := c.Service.ExportReservations(ctx.GetUint("tenant_id"), hotelID, ctx.Query("status"), ctx.Query("order_no"))
	if err != nil {
		hotelError(ctx, err)
		return
	}
	var buffer bytes.Buffer
	buffer.WriteString("\xEF\xBB\xBF")
	writer := csv.NewWriter(&buffer)
	_ = writer.Write([]string{"预订号", "订单号", "套餐", "票码", "入住人", "联系电话", "酒店", "房型", "价格计划", "入住日期", "离店日期", "房间数", "状态"})
	for i := range rows {
		row := rows[i]
		_ = writer.Write([]string{row.ReservationNo, row.OrderNo, row.ProductName, row.TicketCode, row.GuestName, row.ContactPhone, row.HotelName, row.RoomTypeName, row.RatePlanName, row.CheckInDate.Format("2006-01-02"), row.CheckOutDate.Format("2006-01-02"), strconv.Itoa(row.Rooms), hotelReservationStatusText(row.Status)})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		hotelError(ctx, err)
		return
	}
	ctx.Header("Content-Disposition", `attachment; filename="hotel-reservations.csv"`)
	ctx.Data(http.StatusOK, "text/csv; charset=utf-8", buffer.Bytes())
}

func optionalHotelID(ctx *gin.Context) (uint, bool) {
	raw := strings.TrimSpace(ctx.Query("hotel_id"))
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid hotel_id"})
		return 0, false
	}
	return uint(value), true
}

func hotelReservationStatusText(status string) string {
	return map[string]string{"reserved": "待支付", "confirmed": "待入住", "checked_in": "已入住", "checked_out": "已离店", "no_show": "未到店", "cancelled": "已取消", "refunded": "已退款"}[status]
}
