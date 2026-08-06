package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeadersProtectAPIResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(SecurityHeaders())
	engine.GET("/api/v1/example", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/example", nil))
	if response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("X-Frame-Options") != "DENY" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("security headers=%v", response.Header())
	}
}
