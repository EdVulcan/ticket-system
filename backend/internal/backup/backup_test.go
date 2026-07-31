//go:build cgo

package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCreateProducesConsistentDatabaseAndKeyBackup(t *testing.T) {
	tempDirectory := t.TempDir()
	databasePath := filepath.Join(tempDirectory, "source.db")
	db, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sourceSQLDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sourceSQLDB.Close()
	if err := db.Exec("CREATE TABLE samples (id INTEGER PRIMARY KEY, name TEXT NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO samples(name) VALUES (?)", "preserved").Error; err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(tempDirectory, "instance-key.json")
	keyContents := []byte(`{"jwt_secret":"test","encryption_key":"test"}`)
	if err := os.WriteFile(keyFile, keyContents, 0600); err != nil {
		t.Fatal(err)
	}

	backupDirectory := filepath.Join(tempDirectory, "backups")
	backupPath, err := Create(db, backupDirectory, keyFile, 1)
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	backupDB, err := gorm.Open(sqlite.Open(backupPath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	backupSQLDB, err := backupDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := backupDB.Table("samples").Where("name = ?", "preserved").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("backup row count = %d, want 1", count)
	}
	backedUpKey, err := os.ReadFile(backupPath[:len(backupPath)-len(".db")] + ".key.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(backedUpKey) != string(keyContents) {
		t.Fatal("security key backup does not match source")
	}
	if err := backupSQLDB.Close(); err != nil {
		t.Fatal(err)
	}

	time.Sleep(2 * time.Millisecond)
	if _, err := Create(db, backupDirectory, keyFile, 1); err != nil {
		t.Fatalf("create retained backup: %v", err)
	}
	databaseBackups, _ := filepath.Glob(filepath.Join(backupDirectory, "*.db"))
	keyBackups, _ := filepath.Glob(filepath.Join(backupDirectory, "*.key.json"))
	if len(databaseBackups) != 1 || len(keyBackups) != 1 {
		t.Fatalf("retained backups: databases=%d keys=%d, want one pair", len(databaseBackups), len(keyBackups))
	}
}

func TestRestoreVerifiesAndPreservesPreviousTarget(t *testing.T) {
	tempDirectory := t.TempDir()
	sourceDB := filepath.Join(tempDirectory, "source.db")
	db, err := gorm.Open(sqlite.Open(sourceDB), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO schema_migrations(version) VALUES (32)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE samples (id INTEGER PRIMARY KEY, name TEXT NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO samples(name) VALUES (?)", "from-backup").Error; err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(tempDirectory, "source.key.json")
	if err := os.WriteFile(key, []byte("source-key"), 0600); err != nil {
		t.Fatal(err)
	}
	backupDB := filepath.Join(tempDirectory, "backup.db")
	if err := os.Remove(backupDB); err == nil {
		t.Fatal("unexpected pre-existing backup")
	}
	created, err := Create(db, filepath.Join(tempDirectory, "backups"), key, 2)
	if err != nil {
		t.Fatal(err)
	}
	sourceSQLDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceSQLDB.Close(); err != nil {
		t.Fatal(err)
	}
	backupKey := strings.TrimSuffix(created, ".db") + ".key.json"
	targetDB := filepath.Join(tempDirectory, "live.db")
	targetKey := filepath.Join(tempDirectory, "live.key.json")
	target, err := gorm.Open(sqlite.Open(targetDB), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Exec("CREATE TABLE samples (id INTEGER PRIMARY KEY, name TEXT NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Exec("INSERT INTO samples(name) VALUES (?)", "before-restore").Error; err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetKey, []byte("old-key"), 0600); err != nil {
		t.Fatal(err)
	}
	targetSQLDB, err := target.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := targetSQLDB.Close(); err != nil {
		t.Fatal(err)
	}
	rollback, err := Restore(created, backupKey, targetDB, targetKey)
	if err != nil {
		t.Fatal(err)
	}
	if rollback == "" {
		t.Fatal("restore did not return a rollback path")
	}
	if _, err := os.Stat(rollback); err != nil {
		t.Fatal(err)
	}
	verified, err := gorm.Open(sqlite.Open(targetDB), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	verifiedSQLDB, err := verified.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer verifiedSQLDB.Close()
	var count int64
	if err := verified.Table("samples").Where("name = ?", "from-backup").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("restored row count=%d err=%v", count, err)
	}
	contents, err := os.ReadFile(targetKey)
	if err != nil || string(contents) != "source-key" {
		t.Fatalf("restored key=%q err=%v", contents, err)
	}
}
