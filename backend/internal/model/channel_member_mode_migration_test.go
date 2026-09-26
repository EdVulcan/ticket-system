package model

import (
	"testing"
	"time"

	"ticket-backend/internal/testdb"
)

func TestChannelMemberModeMigrationBackfillsFirstPartyAccountsAndFailsClosed(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatalf("initial migrations: %v", err)
	}
	tenant := Tenant{Name: "Member mode migration tenant", SystemCode: "MEMBER-MODE-MIGRATION", SecretKey: "secret", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	legacyWechat := ChannelAccount{TenantID: tenant.ID, Code: "legacy-wechat-member-mode", Type: "wechat_miniapp", Status: "active", Environment: "production"}
	legacyOTA := ChannelAccount{TenantID: tenant.ID, Code: "legacy-ota-member-mode", Type: "ctrip", Status: "active", Environment: "production"}
	if err := db.Create(&legacyWechat).Error; err != nil {
		t.Fatalf("create legacy wechat account: %v", err)
	}
	if err := db.Create(&legacyOTA).Error; err != nil {
		t.Fatalf("create legacy ota account: %v", err)
	}
	if err := db.Model(&legacyWechat).Update("member_mode", ChannelMemberModeDisabled).Error; err != nil {
		t.Fatalf("reset legacy mode: %v", err)
	}
	if err := db.Where("version = ?", CurrentPostgresSchemaVersion).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatalf("remove current marker: %v", err)
	}
	if err := db.Create(&SchemaMigration{Version: CurrentPostgresSchemaVersion - 1, Name: "pre member mode", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatalf("create previous marker: %v", err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("member mode migration: %v", err)
	}
	var migratedWechat, migratedOTA ChannelAccount
	if err := db.First(&migratedWechat, legacyWechat.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&migratedOTA, legacyOTA.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migratedWechat.MemberMode != ChannelMemberModeFirstParty {
		t.Fatalf("legacy first-party account mode=%q, want %q", migratedWechat.MemberMode, ChannelMemberModeFirstParty)
	}
	if migratedOTA.MemberMode != ChannelMemberModeDisabled {
		t.Fatalf("external account mode=%q, want %q", migratedOTA.MemberMode, ChannelMemberModeDisabled)
	}
	invalid := ChannelAccount{TenantID: tenant.ID, Code: "invalid-member-mode", Type: "ctrip", Status: "active", Environment: "production", MemberMode: "forged"}
	if err := db.Create(&invalid).Error; err == nil {
		t.Fatal("invalid channel member mode was accepted")
	}
}
