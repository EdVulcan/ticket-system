package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestUpstreamSchedulingEffectiveUsesConservativeDefaults(t *testing.T) {
	got := (UpstreamSchedulingConfig{}).Effective()
	want := UpstreamSchedulingConfig{
		RequestIntervalMS:  500,
		CooldownSeconds:    60,
		MaxCooldownSeconds: 1800,
		TodayPollSeconds:   120,
		FuturePollSeconds:  3600,
		UsedPollSeconds:    600,
		NightPollSeconds:   1800,
	}
	if got != want {
		t.Fatalf("effective config=%+v, want %+v", got, want)
	}
}

func TestUpstreamSchedulingEffectivePreservesConfiguredValues(t *testing.T) {
	configured := UpstreamSchedulingConfig{
		RequestIntervalMS:  2500,
		CooldownSeconds:    30,
		MaxCooldownSeconds: 300,
		TodayPollSeconds:   45,
		FuturePollSeconds:  900,
		UsedPollSeconds:    180,
		NightPollSeconds:   720,
	}
	if got := configured.Effective(); got != configured {
		t.Fatalf("effective config=%+v, want configured values preserved", got)
	}
}

func TestUpstreamSchedulingViperEnvironmentOverrides(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.SetEnvPrefix("TICKET")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	viper.SetDefault("upstream_scheduling.request_interval_ms", defaultUpstreamRequestIntervalMS)
	viper.SetDefault("upstream_scheduling.cooldown_seconds", defaultUpstreamCooldownSeconds)
	viper.SetDefault("upstream_scheduling.max_cooldown_seconds", defaultUpstreamMaxCooldown)
	viper.SetDefault("upstream_scheduling.today_poll_seconds", defaultUpstreamTodayPoll)
	viper.SetDefault("upstream_scheduling.future_poll_seconds", defaultUpstreamFuturePoll)
	viper.SetDefault("upstream_scheduling.used_poll_seconds", defaultUpstreamUsedPoll)
	viper.SetDefault("upstream_scheduling.night_poll_seconds", defaultUpstreamNightPoll)
	t.Setenv("TICKET_UPSTREAM_SCHEDULING_REQUEST_INTERVAL_MS", "2750")

	var configuration Config
	if err := viper.Unmarshal(&configuration); err != nil {
		t.Fatal(err)
	}
	if configuration.UpstreamScheduling.RequestIntervalMS != 2750 {
		t.Fatalf("request interval=%d, want 2750", configuration.UpstreamScheduling.RequestIntervalMS)
	}
	if got := configuration.UpstreamScheduling.Effective(); got.CooldownSeconds != 60 || got.NightPollSeconds != 1800 {
		t.Fatalf("unconfigured values were not defaulted: %+v", got)
	}
}

func TestConfigRejectsInvalidUpstreamScheduling(t *testing.T) {
	base := Config{
		Database: DatabaseConfig{Driver: "postgres", Host: "127.0.0.1", Port: 5432, Name: "ticket_system", User: "ticket_app", MaxOpenConnections: 1, ConnMaxLifetimeMinutes: 1, WriteTimeoutSeconds: 1},
		Security: SecurityConfig{JWTSecret: strings.Repeat("j", 32), EncryptionKey: strings.Repeat("e", 32), OTAMaxClockSkewSeconds: 1},
		Backup:   BackupConfig{IntervalHours: 1, Retention: 1},
		Maintenance: MaintenanceConfig{
			Path: "/api/v1/hardware/maintenance/ws", SessionTTLSeconds: 1, MaxSessionTTL: 1,
		},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("zero-value upstream scheduling should use defaults: %v", err)
	}

	for _, test := range []struct {
		name   string
		config UpstreamSchedulingConfig
		want   string
	}{
		{name: "negative poll interval", config: UpstreamSchedulingConfig{TodayPollSeconds: -1}, want: "must not be negative"},
		{name: "negative request interval", config: UpstreamSchedulingConfig{RequestIntervalMS: -1}, want: "must not be negative"},
		{name: "maximum below base", config: UpstreamSchedulingConfig{CooldownSeconds: 120, MaxCooldownSeconds: 60}, want: "at least the base cooldown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			configuration := base
			configuration.UpstreamScheduling = test.config
			if err := configuration.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error=%v, want %q", err, test.want)
			}
		})
	}
}
