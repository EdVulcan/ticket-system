package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// Reproduce systemd ProtectSystem's separate mount namespace without touching
// the running service, its configuration, database, keys, or provider APIs.
func TestActiveServiceMountNamespaceConfig(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux mount namespaces")
	}
	if err := exec.Command("unshare", "--user", "--map-root-user", "--mount", "true").Run(); err != nil {
		t.Skip("unprivileged mount namespaces unavailable")
	}
	viper.Reset()
	t.Cleanup(viper.Reset)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "TICKET_") {
			t.Setenv(name, "")
		}
	}
	release := t.TempDir()
	t.Chdir(release)
	// Root launches the production tool outside the service release directory.
	t.Setenv("PWD", "/")
	if err := os.Mkdir("config", 0700); err != nil {
		t.Fatal(err)
	}
	key := `{"jwt_secret":"01234567890123456789012345678901","encryption_key":"01234567890123456789012345678901"}`
	if err := os.WriteFile("existing-key.json", []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("config/config.yaml", []byte("security:\n  key_file: existing-key.json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("unshare", "--user", "--map-root-user", "--mount", "sh", "-c",
		`mount --make-rprivate / && mount --bind "$1" "$1" && cd "$1" && echo ready && exec sleep 30`, "fixture", release)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatal("mount namespace fixture did not start")
	}
	proc := filepath.Join("/proc", strconv.Itoa(cmd.Process.Pid))
	// Prove the original pattern is broken despite relative config readability.
	if err := os.Chdir(filepath.Join(proc, "cwd")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile("config/config.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Getwd(); err == nil {
		t.Fatal("fixture did not reproduce unreachable mount-namespace cwd")
	}
	if err := os.Chdir(release); err != nil {
		t.Fatal(err)
	}
	if err := loadActiveConfig(proc); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(release, "existing-key.json")); err != nil || string(got) != key {
		t.Fatal("existing instance key changed")
	}
}
