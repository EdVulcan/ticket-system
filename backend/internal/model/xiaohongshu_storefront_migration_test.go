package model

import (
	"testing"
	"ticket-backend/internal/testdb"
	"time"
)

func TestPostgresSchema113UpgradesStorefrontImageFrom112AndRepeats(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropColumn(&ChannelAccount{}, "storefront_image_url"); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version >= ?", 113).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 112, Name: "xiaohongshu product audit reconciliation", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := runMigrations(db); err != nil {
			t.Fatal(err)
		}
	}
	if !db.Migrator().HasColumn(&ChannelAccount{}, "storefront_image_url") {
		t.Fatal("missing storefront_image_url")
	}
	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil || latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}
