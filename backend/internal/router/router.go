package router

import (
	"ticket-backend/internal/api"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/config"
	"ticket-backend/internal/middleware"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

func InitRouter(r *gin.Engine) {
	// Global Middleware
	r.Use(middleware.SecurityHeaders(), middleware.RequestBodyLimit(8<<20), middleware.Cors())

	apiGroup := r.Group("/api/v1")

	// Public Routes
	authController := &api.AuthController{Service: service.AuthService{}}
	loginLimit := middleware.LoginRateLimit()
	apiGroup.POST("/auth/login", loginLimit, authController.Login)
	apiGroup.POST("/auth/staff/login", loginLimit, authController.StaffLogin)
	apiGroup.POST("/auth/platform/login", loginLimit, authController.PlatformLogin)

	miniappController := &api.MiniappController{Service: service.NewMiniappService()}
	miniappGroup := apiGroup.Group("/storefront/xiaohongshu")
	miniappGroup.POST("/session", middleware.MiniappLoginRateLimit(), miniappController.LoginXiaohongshu)
	miniappGroup.GET("/catalog", miniappController.ListCatalog)
	miniappGroup.POST("/orders", miniappController.CreateOrder)
	miniappGroup.GET("/orders", miniappController.ListOrders)
	miniappGroup.GET("/orders/:orderNo", miniappController.GetOrder)
	miniappGroup.POST("/orders/:orderNo/package-bookings", miniappController.BookPackage)
	miniappGroup.POST("/orders/:orderNo/package-bookings/:entitlementNo/cancel", miniappController.CancelPackageBooking)
	xiaohongshuWebhookController := api.XiaohongshuWebhookController{}
	xiaohongshuWebhookGroup := apiGroup.Group("/integrations/xiaohongshu/events")
	xiaohongshuWebhookGroup.GET("/:appID", xiaohongshuWebhookController.Verify)
	xiaohongshuWebhookGroup.POST("/:appID", xiaohongshuWebhookController.Receive)

	// Protected Routes
	protected := apiGroup.Group("")
	protected.Use(middleware.JWTAuth())
	protected.PUT("/auth/password", authController.ChangePassword)
	platformController := &api.PlatformController{Service: service.PlatformService{}}
	platformGroup := protected.Group("/platform")
	platformGroup.Use(middleware.RequirePlatformScope(), middleware.RequireAnyRole("platform_admin", "platform_operator"))
	platformGroup.GET("/overview", platformController.Overview)
	platformGroup.GET("/finance", platformController.FinanceOverview)
	platformGroup.GET("/orders", platformController.ListOrders)
	platformGroup.GET("/issues", platformController.ListIssues)
	platformGroup.GET("/devices", platformController.ListDevices)
	platformGroup.GET("/settlements", platformController.ListSettlements)
	platformGroup.GET("/audit-logs", platformController.ListAuditLogs)

	platformUserController := &api.PlatformUserController{}
	platformUserGroup := protected.Group("/platform-users")
	platformUserGroup.Use(middleware.RequirePlatformScope(), middleware.RequireAnyRole("platform_admin"))
	platformUserGroup.GET("", platformUserController.List)
	platformUserGroup.POST("", platformUserController.Create)
	platformUserGroup.PUT("/:id", platformUserController.Update)
	platformUserGroup.PUT("/:id/password", platformUserController.ResetPassword)
	platformUserGroup.DELETE("/:id", platformUserController.Delete)

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
		tenantGroup.PUT("/:id/supplier-business-types/:businessType", middleware.RequirePlatformScope(), middleware.RequireAnyRole("platform_admin"), tenantController.SetSupplierBusinessType)
		tenantGroup.DELETE("/:id", middleware.RequirePlatformScope(), middleware.RequireAnyRole("platform_admin"), tenantController.Delete)
	}

	// User Routes (Staff Management)
	userController := &api.UserController{}
	userGroup := protected.Group("/users")
	userGroup.Use(middleware.RequireTenantPermission(authz.PermissionTenantAccounts))
	{
		userGroup.POST("", userController.Create)
		userGroup.GET("", userController.List)
		userGroup.DELETE("/:id", userController.Delete)
		userGroup.PUT("/:id/password", userController.ResetPassword)
		userGroup.PUT("/:id/role", userController.UpdateRole)
	}

	// Staff Routes (Employee Management)
	staffController := &api.StaffController{}
	staffGroup := protected.Group("/staff")
	staffGroup.Use(middleware.RequireTenantPermission(authz.PermissionOnsiteManage), middleware.RequireAnyTenantCapability("supplier"), middleware.RequireAnySupplierBusinessType("scenic"))
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

	hardwareGroup := apiGroup.Group("/hardware")
	hardwareGroup.Use(middleware.DeviceAuth())
	hardwareGroup.POST("/heartbeat", deviceController.Heartbeat)
	hardwareGroup.POST("/verify", deviceController.Verify)
	hardwareGroup.POST("/open-result", deviceController.OpenResult)
	hardwareGroup.POST("/commands/poll", deviceController.PollCommand)
	hardwareGroup.POST("/commands/ack", deviceController.AckCommand)

	// Admin APIs (Protected)
	deviceGroup := protected.Group("/devices")
	deviceGroup.Use(middleware.RequireAnyTenantCapability("supplier"), middleware.RequireAnySupplierBusinessType("scenic"))
	{
		deviceGroup.POST("", middleware.RequireTenantPermission(authz.PermissionOnsiteManage), deviceController.Create)
		deviceGroup.GET("", middleware.RequireTenantPermission(authz.PermissionOnsiteRead), deviceController.List)
		deviceGroup.PUT("/:id", middleware.RequireTenantPermission(authz.PermissionOnsiteManage), deviceController.Update)
		deviceGroup.POST("/:id/rotate-key", middleware.RequireTenantPermission(authz.PermissionOnsiteManage), deviceController.RotateKey)
		deviceGroup.DELETE("/:id", middleware.RequireTenantPermission(authz.PermissionOnsiteManage), deviceController.Delete)
	}
	hardwareCommandGroup := protected.Group("/hardware-commands")
	hardwareCommandGroup.Use(middleware.RequireTenantPermission(authz.PermissionOnsiteManage), middleware.RequireAnyTenantCapability("supplier"), middleware.RequireAnySupplierBusinessType("scenic"))
	{
		hardwareCommandGroup.POST("", deviceController.QueueCommand)
	}

	// Product Routes
	productController := &api.ProductController{}
	productGroup := protected.Group("/products")
	productGroup.Use(middleware.RequireAnyTenantCapability("supplier", "distributor"))
	{
		productGroup.POST("", middleware.RequireTenantPermission(authz.PermissionCatalogWrite), productController.Create)
		productGroup.PUT("/:id", middleware.RequireTenantPermission(authz.PermissionCatalogWrite), productController.Update)
		productGroup.GET("", middleware.RequireTenantPermission(authz.PermissionCatalogRead), productController.List)
		productGroup.GET("/:id", middleware.RequireTenantPermission(authz.PermissionCatalogRead), productController.Get)
		productGroup.DELETE("/:id", middleware.RequireTenantPermission(authz.PermissionCatalogWrite), productController.Delete)
		productGroup.PATCH("/:id/status", middleware.RequireTenantPermission(authz.PermissionCatalogWrite), productController.UpdateStatus)
	}

	orderController := &api.OrderController{}
	orderGroup := protected.Group("/orders")
	orderGroup.Use(middleware.RequireAnyTenantCapability("supplier", "distributor", "travel_agency"))
	{
		orderGroup.POST("", middleware.RequireTenantPermission(authz.PermissionOrdersWrite), orderController.Create)
		orderGroup.GET("", middleware.RequireTenantPermission(authz.PermissionOrdersRead), orderController.List)
		orderGroup.GET("/:orderNo", middleware.RequireTenantPermission(authz.PermissionOrdersRead), orderController.Get)
		orderGroup.POST("/:orderNo/cancel", middleware.RequireTenantPermission(authz.PermissionOrdersWrite), orderController.Cancel)
	}

	afterSaleController := &api.AfterSaleController{Service: service.AfterSaleService{}}
	afterSaleGroup := protected.Group("/after-sales")
	afterSaleGroup.Use(middleware.RequireAnyTenantCapability("supplier", "distributor", "travel_agency"))
	{
		afterSaleGroup.POST("", middleware.RequireTenantPermission(authz.PermissionAfterSalesWrite), afterSaleController.Create)
		afterSaleGroup.GET("", middleware.RequireTenantPermission(authz.PermissionAfterSalesRead), afterSaleController.List)
		afterSaleGroup.GET("/:id", middleware.RequireTenantPermission(authz.PermissionAfterSalesRead), afterSaleController.Get)
		afterSaleGroup.POST("/:id/approve", middleware.RequireTenantPermission(authz.PermissionAfterSalesApprove), afterSaleController.Approve)
		afterSaleGroup.POST("/:id/reject", middleware.RequireTenantPermission(authz.PermissionAfterSalesApprove), afterSaleController.Reject)
		afterSaleGroup.POST("/:id/execute", middleware.RequireTenantPermission(authz.PermissionAfterSalesWrite), afterSaleController.Execute)
		afterSaleGroup.POST("/:id/difference-payment", middleware.RequireTenantPermission(authz.PermissionAfterSalesWrite), afterSaleController.CollectDifference)
	}

	// Ticket Routes
	ticketController := &api.TicketController{}
	ticketGroup := protected.Group("/tickets")
	ticketGroup.Use(middleware.RequireTenantPermission(authz.PermissionTicketsVerify), middleware.RequireAnyTenantCapability("supplier"), middleware.RequireAnySupplierBusinessType("scenic"))
	{
		ticketGroup.POST("/verify", ticketController.Verify)
	}

	// CheckPoint Routes
	scenicAreaController := &api.ScenicAreaController{}
	scenicAreaGroup := protected.Group("/scenic-areas")
	scenicAreaGroup.Use(middleware.RequireAnyTenantCapability("supplier"), middleware.RequireAnySupplierBusinessType("scenic"))
	{
		scenicAreaGroup.GET("", middleware.RequireTenantPermission(authz.PermissionOnsiteRead), scenicAreaController.List)
		scenicAreaGroup.POST("", middleware.RequireTenantPermission(authz.PermissionOnsiteManage), scenicAreaController.Create)
		scenicAreaGroup.PUT("/:id", middleware.RequireTenantPermission(authz.PermissionOnsiteManage), scenicAreaController.Update)
		scenicAreaGroup.DELETE("/:id", middleware.RequireTenantPermission(authz.PermissionOnsiteManage), scenicAreaController.Delete)
	}

	// Hotel catalog and room inventory remain independent from scenic tickets.
	hotelController := &api.HotelController{Service: service.HotelService{}}
	hotelGroup := protected.Group("/hotels")
	{
		hotelGroup.GET("", middleware.RequireConfiguredSupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogRead), hotelController.ListProperties)
		hotelGroup.POST("", middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogWrite), hotelController.CreateProperty)
		hotelGroup.PUT("/:hotelID", middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogWrite), hotelController.UpdateProperty)
		hotelGroup.DELETE("/:hotelID", middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogWrite), hotelController.DeleteProperty)
		hotelGroup.GET("/:hotelID/room-types", middleware.RequireConfiguredSupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogRead), hotelController.ListRoomTypes)
		hotelGroup.POST("/:hotelID/room-types", middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogWrite), hotelController.CreateRoomType)
		hotelGroup.PUT("/:hotelID/room-types/:roomTypeID", middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogWrite), hotelController.UpdateRoomType)
		hotelGroup.DELETE("/:hotelID/room-types/:roomTypeID", middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogWrite), hotelController.DeleteRoomType)
		hotelGroup.GET("/:hotelID/room-types/:roomTypeID/rate-plans", middleware.RequireConfiguredSupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogRead), hotelController.ListRatePlans)
		hotelGroup.POST("/:hotelID/room-types/:roomTypeID/rate-plans", middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogWrite), hotelController.CreateRatePlan)
		hotelGroup.PUT("/:hotelID/room-types/:roomTypeID/rate-plans/:ratePlanID", middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogWrite), hotelController.UpdateRatePlan)
		hotelGroup.DELETE("/:hotelID/room-types/:roomTypeID/rate-plans/:ratePlanID", middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogWrite), hotelController.DeleteRatePlan)
		hotelGroup.GET("/:hotelID/room-types/:roomTypeID/inventory", middleware.RequireConfiguredSupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogRead), hotelController.ListInventory)
		hotelGroup.PUT("/:hotelID/room-types/:roomTypeID/inventory", middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogWrite), hotelController.SetInventory)
	}

	packageController := &api.ScenicHotelPackageController{Service: service.ScenicHotelPackageService{}}
	packageGroup := protected.Group("/scenic-hotel-packages")
	{
		packageGroup.GET("", middleware.RequireConfiguredSupplierBusinessType("scenic"), middleware.RequireConfiguredSupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogRead), packageController.List)
		packageGroup.GET("/reservations", middleware.RequireConfiguredSupplierBusinessType("scenic"), middleware.RequireConfiguredSupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionHotelReservationsRead), packageController.ListReservations)
		packageGroup.GET("/entitlements", middleware.RequireConfiguredSupplierBusinessType("scenic"), middleware.RequireConfiguredSupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionHotelReservationsRead), packageController.ListEntitlements)
		packageGroup.GET("/reservations/export", middleware.RequireConfiguredSupplierBusinessType("scenic"), middleware.RequireConfiguredSupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionHotelReservationsExport), packageController.ExportReservations)
		packageGroup.GET("/business-summary", middleware.RequireConfiguredSupplierBusinessType("scenic"), middleware.RequireConfiguredSupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionReportsRead), packageController.BusinessSummary)
		packageGroup.PATCH("/reservations/:reservationID/status", middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionHotelReservationsWrite), packageController.SetReservationStatus)
		packageGroup.POST("", middleware.RequireAnySupplierBusinessType("scenic"), middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogWrite), packageController.Create)
		packageGroup.PUT("/:packageID", middleware.RequireAnySupplierBusinessType("scenic"), middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogWrite), packageController.Update)
		packageGroup.DELETE("/:packageID", middleware.RequireAnySupplierBusinessType("scenic"), middleware.RequireAnySupplierBusinessType("hotel"), middleware.RequireTenantPermission(authz.PermissionCatalogWrite), packageController.Delete)
	}

	// CheckPoint Routes
	cpController := &api.CheckPointController{}
	cpGroup := protected.Group("/checkpoints")
	cpGroup.Use(middleware.RequireAnyTenantCapability("supplier"), middleware.RequireAnySupplierBusinessType("scenic"))
	{
		cpGroup.POST("", middleware.RequireTenantPermission(authz.PermissionOnsiteManage), cpController.Create)
		cpGroup.GET("", middleware.RequireTenantPermission(authz.PermissionOnsiteRead), cpController.List)
		cpGroup.PUT("/:id", middleware.RequireTenantPermission(authz.PermissionOnsiteManage), cpController.Update)
		cpGroup.DELETE("/:id", middleware.RequireTenantPermission(authz.PermissionOnsiteManage), cpController.Delete)
	}

	// Policy Routes
	policyController := &api.PolicyController{Service: service.PolicyService{}}
	policyGroup := protected.Group("/policies")
	policyGroup.Use(middleware.RequireAnyTenantCapability("supplier"), middleware.RequireAnySupplierBusinessType("scenic"))
	{
		policyGroup.POST("", middleware.RequireTenantPermission(authz.PermissionCatalogWrite), policyController.Create)
		policyGroup.GET("", middleware.RequireTenantPermission(authz.PermissionCatalogRead), policyController.List)
		policyGroup.PUT("/:id", middleware.RequireTenantPermission(authz.PermissionCatalogWrite), policyController.Update)
		policyGroup.DELETE("/:id", middleware.RequireTenantPermission(authz.PermissionCatalogWrite), policyController.Delete)
	}

	// Distribution Routes (B2B)
	distController := &api.DistributionController{Service: service.DistributionService{}, BundleService: service.BundleService{}, RefundService: service.RefundService{}}
	distGroup := protected.Group("/distribution")
	distGroup.Use(middleware.RequireAnyTenantCapability("supplier", "distributor"))
	{
		distGroup.GET("/search", middleware.RequireTenantPermission(authz.PermissionDistributionRead), distController.Search)
		distGroup.POST("/apply", middleware.RequireTenantPermission(authz.PermissionDistributionWrite), distController.Apply)
		distGroup.GET("/suppliers", middleware.RequireTenantPermission(authz.PermissionDistributionRead), distController.ListSuppliers)

		// Supplier Side
		distGroup.GET("/agents", middleware.RequireTenantPermission(authz.PermissionDistributionRead), distController.ListAgents)
		distGroup.POST("/agents/:id/audit", middleware.RequireTenantPermission(authz.PermissionDistributionWrite), distController.AuditAgent)
		distGroup.POST("/agents/:id/recharge", middleware.RequireTenantPermission(authz.PermissionDistributionWrite), distController.RechargeAgent)

		// Product Distribution
		distGroup.POST("/offers", middleware.RequireTenantPermission(authz.PermissionDistributionWrite), distController.CreateOffer)
		distGroup.GET("/offers", middleware.RequireTenantPermission(authz.PermissionDistributionRead), distController.ListOffers)
		distGroup.PATCH("/offers/:id/status", middleware.RequireTenantPermission(authz.PermissionDistributionWrite), distController.SetOfferStatus)
		distGroup.GET("/fulfillments", middleware.RequireTenantPermission(authz.PermissionDistributionRead), distController.ListFulfillmentOrders)
		distGroup.GET("/fulfillments/:id", middleware.RequireTenantPermission(authz.PermissionDistributionRead), distController.GetFulfillmentOrder)
		distGroup.POST("/fulfillments/:id/used-refunds", middleware.RequireTenantPermission(authz.PermissionAfterSalesApprove), distController.RefundUsedFulfillmentTicket)
		distGroup.GET("/products", middleware.RequireTenantPermission(authz.PermissionDistributionRead), distController.ListDistributableProducts)
		distGroup.POST("/products/import", middleware.RequireTenantPermission(authz.PermissionDistributionWrite), distController.ImportProduct)
		distGroup.POST("/listings/:id/sync", middleware.RequireTenantPermission(authz.PermissionDistributionWrite), distController.SyncListing)
		distGroup.GET("/bundle-components", middleware.RequireTenantPermission(authz.PermissionDistributionRead), distController.ListBundleComponents)
		distGroup.GET("/bundles", middleware.RequireTenantPermission(authz.PermissionDistributionRead), distController.ListBundles)
		distGroup.POST("/bundles", middleware.RequireTenantPermission(authz.PermissionDistributionWrite), distController.CreateBundle)
		distGroup.GET("/bundles/:id", middleware.RequireTenantPermission(authz.PermissionDistributionRead), distController.GetBundle)
		distGroup.PUT("/bundles/:id", middleware.RequireTenantPermission(authz.PermissionDistributionWrite), distController.ReviseBundle)
		distGroup.PATCH("/bundles/:id/status", middleware.RequireTenantPermission(authz.PermissionDistributionWrite), distController.SetBundleStatus)
	}
	protected.GET("/bundle-catalog", middleware.RequireTenantPermission(authz.PermissionCatalogRead), middleware.RequireAnyTenantCapability("supplier", "distributor"), distController.ListBundleCatalog)

	// OTA Routes (External Integration)
	otaController := &api.OTAController{
		OrderService:   service.OrderService{},
		ProductService: service.ProductService{},
	}
	channelRegistry := service.NewChannelAdapterRegistry(service.NewCoreChannelAdapter())
	channelGateway := &service.ChannelGatewayService{Registry: channelRegistry}
	otaController.Gateway = channelGateway
	channelController := &api.ChannelController{
		Service: service.ChannelService{}, Gateway: channelGateway, CtripSync: service.CtripSyncService{},
		XiaohongshuProducts: service.NewXiaohongshuProductService(),
		XiaohongshuImages: service.XiaohongshuImageStore{
			Directory: config.GlobalConfig.Server.UploadDirectory, PublicBaseURL: config.GlobalConfig.Server.PublicBaseURL,
		},
	}
	ctripController := &api.CtripController{Service: service.CtripProtocolService{OrderService: service.OrderService{}}}
	apiGroup.POST("/integrations/ctrip/order", ctripController.HandleOrder)

	// Independently credentialed channel routes are the only generic external
	// sales surface. The weaker tenant-secret /ota compatibility route is not
	// registered in this new-system deployment.
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
	channelAdminGroup.Use(middleware.RequireAnyTenantCapability("supplier", "distributor"))
	{
		channelAdminGroup.GET("", middleware.RequireTenantPermission(authz.PermissionChannelsRead), channelController.List)
		channelAdminGroup.POST("", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.Create)
		channelAdminGroup.PATCH("/:id/status", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.SetStatus)
		channelAdminGroup.POST("/:id/rotate-secret", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.RotateSecret)
		channelAdminGroup.PUT("/:id/ctrip-config", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.ConfigureCtrip)
		channelAdminGroup.PUT("/:id/xiaohongshu-config", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.ConfigureXiaohongshu)
		channelAdminGroup.GET("/mappings", middleware.RequireTenantPermission(authz.PermissionChannelsRead), channelController.ListMappings)
		channelAdminGroup.POST("/mappings", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.AddMapping)
		channelAdminGroup.PATCH("/:id/mappings/:mappingId", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.UpdateMapping)
		channelAdminGroup.GET("/:id/xiaohongshu-categories", middleware.RequireTenantPermission(authz.PermissionChannelsRead), channelController.ListXiaohongshuCategories)
		channelAdminGroup.GET("/:id/xiaohongshu-pois", middleware.RequireTenantPermission(authz.PermissionChannelsRead), channelController.ListXiaohongshuPOIs)
		channelAdminGroup.GET("/:id/mappings/:mappingId/xiaohongshu-product", middleware.RequireTenantPermission(authz.PermissionChannelsRead), channelController.GetXiaohongshuProductConfig)
		channelAdminGroup.PUT("/:id/mappings/:mappingId/xiaohongshu-product", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.SaveXiaohongshuProductConfig)
		channelAdminGroup.POST("/:id/mappings/:mappingId/xiaohongshu-product-image", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.UploadXiaohongshuProductImage)
		channelAdminGroup.POST("/:id/mappings/:mappingId/xiaohongshu-sync", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.SyncXiaohongshuProduct)
		channelAdminGroup.POST("/:id/mappings/:mappingId/ctrip-sync", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.SyncCtripMapping)
		channelAdminGroup.POST("/:id/ctrip-sandbox-consume", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.SimulateCtripSandboxConsumption)
		channelAdminGroup.PATCH("/:id/mappings/:mappingId/ctrip-pricing", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.UpdateCtripMappingPricing)
		channelAdminGroup.GET("/:id/ctrip-sync-tasks", middleware.RequireTenantPermission(authz.PermissionChannelsRead), channelController.ListCtripSyncTasks)
		channelAdminGroup.POST("/:id/bills/import", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.ImportBill)
		channelAdminGroup.GET("/:id/reconciliations", middleware.RequireTenantPermission(authz.PermissionChannelsRead), channelController.ListReconciliations)
		channelAdminGroup.GET("/:id/reconciliations/:reconciliationId", middleware.RequireTenantPermission(authz.PermissionChannelsRead), channelController.GetReconciliation)
		channelAdminGroup.GET("/:id/requests", middleware.RequireTenantPermission(authz.PermissionChannelsRead), channelController.ListRequests)
		channelAdminGroup.POST("/:id/requests/:requestId/authorize-retry", middleware.RequireTenantPermission(authz.PermissionChannelsWrite), channelController.AuthorizeRequestRetry)
		channelAdminGroup.GET("/:id/orders", middleware.RequireTenantPermission(authz.PermissionChannelsRead), channelController.ListOrders)
		channelAdminGroup.GET("/:id/orders/:orderNo", middleware.RequireTenantPermission(authz.PermissionChannelsRead), channelController.GetOrder)
	}

	teamController := &api.TeamController{Service: service.TeamService{}}
	teamGroup := protected.Group("/teams")
	teamGroup.Use(middleware.RequireAnyTenantCapability("supplier", "travel_agency"))
	{
		teamGroup.GET("/partners/supplier-search", middleware.RequireTenantPermission(authz.PermissionTeamsRead), middleware.RequireAnyTenantCapability("travel_agency"), teamController.SearchSupplierPartner)
		teamGroup.POST("/partners/suppliers", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), middleware.RequireAnyTenantCapability("travel_agency"), teamController.ApplySupplierPartner)
		teamGroup.GET("/partners/suppliers", middleware.RequireTenantPermission(authz.PermissionTeamsRead), middleware.RequireAnyTenantCapability("travel_agency"), teamController.ListSupplierPartners)
		teamGroup.GET("/partners/travel-agencies", middleware.RequireTenantPermission(authz.PermissionTeamsRead), middleware.RequireAnyTenantCapability("supplier"), teamController.ListTravelAgencyPartners)
		teamGroup.POST("/partners/travel-agencies/:id/audit", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), middleware.RequireAnyTenantCapability("supplier"), teamController.AuditTravelAgencyPartner)
		teamGroup.GET("/contract-partners", middleware.RequireTenantPermission(authz.PermissionTeamsRead), teamController.ListContractPartners)
		teamGroup.GET("/contract-products", middleware.RequireTenantPermission(authz.PermissionTeamsRead), middleware.RequireAnyTenantCapability("supplier"), middleware.RequireAnySupplierBusinessType("scenic"), teamController.ListContractProducts)
		teamGroup.GET("/contracts", middleware.RequireTenantPermission(authz.PermissionTeamsRead), teamController.ListContracts)
		teamGroup.POST("/contracts", middleware.RequireTenantPermission(authz.PermissionTeamContractsWrite), teamController.CreateContract)
		teamGroup.PUT("/contracts/:id", middleware.RequireTenantPermission(authz.PermissionTeamContractsWrite), teamController.UpdateContract)
		teamGroup.GET("/agents", middleware.RequireTenantPermission(authz.PermissionTeamsRead), teamController.ListAgents)
		teamGroup.POST("/agents", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), middleware.RequireAnyTenantCapability("travel_agency"), teamController.CreateAgent)
		teamGroup.PUT("/agents/:id", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), middleware.RequireAnyTenantCapability("travel_agency"), teamController.UpdateAgent)
		teamGroup.PATCH("/agents/:id/status", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), middleware.RequireAnyTenantCapability("travel_agency"), teamController.SetAgentStatus)
		teamGroup.GET("/guides", middleware.RequireTenantPermission(authz.PermissionTeamsRead), teamController.ListGuides)
		teamGroup.POST("/guides", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), middleware.RequireAnyTenantCapability("travel_agency"), teamController.CreateGuide)
		teamGroup.PUT("/guides/:id", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), middleware.RequireAnyTenantCapability("travel_agency"), teamController.UpdateGuide)
		teamGroup.PATCH("/guides/:id/status", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), middleware.RequireAnyTenantCapability("travel_agency"), teamController.SetGuideStatus)
		teamGroup.GET("/vehicles", middleware.RequireTenantPermission(authz.PermissionTeamsRead), teamController.ListVehicles)
		teamGroup.POST("/vehicles", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), middleware.RequireAnyTenantCapability("travel_agency"), teamController.CreateVehicle)
		teamGroup.PUT("/vehicles/:id", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), middleware.RequireAnyTenantCapability("travel_agency"), teamController.UpdateVehicle)
		teamGroup.PATCH("/vehicles/:id/status", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), middleware.RequireAnyTenantCapability("travel_agency"), teamController.SetVehicleStatus)
		teamGroup.GET("", middleware.RequireTenantPermission(authz.PermissionTeamsRead), teamController.ListGroups)
		teamGroup.POST("", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), teamController.CreateGroup)
		teamGroup.PATCH("/:id", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), middleware.RequireAnyTenantCapability("travel_agency"), teamController.UpdateGroupPlan)
		teamGroup.POST("/:id/cancel", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), middleware.RequireAnyTenantCapability("travel_agency"), teamController.CancelGroup)
		teamGroup.POST("/:id/contract-order", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), teamController.CreateContractOrder)
		teamGroup.GET("/:id/members", middleware.RequireTenantPermission(authz.PermissionTeamsRead), teamController.ListMembers)
		teamGroup.POST("/:id/members", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), teamController.AddMembers)
		teamGroup.PUT("/:id/members", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), teamController.ReplaceMembers)
		teamGroup.POST("/:id/enter-batch", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), teamController.EnterBatch)
		teamGroup.GET("/:id/entry-batches", middleware.RequireTenantPermission(authz.PermissionTeamsRead), teamController.ListEntryBatches)
		teamGroup.GET("/:id/confirmations", middleware.RequireTenantPermission(authz.PermissionTeamsRead), teamController.ListConfirmations)
		teamGroup.POST("/:id/confirmations", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), teamController.SubmitConfirmation)
		teamGroup.POST("/:id/confirmations/:confirmationId/acknowledge", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), teamController.AcknowledgeConfirmation)
		teamGroup.GET("/:id/member-changes", middleware.RequireTenantPermission(authz.PermissionTeamsRead), teamController.ListMemberChanges)
		teamGroup.POST("/:id/member-changes", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), teamController.ChangeMember)
		teamGroup.POST("/:id/attach-order", middleware.RequireTenantPermission(authz.PermissionTeamsWrite), teamController.AttachOrder)
		teamGroup.GET("/settlements", middleware.RequireTenantPermission(authz.PermissionSettlementsRead), teamController.ListSettlements)
		teamGroup.GET("/settlements/:id/export", middleware.RequireTenantPermission(authz.PermissionSettlementsRead), teamController.ExportSettlement)
		teamGroup.GET("/accounts", middleware.RequireTenantPermission(authz.PermissionFinanceRead), teamController.ListAccountSummaries)
		teamGroup.POST("/:id/settlement", middleware.RequireTenantPermission(authz.PermissionSettlementsWrite), teamController.GenerateSettlement)
		teamGroup.PATCH("/settlements/:id/status", middleware.RequireTenantPermission(authz.PermissionSettlementsWrite), teamController.SetSettlementStatus)
		teamGroup.POST("/settlements/:id/adjustments", middleware.RequireTenantPermission(authz.PermissionSettlementsWrite), teamController.AdjustSettlement)
	}

	operationsController := &api.OperationsController{Service: service.OperationsService{}}
	operationsGroup := protected.Group("/operations")
	operationsGroup.Use(middleware.RequireTenantPermission(authz.PermissionOperationsRead), middleware.RequireAnyTenantCapability("supplier"), middleware.RequireAnySupplierBusinessType("scenic"))
	{
		operationsGroup.GET("/terminals", operationsController.ListPOSTerminals)
		operationsGroup.GET("/shifts", operationsController.ListShifts)
		operationsGroup.GET("/shifts/:id/summary", operationsController.GetShiftSummary)
		operationsGroup.GET("/shifts/open", operationsController.GetOpenShift)
		operationsGroup.POST("/shifts", middleware.RequireTenantPermission(authz.PermissionOperationsWrite), operationsController.OpenShift)
		operationsGroup.POST("/shifts/:id/close", middleware.RequireTenantPermission(authz.PermissionOperationsWrite), operationsController.CloseShift)
		operationsGroup.POST("/shifts/:id/reconcile", middleware.RequireTenantPermission(authz.PermissionOnsiteManage), operationsController.ReconcileShift)
		operationsGroup.POST("/shifts/:id/corrections", middleware.RequireTenantPermission(authz.PermissionOnsiteManage), operationsController.CorrectShift)
		operationsGroup.POST("/print-jobs", middleware.RequireTenantPermission(authz.PermissionOperationsWrite), operationsController.QueuePrint)
		operationsGroup.GET("/print-jobs", operationsController.ListPrintJobs)
		operationsGroup.POST("/print-jobs/:id/status", middleware.RequireTenantPermission(authz.PermissionOperationsWrite), operationsController.UpdatePrintStatus)
		operationsGroup.POST("/holds", middleware.RequireTenantPermission(authz.PermissionOperationsWrite), operationsController.CreateHold)
		operationsGroup.GET("/holds", operationsController.ListHolds)
		operationsGroup.POST("/holds/:id/resume", middleware.RequireTenantPermission(authz.PermissionOperationsWrite), operationsController.ResumeHold)
		operationsGroup.POST("/holds/:id/cancel", middleware.RequireTenantPermission(authz.PermissionOperationsWrite), operationsController.CancelHold)
		operationsGroup.GET("/alerts", operationsController.ListAlerts)
	}
	// Finance Routes
	financeController := &api.FinanceController{Service: service.FinanceService{}}
	financeGroup := protected.Group("/finance")
	financeGroup.Use(middleware.RequireTenantPermission(authz.PermissionFinanceRead), middleware.RequireAnyTenantCapability("supplier", "distributor"))
	{
		financeGroup.GET("/accounts", financeController.ListAccounts)
		financeGroup.GET("/managed-accounts", financeController.ListManagedAccounts)
		financeGroup.GET("/transactions", financeController.ListTransactions)
		financeGroup.GET("/ledger", financeController.ListLedger)
		financeGroup.GET("/managed-ledger", financeController.ListManagedLedger)
		financeGroup.GET("/documents", financeController.ListDocuments)
		financeGroup.POST("/documents", middleware.RequireTenantPermission(authz.PermissionFinanceWrite), financeController.CreateDocument)
		financeGroup.POST("/documents/:id/approve", middleware.RequireTenantPermission(authz.PermissionFinanceWrite), financeController.ApproveDocument)
	}

	settlementController := &api.SettlementController{Service: service.SettlementService{}}
	settlementGroup := protected.Group("/settlements")
	settlementGroup.Use(middleware.RequireTenantPermission(authz.PermissionSettlementsRead), middleware.RequireAnyTenantCapability("supplier", "distributor"))
	{
		settlementGroup.GET("", settlementController.List)
		settlementGroup.GET("/:id", settlementController.Get)
		settlementGroup.GET("/:id/export", settlementController.Export)
		settlementGroup.POST("/generate", middleware.RequireTenantPermission(authz.PermissionSettlementsWrite), settlementController.Generate)
		settlementGroup.PATCH("/:id/status", middleware.RequireTenantPermission(authz.PermissionSettlementsWrite), settlementController.SetStatus)
		settlementGroup.POST("/:id/adjustments", middleware.RequireTenantPermission(authz.PermissionSettlementsWrite), settlementController.Adjust)
	}

	// Report Routes
	reportController := &api.ReportController{Service: service.ReportService{}}
	reportGroup := protected.Group("/reports")
	reportGroup.Use(middleware.RequireTenantPermission(authz.PermissionReportsRead))
	{
		reportGroup.GET("/sales", middleware.RequireAnyTenantCapability("supplier", "distributor", "travel_agency"), reportController.GetSales)
		reportGroup.GET("/products", middleware.RequireAnyTenantCapability("supplier", "distributor"), reportController.GetProducts)
		reportGroup.GET("/channels", middleware.RequireAnyTenantCapability("supplier", "distributor"), reportController.GetChannels)
		reportGroup.GET("/daily", middleware.RequireAnyTenantCapability("supplier", "distributor", "travel_agency"), reportController.GetDaily)
		reportGroup.GET("/operations", middleware.RequireConfiguredSupplierBusinessType("scenic"), reportController.GetOperations)
		reportGroup.GET("/business-summary", middleware.RequireAnyTenantCapability("supplier", "distributor", "travel_agency"), reportController.GetBusinessSummary)
		reportGroup.GET("/business-details", middleware.RequireAnyTenantCapability("supplier", "distributor", "travel_agency"), reportController.GetBusinessDetails)
		reportGroup.GET("/verification-summary", middleware.RequireConfiguredSupplierBusinessType("scenic"), reportController.GetVerificationSummary)
		reportGroup.GET("/verification-details", middleware.RequireConfiguredSupplierBusinessType("scenic"), reportController.GetVerificationDetails)
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
		paymentGroup.POST("/pay", middleware.RequireTenantPermission(authz.PermissionPaymentsWrite), middleware.RequireAnyTenantCapability("supplier", "distributor"), paymentController.Pay)
		paymentGroup.GET("/orders/:orderNo", middleware.RequireTenantPermission(authz.PermissionPaymentsRead), middleware.RequireAnyTenantCapability("supplier", "distributor"), paymentController.OrderProgress)
		paymentGroup.POST("/orders/:orderNo/cancel-partial-cash", middleware.RequireTenantPermission(authz.PermissionPaymentsWrite), middleware.RequireAnyTenantCapability("supplier"), paymentController.CancelPartialCash)
		paymentGroup.POST("/refunds/cash", middleware.RequireTenantPermission(authz.PermissionRefundsWrite), middleware.RequireAnyTenantCapability("supplier"), refundController.CreateCash)
		paymentGroup.POST("/refunds/mixed", middleware.RequireTenantPermission(authz.PermissionRefundsWrite), middleware.RequireAnyTenantCapability("supplier", "distributor", "travel_agency"), refundController.CreateMixed)
		paymentGroup.GET("/refunds/:id", middleware.RequireTenantPermission(authz.PermissionRefundsRead), middleware.RequireAnyTenantCapability("supplier", "distributor", "travel_agency"), refundController.GetGroup)
		paymentGroup.POST("/refunds/digital", middleware.RequireTenantPermission(authz.PermissionRefundsWrite), middleware.RequireAnyTenantCapability("supplier", "distributor"), refundController.CreateDigital)
		paymentGroup.GET("/refund-tasks", middleware.RequireTenantPermission(authz.PermissionRefundsRead), middleware.RequireAnyTenantCapability("supplier", "distributor"), refundController.ListDigitalTasks)
		paymentGroup.POST("/refund-tasks/:id/retry", middleware.RequireTenantPermission(authz.PermissionRefundsWrite), middleware.RequireAnyTenantCapability("supplier", "distributor"), refundController.RetryDigitalTask)
		paymentGroup.GET("/configs", middleware.RequireTenantPermission(authz.PermissionPaymentConfig), middleware.RequireAnyTenantCapability("supplier", "distributor"), configController.GetConfigs)
		paymentGroup.GET("/configs/readiness", middleware.RequireTenantPermission(authz.PermissionPaymentConfig), middleware.RequireAnyTenantCapability("supplier", "distributor"), configController.GetReadiness)
		paymentGroup.POST("/configs", middleware.RequireTenantPermission(authz.PermissionPaymentConfig), middleware.RequireAnyTenantCapability("supplier", "distributor"), configController.SaveConfig)
		paymentGroup.POST("/configs/wechat", middleware.RequireTenantPermission(authz.PermissionPaymentConfig), middleware.RequireAnyTenantCapability("supplier", "distributor"), configController.SaveWechatConfig)
		paymentGroup.GET("/:id", middleware.RequireTenantPermission(authz.PermissionPaymentsRead), middleware.RequireAnyTenantCapability("supplier", "distributor"), paymentController.Query)
	}
}
