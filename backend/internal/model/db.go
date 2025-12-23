package model

import (
	"fmt"
	"strings"
	"ticket-backend/internal/config"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB() error {
	dsn := config.GlobalConfig.Database.Source

	// Try to create database if not exists
	if err := createDatabase(dsn); err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}

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
		&DistributorRelationship{},
		&CapitalAccount{},
		&TransactionRecord{},
		&Policy{},
	)
	if err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	InitRedis()
	return nil
}

func createDatabase(fullDSN string) error {
	// 1. Strip parameters to find db name safely
	dsnWithoutParams := fullDSN
	if idx := strings.Index(fullDSN, "?"); idx != -1 {
		dsnWithoutParams = fullDSN[:idx]
	}

	// 2. Find the last slash which separates address from dbname
	slashIndex := strings.LastIndex(dsnWithoutParams, "/")
	if slashIndex == -1 {
		return nil // Could not verify, proceed to normal connection
	}

	// 3. Extract DB name
	dbName := dsnWithoutParams[slashIndex+1:]
	if dbName == "" {
		return nil
	}

	// 4. Create root DSN (remove dbname but keep params)
	// We construct a DSN that connects to the server but not the specific DB
	rootDSN := fullDSN[:slashIndex+1]

	// Append parameters if they existed
	if idx := strings.Index(fullDSN, "?"); idx != -1 {
		rootDSN += fullDSN[idx:]
	}

	db, err := gorm.Open(mysql.Open(rootDSN), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	sqlDB, err := db.DB()
	if err == nil {
		defer sqlDB.Close()
		// Use backticks for safety, though dbName from DSN should be safe enough if it's valid
		query := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;", dbName)
		_, err = sqlDB.Exec(query)
		if err != nil {
			return fmt.Errorf("failed to create database: %w", err)
		}
	}
	return err
}
