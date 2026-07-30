//go:build cgo

package model

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestWriterSerializesAndRejectsWritesAfterClose(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "writer.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := db.Exec("CREATE TABLE counters (id INTEGER PRIMARY KEY, value INTEGER NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO counters(id, value) VALUES (1, 0)").Error; err != nil {
		t.Fatal(err)
	}
	InitWriter(db, 32, time.Second, 5*time.Second)
	for i := 0; i < 50; i++ {
		if err := Write(func(tx *gorm.DB) error {
			return tx.Exec("UPDATE counters SET value = value + 1 WHERE id = 1").Error
		}); err != nil {
			t.Fatal(err)
		}
	}
	closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := CloseWriter(closeContext); err != nil {
		t.Fatal(err)
	}
	if err := Write(func(*gorm.DB) error { return nil }); !errors.Is(err, ErrWriterClosed) {
		t.Fatalf("write after close error = %v, want ErrWriterClosed", err)
	}
	var value int
	if err := db.Raw("SELECT value FROM counters WHERE id = 1").Scan(&value).Error; err != nil {
		t.Fatal(err)
	}
	if value != 50 {
		t.Fatalf("counter value = %d, want 50", value)
	}
}
