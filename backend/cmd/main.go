package main

import (
	"fmt"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/router"
	"ticket-backend/pkg/logger"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
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
	}

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

	addr := fmt.Sprintf(":%d", config.GlobalConfig.Server.Port)
	logger.Log.Info(fmt.Sprintf("Server starting on %s", addr))
	if err := r.Run(addr); err != nil {
		panic(fmt.Sprintf("Failed to start server: %v", err))
	}
}

func seedAdminUser() error {
	var count int64
	model.DB.Model(&model.User{}).Count(&count)
	if count == 0 {
		// Create default tenant
		tenant := model.Tenant{
			Name:       "Default Tenant",
			SystemCode: "SYS001",
		}
		if err := model.DB.Create(&tenant).Error; err != nil {
			return err
		}

		// Create admin user
		hashedPwd, _ := bcrypt.GenerateFromPassword([]byte("123456"), 14)

		admin := model.User{
			Username: "admin",
			Password: string(hashedPwd),
			Role:     "admin",
			TenantID: tenant.ID,
		}
		if err := model.DB.Create(&admin).Error; err != nil {
			return err
		}
		fmt.Println("Seeded default admin user: admin / 123456")
	}
	return nil
}
