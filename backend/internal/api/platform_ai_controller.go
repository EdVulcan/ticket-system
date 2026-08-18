package api

import (
	"errors"
	"net/http"
	"strconv"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type PlatformAIController struct {
	Service service.PlatformAIService
}

func (c *PlatformAIController) GetConfig(ctx *gin.Context) {
	config, err := c.Service.GetConfig()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, config)
}

func (c *PlatformAIController) SaveConfig(ctx *gin.Context) {
	var input service.PlatformAIConfigInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	config, err := c.Service.SaveConfig(input, ctx.GetUint("platform_user_id"), ctx.GetString("role"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, config)
}

func (c *PlatformAIController) TestConfig(ctx *gin.Context) {
	var input service.PlatformAIConfigInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.TestConfig(ctx.Request.Context(), input); err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, service.ErrAIUnavailable) {
			status = http.StatusServiceUnavailable
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (c *PlatformAIController) ListTenantQuotas(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	result, err := c.Service.ListTenantQuotaPolicies(ctx.Query("period"), ctx.Query("search"), page, pageSize)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrAIQuotaInvalidPeriod) {
			status = http.StatusBadRequest
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *PlatformAIController) UpdateTenantQuota(ctx *gin.Context) {
	tenantID, err := strconv.ParseUint(ctx.Param("tenantID"), 10, 32)
	if err != nil || tenantID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant id"})
		return
	}
	var input service.AITenantQuotaPolicyInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := c.Service.UpdateTenantQuotaPolicy(uint(tenantID), input, ctx.GetUint("platform_user_id"), ctx.GetString("role"))
	if err != nil {
		status := http.StatusInternalServerError
		var validationErr *service.AIQuotaValidationError
		if errors.As(err, &validationErr) {
			status = http.StatusBadRequest
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, result)
}
