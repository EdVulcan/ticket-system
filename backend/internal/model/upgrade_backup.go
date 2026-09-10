package model

import (
	"context"
	"fmt"
	"path/filepath"
	"ticket-backend/internal/config"
	"time"

	"gorm.io/gorm"
)

type postgresBackupCreator func(context.Context, config.DatabaseConfig, string, string, string, int) (string, error)

const preUpgradeBackupTimeout = 2 * time.Minute

// initializePostgresSchema takes a verified backup before changing an existing
// older schema. Fresh databases and schemas already at the current version do
// not need an upgrade rollback point.
func initializePostgresSchema(db *gorm.DB, application config.Config, createBackup postgresBackupCreator, migrate func(*gorm.DB) error) error {
	needsBackup, version, err := needsPreUpgradeBackup(db)
	if err != nil {
		return err
	}
	if needsBackup {
		backupContext, cancel := context.WithTimeout(context.Background(), preUpgradeBackupTimeout)
		defer cancel()
		directory := filepath.Join(application.Backup.Directory, "pre-upgrade")
		if _, err := createBackup(
			backupContext,
			application.Database,
			directory,
			application.Security.KeyFile,
			application.Backup.PostgresBinDir,
			application.Backup.Retention,
		); err != nil {
			return fmt.Errorf("create pre-upgrade backup for schema version %d: %w", version, err)
		}
		if err := backupContext.Err(); err != nil {
			return fmt.Errorf("create pre-upgrade backup for schema version %d: %w", version, err)
		}
		cancel()
	}
	if err := migrate(db); err != nil {
		return err
	}
	return nil
}

func needsPreUpgradeBackup(db *gorm.DB) (bool, int, error) {
	if !db.Migrator().HasTable(&SchemaMigration{}) {
		return false, 0, nil
	}
	var version int
	if err := db.Model(&SchemaMigration{}).Select("COALESCE(MAX(version), 0)").Scan(&version).Error; err != nil {
		return false, 0, fmt.Errorf("read current PostgreSQL schema version before backup: %w", err)
	}
	return version > 0 && version < CurrentPostgresSchemaVersion, version, nil
}
