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
	tenant := Tenant{Name: "storefront migration tenant", SystemCode: "storefront-migration-tenant", SecretKey: "test-secret", Status: "active", QualificationStatus: "approved"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	scenicArea := ScenicArea{TenantID: tenant.ID, Code: "storefront-migration-area", Name: "storefront migration area", Status: "active"}
	if err := db.Create(&scenicArea).Error; err != nil {
		t.Fatal(err)
	}
	product := Product{Name: "legacy storefront product", TenantID: tenant.ID, ScenicAreaID: scenicArea.ID, FulfillmentTenantID: tenant.ID, FulfillmentScenicAreaID: scenicArea.ID, Type: "online", Status: "online"}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	account := ChannelAccount{TenantID: tenant.ID, Code: "storefront-migration-account", Type: "xiaohongshu", Status: "active", Environment: "production"}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	mapping := ChannelProductMapping{ChannelAccountID: account.ID, ProductID: product.ID, ExternalCode: "LEGACY-PRODUCT", Status: "active"}
	if err := db.Create(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&XiaohongshuProductConfig{
		TenantID: tenant.ID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID,
		ExternalSKUID: "LEGACY-SKU", CategoryID: "ticket", ImageURL: "https://example.test/legacy.png",
		Description: "legacy", ProductPath: "/pages/product/detail", OrderPath: "/pages/order/detail",
		ProductType: 1, SettleType: 1, SyncStatus: "synced", AuditStatus: "approved",
	}).Error; err != nil {
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
		var value int
		if err := db.Raw(`SELECT ` + column + ` FROM xiaohongshu_product_configs WHERE external_sku_id = 'LEGACY-SKU'`).Scan(&value).Error; err != nil || value != 9999 {
			t.Fatalf("legacy row column %s=%d err=%v", column, value, err)
		}
	}
	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil || latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("latest migration=%+v err=%v", latest, err)
	}
}
