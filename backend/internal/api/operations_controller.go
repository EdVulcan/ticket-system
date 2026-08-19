package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

type OperationsController struct{ Service service.OperationsService }

func (c *OperationsController) ListPOSTerminals(ctx *gin.Context) {
	rows, err := c.Service.ListPOSTerminals(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *OperationsController) ListShifts(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	operatorID := uint(0)
	if strings.HasPrefix(ctx.GetString("subject"), "staff:") && !authz.HasTenantPermission(ctx.GetString("role"), authz.PermissionOnsiteManage) {
		operatorID = ctx.GetUint("user_id")
	}
	rows, total, err := c.Service.ListShiftsForOperator(ctx.GetUint("tenant_id"), operatorID, page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *OperationsController) GetShiftSummary(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid shift id"})
		return
	}
	summary, err := c.Service.GetShiftSummary(ctx.GetUint("tenant_id"), uint(id))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if strings.HasPrefix(ctx.GetString("subject"), "staff:") && !authz.HasTenantPermission(ctx.GetString("role"), authz.PermissionOnsiteManage) && summary.Shift.OperatorID != ctx.GetUint("user_id") {
		ctx.JSON(http.StatusForbidden, gin.H{"error": "无权查看其他收银员班次"})
		return
	}
	ctx.JSON(http.StatusOK, summary)
}

func (c *OperationsController) OpenShift(ctx *gin.Context) {
	var body struct {
		DeviceID     uint  `json:"device_id" binding:"required"`
		OpeningCents int64 `json:"opening_cents"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.RequireStaffResource(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), "device", body.DeviceID); err != nil {
		ctx.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	shift, err := c.Service.OpenShift(ctx.GetUint("tenant_id"), body.DeviceID, ctx.GetUint("user_id"), body.OpeningCents)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, shift)
}

func (c *OperationsController) GetOpenShift(ctx *gin.Context) {
	deviceID, err := strconv.ParseUint(ctx.Query("device_id"), 10, 32)
	if err != nil || deviceID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "device_id is required"})
		return
	}
	shift, err := c.Service.GetOpenShift(ctx.GetUint("tenant_id"), uint(deviceID), ctx.GetUint("user_id"))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "open shift not found"})
		return
	}
	ctx.JSON(http.StatusOK, shift)
}

func (c *OperationsController) CloseShift(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid shift id"})
		return
	}
	var body struct {
		ClosingCents int64  `json:"closing_cents"`
		Notes        string `json:"notes"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	shift, err := c.Service.CloseShiftForOperator(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), ctx.GetString("role"), body.ClosingCents, body.Notes)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, shift)
}

func (c *OperationsController) ReconcileShift(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid shift id"})
		return
	}
	var body struct {
		Notes string `json:"notes"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil && err.Error() != "EOF" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	shift, err := c.Service.ReconcileShift(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), ctx.GetString("role"), body.Notes)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, shift)
}

func (c *OperationsController) CorrectShift(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid shift id"})
		return
	}
	var body struct {
		CorrectedCents int64  `json:"corrected_cents"`
		Reason         string `json:"reason" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	correction, err := c.Service.RecordShiftCorrection(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), ctx.GetString("role"), body.CorrectedCents, body.Reason)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, correction)
}

func (c *OperationsController) QueuePrint(ctx *gin.Context) {
	var body struct {
		DeviceID   uint   `json:"device_id" binding:"required"`
		OrderNo    string `json:"order_no" binding:"required"`
		TicketCode string `json:"ticket_code"`
		ShiftID    uint   `json:"shift_id" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.RequireStaffResource(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), "device", body.DeviceID); err != nil {
		ctx.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	job, err := c.Service.QueuePrint(ctx.GetUint("tenant_id"), body.DeviceID, ctx.GetUint("user_id"), body.ShiftID, body.OrderNo, body.TicketCode)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusAccepted, job)
}

func (c *OperationsController) ListPrintJobs(ctx *gin.Context) {
	deviceID, _ := strconv.ParseUint(ctx.Query("device_id"), 10, 32)
	operatorID := uint(0)
	if strings.HasPrefix(ctx.GetString("subject"), "staff:") && !authz.HasTenantPermission(ctx.GetString("role"), authz.PermissionOnsiteManage) {
		operatorID = ctx.GetUint("user_id")
	}
	jobs, err := c.Service.ListPrintJobsForOperator(ctx.GetUint("tenant_id"), uint(deviceID), operatorID, ctx.Query("status"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": jobs})
}

func (c *OperationsController) ListAlerts(ctx *gin.Context) {
	alerts, err := c.Service.ListAlerts(ctx.GetUint("tenant_id"), ctx.Query("status"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": alerts})
}

func (c *OperationsController) UpdatePrintStatus(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid print job id"})
		return
	}
	var body struct {
		DeviceID uint   `json:"device_id" binding:"required"`
		Status   string `json:"status" binding:"required"`
		Error    string `json:"error"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var job interface{}
	switch body.Status {
	case "printing":
		job, err = c.Service.StartPrint(ctx.GetUint("tenant_id"), uint(id), body.DeviceID, ctx.GetUint("user_id"))
	case "printed":
		job, err = c.Service.CompletePrint(ctx.GetUint("tenant_id"), uint(id), body.DeviceID, ctx.GetUint("user_id"))
	case "failed":
		job, err = c.Service.FailPrint(ctx.GetUint("tenant_id"), uint(id), body.DeviceID, ctx.GetUint("user_id"), body.Error)
	default:
		err = errors.New("unsupported print status")
	}
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, job)
}

func (c *OperationsController) CreateHold(ctx *gin.Context) {
	var body struct {
		DeviceID     uint                `json:"device_id" binding:"required"`
		ShiftID      uint                `json:"shift_id" binding:"required"`
		Items        []model.POSHoldLine `json:"items" binding:"required,min=1"`
		ContactName  string              `json:"contact_name"`
		ContactPhone string              `json:"contact_phone"`
		Notes        string              `json:"notes"`
		TTLSeconds   int                 `json:"ttl_seconds"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.RequireStaffResource(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), "device", body.DeviceID); err != nil {
		ctx.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	ttl := time.Duration(body.TTLSeconds) * time.Second
	hold, err := c.Service.CreatePOSHold(ctx.GetUint("tenant_id"), body.DeviceID, ctx.GetUint("user_id"), body.ShiftID, body.Items, body.ContactName, body.ContactPhone, body.Notes, ttl)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, hold)
}

func (c *OperationsController) ListHolds(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	operatorID := ctx.GetUint("user_id")
	if ctx.GetString("role") == "admin" || ctx.GetString("role") == "super_admin" {
		operatorID = 0
		if raw := ctx.Query("operator_id"); raw != "" {
			parsed, _ := strconv.ParseUint(raw, 10, 32)
			operatorID = uint(parsed)
		}
	}
	rows, total, err := c.Service.ListPOSHolds(ctx.GetUint("tenant_id"), uint(operatorID), ctx.Query("status"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *OperationsController) ResumeHold(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid hold id"})
		return
	}
	hold, err := c.Service.ResumePOSHold(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"))
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, hold)
}

func (c *OperationsController) CancelHold(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid hold id"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	hold, err := c.Service.CancelPOSHold(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), ctx.GetString("role"), body.Reason)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, hold)
}
