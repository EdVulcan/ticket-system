package main

import (
	"fmt"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/pkg/logger"
)

func main() {
	// 1. Init
	config.InitConfig()
	logger.InitLogger("info", "stdout", 10, 10, 1)
	model.InitDB()

	var tenants []model.Tenant
	model.DB.Find(&tenants)

	fmt.Println("--- Backfilling Secret Keys ---")
	for _, t := range tenants {
		if t.SecretKey == "" {
			newKey := utils.GenerateRandomString(32)
			model.DB.Model(&t).Update("secret_key", newKey)
			fmt.Printf("Updated Tenant ID %d (%s) with key: %s\n", t.ID, t.Name, newKey)
		} else {
			fmt.Printf("Tenant ID %d (%s) already has key.\n", t.ID, t.Name)
		}
	}
	fmt.Println("--- Done ---")
}
