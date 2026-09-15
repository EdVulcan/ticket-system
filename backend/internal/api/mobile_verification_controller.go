package api

import (
	"errors"
	"net/http"
	"strings"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

const mobileSessionHeader = "X-Mobile-Session"

type MobileVerificationController struct {
	Service *service.MobileVerificationService
}

type mobileSessionRequest struct {
	CheckPointID uint `json:"check_point_id" binding:"required"`
	DeviceID     uint `json:"device_id" binding:"required"`
}

type mobileVerifyRequest struct {
	TicketCode string `json:"ticket_code" binding:"required"`
	RequestID  string `json:"request_id" binding:"required"`
}

type mobilePreviewRequest struct {
	TicketCode string `json:"ticket_code" binding:"required"`
}
type mobileOperationRequest struct {
	PreviewID      string `json:"preview_id" binding:"required"`
	OperationID    string `json:"operation_id" binding:"required"`
	Quantity       int    `json:"quantity" binding:"required"`
	ContinuationOf string `json:"continuation_of"`
}

func NewMobileVerificationController(s *service.MobileVerificationService) *MobileVerificationController {
	return &MobileVerificationController{Service: s}
}

func (c *MobileVerificationController) Targets(ctx *gin.Context) {
	result, err := c.Service.Targets(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"))
	if err != nil {
		writeMobileError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *MobileVerificationController) CreateSession(ctx *gin.Context) {
	var req mobileSessionRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := c.Service.CreateSession(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), req.CheckPointID, req.DeviceID)
	if err != nil {
		writeMobileError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, result)
}

func (c *MobileVerificationController) Heartbeat(ctx *gin.Context) {
	if err := c.Service.Heartbeat(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), mobileSessionToken(ctx), ctx.GetString("role")); err != nil {
		writeMobileError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "active"})
}

func (c *MobileVerificationController) Close(ctx *gin.Context) {
	if err := c.Service.Close(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), mobileSessionToken(ctx), ctx.GetString("role")); err != nil {
		writeMobileError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "closed"})
}

func (c *MobileVerificationController) Verify(ctx *gin.Context) {
	var req mobileVerifyRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := c.Service.Verify(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), mobileSessionToken(ctx), req.TicketCode, req.RequestID, ctx.GetString("role"))
	if err != nil {
		if errors.Is(err, service.ErrMobileRepeatConfirmation) {
			ctx.JSON(http.StatusOK, gin.H{"code": 409, "result": "deny", "reason_code": "repeat_confirmation_required", "display_text": err.Error()})
			return
		}
		if errors.Is(err, service.ErrVerificationProcessing) {
			ctx.JSON(http.StatusConflict, gin.H{"error": "该扫码请求正在处理中，请稍后重试", "request_id": req.RequestID, "retryable": true})
			return
		}
		writeMobileError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *MobileVerificationController) VerificationPreview(ctx *gin.Context) {
	var req mobilePreviewRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := c.Service.VerificationPreview(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), mobileSessionToken(ctx), req.TicketCode, ctx.GetString("role"))
	if err != nil {
		writeMobileError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *MobileVerificationController) VerificationOperation(ctx *gin.Context) {
	var req mobileOperationRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := c.Service.VerificationOperation(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), mobileSessionToken(ctx), req.PreviewID, req.OperationID, req.Quantity, req.ContinuationOf, ctx.GetString("role"))
	if err != nil {
		if errors.Is(err, service.ErrMobileRepeatConfirmation) {
			ctx.JSON(http.StatusConflict, gin.H{"error": err.Error(), "requires_confirmation": true})
			return
		}
		writeMobileError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *MobileVerificationController) GetVerificationOperation(ctx *gin.Context) {
	result, err := c.Service.GetVerificationOperation(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), mobileSessionToken(ctx), ctx.Param("operationID"), ctx.GetString("role"))
	if err != nil {
		writeMobileError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func mobileSessionToken(ctx *gin.Context) string {
	return strings.TrimSpace(ctx.GetHeader(mobileSessionHeader))
}

func writeMobileError(ctx *gin.Context, err error) {
	status, reasonCode, message := mobileErrorPresentation(err)
	ctx.JSON(status, gin.H{"error": message, "display_text": message, "reason_code": reasonCode})
}

func mobileErrorPresentation(err error) (int, string, string) {
	switch {
	case errors.Is(err, service.ErrMobileSessionInvalid):
		return http.StatusUnauthorized, "session_expired", "核销会话已失效，请重新选择检票点"
	case errors.Is(err, service.ErrMobileTargetDenied):
		return http.StatusForbidden, "target_denied", "当前账号不能使用这个检票点或移动终端"
	case errors.Is(err, service.ErrMobileRepeatConfirmation):
		return http.StatusConflict, "repeat_confirmation_required", "该票码刚刚核销过，请重新核对后再继续"
	case errors.Is(err, service.ErrInvalidTicket):
		return http.StatusUnprocessableEntity, "invalid_ticket", "无效票"
	case errors.Is(err, service.ErrTicketRefunded):
		return http.StatusUnprocessableEntity, "refunded", "订单已退款，不能核销"
	case errors.Is(err, service.ErrTicketExpired):
		return http.StatusUnprocessableEntity, "expired", "门票已过期"
	case errors.Is(err, service.ErrTicketNotStarted):
		return http.StatusUnprocessableEntity, "not_started", "门票尚未生效"
	case errors.Is(err, service.ErrOrderNotPaid):
		return http.StatusUnprocessableEntity, "order_not_paid", "订单尚未支付"
	case errors.Is(err, service.ErrAccessDenied), errors.Is(err, service.ErrCheckpointNotFound):
		return http.StatusUnprocessableEntity, "wrong_checkpoint", "当前检票点不能核销此票"
	case errors.Is(err, service.ErrPointLimitReached):
		return http.StatusUnprocessableEntity, "already_used", "当前检票点可用次数已满"
	case errors.Is(err, service.ErrGroupLimitReached):
		return http.StatusUnprocessableEntity, "benefit_exhausted", "该票可用权益已用完"
	case errors.Is(err, service.ErrTicketUnavailable):
		return http.StatusConflict, "processing", "门票状态正在处理中，请稍后重试"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" || isASCIIText(message) {
		message = "暂时无法读取票券，请稍后重试"
	}
	return http.StatusBadRequest, "request_failed", message
}

func isASCIIText(value string) bool {
	for _, character := range value {
		if character > 127 {
			return false
		}
	}
	return true
}
