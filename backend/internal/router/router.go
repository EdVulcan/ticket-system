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
	apiGroup.POST("/auth/platform/login", authController.PlatformLogin)

	// Protected Routes
	protected := apiGroup.Group("")
	protected.Use(middleware.JWTAuth())
	platformController := &api.PlatformController{Service: service.PlatformService{}}
	platformGroup := protected.Group("/platform")
	platformGroup.Use(middleware.RequirePlatformScope(), middleware.RequireAnyRole("platform_admin"))
	platformGroup.GET("/overview", platformController.Overview)
	platformGroup.GET("/finance", platformController.FinanceOverview)
	platformGroup.GET("/orders", platformController.ListOrders)
	platformGroup.GET("/issues", platformController.ListIssues)
	platformGroup.GET("/devices", platformController.ListDevices)
	platformGroup.GET("/settlements", platformController.ListSettlements)
	platformGroup.GET("/audit-logs", platformController.ListAuditLogs)

	// Tenant Routes
	tenantController := &api.TenantController{}
	tenantGroup := protected.Group("/tenants")
	{
		tenantGroup.GET("/me", tenantController.GetSelf)
		tenantGroup.POST("", middleware.RequirePlatformScope(), middleware.RequireAnyRole("platform_admin"), tenantController.Create)
		tenantGroup.GET("", middleware.RequirePlatformScope(), middleware.RequireAnyRole("platform_admin"), tenantController.List)
		tenantGroup.PUT("/:id", middleware.RequirePlatformScope(), middleware.RequireAnyRole("platform_admin"), tenantController.Update)
		tenantGroup.PATCH("/:id/status", middleware.RequirePlatformScope(), middleware.RequireAnyRole("platform_admin"), tenantController.UpdateStatus)
		tenantGroup.PATCH("/:id/lifecycle", middleware.RequirePlatformScope(), middleware.RequireAnyRole("platform_admin"), tenantController.UpdateLifecycle)
		tenantGroup.POST("/:id/revoke-sessions", middleware.RequirePlatformScope(), middleware.RequireAnyRole("platform_admin"), tenantController.RevokeSessions)
		tenantGroup.PUT("/:id/capabilities/:capability", middleware.RequirePlatformScope(), middleware.RequireAnyRole("platform_admin"), tenantController.SetCapability)
		tenantGroup.DELETE("/:id", middleware.RequirePlatformScope(), middleware.RequireAnyRole("platform_admin"), tenantController.Delete)
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
		staffGroup.PUT("/:id/resource-scopes", staffController.SetResourceScopes)
	}

	// Device Routes
	// Device Routes
	deviceService := service.NewDeviceService(model.DB, &service.TicketService{})
	deviceController := api.NewDeviceController(deviceService)

	// Public Hardware APIs

	apiGroup.POST("/hardware/heartbeat", deviceController.Heartbeat)
	apiGroup.POST("/hardware/verify", deviceController.Verify)
	apiGroup.POST("/hardware/commands/poll", deviceController.PollCommand)
	apiGroup.POST("/hardware/commands/ack", deviceController.AckCommand)

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
	hardwareCommandGroup := protected.Group("/hardware-commands")
	hardwareCommandGroup.Use(middleware.RequireAnyRole("seller", "admin", "super_admin"))
	{
		hardwareCommandGroup.POST("", deviceController.QueueCommand)
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
		orderGroup.POST("/:orderNo/cancel", orderController.Cancel)
	}

	afterSaleController := &api.AfterSaleController{Service: service.AfterSaleService{}}
	afterSaleGroup := protected.Group("/after-sales")
	{
		afterSaleGroup.POST("", middleware.RequireAnyRole("seller", "admin", "super_admin"), afterSaleController.Create)
		afterSaleGroup.GET("", middleware.RequireAnyRole("seller", "admin", "super_admin"), afterSaleController.List)
		afterSaleGroup.GET("/:id", middleware.RequireAnyRole("seller", "admin", "super_admin"), afterSaleController.Get)
		afterSaleGroup.POST("/:id/approve", middleware.RequireAnyRole("admin", "super_admin"), afterSaleController.Approve)
		afterSaleGroup.POST("/:id/reject", middleware.RequireAnyRole("admin", "super_admin"), afterSaleController.Reject)
		afterSaleGroup.POST("/:id/execute", middleware.RequireAnyRole("seller", "admin", "super_admin"), afterSaleController.Execute)
	}

	// Ticket Routes
	ticketController := &api.TicketController{}
	ticketGroup := protected.Group("/tickets")
	ticketGroup.Use(middleware.RequireAnyRole("checker", "admin", "super_admin"))
	{
		ticketGroup.POST("/verify", ticketController.Verify)
	}

	// CheckPoint Routes
	scenicAreaController := &api.ScenicAreaController{}
	scenicAreaGroup := protected.Group("/scenic-areas")
	{
		scenicAreaGroup.GET("", middleware.RequireAnyRole("seller", "checker", "admin", "super_admin"), scenicAreaController.List)
		scenicAreaGroup.POST("", middleware.RequireAnyRole("admin", "super_admin"), scenicAreaController.Create)
		scenicAreaGroup.PUT("/:id", middleware.RequireAnyRole("admin", "super_admin"), scenicAreaController.Update)
		scenicAreaGroup.DELETE("/:id", middleware.RequireAnyRole("admin", "super_admin"), scenicAreaController.Delete)
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
		distGroup.POST("/offers", distController.CreateOffer)
		distGroup.GET("/offers", distController.ListOffers)
		distGroup.PATCH("/offers/:id/status", distController.SetOfferStatus)
		distGroup.GET("/fulfillments", distController.ListFulfillmentOrders)
		distGroup.GET("/products", distController.ListDistributableProducts)
		distGroup.POST("/products/import", distController.ImportProduct)
		distGroup.POST("/listings/:id/sync", distController.SyncListing)
	}

	// OTA Routes (External Integration)
	otaController := &api.OTAController{
		OrderService:   service.OrderService{},
		ProductService: service.ProductService{},
	}
	channelRegistry := service.NewChannelAdapterRegistry(service.NewCoreChannelAdapter())
	channelGateway := &service.ChannelGatewayService{Registry: channelRegistry}
	otaController.Gateway = channelGateway
	channelController := &api.ChannelController{Service: service.ChannelService{}, Gateway: channelGateway}

	// Independently credentialed channel routes. The legacy /ota routes remain
	// available for migration and continue using the tenant OTA secret.
	channelGroup := apiGroup.Group("/channels/:code")
	channelGroup.Use(middleware.ChannelAuthMiddleware())
	{
		channelGroup.POST("/products", otaController.ListProducts)
		channelGroup.POST("/orders/create", otaController.CreateOrder)
		channelGroup.POST("/orders/cancel", otaController.CancelOrder)
		channelGroup.POST("/orders/query", otaController.QueryOrder)
		channelGroup.POST("/orders/refund", otaController.RefundOrder)
		channelGroup.POST("/reservations/create", channelController.Reserve)
		channelGroup.POST("/reservations/confirm", channelController.Confirm)
		channelGroup.POST("/reservations/release", channelController.Release)
	}

	channelAdminGroup := protected.Group("/channel-accounts")
	channelAdminGroup.Use(middleware.RequireAnyRole("admin", "super_admin"))
	{
		channelAdminGroup.GET("", channelController.List)
		channelAdminGroup.POST("", channelController.Create)
		channelAdminGroup.PATCH("/:id/status", channelController.SetStatus)
		channelAdminGroup.POST("/:id/rotate-secret", channelController.RotateSecret)
		channelAdminGroup.GET("/mappings", channelController.ListMappings)
		channelAdminGroup.POST("/mappings", channelController.AddMapping)
		channelAdminGroup.POST("/:id/bills/import", channelController.ImportBill)
		channelAdminGroup.GET("/:id/reconciliations", channelController.ListReconciliations)
	}

	teamController := &api.TeamController{Service: service.TeamService{}}
	teamGroup := protected.Group("/teams")
	teamGroup.Use(middleware.RequireAnyRole("seller", "admin", "super_admin"))
	{
		teamGroup.GET("/contracts", teamController.ListContracts)
		teamGroup.POST("/contracts", teamController.CreateContract)
		teamGroup.GET("/agents", teamController.ListAgents)
		teamGroup.POST("/agents", teamController.CreateAgent)
		teamGroup.GET("/guides", teamController.ListGuides)
		teamGroup.POST("/guides", teamController.CreateGuide)
		teamGroup.GET("/vehicles", teamController.ListVehicles)
		teamGroup.POST("/vehicles", teamController.CreateVehicle)
		teamGroup.GET("", teamController.ListGroups)
		teamGroup.POST("", teamController.CreateGroup)
		teamGroup.GET("/:id/members", teamController.ListMembers)
		teamGroup.POST("/:id/members", teamController.AddMembers)
		teamGroup.PUT("/:id/members", teamController.ReplaceMembers)
		teamGroup.POST("/:id/enter-batch", teamController.EnterBatch)
		teamGroup.POST("/:id/attach-order", teamController.AttachOrder)
		teamGroup.GET("/settlements", teamController.ListSettlements)
		teamGroup.POST("/:id/settlement", teamController.GenerateSettlement)
		teamGroup.PATCH("/settlements/:id/status", teamController.SetSettlementStatus)
	}

	operationsController := &api.OperationsController{Service: service.OperationsService{}}
	operationsGroup := protected.Group("/operations")
	operationsGroup.Use(middleware.RequireAnyRole("seller", "admin", "super_admin"))
	{
		operationsGroup.GET("/shifts", operationsController.ListShifts)
		operationsGroup.GET("/shifts/:id/summary", operationsController.GetShiftSummary)
		operationsGroup.GET("/shifts/open", operationsController.GetOpenShift)
		operationsGroup.POST("/shifts", operationsController.OpenShift)
		operationsGroup.POST("/shifts/:id/close", operationsController.CloseShift)
		operationsGroup.POST("/shifts/:id/reconcile", operationsController.ReconcileShift)
		operationsGroup.POST("/shifts/:id/corrections", operationsController.CorrectShift)
		operationsGroup.POST("/print-jobs", operationsController.QueuePrint)
		operationsGroup.GET("/print-jobs", operationsController.ListPrintJobs)
		operationsGroup.POST("/print-jobs/:id/status", operationsController.UpdatePrintStatus)
		operationsGroup.POST("/holds", operationsController.CreateHold)
		operationsGroup.GET("/holds", operationsController.ListHolds)
		operationsGroup.POST("/holds/:id/resume", operationsController.ResumeHold)
		operationsGroup.POST("/holds/:id/cancel", operationsController.CancelHold)
		operationsGroup.GET("/alerts", operationsController.ListAlerts)
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
		financeGroup.GET("/managed-accounts", financeController.ListManagedAccounts)
		financeGroup.GET("/transactions", financeController.ListTransactions)
		financeGroup.GET("/ledger", financeController.ListLedger)
		financeGroup.GET("/managed-ledger", financeController.ListManagedLedger)
		financeGroup.GET("/documents", financeController.ListDocuments)
		financeGroup.POST("/documents", financeController.CreateDocument)
		financeGroup.POST("/documents/:id/approve", financeController.ApproveDocument)
	}

	settlementController := &api.SettlementController{Service: service.SettlementService{}}
	settlementGroup := protected.Group("/settlements")
	settlementGroup.Use(middleware.RequireAnyRole("admin", "super_admin"))
	{
		settlementGroup.GET("", settlementController.List)
		settlementGroup.GET("/:id", settlementController.Get)
		settlementGroup.POST("/generate", settlementController.Generate)
		settlementGroup.PATCH("/:id/status", settlementController.SetStatus)
	}

	// Report Routes
	reportController := &api.ReportController{Service: service.ReportService{}}
	reportGroup := protected.Group("/reports")
	reportGroup.Use(middleware.RequireAnyRole("admin", "super_admin"))
	{
		reportGroup.GET("/sales", reportController.GetSales)
		reportGroup.GET("/products", reportController.GetProducts)
		reportGroup.GET("/channels", reportController.GetChannels)
		reportGroup.GET("/daily", reportController.GetDaily)
		reportGroup.GET("/operations", reportController.GetOperations)
	}

	// Payment Routes
	orderSvc := service.OrderService{}
	paymentSvc := service.PaymentService{OrderService: &orderSvc}

	paymentController := &api.PaymentController{Service: paymentSvc}
	refundController := &api.RefundController{}
	configController := &api.PaymentConfigController{Service: paymentSvc}
	// Provider callbacks are authenticated by the provider signature inside the
	// payment service. They must remain outside the tenant JWT middleware because
	// the provider cannot present an operator token.
	apiGroup.POST("/payments/notify/wechat/:tenantID", paymentController.WeChatNotify)
	apiGroup.POST("/payments/notify/alipay/:tenantID", paymentController.AlipayNotify)

	paymentGroup := protected.Group("/payments")
	{
		paymentGroup.POST("/pay", middleware.RequireAnyRole("seller", "admin", "super_admin"), paymentController.Pay)
		paymentGroup.POST("/refunds/cash", middleware.RequireAnyRole("seller", "admin", "super_admin"), refundController.CreateCash)
		paymentGroup.POST("/refunds/digital", middleware.RequireAnyRole("seller", "admin", "super_admin"), refundController.CreateDigital)
		paymentGroup.GET("/refund-tasks", middleware.RequireAnyRole("admin", "super_admin"), refundController.ListDigitalTasks)
		paymentGroup.POST("/refund-tasks/:id/retry", middleware.RequireAnyRole("admin", "super_admin"), refundController.RetryDigitalTask)
		paymentGroup.GET("/configs", middleware.RequireAnyRole("admin", "super_admin"), configController.GetConfigs)
		paymentGroup.POST("/configs", middleware.RequireAnyRole("admin", "super_admin"), configController.SaveConfig)
		paymentGroup.GET("/:id", middleware.RequireAnyRole("seller", "admin", "super_admin"), paymentController.Query)
	}
}
