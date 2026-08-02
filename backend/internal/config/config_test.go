package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestResolveSecretsPersistsGeneratedKeys(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "nested", "instance-key.json")
	first := Config{Security: SecurityConfig{KeyFile: keyFile}}
	if err := first.resolveSecrets(); err != nil {
		t.Fatal(err)
	}
	if len(first.Security.JWTSecret) != 64 || len(first.Security.EncryptionKey) != 32 {
		t.Fatalf("unexpected generated key lengths: jwt=%d encryption=%d", len(first.Security.JWTSecret), len(first.Security.EncryptionKey))
	}

	second := Config{Security: SecurityConfig{KeyFile: keyFile}}
	if err := second.resolveSecrets(); err != nil {
		t.Fatal(err)
	}
	if second.Security.JWTSecret != first.Security.JWTSecret || second.Security.EncryptionKey != first.Security.EncryptionKey {
		t.Fatal("generated instance keys were not stable across reloads")
	}
}

func TestPostgresDSNPreservesCredentialsAndTimeZone(t *testing.T) {
	database := DatabaseConfig{
		Host: "127.0.0.1", Port: 5432, Name: "ticket system", User: "ticket app",
		Password: `p'a\\ss word`, SSLMode: "disable", TimeZone: "Asia/Shanghai",
	}
	dsn, err := database.PostgresDSN()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse generated DSN: %v", err)
	}
	if parsed.Host != database.Host || parsed.Port != uint16(database.Port) || parsed.Database != database.Name || parsed.User != database.User || parsed.Password != database.Password {
		t.Fatalf("generated DSN changed connection credentials")
	}
	if got := parsed.RuntimeParams["TimeZone"]; got != database.TimeZone {
		t.Fatalf("timezone=%q, want %q", got, database.TimeZone)
	}
}

func TestPostgresDSNRejectsInvalidTimeZone(t *testing.T) {
	database := DatabaseConfig{
		Host: "127.0.0.1", Port: 5432, Name: "ticket_system", User: "ticket_app",
		SSLMode: "disable", TimeZone: "not/a-time-zone",
	}
	if _, err := database.PostgresDSN(); err == nil || !strings.Contains(err.Error(), "invalid PostgreSQL time zone") {
		t.Fatalf("unexpected error: %v", err)
	}
}
