package api

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
	"ticket-backend/internal/zyb"
	"time"
)

func TestUpstreamDeferralIsNotSpecialRefundPermission(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	if !respondUpstreamDeferred(ctx, &zyb.DeferredError{RetryAt: time.Now().Add(time.Minute)}) {
		t.Fatal("deferral not handled")
	}
	var body struct {
		RequiresConfirmation bool   `json:"requires_confirmation"`
		ReasonCode           string `json:"reason_code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != 429 || body.RequiresConfirmation || body.ReasonCode != "upstream_waiting" || recorder.Header().Get("Retry-After") == "" {
		t.Fatal("deferral exposed a special refund path")
	}
}
