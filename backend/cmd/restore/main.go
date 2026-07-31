package main

import (
	"flag"
	"fmt"
	"os"
	"ticket-backend/internal/backup"
)

func main() {
	sourceDB := flag.String("source-db", "", "path to a verified backup SQLite database")
	sourceKey := flag.String("source-key", "", "matching backup instance-key.json")
	targetDB := flag.String("target-db", "", "live SQLite database path")
	targetKey := flag.String("target-key", "", "live instance-key.json path")
	verifyOnly := flag.Bool("verify-only", false, "only verify the source database")
	flag.Parse()
	if *sourceDB == "" {
		fmt.Fprintln(os.Stderr, "-source-db is required")
		os.Exit(2)
	}
	if err := backup.Verify(*sourceDB); err != nil {
		fmt.Fprintf(os.Stderr, "backup verification failed: %v\n", err)
		os.Exit(1)
	}
	if *verifyOnly {
		fmt.Println("backup verification passed")
		return
	}
	if *targetDB == "" || *targetKey == "" || *sourceKey == "" {
		fmt.Fprintln(os.Stderr, "-target-db, -target-key and -source-key are required unless -verify-only is used")
		os.Exit(2)
	}
	rollback, err := backup.Restore(*sourceDB, *sourceKey, *targetDB, *targetKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore failed: %v\n", err)
		os.Exit(1)
	}
	if rollback == "" {
		fmt.Println("restore completed; target did not previously exist")
		return
	}
	fmt.Printf("restore completed; previous database retained at %s\n", rollback)
}
