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
	)
	if err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	InitRedis()
	return nil
}

func createDatabase(fullDSN string) error {
	// Parse DSN to get database name and creating a DSN without it for initial connection
	// Format assumption: user:pass@tcp(host:port)/dbname?params

	// Find the database name start
	slashIndex := -1
	for i := len(fullDSN) - 1; i >= 0; i-- {
		if fullDSN[i] == '/' {
			if i > 0 && fullDSN[i-1] != ')' {
				slashIndex = i
				break
			}
		}
	}

	if slashIndex == -1 {
		return nil // Could not verify, proceed to normal connection
	}

	// Extract DB name
	dbNameAndParams := fullDSN[slashIndex+1:]
	dbName := dbNameAndParams
	if len(dbNameAndParams) > 0 {
		for i, c := range dbNameAndParams {
			if c == '?' {
				dbName = dbNameAndParams[:i]
				break
			}
		}
	}

	if dbName == "" {
		return nil
	}

	// Create root DSN (remove dbname)
	// We replace /dbname with / to connect safely without selecting a DB
	rootDSN := fullDSN[:slashIndex+1]

	// Append parameters if any needed (often not needed for just CREATE DB, but charset might be good)
	// Simple approach: just keep params
	paramIndex := -1
	for i := 0; i < len(fullDSN); i++ {
		if fullDSN[i] == '?' {
			paramIndex = i
			break
		}
	}
	if paramIndex != -1 {
		rootDSN += fullDSN[paramIndex:]
	}

	db, err := gorm.Open(mysql.Open(rootDSN), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	sqlDB, err := db.DB()
	if err == nil {
		defer sqlDB.Close()
		query := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;", dbName)
		_, err = sqlDB.Exec(query)
		if err != nil {
			return fmt.Errorf("failed to create database: %w", err)
		}
	}
	return err
}
