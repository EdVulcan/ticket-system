package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"ticket-backend/internal/config"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostgresBackupAndRestore(t *testing.T) {
	if os.Getenv("TICKET_TEST_POSTGRES") != "1" {
		t.Skip("set TICKET_TEST_POSTGRES=1 to run PostgreSQL backup integration")
	}
	password := os.Getenv("PGPASSWORD")
	if password == "" {
		t.Fatal("PGPASSWORD is required")
	}
	binDirectory := os.Getenv("TICKET_TEST_POSTGRES_BIN")
	source := testPostgresConfig("ticket_system_test", password)
	target := testPostgresConfig("ticket_system_restore_test", password)
	temp := t.TempDir()
	keyFile := filepath.Join(temp, "instance-key.json")
	if err := os.WriteFile(keyFile, []byte(`{"test":"source-key"}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dump, err := CreatePostgres(ctx, source, filepath.Join(temp, "backups"), keyFile, binDirectory, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPostgres(ctx, dump, binDirectory); err != nil {
		t.Fatal(err)
	}
	targetKey := filepath.Join(temp, "target-key.json")
	if err := os.WriteFile(targetKey, []byte(`{"test":"old-key"}`), 0600); err != nil {
		t.Fatal(err)
	}
	backupKey := strings.TrimSuffix(dump, ".dump") + ".key.json"
	rollback, err := RestorePostgres(ctx, target, dump, backupKey, targetKey, filepath.Join(temp, "rollback"), binDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(rollback); err != nil || info.Size() == 0 {
		t.Fatalf("rollback dump info=%v err=%v", info, err)
	}
	contents, err := os.ReadFile(targetKey)
	if err != nil || string(contents) != `{"test":"source-key"}` {
		t.Fatalf("restored key=%q err=%v", contents, err)
	}
	dsn, err := target.PostgresDSN()
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		defer sqlDB.Close()
	}
	var version int
	if err := db.Raw("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version != 59 {
		t.Fatalf("restored schema version=%d, want 59", version)
	}
}

func testPostgresConfig(name, password string) config.DatabaseConfig {
	return config.DatabaseConfig{
		Driver: "postgres", Host: "127.0.0.1", Port: 5432, Name: name, User: "postgres",
		Password: password, SSLMode: "disable", TimeZone: "Asia/Shanghai",
		MaxOpenConnections: 5, MaxIdleConnections: 1, ConnMaxLifetimeMinutes: 5,
	}
}
