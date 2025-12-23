package main

import (
	"fmt"
	"log"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func main() {
	config.InitConfig()
	dsn := config.GlobalConfig.Database.Source
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	ticketCodes := []string{"T12240041484476"}

	for _, code := range ticketCodes {
		var ticket model.Ticket
		// Unscoped to find even if deleted
		err := db.Unscoped().Where("ticket_code = ?", code).First(&ticket).Error
		if err != nil {
			fmt.Printf("Code %s: Not Found (Error: %v)\n", code, err)
		} else {
			fmt.Printf("Code %s: Found! ID=%d, TenantID=%d, Status=%s\n", code, ticket.ID, ticket.TenantID, ticket.Status)
		}
	}
}
