package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestBodyLimitRejectsKnownOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(RequestBodyLimit(4))
	engine.POST("/body", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodPost, "/body", strings.NewReader("12345"))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestLoginRateLimitBlocksRepeatedIdentityFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/v1/auth/login", LoginRateLimit(), func(ctx *gin.Context) {
		ctx.Status(http.StatusUnauthorized)
	})
	body := `{"system_code":"SYS001","username":"admin","password":"wrong"}`
	for attempt := 1; attempt <= 11; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		request.RemoteAddr = "192.0.2.10:12345"
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if attempt <= 10 && response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d, want %d", attempt, response.Code, http.StatusUnauthorized)
		}
		if attempt == 11 && response.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d status=%d, want %d", attempt, response.Code, http.StatusTooManyRequests)
		}
	}
}
