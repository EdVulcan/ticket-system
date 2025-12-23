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
	// 1. Load Config
	config.InitConfig()

	// 2. Connect DB
	dsn := config.GlobalConfig.Database.Source
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("DB Error:", err)
	}

	// 3. Unscoped() allows us to see and manipulate soft-deleted records
	var device model.Device

	// Check if it exists (including soft deleted)
	err = db.Unscoped().Where("serial_number = ?", "YMS-DM-001").First(&device).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			fmt.Println("No device found with SN: YMS-DM-001 (Clean)")
		} else {
			log.Fatal("Error querying:", err)
		}
		return
	}

	fmt.Printf("Found 'Phantom' Device: ID=%d, SN=%s, DeletedAt=%v\n", device.ID, device.SerialNumber, device.DeletedAt)

	// 4. Hard Delete
	// using Unscoped().Delete() physically removes the row
	err = db.Unscoped().Delete(&device).Error
	if err != nil {
		log.Fatal("Delete failed:", err)
	}

	fmt.Println("Successfully HARD deleted device: YMS-DM-001")
}
