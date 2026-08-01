package router

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRoutesRegisterWithoutConflicts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	InitRouter(engine)
	wanted := map[string]bool{
		"GET /api/v1/orders/:orderNo":              false,
		"GET /api/v1/settlements/:id/export":       false,
		"GET /api/v1/teams/settlements/:id/export": false,
	}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := wanted[key]; ok {
			wanted[key] = true
		}
	}
	for route, registered := range wanted {
		if !registered {
			t.Fatalf("route not registered: %s", route)
		}
	}
}
