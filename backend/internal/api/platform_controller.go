package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type PlatformController struct{ Service service.PlatformService }

// SetChannelMemberMode is deliberately platform-scoped. Channel owners can
// view the resulting mode, but only a platform administrator can approve a
// channel as a first-party customer source.
func (c *PlatformController) SetChannelMemberMode(ctx *gin.Context) {
	tenantID, err := strconv.ParseUint(ctx.Param("tenantID"), 10, 32)
	if err != nil || tenantID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant id"})
		return
	}
	accountID, err := strconv.ParseUint(ctx.Param("accountID"), 10, 32)
	if err != nil || accountID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel account id"})
		return
	}
	var body struct {
		MemberMode string `json:"member_mode" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := (&service.ChannelService{}).SetMemberMode(uint(tenantID), uint(accountID), body.MemberMode); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if err := service.RecordAudit(platformActorID(ctx), uint(tenantID), ctx.GetString("role"), "platform", "platform.channel_member_mode.update", "channel_account", uint(accountID), "platform-controlled first-party member source approval", "", marshalPlatformMode(body.MemberMode)); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "channel member mode updated but audit logging failed"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"member_mode": body.MemberMode})
}

func marshalPlatformMode(mode string) string {
	payload, _ := json.Marshal(map[string]string{"member_mode": mode})
	return string(payload)
}

func (c *PlatformController) Overview(ctx *gin.Context) {
	result, err := c.Service.Overview()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := service.RecordAudit(platformActorID(ctx), 0, ctx.GetString("role"), "platform", "platform.overview.read", "platform", 0, "global operational overview", "", ""); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "overview loaded but audit logging failed"})
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *PlatformController) ListOrders(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	tenantID, _ := strconv.ParseUint(ctx.Query("tenant_id"), 10, 32)
	rows, total, err := c.Service.ListOrders(uint(tenantID), ctx.Query("status"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := service.RecordAudit(platformActorID(ctx), 0, ctx.GetString("role"), "platform", "platform.orders.read", "platform", 0, "global order worklist", "", ""); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "orders loaded but audit logging failed"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *PlatformController) ListIssues(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	tenantID, _ := strconv.ParseUint(ctx.Query("tenant_id"), 10, 32)
	rows, total, err := c.Service.ListIssues(uint(tenantID), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := service.RecordAudit(platformActorID(ctx), 0, ctx.GetString("role"), "platform", "platform.issues.read", "platform", 0, "global issue worklist", "", ""); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "issues loaded but audit logging failed"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *PlatformController) FinanceOverview(ctx *gin.Context) {
	tenantID, _ := strconv.ParseUint(ctx.Query("tenant_id"), 10, 32)
	result, err := c.Service.FinanceOverview(uint(tenantID))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := service.RecordAudit(platformActorID(ctx), 0, ctx.GetString("role"), "platform", "platform.finance.read", "platform", 0, "global finance overview", "", ""); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "finance loaded but audit logging failed"})
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *PlatformController) ListDevices(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	tenantID, _ := strconv.ParseUint(ctx.Query("tenant_id"), 10, 32)
	rows, total, err := c.Service.ListDevices(uint(tenantID), ctx.Query("status"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := service.RecordAudit(platformActorID(ctx), 0, ctx.GetString("role"), "platform", "platform.devices.read", "platform", 0, "global device worklist", "", ""); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "devices loaded but audit logging failed"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *PlatformController) ListSettlements(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	tenantID, _ := strconv.ParseUint(ctx.Query("tenant_id"), 10, 32)
	rows, total, err := c.Service.ListSettlements(uint(tenantID), ctx.Query("status"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := service.RecordAudit(platformActorID(ctx), 0, ctx.GetString("role"), "platform", "platform.settlements.read", "platform", 0, "global settlement worklist", "", ""); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "settlements loaded but audit logging failed"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *PlatformController) ListAuditLogs(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "50"))
	tenantID, _ := strconv.ParseUint(ctx.Query("tenant_id"), 10, 32)
	rows, total, err := c.Service.ListAuditLogs(uint(tenantID), ctx.Query("action"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}
