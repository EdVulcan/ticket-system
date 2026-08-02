package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"ticket-backend/internal/backup"
	"ticket-backend/internal/config"
	"time"
)

func main() {
	driver := flag.String("driver", "postgres", "database driver: postgres or sqlite")
	sourceDump := flag.String("source-dump", "", "verified PostgreSQL custom-format dump")
	sourceDB := flag.String("source-db", "", "legacy SQLite backup database")
	sourceKey := flag.String("source-key", "", "matching backup instance-key.json")
	targetDB := flag.String("target-db", "", "legacy live SQLite database path")
	targetKey := flag.String("target-key", "data/instance-key.json", "live instance-key.json path")
	rollbackDirectory := flag.String("rollback-dir", "data/backups", "directory for the pre-restore PostgreSQL rollback dump")
	verifyOnly := flag.Bool("verify-only", false, "only verify the source backup")
	flag.Parse()

	if strings.EqualFold(*driver, "sqlite") {
		restoreSQLite(*sourceDB, *sourceKey, *targetDB, *targetKey, *verifyOnly)
		return
	}
	if !strings.EqualFold(*driver, "postgres") && !strings.EqualFold(*driver, "postgresql") {
		fatalf("unsupported driver %q", *driver)
	}
	if strings.TrimSpace(*sourceDump) == "" {
		fatalf("-source-dump is required")
	}
	if err := config.InitConfig(); err != nil {
		fatalf("load config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	binDirectory := config.GlobalConfig.Backup.PostgresBinDir
	if err := backup.VerifyPostgres(ctx, *sourceDump, binDirectory); err != nil {
		fatalf("backup verification failed: %v", err)
	}
	if *verifyOnly {
		fmt.Println("PostgreSQL backup verification passed")
		return
	}
	if strings.TrimSpace(*sourceKey) == "" {
		fatalf("-source-key is required for restore")
	}
	rollback, err := backup.RestorePostgres(ctx, config.GlobalConfig.Database, *sourceDump, *sourceKey, *targetKey, *rollbackDirectory, binDirectory)
	if err != nil {
		fatalf("restore failed; rollback dump retained at %s: %v", rollback, err)
	}
	fmt.Printf("restore completed; previous database retained at %s\n", rollback)
}

func restoreSQLite(sourceDB, sourceKey, targetDB, targetKey string, verifyOnly bool) {
	if strings.TrimSpace(sourceDB) == "" {
		fatalf("-source-db is required")
	}
	if err := backup.Verify(sourceDB); err != nil {
		fatalf("backup verification failed: %v", err)
	}
	if verifyOnly {
		fmt.Println("SQLite backup verification passed")
		return
	}
	if strings.TrimSpace(targetDB) == "" || strings.TrimSpace(targetKey) == "" || strings.TrimSpace(sourceKey) == "" {
		fatalf("-target-db, -target-key and -source-key are required for SQLite restore")
	}
	rollback, err := backup.Restore(sourceDB, sourceKey, targetDB, targetKey)
	if err != nil {
		fatalf("restore failed: %v", err)
	}
	if rollback == "" {
		fmt.Println("restore completed; target did not previously exist")
		return
	}
	fmt.Printf("restore completed; previous database retained at %s\n", rollback)
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
