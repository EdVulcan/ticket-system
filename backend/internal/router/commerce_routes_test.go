package router

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCommerceOrderRoutesRegisterWithoutWildcardConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	InitRouter(engine)

	routes := map[string]bool{}
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, expected := range []string{
		"GET /api/v1/commerce/orders/:id",
		"GET /api/v1/commerce/orders/:id/shipment",
		"POST /api/v1/commerce/orders/:id/shipments",
	} {
		if !routes[expected] {
			t.Fatalf("route %q is not registered", expected)
		}
	}
}
