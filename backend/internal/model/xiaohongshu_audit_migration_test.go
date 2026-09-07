package model

import (
	"testing"
	"ticket-backend/internal/testdb"
	"time"
)

func TestPostgresSchema112UpgradesAuditSchedulingFrom111(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the previous schema in this isolated test database.
	for _, column := range []string{"audit_checked_at", "audit_check_error"} {
		if err := db.Migrator().DropColumn(&XiaohongshuProductConfig{}, column); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Where("version >= ?", 111).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 111, Name: "mobile web verification sessions", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := runMigrations(db); err != nil {
			t.Fatal(err)
		}
	}
	for _, column := range []string{"audit_checked_at", "audit_check_error"} {
		if !db.Migrator().HasColumn(&XiaohongshuProductConfig{}, column) {
			t.Fatalf("missing audit scheduling column %s", column)
		}
	}
	if !db.Migrator().HasIndex(&XiaohongshuProductConfig{}, "idx_xhs_product_audit_reconciliation") {
		t.Fatal("missing audit scheduling index")
	}
	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil {
		t.Fatal(err)
	}
	if latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("schema version = %d", latest.Version)
	}
}
