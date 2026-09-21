package model

import (
	"testing"

	"ticket-backend/internal/testdb"
)

func TestCommerceStorefrontContactMigrationDefaultsExistingAccountsDisabled(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	if CurrentPostgresSchemaVersion < 141 {
		t.Fatalf("schema version=%d, want at least 141", CurrentPostgresSchemaVersion)
	}
	if !db.Migrator().HasColumn(&ChannelAccount{}, "storefront_contact_type") ||
		!db.Migrator().HasColumn(&ChannelAccount{}, "storefront_contact_qr_code_url") ||
		!db.Migrator().HasColumn(&ChannelAccount{}, "storefront_contact_status") {
		t.Fatal("commerce storefront contact columns are missing")
	}

	tenant := Tenant{Name: "Contact migration tenant", SystemCode: "CONTACT-MIGRATION", SecretKey: "test-secret", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	account := ChannelAccount{TenantID: tenant.ID, Code: "CONTACT-MIGRATION-WECHAT", Type: "wechat_miniapp", Status: "active", Environment: "production"}
	if err := db.Create(&account).Error; err != nil {
		t.Fatalf("create channel account: %v", err)
	}
	if account.StorefrontContactType != "personal_wechat" || account.StorefrontContactStatus != "disabled" || account.StorefrontContactQRCodeURL != "" {
		t.Fatalf("unexpected contact defaults: %+v", account)
	}

	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil || latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("latest migration=%+v err=%v", latest, err)
	}
}
