package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// XiaohongshuRefundCoordinationController exposes the tenant-scoped
// operational view and the deliberately small administrator resolution API.
// It does not accept protected internal identities or financial amounts.
type XiaohongshuRefundCoordinationController struct {
	Service service.XiaohongshuRefundCoordinationService
}

func (c XiaohongshuRefundCoordinationController) List(ctx *gin.Context) {
	limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "50"))
	states := coordinationStates(ctx)
	rows, err := c.Service.List(ctx.GetUint("tenant_id"), states, limit)
	if err != nil {
		writeXiaohongshuRefundCoordinationError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

type xiaohongshuRefundResolutionBody struct {
	Action          string `json:"action"`
	ExternalOrderID string `json:"external_order_id"`
	Reason          string `json:"reason"`
	Evidence        string `json:"evidence"`
	IdempotencyKey  string `json:"idempotency_key"`
}

func (c XiaohongshuRefundCoordinationController) Resolve(ctx *gin.Context) {
	coordinationID, ok := positiveID(ctx.Param("id"))
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid xiaohongshu refund coordination id"})
		return
	}
	var body xiaohongshuRefundResolutionBody
	if err := decodeStrictJSON(ctx, &body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "小红书售后处置请求格式错误"})
		return
	}
	if strings.TrimSpace(body.IdempotencyKey) == "" {
		body.IdempotencyKey = ctx.GetHeader("Idempotency-Key")
	}
	result, err := c.Service.Resolve(service.XiaohongshuRefundResolutionRequest{
		TenantID: ctx.GetUint("tenant_id"), CoordinationID: coordinationID,
		ActorUserID: ctx.GetUint("user_id"), ActorRole: ctx.GetString("role"),
		Action: body.Action, ExternalOrderID: body.ExternalOrderID,
		Reason: body.Reason, Evidence: body.Evidence, IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		writeXiaohongshuRefundCoordinationError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func coordinationStates(ctx *gin.Context) []string {
	values := ctx.QueryArray("state")
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		for _, state := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' }) {
			state = strings.TrimSpace(state)
			if state != "" {
				result = append(result, state)
			}
		}
	}
	return result
}

func decodeStrictJSON(ctx *gin.Context, destination any) error {
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeXiaohongshuRefundCoordinationError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrXiaohongshuRefundResolutionDenied):
		ctx.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrXiaohongshuRefundResolutionInvalid):
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrXiaohongshuRefundOrderNotFound):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		ctx.JSON(http.StatusNotFound, gin.H{"error": "小红书售后协调记录不存在"})
	default:
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "小红书售后协调处理失败，请稍后重试"})
	}
}
