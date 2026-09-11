package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders applies browser-safe defaults without constraining the
// separately hosted admin UI's script or connection policy.
func SecurityHeaders() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Header("X-Content-Type-Options", "nosniff")
		ctx.Header("X-Frame-Options", "DENY")
		ctx.Header("Referrer-Policy", "no-referrer")
		cameraPolicy := "camera=(), microphone=(), geolocation=()"
		// The mobile verifier is served from the same origin and needs the
		// browser camera. Keep the capability scoped to that route instead of
		// weakening the default policy for the admin/API surface.
		path := ctx.Request.URL.Path
		if path == "/mobile" || strings.HasPrefix(path, "/mobile/") {
			cameraPolicy = "camera=(self), microphone=(), geolocation=()"
		}
		ctx.Header("Permissions-Policy", cameraPolicy)
		if len(ctx.Request.URL.Path) >= 8 && ctx.Request.URL.Path[:8] == "/api/v1/" {
			ctx.Header("Cache-Control", "no-store")
		}
		ctx.Next()
	}
}
