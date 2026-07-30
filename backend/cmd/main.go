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
	backup.Start(backupContext, model.DB, backupConfig.Directory, config.GlobalConfig.Security.KeyFile, time.Duration(backupConfig.IntervalHours)*time.Hour, backupConfig.Retention, func(err error) {
		logger.Log.Error(fmt.Sprintf("Database backup failed: %v", err))
	})

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
		if count > 0 {
			return nil
		}
		bootstrap := config.GlobalConfig.Bootstrap
		if bootstrap.AdminPassword == "" {
			return errors.New("database has no users; set TICKET_BOOTSTRAP_ADMIN_PASSWORD for the first startup")
		}
		// Create default tenant
		tenant := model.Tenant{
			Name:       bootstrap.TenantName,
			SystemCode: bootstrap.SystemCode,
			SecretKey:  utils.GenerateRandomString(32),
		}
		if err := tx.Create(&tenant).Error; err != nil {
			return err
		}

		// Create admin user
		hashedPwd, err := bcrypt.GenerateFromPassword([]byte(bootstrap.AdminPassword), 14)
		if err != nil {
			return err
		}

		admin := model.User{
			Username: bootstrap.AdminUsername,
			Password: string(hashedPwd),
			Role:     "super_admin",
			TenantID: tenant.ID,
		}
		if err := tx.Create(&admin).Error; err != nil {
			return err
		}
		fmt.Printf("Seeded bootstrap administrator %q for tenant %q\n", bootstrap.AdminUsername, bootstrap.SystemCode)
		return nil
	})
}
