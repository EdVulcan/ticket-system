package main

import (
	"fmt"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/pkg/logger"
)

func main() {
	// 1. Init Config
	if err := config.InitConfig(); err != nil {
		panic(fmt.Sprintf("Failed to load config: %v", err))
	}

	// 2. Init Logger (minimal)
	logger.InitLogger("info", "stdout", 10, 10, 1)

	// 3. Init DB
	if err := model.InitDB(); err != nil {
		panic(fmt.Sprintf("Failed to connect DB: %v", err))
	}

	var tenants []model.Tenant
	model.DB.Find(&tenants)

	fmt.Println("--- Tenant Secret Keys Debug ---")
	for _, t := range tenants {
		fmt.Printf("ID: %d | Name: %s | Code: %s | SecretKey: [%s] (Len: %d)\n", t.ID, t.Name, t.SystemCode, t.SecretKey, len(t.SecretKey))
	}
	fmt.Println("--------------------------------")
}
