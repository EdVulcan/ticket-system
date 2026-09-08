package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMiniappRefundApplicationRequiresCustomerSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	controller := &MiniappController{}
	router.POST("/orders/:orderNo/refund-applications", controller.ApplyRefund)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/orders/UNRELATED/refund-applications", strings.NewReader(`{"reason":"test","request_id":"1","tenant_id":1,"amount_cents":1}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated application returned %d: %s", response.Code, response.Body)
	}
}

func TestMiniappRefundErrorsDoNotExposeTicketOrProviderDetails(t *testing.T) {
	for _, detail := range []string{"ticket SECRET-TICKET is already used", "provider refund SECRET-OPENID failed", "order is not eligible SECRET-ID"} {
		message := miniappRefundApplicationError(errors.New(detail))
		if strings.Contains(message, "SECRET") {
			t.Fatalf("sensitive error leaked: %s", message)
		}
	}
}
