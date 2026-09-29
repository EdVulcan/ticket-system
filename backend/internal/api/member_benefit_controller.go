package api

import (
	"errors"
	"net/http"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

// MemberBenefitController exposes the tenant-scoped unified membership
// discount rule. Tenant identity comes only from the authenticated context.
type MemberBenefitController struct{ Service *service.MemberService }

func (c *MemberBenefitController) Get(ctx *gin.Context) {
	if c == nil || c.Service == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "member service unavailable"})
		return
	}
	config, err := c.Service.GetBenefitConfig(ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if config == nil {
		ctx.JSON(http.StatusOK, gin.H{"configured": false, "discount_percent": nil})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"configured": true, "discount_percent": config.DiscountPercent})
}

func (c *MemberBenefitController) Save(ctx *gin.Context) {
	if c == nil || c.Service == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "member service unavailable"})
		return
	}
	var body struct {
		DiscountPercent *int `json:"discount_percent"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil || body.DiscountPercent == nil || *body.DiscountPercent < 0 || *body.DiscountPercent > 100 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "discount_percent is required and must be between 0 and 100"})
		return
	}
	config, err := c.Service.SaveBenefitConfigAudited(ctx.GetUint("tenant_id"), *body.DiscountPercent, ctx.GetUint("user_id"), ctx.GetString("role"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrMemberInvalidInput) {
			status = http.StatusBadRequest
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"configured": true, "discount_percent": config.DiscountPercent})
}
