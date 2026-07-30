package router

import (
	"ticket-backend/internal/api"
	"ticket-backend/internal/middleware"
	"ticket-backend/internal/model"
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
		tenantGroup.GET("/me", tenantController.GetSelf)
		tenantGroup.POST("", middleware.RequireAnyRole("super_admin"), tenantController.Create)
		tenantGroup.GET("", middleware.RequireAnyRole("super_admin"), tenantController.List)
		tenantGroup.PUT("/:id", middleware.RequireAnyRole("super_admin"), tenantController.Update)
		tenantGroup.DELETE("/:id", middleware.RequireAnyRole("super_admin"), tenantController.Delete)
	}

	// User Routes (Staff Management)
	userController := &api.UserController{}
	userGroup := protected.Group("/users")
	userGroup.Use(middleware.RequireAnyRole("admin", "super_admin"))
	{
		userGroup.POST("", userController.Create)
		userGroup.GET("", userController.List)
		userGroup.DELETE("/:id", userController.Delete)
		userGroup.PUT("/:id/password", userController.ResetPassword)
	}

	// Staff Routes (Employee Management)
	staffController := &api.StaffController{}
	staffGroup := protected.Group("/staff")
	staffGroup.Use(middleware.RequireAnyRole("admin", "super_admin"))
	{
		staffGroup.POST("", staffController.Create)
		staffGroup.GET("", staffController.List)
		staffGroup.DELETE("/:id", staffController.Delete)
		staffGroup.PUT("/:id/password", staffController.ResetPassword)
	}

	// Device Routes
	// Device Routes
	deviceService := service.NewDeviceService(model.DB, &service.TicketService{})
	deviceController := api.NewDeviceController(deviceService)

	// Public Hardware APIs

	apiGroup.POST("/hardware/heartbeat", deviceController.Heartbeat)
	apiGroup.POST("/hardware/verify", deviceController.Verify)

	// Admin APIs (Protected)
	deviceGroup := protected.Group("/devices")
	deviceGroup.Use(middleware.RequireAnyRole("admin", "super_admin"))
	{
		deviceGroup.POST("", deviceController.Create)
		deviceGroup.GET("", deviceController.List)
		deviceGroup.PUT("/:id", deviceController.Update)
		deviceGroup.POST("/:id/rotate-key", deviceController.RotateKey)
		deviceGroup.DELETE("/:id", deviceController.Delete)
	}

	// Product Routes
	productController := &api.ProductController{}
	productGroup := protected.Group("/products")
	{
		productGroup.POST("", middleware.RequireAnyRole("admin", "super_admin"), productController.Create)
		productGroup.PUT("/:id", middleware.RequireAnyRole("admin", "super_admin"), productController.Update)
		productGroup.GET("", middleware.RequireAnyRole("seller", "checker", "admin", "super_admin"), productController.List)
		productGroup.GET("/:id", middleware.RequireAnyRole("seller", "checker", "admin", "super_admin"), productController.Get)
		productGroup.DELETE("/:id", middleware.RequireAnyRole("admin", "super_admin"), productController.Delete)
		productGroup.PATCH("/:id/status", middleware.RequireAnyRole("admin", "super_admin"), productController.UpdateStatus)
	}

	orderController := &api.OrderController{}
	orderGroup := protected.Group("/orders")
	orderGroup.Use(middleware.RequireAnyRole("seller", "admin", "super_admin"))
	{
		orderGroup.POST("", orderController.Create)
		orderGroup.GET("", orderController.List)
	}

	// Ticket Routes
	ticketController := &api.TicketController{}
	ticketGroup := protected.Group("/tickets")
	ticketGroup.Use(middleware.RequireAnyRole("checker", "admin", "super_admin"))
	{
		ticketGroup.POST("/verify", ticketController.Verify)
	}

	// CheckPoint Routes
	cpController := &api.CheckPointController{}
	cpGroup := protected.Group("/checkpoints")
	{
		cpGroup.POST("", middleware.RequireAnyRole("admin", "super_admin"), cpController.Create)
		cpGroup.GET("", middleware.RequireAnyRole("seller", "checker", "admin", "super_admin"), cpController.List)
		cpGroup.PUT("/:id", middleware.RequireAnyRole("admin", "super_admin"), cpController.Update)
		cpGroup.DELETE("/:id", middleware.RequireAnyRole("admin", "super_admin"), cpController.Delete)
	}

	// Policy Routes
	policyController := &api.PolicyController{Service: service.PolicyService{}}
	policyGroup := protected.Group("/policies")
	{
		policyGroup.POST("", middleware.RequireAnyRole("admin", "super_admin"), policyController.Create)
		policyGroup.GET("", middleware.RequireAnyRole("seller", "admin", "super_admin"), policyController.List)
		policyGroup.PUT("/:id", middleware.RequireAnyRole("admin", "super_admin"), policyController.Update)
		policyGroup.DELETE("/:id", middleware.RequireAnyRole("admin", "super_admin"), policyController.Delete)
	}

	// Distribution Routes (B2B)
	distController := &api.DistributionController{Service: service.DistributionService{}}
	distGroup := protected.Group("/distribution")
	distGroup.Use(middleware.RequireAnyRole("admin", "super_admin"))
	{
		distGroup.GET("/search", distController.Search) // Search before apply
		distGroup.POST("/apply", distController.Apply)
		distGroup.GET("/suppliers", distController.ListSuppliers)

		// Supplier Side
		distGroup.GET("/agents", distController.ListAgents)
		distGroup.POST("/agents/:id/audit", distController.AuditAgent)
		distGroup.POST("/agents/:id/recharge", distController.RechargeAgent) // New Recharge Route

		// Product Distribution
		distGroup.GET("/products", distController.ListDistributableProducts)
		distGroup.POST("/products/import", distController.ImportProduct)
	}

	// OTA Routes (External Integration)
	otaController := &api.OTAController{
		OrderService:   service.OrderService{},
		ProductService: service.ProductService{},
	}
	otaGroup := apiGroup.Group("/ota")
	otaGroup.Use(middleware.OTASignMiddleware())
	{
		otaGroup.POST("/products", otaController.ListProducts)
		otaGroup.POST("/orders/create", otaController.CreateOrder)
		otaGroup.POST("/orders/cancel", otaController.CancelOrder)
		otaGroup.POST("/orders/query", otaController.QueryOrder)
	}

	// Finance Routes
	financeController := &api.FinanceController{Service: service.FinanceService{}}
	financeGroup := protected.Group("/finance")
	financeGroup.Use(middleware.RequireAnyRole("admin", "super_admin"))
	{
		financeGroup.GET("/accounts", financeController.ListAccounts)
		financeGroup.GET("/transactions", financeController.ListTransactions)
	}

	// Report Routes
	reportController := &api.ReportController{Service: service.ReportService{}}
	reportGroup := protected.Group("/reports")
	reportGroup.Use(middleware.RequireAnyRole("admin", "super_admin"))
	{
		reportGroup.GET("/sales", reportController.GetSales)
		reportGroup.GET("/products", reportController.GetProducts)
		reportGroup.GET("/channels", reportController.GetChannels)
	}

	// Payment Routes
	orderSvc := service.OrderService{}
	paymentSvc := service.PaymentService{OrderService: &orderSvc}

	paymentController := &api.PaymentController{Service: paymentSvc}
	configController := &api.PaymentConfigController{Service: paymentSvc}

	paymentGroup := protected.Group("/payments")
	{
		paymentGroup.POST("/pay", middleware.RequireAnyRole("seller", "admin", "super_admin"), paymentController.Pay)
		paymentGroup.GET("/configs", middleware.RequireAnyRole("admin", "super_admin"), configController.GetConfigs)
		paymentGroup.POST("/configs", middleware.RequireAnyRole("admin", "super_admin"), configController.SaveConfig)
		paymentGroup.GET("/:id", middleware.RequireAnyRole("seller", "admin", "super_admin"), paymentController.Query)
	}
}
