package testdb

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/config"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var databaseSequence atomic.Uint64

// CreateDatabase creates a process-isolated PostgreSQL database for tests.
// The test role must have CREATEDB, which is true for the CI postgres service.
func CreateDatabase() (config.DatabaseConfig, func() error, error) {
	maintenance := databaseConfig(envOr("TICKET_TEST_POSTGRES_MAINTENANCE_DB", "postgres"))
	dsn, err := maintenance.PostgresDSN()
	if err != nil {
		return config.DatabaseConfig{}, nil, err
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return config.DatabaseConfig{}, nil, fmt.Errorf("connect PostgreSQL test server: %w", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		return config.DatabaseConfig{}, nil, err
	}
	name := fmt.Sprintf("ticket_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), databaseSequence.Add(1))
	if err := admin.Exec(`CREATE DATABASE "` + name + `"`).Error; err != nil {
		_ = adminSQL.Close()
		return config.DatabaseConfig{}, nil, fmt.Errorf("create PostgreSQL test database: %w", err)
	}
	testConfig := databaseConfig(name)
	cleanup := func() error {
		dropErr := admin.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`).Error
		closeErr := adminSQL.Close()
		if dropErr != nil {
			return dropErr
		}
		return closeErr
	}
	return testConfig, cleanup, nil
}

func Open(t testing.TB) *gorm.DB {
	t.Helper()
	database, drop, err := CreateDatabase()
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := database.PostgresDSN()
	if err != nil {
		_ = drop()
		t.Fatal(err)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		_ = drop()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
		if err := drop(); err != nil {
			t.Errorf("drop PostgreSQL test database: %v", err)
		}
	})
	return db
}

func databaseConfig(name string) config.DatabaseConfig {
	port, err := strconv.Atoi(envOr("TICKET_TEST_POSTGRES_PORT", envOr("PGPORT", "5432")))
	if err != nil || port <= 0 {
		port = 5432
	}
	return config.DatabaseConfig{
		Driver: "postgres", Host: envOr("TICKET_TEST_POSTGRES_HOST", envOr("PGHOST", "127.0.0.1")), Port: port,
		Name: name, User: envOr("TICKET_TEST_POSTGRES_USER", envOr("PGUSER", "postgres")), Password: os.Getenv("PGPASSWORD"),
		SSLMode: envOr("TICKET_TEST_POSTGRES_SSLMODE", "disable"), TimeZone: "Asia/Shanghai",
		MaxOpenConnections: 30, MaxIdleConnections: 5, ConnMaxLifetimeMinutes: 5, WriteTimeoutSeconds: 10,
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
