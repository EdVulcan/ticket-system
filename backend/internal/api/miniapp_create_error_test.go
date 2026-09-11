package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"ticket-backend/internal/service"
	"ticket-backend/internal/xiaohongshu"

	"github.com/gin-gonic/gin"
)

func TestMiniappCreateErrorDistinguishesSafeRejectionFromUnknownResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name          string
		err           error
		status        int
		code, orderNo string
	}{
		{"conflicting retry", &service.MiniappOrderCreateError{Code: "idempotency_payload_mismatch", OrderNo: "ORD-original", Message: "购买内容与原订单不一致"}, 409, "idempotency_payload_mismatch", "ORD-original"},
		{"wrapped legacy recovery", fmt.Errorf("wrapped: %w", &service.MiniappOrderCreateError{Code: "existing_order_recovery_required", OrderNo: "ORD-legacy", Message: "请查看原订单"}), 409, "existing_order_recovery_required", "ORD-legacy"},
		{"safe quote rejection", &service.MiniappOrderCreateError{Code: "order_not_created", Message: "请确认最新金额"}, 409, "order_not_created", ""},
		{"unknown failure", errors.New("order operation temporarily unavailable"), 409, "", ""},
		{"platform failure", &xiaohongshu.APIError{Code: 12, Message: "sensitive upstream details"}, 502, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/miniapp/orders", nil)
			writeMiniappCreateOrderError(ctx, tt.err)
			var body map[string]interface{}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != tt.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			code, _ := body["error_code"].(string)
			orderNo, _ := body["order_no"].(string)
			if code != tt.code || orderNo != tt.orderNo {
				t.Fatalf("unexpected recovery contract: %v", body)
			}
			if _, ok := body["pay_token"]; ok {
				t.Fatal("error response exposed payment token")
			}
			if body["error"] == "sensitive upstream details" {
				t.Fatal("platform detail leaked")
			}
		})
	}
}
