package model

import (
	"fmt"
	"os"
	"path/filepath"
	"ticket-backend/internal/config"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB() error {
	databaseConfig := config.GlobalConfig.Database
	databasePath, err := filepath.Abs(databaseConfig.Path)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0750); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_busy_timeout=%d&_journal_mode=WAL&_foreign_keys=on&_synchronous=NORMAL", filepath.ToSlash(databasePath), databaseConfig.BusyTimeoutMS)
	DB, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to connect database: %w", err)
	}

	// Connection pool settings could be added here
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetMaxOpenConns(databaseConfig.MaxReadConnections)

	if err := runMigrations(DB); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}
	InitWriter(DB, databaseConfig.WriteQueueSize, time.Duration(databaseConfig.EnqueueTimeoutSeconds)*time.Second, time.Duration(databaseConfig.WriteTimeoutSeconds)*time.Second)

	return nil
}
