package main

import (
	"fmt"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/router"
	"ticket-backend/pkg/logger"

	"github.com/gin-gonic/gin"
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
