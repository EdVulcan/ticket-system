package middleware

import (
	"net/http"
	"strings"
	"ticket-backend/internal/config"

	"github.com/gin-gonic/gin"
)

func Cors() gin.HandlerFunc {
	allowed := make(map[string]struct{})
	// Wails serves the bundled frontend from fixed local application origins.
	// Keep these first-party desktop origins available even when production
	// environment variables replace the configurable browser-origin list.
	for _, origin := range []string{"http://wails.localhost", "wails://wails.localhost"} {
		allowed[origin] = struct{}{}
	}
	for _, origin := range strings.Split(config.GlobalConfig.Server.CORSAllowedOrigins, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			allowed[origin] = struct{}{}
		}
	}
	return func(c *gin.Context) {
		method := c.Request.Method
		origin := c.Request.Header.Get("Origin")

		if _, ok := allowed[origin]; origin != "" && ok {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE, PATCH")
			c.Header("Access-Control-Allow-Headers", "Origin, X-Requested-With, Content-Type, Accept, Authorization, X-Device-Key, X-Mobile-Session")
			c.Header("Access-Control-Expose-Headers", "Content-Length, Access-Control-Allow-Origin, Access-Control-Allow-Headers, Cache-Control, Content-Language, Content-Type")
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		if method == "OPTIONS" {
			if origin != "" {
				if _, ok := allowed[origin]; !ok {
					c.AbortWithStatus(http.StatusForbidden)
					return
				}
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
