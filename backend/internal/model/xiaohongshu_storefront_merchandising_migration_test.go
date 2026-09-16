package model

import (
	"strings"
	"testing"
	"time"

	"gorm.io/gorm/clause"
	"ticket-backend/internal/testdb"
)

func TestPostgresSchema121To122AddsXiaohongshuStorefrontMerchandising(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP INDEX IF EXISTS idx_xhs_storefront_merchandising").Error; err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"storefront_category", "storefront_category_order", "storefront_product_order"} {
		if err := db.Migrator().DropColumn(&XiaohongshuProductConfig{}, column); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Where("version >= ?", 122).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&SchemaMigration{Version: 121, Name: "mobile verification preview and atomic confirmation", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"StorefrontCategory", "StorefrontCategoryOrder", "StorefrontProductOrder"} {
		if !db.Migrator().HasColumn(&XiaohongshuProductConfig{}, column) {
			t.Fatalf("column %s is missing", column)
		}
	}
	if !db.Migrator().HasIndex(&XiaohongshuProductConfig{}, "idx_xhs_storefront_merchandising") {
		t.Fatal("storefront merchandising index is missing")
	}
	for _, column := range []string{"storefront_category_order", "storefront_product_order"} {
		var columnDefault string
		if err := db.Raw(`SELECT column_default FROM information_schema.columns WHERE table_name = 'xiaohongshu_product_configs' AND column_name = ?`, column).Scan(&columnDefault).Error; err != nil || !strings.Contains(columnDefault, "9999") {
			t.Fatalf("column %s default=%q err=%v", column, columnDefault, err)
		}
	}
	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil || latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("latest migration=%+v err=%v", latest, err)
	}
}
