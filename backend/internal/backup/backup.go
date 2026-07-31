package backup

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

const filePrefix = "ticket-system-"

func Start(ctx context.Context, db *gorm.DB, directory, keyFile string, interval time.Duration, retention int, report func(error)) {
	run := func() {
		if _, err := Create(db, directory, keyFile, retention); err != nil && report != nil {
			report(err)
		}
	}

	go func() {
		run()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}

func Create(db *gorm.DB, directory, keyFile string, retention int) (string, error) {
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve backup directory: %w", err)
	}
	if err := os.MkdirAll(absDirectory, 0750); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}

	filename := filePrefix + time.Now().Format("20060102-150405.000") + ".db"
	target := filepath.Join(absDirectory, filename)
	escapedTarget := strings.ReplaceAll(filepath.ToSlash(target), "'", "''")
	if err := db.Exec("VACUUM INTO '" + escapedTarget + "'").Error; err != nil {
		return "", fmt.Errorf("create SQLite backup: %w", err)
	}
	if err := copyKeyFile(keyFile, strings.TrimSuffix(target, ".db")+".key.json"); err != nil {
		_ = os.Remove(target)
		return "", err
	}
	if err := prune(absDirectory, retention); err != nil {
		return "", err
	}
	return target, nil
}

// Verify checks a SQLite backup before it is used for recovery. It deliberately
// opens the file read-only and requires both SQLite integrity_check and a
// readable migration table when present.
func Verify(databasePath string) error {
	abs, err := filepath.Abs(databasePath)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(abs)+"?mode=ro&_foreign_keys=on")
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("run SQLite integrity check: %w", err)
	}
	if strings.ToLower(strings.TrimSpace(result)) != "ok" {
		return fmt.Errorf("SQLite integrity check failed: %s", result)
	}
	var tableCount int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'").Scan(&tableCount); err != nil {
		return fmt.Errorf("inspect migration table: %w", err)
	}
	if tableCount == 1 {
		var latest int
		if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&latest); err != nil {
			return fmt.Errorf("read schema version: %w", err)
		}
		if latest <= 0 {
			return fmt.Errorf("backup has no applied schema migration")
		}
	}
	return nil
}

// Restore verifies the source, keeps a copy of the existing target, and then
// atomically replaces the target through a temporary file. The returned path
// is the pre-restore target copy and can be used for rollback.
func Restore(sourceDB, sourceKey, targetDB, targetKey string) (string, error) {
	source, err := filepath.Abs(sourceDB)
	if err != nil {
		return "", fmt.Errorf("resolve source database: %w", err)
	}
	target, err := filepath.Abs(targetDB)
	if err != nil {
		return "", fmt.Errorf("resolve target database: %w", err)
	}
	if source == target {
		return "", fmt.Errorf("source and target database must differ")
	}
	if err := Verify(source); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
		return "", fmt.Errorf("create target directory: %w", err)
	}
	rollback := ""
	if _, err := os.Stat(target); err == nil {
		rollback = target + ".before-restore-" + time.Now().Format("20060102-150405.000")
		if err := copyFile(target, rollback, 0600); err != nil {
			return "", fmt.Errorf("preserve current database: %w", err)
		}
	}
	temporary := target + ".restore.tmp"
	_ = os.Remove(temporary)
	if err := copyFile(source, temporary, 0600); err != nil {
		return "", fmt.Errorf("stage restored database: %w", err)
	}
	if err := Verify(temporary); err != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("verify staged database: %w", err)
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("replace target database: %w", err)
	}
	if strings.TrimSpace(sourceKey) != "" && strings.TrimSpace(targetKey) != "" {
		if _, err := os.Stat(sourceKey); err != nil {
			return rollback, fmt.Errorf("source key file is unavailable: %w", err)
		}
		keyRollback := targetKey + ".before-restore-" + time.Now().Format("20060102-150405.000")
		if _, err := os.Stat(targetKey); err == nil {
			if err := copyFile(targetKey, keyRollback, 0600); err != nil {
				return rollback, fmt.Errorf("preserve current key file: %w", err)
			}
		}
		if err := copyFile(sourceKey, targetKey, 0600); err != nil {
			return rollback, fmt.Errorf("restore key file: %w", err)
		}
	}
	return rollback, nil
}

func copyFile(source, target string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func copyKeyFile(source, target string) error {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	absSource, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolve security key file: %w", err)
	}
	data, err := os.ReadFile(absSource)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read security key file: %w", err)
	}
	if err := os.WriteFile(target, data, 0600); err != nil {
		return fmt.Errorf("back up security key file: %w", err)
	}
	return nil
}

func prune(directory string, retention int) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read backup directory: %w", err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), filePrefix) && strings.HasSuffix(entry.Name(), ".db") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	for len(files) > retention {
		oldest := filepath.Join(directory, files[0])
		if err := os.Remove(oldest); err != nil {
			return fmt.Errorf("remove old backup %s: %w", oldest, err)
		}
		keyBackup := strings.TrimSuffix(oldest, ".db") + ".key.json"
		if err := os.Remove(keyBackup); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove old key backup %s: %w", keyBackup, err)
		}
		files = files[1:]
	}
	return nil
}
