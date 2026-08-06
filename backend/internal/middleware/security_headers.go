package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders applies browser-safe defaults without constraining the
// separately hosted admin UI's script or connection policy.
func SecurityHeaders() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Header("X-Content-Type-Options", "nosniff")
		ctx.Header("X-Frame-Options", "DENY")
		ctx.Header("Referrer-Policy", "no-referrer")
		ctx.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if len(ctx.Request.URL.Path) >= 8 && ctx.Request.URL.Path[:8] == "/api/v1/" {
			ctx.Header("Cache-Control", "no-store")
		}
		ctx.Next()
	}
}
