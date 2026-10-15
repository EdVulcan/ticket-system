package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"ticket-backend/internal/authz"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// MemberCouponGrantService resolves recipients and creates coupon entitlements
// inside its own transaction. HTTP input never supplies wallet or actor identity.
type MemberCouponGrantService interface {
	Preview(context.Context, uint, uint, []uint) (*service.MemberCouponGrantPreviewResult, error)
	Issue(context.Context, uint, service.MemberCouponGrantIssueInput) (*service.MemberCouponGrantResult, error)
	Get(context.Context, uint, string) (*service.MemberCouponGrantResult, error)
}

type MemberCouponGrantController struct{ Service MemberCouponGrantService }

func (c *MemberCouponGrantController) authorized(ctx *gin.Context) bool {
	role := ctx.GetString("role")
	if ctx.GetString("scope") != "tenant" || ctx.GetUint("tenant_id") == 0 ||
		!authz.HasTenantPermission(role, authz.PermissionMembersRead) ||
		!authz.HasTenantPermission(role, authz.PermissionMembersCouponsWrite) ||
		!authz.HasTenantPermission(role, authz.PermissionCatalogWrite) {
		ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "当前账号没有给会员发券的权限"})
		return false
	}
	if c.Service == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "会员发券服务暂不可用"})
		return false
	}
	return true
}

func (c *MemberCouponGrantController) Preview(ctx *gin.Context) {
	if !c.authorized(ctx) {
		return
	}
	var input struct {
		TemplateID uint   `json:"template_id"`
		MemberIDs  []uint `json:"member_ids"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请选择优惠券模板和收券会员"})
		return
	}
	result, err := c.Service.Preview(ctx.Request.Context(), ctx.GetUint("tenant_id"), input.TemplateID, input.MemberIDs)
	if err != nil {
		writeMemberCouponGrantError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *MemberCouponGrantController) Issue(ctx *gin.Context) {
	if !c.authorized(ctx) {
		return
	}
	var body struct {
		TemplateID     uint   `json:"template_id"`
		MemberIDs      []uint `json:"member_ids"`
		IdempotencyKey string `json:"idempotency_key"`
		Reason         string `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "发券信息格式不正确"})
		return
	}
	result, err := c.Service.Issue(ctx.Request.Context(), ctx.GetUint("tenant_id"), service.MemberCouponGrantIssueInput{
		TemplateID: body.TemplateID, MemberIDs: body.MemberIDs, IdempotencyKey: strings.TrimSpace(body.IdempotencyKey),
		Reason: strings.TrimSpace(body.Reason), ActorUserID: ctx.GetUint("user_id"), ActorRole: ctx.GetString("role"),
	})
	if err != nil {
		writeMemberCouponGrantError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *MemberCouponGrantController) Get(ctx *gin.Context) {
	if !c.authorized(ctx) {
		return
	}
	result, err := c.Service.Get(ctx.Request.Context(), ctx.GetUint("tenant_id"), strings.TrimSpace(ctx.Param("batchKey")))
	if err != nil {
		writeMemberCouponGrantError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func writeMemberCouponGrantError(ctx *gin.Context, err error) {
	status, message, code := http.StatusInternalServerError, "发券结果暂时无法确认，请保留原批次并查询结果", "MEMBER_COUPON_GRANT_UNKNOWN"
	switch {
	case errors.Is(err, service.ErrMemberCouponGrantInvalid), errors.Is(err, service.ErrCommercePromotionInvalid):
		status, message, code = http.StatusBadRequest, "发券参数不符合要求，请检查模板、会员和发放备注", "MEMBER_COUPON_GRANT_INVALID"
	case errors.Is(err, service.ErrMemberCouponGrantEligibility), errors.Is(err, service.ErrPromotionUnavailable):
		status, message, code = http.StatusBadRequest, "当前模板或部分会员不符合发券条件，本批次未发放，请重新预览", "MEMBER_COUPON_GRANT_INELIGIBLE"
	case errors.Is(err, service.ErrMemberCouponGrantConflict):
		status, message, code = http.StatusConflict, "该批次已用于其他发券请求，请先查询原发放结果", "MEMBER_COUPON_GRANT_CONFLICT"
	case errors.Is(err, service.ErrMemberCouponGrantScopeDenied), errors.Is(err, service.ErrPromotionScopeDenied), errors.Is(err, service.ErrMemberTenantUnavailable), errors.Is(err, service.ErrTenantUnavailable), errors.Is(err, service.ErrBusinessCapabilityInactive), errors.Is(err, service.ErrCapabilityInactive):
		status, message, code = http.StatusForbidden, "当前店铺或账号没有发券权限", "MEMBER_COUPON_GRANT_FORBIDDEN"
	case errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, service.ErrMemberNotFound):
		status, message, code = http.StatusNotFound, "暂未找到发券记录或模板；结果未知时请保留原批次继续核实", "MEMBER_COUPON_GRANT_NOT_FOUND"
	case errors.Is(err, service.ErrMemberCouponGrantUnavailable):
		status, message, code = http.StatusServiceUnavailable, "会员发券服务暂不可用，请保留原批次稍后核实", "MEMBER_COUPON_GRANT_UNAVAILABLE"
	}
	ctx.JSON(status, gin.H{"error": message, "reason_code": code})
}
