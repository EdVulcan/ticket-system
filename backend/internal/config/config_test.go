package config

import (
	"path/filepath"
	"testing"
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
