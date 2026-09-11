package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseOptionsDefaultsToPreview(t *testing.T) {
	options, err := parseOptions(nil, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	if options.Apply {
		t.Fatal("cleanup must default to preview")
	}
	if options.MinimumAge != (7 * 24 * time.Hour).String() {
		t.Fatalf("minimum age=%q", options.MinimumAge)
	}
}

func TestRunHelpDoesNotConnectToDatabase(t *testing.T) {
	var output strings.Builder
	if err := run([]string{"--help"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "xhs-image-cleanup") {
		t.Fatalf("help output=%q", output.String())
	}
}

func TestParseMinimumAgeHasSevenDayFloor(t *testing.T) {
	if _, err := parseMinimumAge("167h"); err == nil {
		t.Fatal("accepted a retention period below seven days")
	}
	if got, err := parseMinimumAge("240h"); err != nil || got != 240*time.Hour {
		t.Fatalf("longer retention=%s err=%v", got, err)
	}
}

func TestLoadCleanupConfigDoesNotGenerateOrExposeSecrets(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.yaml")
	contents := "server:\n  upload_directory: uploads\ndatabase:\n  driver: postgres\n  url: postgres://user:password@example.invalid/tickets\n"
	if err := os.WriteFile(configFile, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadCleanupConfig(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.UploadDirectory != "uploads" || cfg.Database.Driver != "postgres" {
		t.Fatalf("config=%+v", cfg)
	}
	if cfg.Security.JWTSecret != "" || cfg.Security.EncryptionKey != "" {
		t.Fatalf("cleanup config unexpectedly resolved secrets")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.yaml" {
		t.Fatalf("config loader created files: %+v", entries)
	}
}
