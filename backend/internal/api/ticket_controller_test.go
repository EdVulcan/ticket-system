package api

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestTicketVerifyRequiresDeviceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest("POST", "/api/v1/tickets/verify", bytes.NewBufferString(`{"code":"TICKET-1","check_point_id":1}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = request

	(&TicketController{}).Verify(ctx)
	if response.Code != 400 {
		t.Fatalf("missing device_id status=%d body=%s", response.Code, response.Body.String())
	}
}
