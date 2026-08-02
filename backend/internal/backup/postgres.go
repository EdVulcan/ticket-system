package backup

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"ticket-backend/internal/config"
	"time"
)

const postgresFilePrefix = "ticket-system-pg-"

func StartPostgres(ctx context.Context, database config.DatabaseConfig, directory, keyFile, binDirectory string, interval time.Duration, retention int, report func(error)) {
	run := func() {
		if _, err := CreatePostgres(ctx, database, directory, keyFile, binDirectory, retention); err != nil && report != nil {
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

func CreatePostgres(ctx context.Context, database config.DatabaseConfig, directory, keyFile, binDirectory string, retention int) (string, error) {
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve backup directory: %w", err)
	}
	if err := os.MkdirAll(absDirectory, 0750); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}
	target := filepath.Join(absDirectory, postgresFilePrefix+time.Now().Format("20060102-150405.000")+".dump")
	if err := dumpPostgres(ctx, database, target, binDirectory); err != nil {
		_ = os.Remove(target)
		return "", err
	}
	if err := VerifyPostgres(ctx, target, binDirectory); err != nil {
		_ = os.Remove(target)
		return "", err
	}
	if err := copyKeyFile(keyFile, strings.TrimSuffix(target, ".dump")+".key.json"); err != nil {
		_ = os.Remove(target)
		return "", err
	}
	if err := prunePostgres(absDirectory, retention); err != nil {
		return "", err
	}
	return target, nil
}

func VerifyPostgres(ctx context.Context, dumpPath, binDirectory string) error {
	abs, err := filepath.Abs(dumpPath)
	if err != nil {
		return fmt.Errorf("resolve PostgreSQL backup: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("read PostgreSQL backup: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("PostgreSQL backup is empty")
	}
	command := exec.CommandContext(ctx, postgresTool(binDirectory, "pg_restore"), "--list", abs)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("verify PostgreSQL backup: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "TABLE") || !strings.Contains(string(output), "schema_migrations") {
		return fmt.Errorf("PostgreSQL backup does not contain the application schema")
	}
	return nil
}

// RestorePostgres restores one verified custom-format dump atomically. The
// application must be stopped so no request can hold stale database state.
// A full rollback dump and the previous key file are retained before restore.
func RestorePostgres(ctx context.Context, database config.DatabaseConfig, sourceDump, sourceKey, targetKey, rollbackDirectory, binDirectory string) (string, error) {
	if err := VerifyPostgres(ctx, sourceDump, binDirectory); err != nil {
		return "", err
	}
	if err := os.MkdirAll(rollbackDirectory, 0750); err != nil {
		return "", fmt.Errorf("create rollback directory: %w", err)
	}
	rollback := filepath.Join(rollbackDirectory, "ticket-system-before-restore-"+time.Now().Format("20060102-150405.000")+".dump")
	if err := dumpPostgres(ctx, database, rollback, binDirectory); err != nil {
		return "", fmt.Errorf("create pre-restore rollback backup: %w", err)
	}
	connectionArgs, connectionEnv, err := postgresConnection(database)
	if err != nil {
		return rollback, err
	}
	args := append(connectionArgs, "--clean", "--if-exists", "--no-owner", "--no-privileges", "--exit-on-error", "--single-transaction", sourceDump)
	command := exec.CommandContext(ctx, postgresTool(binDirectory, "pg_restore"), args...)
	command.Env = append(os.Environ(), connectionEnv...)
	if output, err := command.CombinedOutput(); err != nil {
		return rollback, fmt.Errorf("restore PostgreSQL backup: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if strings.TrimSpace(sourceKey) != "" && strings.TrimSpace(targetKey) != "" {
		if _, err := os.Stat(sourceKey); err != nil {
			return rollback, fmt.Errorf("source key file is unavailable: %w", err)
		}
		if _, err := os.Stat(targetKey); err == nil {
			if err := copyFile(targetKey, targetKey+".before-restore-"+time.Now().Format("20060102-150405.000"), 0600); err != nil {
				return rollback, fmt.Errorf("preserve current key file: %w", err)
			}
		}
		if err := copyFile(sourceKey, targetKey, 0600); err != nil {
			return rollback, fmt.Errorf("restore key file: %w", err)
		}
	}
	return rollback, nil
}

func dumpPostgres(ctx context.Context, database config.DatabaseConfig, target, binDirectory string) error {
	connectionArgs, connectionEnv, err := postgresConnection(database)
	if err != nil {
		return err
	}
	args := append(connectionArgs, "--format=custom", "--no-owner", "--no-privileges", "--file", target)
	command := exec.CommandContext(ctx, postgresTool(binDirectory, "pg_dump"), args...)
	command.Env = append(os.Environ(), connectionEnv...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("create PostgreSQL backup: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func postgresConnection(database config.DatabaseConfig) ([]string, []string, error) {
	host, port, name, user, password, sslMode := database.Host, database.Port, database.Name, database.User, database.Password, database.SSLMode
	if strings.TrimSpace(database.URL) != "" {
		parsed, err := url.Parse(database.URL)
		if err != nil {
			return nil, nil, fmt.Errorf("parse PostgreSQL URL: %w", err)
		}
		host = parsed.Hostname()
		if parsed.Port() != "" {
			parsedPort, err := strconv.Atoi(parsed.Port())
			if err != nil {
				return nil, nil, fmt.Errorf("parse PostgreSQL port: %w", err)
			}
			port = parsedPort
		}
		name = strings.TrimPrefix(parsed.Path, "/")
		if parsed.User != nil {
			user = parsed.User.Username()
			password, _ = parsed.User.Password()
		}
		if value := parsed.Query().Get("sslmode"); value != "" {
			sslMode = value
		}
	}
	if strings.TrimSpace(host) == "" || port <= 0 || strings.TrimSpace(name) == "" || strings.TrimSpace(user) == "" {
		return nil, nil, fmt.Errorf("PostgreSQL host, port, name and user are required")
	}
	args := []string{"--host", host, "--port", strconv.Itoa(port), "--username", user, "--dbname", name}
	env := []string{"PGPASSWORD=" + password}
	if strings.TrimSpace(sslMode) != "" {
		env = append(env, "PGSSLMODE="+sslMode)
	}
	return args, env, nil
}

func postgresTool(binDirectory, name string) string {
	if strings.TrimSpace(binDirectory) == "" {
		return name
	}
	return filepath.Join(binDirectory, name)
}

func prunePostgres(directory string, retention int) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read backup directory: %w", err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), postgresFilePrefix) && strings.HasSuffix(entry.Name(), ".dump") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	for len(files) > retention {
		oldest := filepath.Join(directory, files[0])
		if err := os.Remove(oldest); err != nil {
			return fmt.Errorf("remove old backup %s: %w", oldest, err)
		}
		keyBackup := strings.TrimSuffix(oldest, ".dump") + ".key.json"
		if err := os.Remove(keyBackup); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove old key backup %s: %w", keyBackup, err)
		}
		files = files[1:]
	}
	return nil
}
