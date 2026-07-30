//go:build cgo

package backup

import (
	"os"
	"path/filepath"
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
