package router

import (
	"ticket-backend/internal/api"
	"ticket-backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func InitRouter(r *gin.Engine) {
	// Global Middleware
	r.Use(middleware.Cors())

	apiGroup := r.Group("/api/v1")

	// Tenant Routes
	tenantController := &api.TenantController{}
	tenantGroup := apiGroup.Group("/tenants")
	{
		tenantGroup.POST("", tenantController.Create)
		tenantGroup.GET("", tenantController.List)
		tenantGroup.PUT("/:id", tenantController.Update)
		tenantGroup.DELETE("/:id", tenantController.Delete)
	}

	// Device Routes
	deviceController := &api.DeviceController{}
	deviceGroup := apiGroup.Group("/devices")
	{
		deviceGroup.POST("", deviceController.Create)
		deviceGroup.GET("", deviceController.List)
		deviceGroup.PUT("/:id", deviceController.Update)
		deviceGroup.DELETE("/:id", deviceController.Delete)
	}

	// Product Routes
	productController := &api.ProductController{}
	productGroup := apiGroup.Group("/products")
	{
		productGroup.POST("", productController.Create)
		productGroup.PUT("/:id", productController.Update)
		productGroup.GET("", productController.List)
		productGroup.DELETE("/:id", productController.Delete)
		productGroup.PATCH("/:id/status", productController.UpdateStatus)
	}

	orderController := &api.OrderController{}
	orderGroup := apiGroup.Group("/orders")
	{
		orderGroup.POST("", orderController.Create)
		orderGroup.GET("", orderController.List)
	}

	// Ticket Routes
	ticketController := &api.TicketController{}
	ticketGroup := apiGroup.Group("/tickets")
	{
		ticketGroup.POST("/verify", ticketController.Verify)
	}

	// CheckPoint Routes
	cpController := &api.CheckPointController{}
	cpGroup := apiGroup.Group("/checkpoints")
	{
		cpGroup.POST("", cpController.Create)
		cpGroup.GET("", cpController.List)
		cpGroup.PUT("/:id", cpController.Update)
		cpGroup.DELETE("/:id", cpController.Delete)
	}
}
