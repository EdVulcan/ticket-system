package model

import (
	"fmt"
	"ticket-backend/internal/config"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB() error {
	dsn := config.GlobalConfig.Database.Source
	var err error
	DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to connect database: %w", err)
	}

	// Connection pool settings could be added here
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)

	// Auto Migrate
	err = DB.AutoMigrate(
		&Tenant{},
		&User{},
		&Staff{},
		&CheckPoint{},
		&Device{},
		&TicketRule{},
		&RuleGroup{},
		&RuleItem{},
		&Product{},
		&Order{},
		&OrderItem{},
		&Ticket{},
		&CheckInRecord{},
	)
	if err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	InitRedis()
	return nil
}
