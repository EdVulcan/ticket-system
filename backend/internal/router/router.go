package router

import (
	"ticket-backend/internal/api"
	"ticket-backend/internal/middleware"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

func InitRouter(r *gin.Engine) {
	// Global Middleware
	r.Use(middleware.Cors())

	apiGroup := r.Group("/api/v1")

	// Public Routes
	authController := &api.AuthController{Service: service.AuthService{}}
	apiGroup.POST("/auth/login", authController.Login)
	apiGroup.POST("/auth/staff/login", authController.StaffLogin)

	// Protected Routes
	protected := apiGroup.Group("")
	protected.Use(middleware.JWTAuth())

	// Tenant Routes
	tenantController := &api.TenantController{}
	tenantGroup := protected.Group("/tenants")
	{
		tenantGroup.POST("", tenantController.Create)
		tenantGroup.GET("", tenantController.List)
		tenantGroup.PUT("/:id", tenantController.Update)
		tenantGroup.DELETE("/:id", tenantController.Delete)
	}

	// User Routes (Staff Management)
	userController := &api.UserController{}
	userGroup := protected.Group("/users")
	{
		userGroup.POST("", userController.Create)
		userGroup.GET("", userController.List)
		userGroup.DELETE("/:id", userController.Delete)
		userGroup.PUT("/:id/password", userController.ResetPassword)
	}

	// Staff Routes (Employee Management)
	staffController := &api.StaffController{}
	staffGroup := protected.Group("/staff")
	{
		staffGroup.POST("", staffController.Create)
		staffGroup.GET("", staffController.List)
		staffGroup.DELETE("/:id", staffController.Delete)
		staffGroup.PUT("/:id/password", staffController.ResetPassword)
	}

	// Device Routes
	deviceController := &api.DeviceController{}
	deviceGroup := protected.Group("/devices")
	{
		deviceGroup.POST("", deviceController.Create)
		deviceGroup.GET("", deviceController.List)
		deviceGroup.PUT("/:id", deviceController.Update)
		deviceGroup.DELETE("/:id", deviceController.Delete)
	}

	// Product Routes
	productController := &api.ProductController{}
	productGroup := protected.Group("/products")
	{
		productGroup.POST("", productController.Create)
		productGroup.PUT("/:id", productController.Update)
		productGroup.GET("", productController.List)
		productGroup.GET("/:id", productController.Get)
		productGroup.DELETE("/:id", productController.Delete)
		productGroup.PATCH("/:id/status", productController.UpdateStatus)
	}

	orderController := &api.OrderController{}
	orderGroup := protected.Group("/orders")
	{
		orderGroup.POST("", orderController.Create)
		orderGroup.GET("", orderController.List)
	}

	// Ticket Routes
	ticketController := &api.TicketController{}
	ticketGroup := protected.Group("/tickets")
	{
		ticketGroup.POST("/verify", ticketController.Verify)
	}

	// CheckPoint Routes
	cpController := &api.CheckPointController{}
	cpGroup := protected.Group("/checkpoints")
	{
		cpGroup.POST("", cpController.Create)
		cpGroup.GET("", cpController.List)
		cpGroup.PUT("/:id", cpController.Update)
		cpGroup.DELETE("/:id", cpController.Delete)
	}
}
