package api

import (
	"errors"
	"net/http"
	"strconv"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type CommerceNotificationController struct {
	Service service.CommerceNotificationService
}

func (c *CommerceNotificationController) List(ctx *gin.Context) {
	pageSize, err := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	if err != nil {
		pageSize = 20
	}
	afterID, err := strconv.ParseUint(ctx.Query("after_id"), 10, 64)
	if ctx.Query("after_id") != "" && err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "增量游标不正确"})
		return
	}
	locationID, err := strconv.ParseUint(ctx.Query("location_id"), 10, 64)
	if ctx.Query("location_id") != "" && err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "门店参数不正确"})
		return
	}
	page, err := c.Service.List(service.CommerceNotificationFilter{
		TenantID: ctx.GetUint("tenant_id"), BusinessType: ctx.Query("business_type"),
		LocationID: uint(locationID), UnreadOnly: ctx.Query("unread_only") == "true", PageSize: pageSize,
		AfterID: uint(afterID),
	})
	if err != nil {
		commerceNotificationError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, page)
}

func (c *CommerceNotificationController) UnreadCount(ctx *gin.Context) {
	locationID, err := strconv.ParseUint(ctx.Query("location_id"), 10, 64)
	if ctx.Query("location_id") != "" && err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "门店参数不正确"})
		return
	}
	count, err := c.Service.UnreadCount(service.CommerceNotificationFilter{
		TenantID: ctx.GetUint("tenant_id"), BusinessType: ctx.Query("business_type"), LocationID: uint(locationID),
	})
	if err != nil {
		commerceNotificationError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"count": count})
}

func (c *CommerceNotificationController) MarkRead(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "通知编号不正确"})
		return
	}
	if err := c.Service.MarkRead(ctx.GetUint("tenant_id"), uint(id)); err != nil {
		commerceNotificationError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func commerceNotificationError(ctx *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "商家通知处理失败"
	if errors.Is(err, gorm.ErrRecordNotFound) {
		status, message = http.StatusNotFound, "通知不存在"
	} else if errors.Is(err, service.ErrCommerceNotificationInvalid) {
		status, message = http.StatusBadRequest, "通知查询条件不正确"
	}
	ctx.JSON(status, gin.H{"error": message})
}
