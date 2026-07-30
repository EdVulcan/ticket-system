package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
