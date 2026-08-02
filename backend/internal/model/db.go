package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"ticket-backend/internal/config"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB() error {
	databaseConfig := config.GlobalConfig.Database
	driver := strings.ToLower(strings.TrimSpace(databaseConfig.Driver))
	var err error
	switch driver {
	case "postgres", "postgresql":
		dsn, dsnErr := databaseConfig.PostgresDSN()
		if dsnErr != nil {
			return dsnErr
		}
		DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	case "sqlite":
		databasePath, pathErr := filepath.Abs(databaseConfig.Path)
		if pathErr != nil {
			return fmt.Errorf("resolve database path: %w", pathErr)
		}
		if pathErr = os.MkdirAll(filepath.Dir(databasePath), 0750); pathErr != nil {
			return fmt.Errorf("create database directory: %w", pathErr)
		}
		dsn := fmt.Sprintf("file:%s?_busy_timeout=%d&_journal_mode=WAL&_foreign_keys=on&_synchronous=NORMAL", filepath.ToSlash(databasePath), databaseConfig.BusyTimeoutMS)
		DB, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	default:
		return fmt.Errorf("unsupported database driver %q", databaseConfig.Driver)
	}
	if err != nil {
		return fmt.Errorf("failed to connect database: %w", err)
	}
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	if driver == "sqlite" {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(databaseConfig.MaxReadConnections)
	} else {
		sqlDB.SetMaxIdleConns(databaseConfig.MaxIdleConnections)
		sqlDB.SetMaxOpenConns(databaseConfig.MaxOpenConnections)
		sqlDB.SetConnMaxLifetime(time.Duration(databaseConfig.ConnMaxLifetimeMinutes) * time.Minute)
	}

	if err := runMigrations(DB); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}
	InitWriter(DB, databaseConfig.WriteQueueSize, time.Duration(databaseConfig.EnqueueTimeoutSeconds)*time.Second, time.Duration(databaseConfig.WriteTimeoutSeconds)*time.Second)

	return nil
}
