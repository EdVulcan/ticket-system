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
	sourceDump := flag.String("source-dump", "", "verified PostgreSQL custom-format dump")
	sourceKey := flag.String("source-key", "", "matching backup instance-key.json")
	targetKey := flag.String("target-key", "data/instance-key.json", "live instance-key.json path")
	rollbackDirectory := flag.String("rollback-dir", "data/backups", "directory for the pre-restore PostgreSQL rollback dump")
	verifyOnly := flag.Bool("verify-only", false, "only verify the source backup")
	flag.Parse()

	if *sourceDump == "" {
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

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
