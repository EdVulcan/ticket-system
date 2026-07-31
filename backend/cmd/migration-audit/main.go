package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"ticket-backend/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func main() {
	databasePath := flag.String("db", "", "legacy SQLite database path")
	outputPath := flag.String("output", "", "optional JSON report path")
	quarantine := flag.Bool("quarantine", false, "persist the report issues in migration_audit_issues")
	flag.Parse()
	if *databasePath == "" {
		fmt.Fprintln(os.Stderr, "-db is required")
		os.Exit(2)
	}
	abs, err := filepath.Abs(*databasePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve database: %v\n", err)
		os.Exit(1)
	}
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(abs)+"?_foreign_keys=on&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "open database: %v\n", err)
		os.Exit(1)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	report, err := model.AuditLegacyMigration(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit failed: %v\n", err)
		os.Exit(1)
	}
	if *quarantine {
		if err := model.PersistMigrationAudit(db, report); err != nil {
			fmt.Fprintf(os.Stderr, "persist audit: %v\n", err)
			os.Exit(1)
		}
	}
	data, err := model.MigrationAuditJSON(report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
		os.Exit(1)
	}
	if *outputPath != "" {
		if err := os.WriteFile(*outputPath, data, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "write report: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Println(string(data))
	}
	if !report.SafeToMigrate {
		os.Exit(3)
	}
}
