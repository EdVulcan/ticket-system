package model

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/testdb"
	"time"

	"gorm.io/gorm"
)

func TestInitializePostgresSchemaBacksUpOlderSchemaBeforeMigration(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: CurrentPostgresSchemaVersion - 1, Name: "previous schema", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	application := upgradeBackupTestConfig(t)
	steps := make([]string, 0, 2)
	backup := func(ctx context.Context, database config.DatabaseConfig, directory, keyFile, binDirectory string, retention int) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 2*time.Minute {
			t.Fatal("upgrade backup must have a bounded two-minute deadline")
		}
		steps = append(steps, "backup")
		if database != application.Database || directory != filepath.Join(application.Backup.Directory, "pre-upgrade") || keyFile != application.Security.KeyFile || binDirectory != application.Backup.PostgresBinDir || retention != application.Backup.Retention {
			t.Fatalf("backup arguments were not preserved")
		}
		return "verified.dump", nil
	}
	migrate := func(_ *gorm.DB) error {
		steps = append(steps, "migrate")
		return nil
	}

	if err := initializePostgresSchema(db, application, backup, migrate); err != nil {
		t.Fatal(err)
	}
	if got, want := len(steps), 2; got != want || steps[0] != "backup" || steps[1] != "migrate" {
		t.Fatalf("steps=%v, want [backup migrate]", steps)
	}
}

func TestInitializePostgresSchemaStopsBeforeMigrationWhenBackupFails(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: CurrentPostgresSchemaVersion - 1, Name: "previous schema", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	migrated := false
	backupFailure := errors.New("backup storage unavailable")
	err := initializePostgresSchema(db, upgradeBackupTestConfig(t), func(context.Context, config.DatabaseConfig, string, string, string, int) (string, error) {
		return "", backupFailure
	}, func(*gorm.DB) error {
		migrated = true
		return nil
	})
	if err == nil || !errors.Is(err, backupFailure) {
		t.Fatalf("error=%v, want backup failure", err)
	}
	if migrated {
		t.Fatal("migration ran after pre-upgrade backup failure")
	}
}

func TestInitializePostgresSchemaSkipsBackupForFreshAndCurrentSchema(t *testing.T) {
	for _, state := range []string{"fresh", "current"} {
		t.Run(state, func(t *testing.T) {
			db := testdb.Open(t)
			if state == "current" {
				if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&SchemaMigration{Version: CurrentPostgresSchemaVersion, Name: "current schema", AppliedAt: time.Now()}).Error; err != nil {
					t.Fatal(err)
				}
			}
			backupCalled := false
			migrated := false
			err := initializePostgresSchema(db, upgradeBackupTestConfig(t), func(context.Context, config.DatabaseConfig, string, string, string, int) (string, error) {
				backupCalled = true
				return "", nil
			}, func(*gorm.DB) error {
				migrated = true
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if backupCalled || !migrated {
				t.Fatalf("backupCalled=%t migrated=%t", backupCalled, migrated)
			}
		})
	}
}

func TestInitializePostgresSchemaBackupTimeoutPreventsMigration(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 116, Name: "previous", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	migrated := false
	err := initializePostgresSchema(db, upgradeBackupTestConfig(t), func(ctx context.Context, _ config.DatabaseConfig, _, _, _ string, _ int) (string, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("backup process has no deadline")
		}
		return "", context.DeadlineExceeded
	}, func(*gorm.DB) error {
		migrated = true
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || migrated {
		t.Fatalf("timeout must abort upgrade: err=%v migrated=%v", err, migrated)
	}
}

func upgradeBackupTestConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		Database: config.DatabaseConfig{Name: "ticket_test", User: "postgres"},
		Security: config.SecurityConfig{KeyFile: filepath.Join(t.TempDir(), "instance-key.json")},
		Backup:   config.BackupConfig{Directory: filepath.Join(t.TempDir(), "backups"), Retention: 3, PostgresBinDir: "/postgres/bin"},
	}
}
