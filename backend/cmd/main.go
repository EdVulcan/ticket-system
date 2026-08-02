package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"ticket-backend/internal/backup"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/router"
	"ticket-backend/internal/service"
	"ticket-backend/internal/utils"
	"ticket-backend/pkg/logger"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func main() {
	// 1. Init Config
	if err := config.InitConfig(); err != nil {
		panic(fmt.Sprintf("Failed to load config: %v", err))
	}

	// 2. Init Logger
	logger.InitLogger(
		config.GlobalConfig.Log.Level,
		config.GlobalConfig.Log.Filename,
		config.GlobalConfig.Log.MaxSize,
		config.GlobalConfig.Log.MaxAge,
		config.GlobalConfig.Log.MaxBackups,
	)
	logger.Log.Info("Config and Logger initialized")

	// 3. Init DB
	if err := model.InitDB(); err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to connect DB: %v", err))
		return
	}
	logger.Log.Info("Database connected")

	// 3.5 Seed Admin User
	if err := seedAdminUser(); err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to seed admin user: %v", err))
		return
	}

	backupContext, stopBackups := context.WithCancel(context.Background())
	defer stopBackups()
	backupConfig := config.GlobalConfig.Backup
	backupReporter := func(err error) { logger.Log.Error(fmt.Sprintf("Database backup failed: %v", err)) }
	if strings.EqualFold(config.GlobalConfig.Database.Driver, "sqlite") {
		backup.Start(backupContext, model.DB, backupConfig.Directory, config.GlobalConfig.Security.KeyFile, time.Duration(backupConfig.IntervalHours)*time.Hour, backupConfig.Retention, backupReporter)
	} else {
		backup.StartPostgres(backupContext, config.GlobalConfig.Database, backupConfig.Directory, config.GlobalConfig.Security.KeyFile, backupConfig.PostgresBinDir, time.Duration(backupConfig.IntervalHours)*time.Hour, backupConfig.Retention, backupReporter)
	}

	orderExpiryContext, stopOrderExpiry := context.WithCancel(context.Background())
	defer stopOrderExpiry()
	go runOrderExpiryWorker(orderExpiryContext)

	paymentReconciliationContext, stopPaymentReconciliation := context.WithCancel(context.Background())
	defer stopPaymentReconciliation()
	go runPaymentReconciliationWorker(paymentReconciliationContext)
	refundContext, stopRefundWorker := context.WithCancel(context.Background())
	defer stopRefundWorker()
	go runDigitalRefundWorker(refundContext)
	deviceContext, stopDeviceWorker := context.WithCancel(context.Background())
	defer stopDeviceWorker()
	go runDeviceHealthWorker(deviceContext)
	printContext, stopPrintWorker := context.WithCancel(context.Background())
	defer stopPrintWorker()
	go runPrintJobRecoveryWorker(printContext)
	reservationContext, stopReservationWorker := context.WithCancel(context.Background())
	defer stopReservationWorker()
	go runChannelReservationWorker(reservationContext)
	holdContext, stopHoldWorker := context.WithCancel(context.Background())
	defer stopHoldWorker()
	go runPOSHoldExpiryWorker(holdContext)

	// 4. Init Router
	gin.SetMode(config.GlobalConfig.Server.Mode)
	r := gin.Default()

	// Register Routes
	router.InitRouter(r)

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})
	serveAdminUI(r, config.GlobalConfig.Server.AdminStaticDir)

	addr := fmt.Sprintf(":%d", config.GlobalConfig.Server.Port)
	logger.Log.Info(fmt.Sprintf("Server starting on %s", addr))
	server := &http.Server{Addr: addr, Handler: r}
	serverError := make(chan error, 1)
	go func() {
		serverError <- server.ListenAndServe()
	}()

	signalContext, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	select {
	case <-signalContext.Done():
		logger.Log.Info("Shutdown signal received")
	case err := <-serverError:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Log.Error(fmt.Sprintf("HTTP server failed: %v", err))
		}
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Log.Error(fmt.Sprintf("HTTP server shutdown failed: %v", err))
	}
	stopBackups()
	if err := model.CloseWriter(shutdownContext); err != nil {
		logger.Log.Error(fmt.Sprintf("Database writer shutdown failed: %v", err))
	}
	if sqlDB, err := model.DB.DB(); err == nil {
		if err := sqlDB.Close(); err != nil {
			logger.Log.Error(fmt.Sprintf("Database close failed: %v", err))
		}
	}
}

func runOrderExpiryWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	orderService := &service.OrderService{}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if _, err := orderService.ExpireUnpaid(now); err != nil {
				logger.Log.Error(fmt.Sprintf("unpaid order expiry failed: %v", err))
			}
		}
	}
}

func runPaymentReconciliationWorker(ctx context.Context) {
	paymentService := &service.PaymentService{OrderService: &service.OrderService{}}
	process := func(now time.Time) {
		if err := paymentService.EnsurePaymentReconciliationTasks(now); err != nil {
			logger.Log.Error(fmt.Sprintf("payment task recovery failed: %v", err))
			return
		}
		if _, err := paymentService.ProcessPaymentReconciliationTasks(ctx, now, 20); err != nil && !errors.Is(err, context.Canceled) {
			logger.Log.Error(fmt.Sprintf("payment reconciliation failed: %v", err))
		}
	}
	// Recover pending provider payments immediately after startup, then poll
	// with a short interval so callbacks and active queries converge quickly.
	process(time.Now())
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			process(now)
		}
	}
}

func runDigitalRefundWorker(ctx context.Context) {
	refundService := &service.RefundService{PaymentService: &service.PaymentService{OrderService: &service.OrderService{}}}
	process := func(now time.Time) {
		if _, err := refundService.ProcessDigitalRefundTasks(ctx, now, 20); err != nil && !errors.Is(err, context.Canceled) {
			logger.Log.Error(fmt.Sprintf("digital refund processing failed: %v", err))
		}
		if err := (&service.AfterSaleService{}).ReconcileRefunds(); err != nil {
			logger.Log.Error(fmt.Sprintf("after-sale refund reconciliation failed: %v", err))
		}
	}
	process(time.Now())
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			process(now)
		}
	}
}

func runDeviceHealthWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	deviceService := service.NewDeviceService(model.DB, &service.TicketService{})
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if _, err := deviceService.MarkOffline(now, 2*time.Minute); err != nil {
				logger.Log.Error(fmt.Sprintf("device health check failed: %v", err))
			}
		}
	}
}

func runPrintJobRecoveryWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	ops := &service.OperationsService{}
	process := func(now time.Time) {
		if _, err := ops.RecoverStalePrintJobs(now, 2*time.Minute, 100); err != nil {
			logger.Log.Error(fmt.Sprintf("stale print job recovery failed: %v", err))
		}
	}
	process(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			process(now)
		}
	}
}

func runChannelReservationWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	workflow := &service.ChannelWorkflowService{OrderService: &service.OrderService{}}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if _, err := workflow.Expire(now, 100); err != nil {
				logger.Log.Error(fmt.Sprintf("channel reservation expiry failed: %v", err))
			}
		}
	}
}

func runPOSHoldExpiryWorker(ctx context.Context) {
	worker := &service.OperationsService{}
	process := func(now time.Time) {
		if _, err := worker.ExpirePOSHolds(now, 100); err != nil {
			logger.Log.Error(fmt.Sprintf("POS hold expiry failed: %v", err))
		}
	}
	process(time.Now())
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			process(now)
		}
	}
}

func serveAdminUI(engine *gin.Engine, directory string) {
	if strings.TrimSpace(directory) == "" {
		return
	}
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to resolve admin UI directory: %v", err))
		return
	}
	indexPath := filepath.Join(absDirectory, "index.html")
	if info, err := os.Stat(indexPath); err != nil || info.IsDir() {
		logger.Log.Info(fmt.Sprintf("Admin UI is not available at %s", absDirectory))
		return
	}
	engine.StaticFS("/assets", http.Dir(filepath.Join(absDirectory, "assets")))
	engine.GET("/", func(ctx *gin.Context) { ctx.File(indexPath) })
	engine.NoRoute(func(ctx *gin.Context) {
		if strings.HasPrefix(ctx.Request.URL.Path, "/api/") || ctx.Request.Method != http.MethodGet {
			ctx.JSON(http.StatusNotFound, gin.H{"error": "route not found"})
			return
		}
		ctx.File(indexPath)
	})
}

func seedAdminUser() error {
	return model.Write(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.User{}).Count(&count).Error; err != nil {
			return err
		}
		bootstrap := config.GlobalConfig.Bootstrap
		if count == 0 && bootstrap.AdminPassword == "" {
			return errors.New("database has no users; set TICKET_BOOTSTRAP_ADMIN_PASSWORD for the first startup")
		}
		if count == 0 {
			// Create default tenant and its tenant-scoped administrator.
			tenant := model.Tenant{
				Name: bootstrap.TenantName, SystemCode: bootstrap.SystemCode,
				SecretKey: utils.GenerateRandomString(32), Status: "active",
			}
			if err := tx.Create(&tenant).Error; err != nil {
				return err
			}
			if err := tx.Create(&model.TenantCapability{TenantID: tenant.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
				return err
			}
			hashedPwd, err := bcrypt.GenerateFromPassword([]byte(bootstrap.AdminPassword), 14)
			if err != nil {
				return err
			}
			if err := tx.Create(&model.User{Username: bootstrap.AdminUsername, Password: string(hashedPwd), Role: "super_admin", TenantID: tenant.ID, IsInitialAdmin: true}).Error; err != nil {
				return err
			}
			fmt.Printf("Seeded bootstrap administrator %q for tenant %q\n", bootstrap.AdminUsername, bootstrap.SystemCode)
		}
		// Platform identity uses independent bootstrap credentials. Reusing the
		// tenant administrator secret would collapse the highest privilege boundary.
		var platformCount int64
		if err := tx.Model(&model.PlatformUser{}).Count(&platformCount).Error; err != nil {
			return err
		}
		if platformCount == 0 && bootstrap.PlatformPassword == "" {
			return errors.New("database has no platform users; set TICKET_BOOTSTRAP_PLATFORM_PASSWORD for the first startup")
		}
		if platformCount == 0 {
			if bootstrap.PlatformPassword == bootstrap.AdminPassword {
				return errors.New("platform bootstrap password must differ from tenant administrator password")
			}
			hashedPwd, err := bcrypt.GenerateFromPassword([]byte(bootstrap.PlatformPassword), 14)
			if err != nil {
				return err
			}
			if err := tx.Create(&model.PlatformUser{Username: bootstrap.PlatformUsername, Password: string(hashedPwd), Role: "platform_admin", Status: "active"}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
