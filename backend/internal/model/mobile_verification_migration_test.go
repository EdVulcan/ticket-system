package model

import (
	"testing"
	"time"

	"gorm.io/gorm/clause"
	"ticket-backend/internal/testdb"
)

func TestPostgresSchema120To121AddsMobileVerificationOperations(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&MobileVerificationOperation{}, &MobileVerificationPreview{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version >= ?", 121).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&SchemaMigration{Version: 120, Name: "shared upstream dispatch gate", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []interface{}{&MobileVerificationPreview{}, &MobileVerificationOperation{}} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("table for %T is missing", table)
		}
	}
	for _, index := range []struct {
		model interface{}
		name  string
	}{
		{&MobileVerificationPreview{}, "idx_mobile_verification_previews_active"},
		{&MobileVerificationOperation{}, "idx_mobile_verification_operations_ticket"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Fatalf("index %s for %T is missing", index.name, index.model)
		}
	}
	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil || latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("latest migration=%+v err=%v", latest, err)
	}
}
