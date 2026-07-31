package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Log       LogConfig       `mapstructure:"log"`
	Security  SecurityConfig  `mapstructure:"security"`
	Bootstrap BootstrapConfig `mapstructure:"bootstrap"`
	Backup    BackupConfig    `mapstructure:"backup"`
}

type ServerConfig struct {
	Port               int    `mapstructure:"port"`
	Mode               string `mapstructure:"mode"`
	CORSAllowedOrigins string `mapstructure:"cors_allowed_origins"`
	AdminStaticDir     string `mapstructure:"admin_static_dir"`
}

type DatabaseConfig struct {
	Path                  string `mapstructure:"path"`
	BusyTimeoutMS         int    `mapstructure:"busy_timeout_ms"`
	MaxReadConnections    int    `mapstructure:"max_read_connections"`
	WriteQueueSize        int    `mapstructure:"write_queue_size"`
	WriteTimeoutSeconds   int    `mapstructure:"write_timeout_seconds"`
	EnqueueTimeoutSeconds int    `mapstructure:"enqueue_timeout_seconds"`
}

type SecurityConfig struct {
	JWTSecret              string `mapstructure:"jwt_secret"`
	EncryptionKey          string `mapstructure:"encryption_key"`
	KeyFile                string `mapstructure:"key_file"`
	OTAMaxClockSkewSeconds int    `mapstructure:"ota_max_clock_skew_seconds"`
}

type BootstrapConfig struct {
	TenantName       string `mapstructure:"tenant_name"`
	SystemCode       string `mapstructure:"system_code"`
	AdminUsername    string `mapstructure:"admin_username"`
	AdminPassword    string `mapstructure:"admin_password"`
	PlatformUsername string `mapstructure:"platform_username"`
	PlatformPassword string `mapstructure:"platform_password"`
}

type BackupConfig struct {
	Directory     string `mapstructure:"directory"`
	IntervalHours int    `mapstructure:"interval_hours"`
	Retention     int    `mapstructure:"retention"`
}

type LogConfig struct {
	Level      string `mapstructure:"level"`
	Filename   string `mapstructure:"filename"`
	MaxSize    int    `mapstructure:"max_size"`
	MaxAge     int    `mapstructure:"max_age"`
	MaxBackups int    `mapstructure:"max_backups"`
}

var GlobalConfig Config

func InitConfig() error {
	viper.SetEnvPrefix("TICKET")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.mode", "release")
	viper.SetDefault("server.cors_allowed_origins", "http://127.0.0.1:5173,http://localhost:5173")
	viper.SetDefault("server.admin_static_dir", "../admin/dist")
	viper.SetDefault("database.path", "data/ticket-system.db")
	viper.SetDefault("database.busy_timeout_ms", 5000)
	viper.SetDefault("database.max_read_connections", 8)
	viper.SetDefault("database.write_queue_size", 1000)
	viper.SetDefault("database.write_timeout_seconds", 10)
	viper.SetDefault("database.enqueue_timeout_seconds", 2)
	viper.SetDefault("security.ota_max_clock_skew_seconds", 300)
	viper.SetDefault("security.key_file", "data/instance-key.json")
	viper.SetDefault("bootstrap.tenant_name", "Default Tenant")
	viper.SetDefault("bootstrap.system_code", "SYS001")
	viper.SetDefault("bootstrap.admin_username", "admin")
	viper.SetDefault("bootstrap.platform_username", "platform-admin")
	viper.SetDefault("backup.directory", "data/backups")
	viper.SetDefault("backup.interval_hours", 24)
	viper.SetDefault("backup.retention", 14)

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")
	viper.AddConfigPath("../config") // Allow running from cmd directory

	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("fatal error config file: %w", err)
	}

	if err := viper.Unmarshal(&GlobalConfig); err != nil {
		return fmt.Errorf("unable to decode into struct: %w", err)
	}
	if err := GlobalConfig.resolveSecrets(); err != nil {
		return err
	}

	return GlobalConfig.Validate()
}

func (c *Config) resolveSecrets() error {
	if c.Security.JWTSecret != "" || c.Security.EncryptionKey != "" {
		return nil
	}
	path, err := filepath.Abs(c.Security.KeyFile)
	if err != nil {
		return fmt.Errorf("resolve security key file: %w", err)
	}
	type storedSecrets struct {
		JWTSecret     string `json:"jwt_secret"`
		EncryptionKey string `json:"encryption_key"`
	}
	var secrets storedSecrets
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &secrets); err != nil {
			return fmt.Errorf("decode security key file: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read security key file: %w", err)
	} else {
		jwtBytes := make([]byte, 32)
		encryptionBytes := make([]byte, 16)
		if _, err := rand.Read(jwtBytes); err != nil {
			return fmt.Errorf("generate JWT secret: %w", err)
		}
		if _, err := rand.Read(encryptionBytes); err != nil {
			return fmt.Errorf("generate encryption key: %w", err)
		}
		secrets.JWTSecret = hex.EncodeToString(jwtBytes)
		secrets.EncryptionKey = hex.EncodeToString(encryptionBytes)
		encoded, err := json.MarshalIndent(secrets, "", "  ")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			return fmt.Errorf("create security directory: %w", err)
		}
		if err := os.WriteFile(path, encoded, 0600); err != nil {
			return fmt.Errorf("write security key file: %w", err)
		}
	}
	c.Security.JWTSecret = secrets.JWTSecret
	c.Security.EncryptionKey = secrets.EncryptionKey
	return nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Database.Path) == "" {
		return fmt.Errorf("database path is required")
	}
	if len(c.Security.JWTSecret) < 32 {
		return fmt.Errorf("JWT secret must be at least 32 characters (set TICKET_SECURITY_JWT_SECRET)")
	}
	if len(c.Security.EncryptionKey) != 32 {
		return fmt.Errorf("encryption key must be exactly 32 bytes (set TICKET_SECURITY_ENCRYPTION_KEY)")
	}
	if c.Security.OTAMaxClockSkewSeconds <= 0 {
		return fmt.Errorf("OTA clock skew must be greater than zero")
	}
	if c.Database.BusyTimeoutMS <= 0 {
		return fmt.Errorf("database busy timeout must be greater than zero")
	}
	if c.Database.MaxReadConnections < 2 || c.Database.WriteQueueSize <= 0 || c.Database.WriteTimeoutSeconds <= 0 || c.Database.EnqueueTimeoutSeconds <= 0 {
		return fmt.Errorf("invalid database concurrency settings")
	}
	if c.Backup.IntervalHours <= 0 || c.Backup.Retention <= 0 {
		return fmt.Errorf("backup interval and retention must be greater than zero")
	}
	if c.Bootstrap.AdminPassword != "" && c.Bootstrap.PlatformPassword != "" && c.Bootstrap.AdminPassword == c.Bootstrap.PlatformPassword {
		return fmt.Errorf("platform bootstrap password must differ from tenant administrator password")
	}
	return nil
}
