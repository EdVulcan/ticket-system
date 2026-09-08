// One-order maintenance tool for the explicitly approved 2026-09-08 repair.
// Not registered in HTTP, not run at startup, and never initiates a refund.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"time"

	"github.com/spf13/viper"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "repair stopped:", err)
		os.Exit(1)
	}
}

func run() error {
	apply := flag.Bool("apply", false, "insert the approved attestation only")
	digest := flag.String("expected-digest", "", "digest printed by the read-only check")
	accepted := flag.Bool("accept-local-history", false, "acknowledge local audit evidence, not provider sale-time confirmation")
	approver := flag.String("approved-by", "", "initial admin username whose out-of-band approval is recorded")
	reference := flag.String("approval-reference", "", "reference to the explicit administrator approval")
	flag.Parse()
	if !*accepted || *approver == "" || strings.TrimSpace(*reference) == "" || flag.NArg() != 0 {
		return fmt.Errorf("explicit local-history acknowledgement, approver and approval reference are required")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("run as the server maintenance root user")
	}
	// Read only the active app's TICKET_ environment. Never print credentials.
	output, err := exec.Command("systemctl", "show", "ticket-system", "--property=MainPID", "--value").Output()
	if err != nil {
		return fmt.Errorf("cannot locate active service")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || pid <= 1 {
		return fmt.Errorf("active service PID unavailable")
	}
	proc := fmt.Sprintf("/proc/%d", pid)
	env, err := os.ReadFile(filepath.Join(proc, "environ"))
	if err != nil {
		return fmt.Errorf("cannot read active service configuration")
	}
	for _, key := range os.Environ() {
		name, _, _ := strings.Cut(key, "=")
		if strings.HasPrefix(name, "TICKET_") {
			_ = os.Unsetenv(name)
		}
	}
	for _, entry := range strings.Split(string(env), "\x00") {
		name, value, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(name, "TICKET_") {
			if err := os.Setenv(name, value); err != nil {
				return fmt.Errorf("cannot load service environment")
			}
		}
	}
	if err := loadActiveConfig(proc); err != nil {
		return err
	}
	return repair(*apply, *digest, *approver, *reference)
}

func loadActiveConfig(proc string) error {
	// A proc cwd magic link can enter the service's private mount namespace:
	// relative reads work, but Getwd (and Viper's absolute search paths) fail.
	// Resolve its release path and enter it through our own mount namespace.
	release, err := os.Readlink(filepath.Join(proc, "cwd"))
	if err != nil || !filepath.IsAbs(release) {
		return fmt.Errorf("cannot resolve active release directory")
	}
	if err := os.Chdir(release); err != nil {
		return fmt.Errorf("cannot select active release")
	}
	// InitConfig can generate absent instance keys. Preflight the exact selected
	// path first, so even --check cannot generate or replace runtime secrets.
	probe := viper.New()
	probe.SetConfigFile("config/config.yaml")
	if err := probe.ReadInConfig(); err != nil {
		return fmt.Errorf("cannot read active configuration")
	}
	keyPath := os.Getenv("TICKET_SECURITY_KEY_FILE")
	if keyPath == "" {
		keyPath = probe.GetString("security.key_file")
	}
	if keyPath == "" {
		keyPath = "data/instance-key.json"
	}
	if _, err := os.Stat(keyPath); err != nil {
		return fmt.Errorf("existing instance key required; no keys will be generated")
	}
	if err := config.InitConfig(); err != nil {
		// Parser failures can embed configuration values; report only their
		// category. Config's other errors contain paths/validation rules only.
		if strings.HasPrefix(err.Error(), "fatal error config file:") {
			return fmt.Errorf("cannot load active configuration: config file read failed")
		}
		if strings.HasPrefix(err.Error(), "unable to decode into struct:") {
			return fmt.Errorf("cannot load active configuration: config field type mismatch")
		}
		return fmt.Errorf("cannot load active configuration: %w", err)
	}
	return nil
}

func repair(apply bool, digest, approver, reference string) error {
	dsn, err := config.GlobalConfig.Database.PostgresDSN()
	if err != nil {
		return fmt.Errorf("database configuration unavailable")
	}
	// No InitDB: do not migrate, seed, or start any background workers.
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return fmt.Errorf("database connection failed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("database connection unavailable")
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	model.DB = db
	model.InitWriter(db, 15*time.Second)
	var user model.User
	if err := db.Where("tenant_id = ? AND username = ? AND is_initial_admin = ?", 1, approver, true).First(&user).Error; err != nil {
		return fmt.Errorf("approved initial administrator not found")
	}
	input := service.LegacyXiaohongshuEvidenceInput{
		TenantID: 1, ChannelAccountID: 2, OrderNo: "ORD1788829969828A20BF97468",
		ConfigureAuditID: 392, SyncAuditID: 393, ApproverUserID: user.ID,
		Executor: "server-root/xhs-legacy-refund-repair", ApprovalReference: reference,
		Reason: "初始管理员明确接受历史配置审计392及同步审计393作为此旧单团购券类型兼容依据；非平台历史回执，不自动退款",
	}
	var result *service.LegacyXiaohongshuEvidenceResult
	if apply {
		result, err = service.ApplyLegacyXiaohongshuRefundEvidence(input, digest)
	} else {
		result, err = service.CheckLegacyXiaohongshuRefundEvidence(input)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
