package model

import (
	"fmt"
	"strings"
	"ticket-backend/internal/config"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB() error {
	databaseConfig := config.GlobalConfig.Database
	driver := strings.ToLower(strings.TrimSpace(databaseConfig.Driver))
	if driver != "postgres" && driver != "postgresql" {
		return fmt.Errorf("unsupported database driver %q", databaseConfig.Driver)
	}
	dsn, err := databaseConfig.PostgresDSN()
	if err != nil {
		return err
	}
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		return fmt.Errorf("failed to connect database: %w", err)
	}
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxIdleConns(databaseConfig.MaxIdleConnections)
	sqlDB.SetMaxOpenConns(databaseConfig.MaxOpenConnections)
	sqlDB.SetConnMaxLifetime(time.Duration(databaseConfig.ConnMaxLifetimeMinutes) * time.Minute)

	if err := runMigrations(DB); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}
	InitWriter(DB, time.Duration(databaseConfig.WriteTimeoutSeconds)*time.Second)

	return nil
}
