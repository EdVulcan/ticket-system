// xhs-image-cleanup is an explicitly invoked, global maintenance command.
// It previews by default and never deletes an upload permanently.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"ticket-backend/internal/config"
	"ticket-backend/internal/service"
	"time"

	"github.com/spf13/viper"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type cleanupOptions struct {
	Apply           bool
	MinimumAge      string
	UploadDirectory string
	ConfigFile      string
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "xiaohongshu image cleanup stopped:", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	options, err := parseOptions(args, output)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	minimumAge, err := parseMinimumAge(options.MinimumAge)
	if err != nil {
		return err
	}
	cfg, err := loadCleanupConfig(options.ConfigFile)
	if err != nil {
		return err
	}
	dsn, err := cfg.Database.PostgresDSN()
	if err != nil {
		return errors.New("database configuration is unavailable")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return errors.New("database connection failed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return errors.New("database connection unavailable")
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	directory := strings.TrimSpace(options.UploadDirectory)
	if directory == "" {
		directory = cfg.Server.UploadDirectory
	}
	cleanup := service.NewXiaohongshuImageCleanupService(db, directory)
	cleanup.MinimumAge = minimumAge
	result, err := cleanup.Run(context.Background(), options.Apply)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func parseOptions(args []string, output io.Writer) (cleanupOptions, error) {
	flags := flag.NewFlagSet("xhs-image-cleanup", flag.ContinueOnError)
	flags.SetOutput(output)
	options := cleanupOptions{}
	flags.BoolVar(&options.Apply, "apply", false, "move eligible files into the recoverable quarantine directory")
	flags.StringVar(&options.MinimumAge, "min-age", service.XiaohongshuImageCleanupMinimumAge.String(), "minimum file age, at least 168h")
	flags.StringVar(&options.UploadDirectory, "upload-directory", "", "override the configured upload directory")
	flags.StringVar(&options.ConfigFile, "config-file", "", "configuration YAML path; otherwise use the existing config search paths")
	if err := flags.Parse(args); err != nil {
		return cleanupOptions{}, err
	}
	if flags.NArg() != 0 {
		return cleanupOptions{}, errors.New("unexpected positional arguments")
	}
	return options, nil
}

func parseMinimumAge(value string) (time.Duration, error) {
	minimumAge, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || minimumAge < service.XiaohongshuImageCleanupMinimumAge {
		return 0, fmt.Errorf("min-age must be at least %s", service.XiaohongshuImageCleanupMinimumAge)
	}
	return minimumAge, nil
}

func loadCleanupConfig(configFile string) (config.Config, error) {
	values := viper.New()
	values.SetEnvPrefix("TICKET")
	values.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	values.AutomaticEnv()
	values.SetDefault("server.upload_directory", "data/uploads")
	values.SetDefault("database.driver", "postgres")
	values.SetDefault("database.url", "")
	values.SetDefault("database.host", "127.0.0.1")
	values.SetDefault("database.port", 5432)
	values.SetDefault("database.name", "ticket_system")
	values.SetDefault("database.user", "postgres")
	values.SetDefault("database.password", "")
	values.SetDefault("database.sslmode", "disable")
	values.SetDefault("database.time_zone", "Asia/Shanghai")
	if strings.TrimSpace(configFile) != "" {
		values.SetConfigFile(configFile)
	} else {
		values.SetConfigName("config")
		values.SetConfigType("yaml")
		values.AddConfigPath("./config")
		values.AddConfigPath("../config")
	}
	if err := values.ReadInConfig(); err != nil {
		return config.Config{}, errors.New("cannot read existing configuration")
	}
	var cfg config.Config
	if err := values.Unmarshal(&cfg); err != nil {
		return config.Config{}, errors.New("cannot decode existing configuration")
	}
	if strings.ToLower(strings.TrimSpace(cfg.Database.Driver)) != "postgres" && strings.ToLower(strings.TrimSpace(cfg.Database.Driver)) != "postgresql" {
		return config.Config{}, errors.New("configured database driver is not PostgreSQL")
	}
	return cfg, nil
}
