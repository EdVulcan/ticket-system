package model

import (
	"encoding/json"
	"strings"
	"testing"
	"ticket-backend/internal/testdb"
	"time"

	"gorm.io/gorm/clause"
)

func TestPostgresSchema154To155AddsIndependentContactSnapshots(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	tenant := Tenant{Name: "contact migration tenant", SystemCode: "CONTACT-MIGRATION", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	area := ScenicArea{TenantID: tenant.ID, Code: "contact-area", Name: "contact migration area", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatal(err)
	}
	product := Product{Name: "historical contact product", TenantID: tenant.ID, ScenicAreaID: area.ID, Price: 10, RealNameRequired: true}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	revision := ProductRevision{ProductID: product.ID, TenantID: tenant.ID, ScenicAreaID: area.ID, Version: 1, Status: "active", PriceCents: 1000, SnapshotJSON: `{"name":"old rule"}`, EffectiveFrom: time.Now()}
	if err := db.Create(&revision).Error; err != nil {
		t.Fatal(err)
	}
	order := Order{TenantID: tenant.ID, OrderNo: "CONTACT-HISTORICAL", Channel: "online", ContactName: "Historical contact", ContactPhone: "13800138000", VisitorID: "historical-visitor", TotalAmount: 10}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	for _, field := range []struct {
		model  interface{}
		column string
	}{
		{&Product{}, "contact_real_name_required"},
		{&ProductRevision{}, "contact_real_name_required"},
		{&Order{}, "contact_real_name_required"},
		{&Order{}, "contact_id_card_ciphertext"},
	} {
		if err := db.Migrator().DropColumn(field.model, field.column); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Where("version >= ?", 155).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&SchemaMigration{Version: 154, Name: "previous schema", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := runMigrations(db); err != nil {
			t.Fatalf("migration pass %d: %v", i+1, err)
		}
	}
	var migratedProduct Product
	if err := db.First(&migratedProduct, product.ID).Error; err != nil || migratedProduct.ContactRealNameRequired || !migratedProduct.RealNameRequired || migratedProduct.Price != product.Price {
		t.Fatalf("historical product changed: %v", err)
	}
	var migratedRevision ProductRevision
	if err := db.First(&migratedRevision, revision.ID).Error; err != nil || migratedRevision.ContactRealNameRequired || migratedRevision.SnapshotJSON != revision.SnapshotJSON {
		t.Fatalf("historical revision changed: %v", err)
	}
	var migratedOrder Order
	if err := db.First(&migratedOrder, order.ID).Error; err != nil || migratedOrder.ContactRealNameRequired || migratedOrder.ContactIDCardCiphertext != "" || migratedOrder.VisitorID != order.VisitorID || migratedOrder.ContactName != order.ContactName || migratedOrder.TotalAmount != order.TotalAmount {
		t.Fatalf("historical order changed: %v", err)
	}
	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil || latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("latest schema=%d err=%v", latest.Version, err)
	}
	for _, table := range []string{"products", "product_revisions", "orders"} {
		var column struct {
			ColumnDefault string
			IsNullable    string
		}
		if err := db.Raw(`SELECT column_default, is_nullable FROM information_schema.columns
			WHERE table_name = ? AND column_name = 'contact_real_name_required'`, table).Scan(&column).Error; err != nil || column.ColumnDefault != "false" || column.IsNullable != "NO" {
			t.Fatalf("%s contact policy default=%q nullable=%q err=%v", table, column.ColumnDefault, column.IsNullable, err)
		}
	}
	if err := db.Model(&migratedRevision).Update("contact_real_name_required", true).Error; err == nil {
		t.Fatal("historical revision policy was rewritten")
	}
	realNameOrder := Order{TenantID: tenant.ID, OrderNo: "CONTACT-NEW", Channel: "xiaohongshu", ContactRealNameRequired: true, ContactIDCardCiphertext: "encrypted-test-contact"}
	if err := db.Create(&realNameOrder).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&realNameOrder).Update("contact_id_card_ciphertext", "replacement-ciphertext").Error; err == nil {
		t.Fatal("order contact identity was rewritten")
	}
	if err := db.Model(&realNameOrder).Updates(map[string]interface{}{"contact_real_name_required": false, "contact_id_card_ciphertext": ""}).Error; err == nil {
		t.Fatal("order contact requirement was rewritten")
	}
	if err := db.Model(&realNameOrder).Update("status", "paid").Error; err != nil {
		t.Fatalf("snapshot guard blocked ordinary order progression: %v", err)
	}
	if err := db.First(&realNameOrder, realNameOrder.ID).Error; err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(realNameOrder)
	if err != nil || strings.Contains(string(raw), "contact_id_card") || strings.Contains(string(raw), realNameOrder.ContactIDCardCiphertext) {
		t.Fatal("generic order JSON exposed encrypted contact identity")
	}
	var decoded Order
	if err := json.Unmarshal([]byte(`{"contact_real_name_required":true,"contact_id_card_ciphertext":"forged"}`), &decoded); err != nil || decoded.ContactRealNameRequired || decoded.ContactIDCardCiphertext != "" {
		t.Fatal("generic order JSON accepted contact identity snapshot fields")
	}
}
