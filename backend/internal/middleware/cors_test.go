package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"ticket-backend/internal/config"

	"github.com/gin-gonic/gin"
)

func TestCorsAllowsWailsDesktopAndRejectsUnknownOrigins(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := config.GlobalConfig.Server.CORSAllowedOrigins
	config.GlobalConfig.Server.CORSAllowedOrigins = "https://admin.example"
	t.Cleanup(func() { config.GlobalConfig.Server.CORSAllowedOrigins = previous })

	engine := gin.New()
	engine.Use(Cors())
	engine.POST("/login", func(ctx *gin.Context) { ctx.Status(http.StatusUnauthorized) })

	for _, origin := range []string{"http://wails.localhost", "wails://wails.localhost"} {
		request := httptest.NewRequest(http.MethodOptions, "/login", nil)
		request.Header.Set("Origin", origin)
		request.Header.Set("Access-Control-Request-Method", http.MethodPost)
		request.Header.Set("Access-Control-Request-Headers", "content-type")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)

		if response.Code != http.StatusNoContent {
			t.Fatalf("origin %s status=%d, want 204", origin, response.Code)
		}
		if got := response.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("origin %s allow-origin=%q", origin, got)
		}
	}

	request := httptest.NewRequest(http.MethodOptions, "/login", nil)
	request.Header.Set("Origin", "https://untrusted.example")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unknown origin status=%d, want 403", response.Code)
	}
}
